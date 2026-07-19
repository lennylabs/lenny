// SPDX-License-Identifier: MIT

package events

import (
	"context"
	"fmt"
	"net/http"
	"time"

	gwevents "github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/ops/conventions"
)

// gatewayPollInterval is the §25.5 SSE fall-back poll cadence: while serving
// from the gateway-buffer fan-out during a Redis-down outage, the handler re-polls
// every replica's buffer on this interval and streams the events it has not
// yet delivered. spec: §25.5 ("poll the fan-out every 2 seconds while the
// connection stays open").
const gatewayPollInterval = 2 * time.Second

// sourceCheckInterval bounds how quickly an open SSE connection notices a
// SourceHealth transition (Redis recovering or failing) so it can switch its
// data path. It is short relative to the outage-detection cadence so a
// recovery is picked up promptly without busy-looping. spec: §25.5
// (transparent source switch).
const sourceCheckInterval = time.Second

// streamSession carries the state of one §25.5 SSE connection across source
// transitions. lastKey / lastKind track the last delivered position as a
// canonical eventKey so a switch to another source resumes with no drop and a
// :gap comment is emitted only when the new source cannot honour it. spec:
// §25.5 (transparent Redis to gateway-buffer switch, cross-switch no-drop).
type streamSession struct {
	s       *Service
	w       http.ResponseWriter
	flusher http.Flusher
	filter  gwevents.EventFilter

	// lastKind / lastKey are the resume position carried across a source
	// switch. lastKey is a CloudEvents id (eventKey); lastKind is its source
	// kind (empty or non-redis so a switch resolves it by eventKey scan).
	lastKind string
	lastKey  string
}

// run serves the SSE connection, following the live SourceHealth signal
// across source transitions until the request context is cancelled. Each
// iteration selects the active source, announces the transition on the
// stream, and serves from that source until either the connection closes or
// the source changes, at which point the loop re-selects. spec: §25.5
// (transparent source switch, recovery).
func (st *streamSession) run(ctx context.Context) {
	prev := dataSource(-1)
	for {
		src, deg, _ := st.s.selectSource()
		st.writeTransition(prev, src, deg)
		switch src {
		case dsRedis:
			st.serveRedis(ctx)
		case dsGateway:
			st.serveGateway(ctx)
		default:
			st.serveLocal(ctx)
		}
		if ctx.Err() != nil {
			return
		}
		prev = src
	}
}

// writeTransition announces a source change on the stream. Entering a
// degraded source (gateway buffer or local ring) writes the :degradation
// envelope so the consumer learns it is receiving a degraded view. Returning
// to the healthy Redis source from a degraded one writes
// :degradation {"level":"healthy"}. The initial entry writes nothing when the
// source is healthy. A :gap comment on a switch back to Redis is emitted by
// serveRedis when the resume position cannot be honoured. spec: §25.5 (lines
// 2768-2780, transparent switch and recovery).
func (st *streamSession) writeTransition(prev, src dataSource, deg *conventions.Degradation) {
	switch src {
	case dsRedis:
		if prev != dataSource(-1) && prev != dsRedis {
			writeSSEHealthy(st.w)
			st.flusher.Flush()
		}
	default:
		if deg != nil {
			writeSSEDegradation(st.w, deg)
			st.flusher.Flush()
		}
	}
}

// serveRedis serves the SSE stream from the Redis ops:events:stream: it
// resumes the backlog via XRANGE from the carried eventKey, then tails live
// via a bounded XREAD BLOCK on a per-connection cursor with no consumer group.
// It returns when the request is cancelled or SourceHealth moves the active
// source off Redis, at which point run re-selects. The last delivered eventKey
// is tracked so a subsequent switch resumes with no drop. spec: §25.5 (XREAD
// BLOCK live tail, XRANGE resume, cross-source cursor translation).
func (st *streamSession) serveRedis(parent context.Context) {
	s := st.s
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	// Resolve the resume point through the same cross-source translation the
	// poll path uses: a redis cursor reads by stream ID, any other cursor (or
	// a Last-Event-ID CloudEvents id) translates by eventKey scan. A read
	// error resolving the point falls back to replaying the whole retained
	// window with the gap flag set.
	start, gap, rerr := s.redisResumePoint(ctx, st.lastKind, st.lastKey)
	if rerr != nil {
		start = ""
		gap = true
	}
	if gap {
		writeSSEGap(st.w, st.lastKey)
		s.observeGap()
	}

	// Capture a concrete live-tail resume position BEFORE scanning the
	// backlog so the tail is contiguous with the scan and drops no event in
	// the seam between the backlog read and the blocking XREAD.
	headStreamID, _, haveHead, headErr := s.redis.head(ctx)

	lastStreamID := start
	if entries, err := s.redis.ReadRange(ctx, start, 0); err == nil {
		for _, e := range entries {
			if st.filter.Matches(e.event.Event) {
				writeSSEFrame(st.w, e.event)
				st.markDelivered(e.event.Event.ID)
			}
			lastStreamID = e.streamID
		}
	}
	if lastStreamID == "" {
		lastStreamID = streamOrigin
		if haveHead && headErr == nil {
			lastStreamID = headStreamID
		}
	}
	st.flusher.Flush()

	// Live tail: each connection runs its own bounded XREAD BLOCK goroutine
	// from its own position, so two subscribers each observe every matching
	// event with no consumer-group competition.
	live := make(chan gwevents.BufferedEvent, 64)
	go s.redis.Tail(ctx, lastStreamID, live)

	ticker := time.NewTicker(sourceCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-parent.Done():
			return
		case <-ticker.C:
			if s.sourceChanged(dsRedis) {
				// The deferred cancel stops the Tail goroutine's bounded block.
				return
			}
		case ev, open := <-live:
			if !open {
				return
			}
			if !st.filter.Matches(ev.Event) {
				continue
			}
			writeSSEFrame(st.w, ev)
			st.markDelivered(ev.Event.ID)
			st.flusher.Flush()
		}
	}
}

// serveGateway serves the SSE stream from the gateway-buffer fan-out during a
// Redis-down / gateway-up outage. It re-polls every
// replica's buffer on gatewayPollInterval and streams the merged events it has
// not yet delivered, deduplicated by eventKey so the repeated poll of the same
// buffer window does not re-deliver an event. It returns when the request is
// cancelled or SourceHealth moves the active source off the gateway buffer.
//
// On entry it seeds the resume position from the carried lastKey the same way
// serveRedis and serveLocal do, so a switch into the gateway buffer (or a
// fresh Last-Event-ID connection that opens directly in the fall-back) does
// not re-deliver events already sent from the Redis stream or a prior gateway
// stint. Because the gateway writes through eventbuffer.StreamEmitter, which
// also XADDs every gateway-originated event to the Redis ops:events:stream,
// the Redis-served window and the gateway-buffer window overlap; seeding from
// lastKey is what keeps the switch exactly-once with no drop. spec: §25.5 (SSE
// fall-back polls the fan-out every 2 seconds; cross-switch no-drop).
func (st *streamSession) serveGateway(ctx context.Context) {
	delivered := make(map[string]struct{})
	resumed := false
	for {
		if ctx.Err() != nil {
			return
		}
		if st.s.sourceChanged(dsGateway) {
			return
		}
		if merged, err := st.s.fetchGatewayBuffer(ctx, st.filter); err == nil {
			if !resumed {
				st.seedGatewayResume(merged, delivered)
				resumed = true
			}
			for _, ev := range merged {
				if _, seen := delivered[ev.Event.ID]; seen {
					continue
				}
				if !st.filter.Matches(ev.Event) {
					continue
				}
				writeSSEFrame(st.w, ev)
				delivered[ev.Event.ID] = struct{}{}
				st.markDelivered(ev.Event.ID)
			}
			st.flusher.Flush()
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(gatewayPollInterval):
		}
	}
}

// seedGatewayResume seeds the delivered set from the carried resume position so
// a switch into the gateway-buffer fall-back does not re-deliver events already
// sent from the Redis stream or an earlier gateway stint. The merged window is
// ordered oldest-first, so every event up to and including lastKey is marked
// delivered; the poll loop then emits only the events after it. When lastKey is
// set but absent from the window, a :gap comment is emitted and the gap counter
// observed, matching the serveRedis and serveLocal resume paths, so the
// consumer re-reads platform state before assuming continuity. spec: §25.5
// (cross-switch no-drop, exactly-once across the source switch).
func (st *streamSession) seedGatewayResume(window []gwevents.BufferedEvent, delivered map[string]struct{}) {
	if st.lastKey == "" {
		return
	}
	for i, ev := range window {
		if ev.Event.ID == st.lastKey {
			for _, seen := range window[:i+1] {
				delivered[seen.Event.ID] = struct{}{}
			}
			return
		}
	}
	// The resume position is no longer in the window: emit a :gap and deliver
	// the whole window (nothing pre-marked), so the consumer re-reads platform
	// state before assuming continuity.
	writeSSEGap(st.w, st.lastKey)
	st.s.observeGap()
}

// serveLocal serves the SSE stream from this replica's local ring buffer: the
// lenny-ops-origin source used when no Redis client is wired or during a dual
// Redis + gateway outage. It replays the backlog after
// the carried resume position, then delivers live publishes until the request
// is cancelled or SourceHealth moves the active source off the local buffer.
// spec: §25.5 (line 2768-2780, dual-outage local-buffer serving).
func (st *streamSession) serveLocal(ctx context.Context) {
	sub := st.s.subscribe(st.filter, 64)
	defer st.s.unsubscribe(sub)

	var since uint64
	gap := false
	if st.lastKey != "" {
		if id, found := st.s.buffer.Lookup(st.lastKey); found {
			since = id
		} else {
			gap = true
		}
	}
	if gap {
		writeSSEGap(st.w, st.lastKey)
		st.s.observeGap()
	}
	backlog := st.s.buffer.Query(since, st.filter, 0)
	for _, ev := range backlog.Events {
		writeSSEFrame(st.w, ev)
		st.markDelivered(ev.Event.ID)
	}
	st.flusher.Flush()

	ticker := time.NewTicker(sourceCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if st.s.sourceChanged(dsLocalBuffer) {
				return
			}
		case ev, open := <-sub.ch:
			if !open {
				return
			}
			writeSSEFrame(st.w, ev)
			st.markDelivered(ev.Event.ID)
			st.flusher.Flush()
		}
	}
}

// markDelivered records the last delivered eventKey so a source switch resumes
// with no drop. The kind is left empty (a CloudEvents id) so a switch to
// another source resolves it by eventKey scan. spec: §25.5 (cross-switch
// no-drop ordering).
func (st *streamSession) markDelivered(eventKey string) {
	if eventKey == "" {
		return
	}
	st.lastKey = eventKey
	st.lastKind = ""
}

// sourceChanged reports whether the live SourceHealth signal has moved the
// active read source off want, so an open SSE connection knows to switch.
// spec: §25.5 (transparent source switch).
func (s *Service) sourceChanged(want dataSource) bool {
	cur, _, _ := s.selectSource()
	return cur != want
}

// writeSSEHealthy writes the §25.5 :degradation {"level":"healthy"} comment
// emitted when an open SSE connection returns to the healthy Redis source
// after serving from a degraded fall-back, so the consumer learns the stream
// has recovered. spec: §25.5 (transparent recovery to Redis).
func writeSSEHealthy(w http.ResponseWriter) {
	fmt.Fprint(w, ":degradation {\"level\":\"healthy\"}\n")
}
