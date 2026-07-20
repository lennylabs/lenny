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

	// delivered is the bounded set of eventKeys already written on this
	// connection. It spans every source the session serves from, so an event
	// that reaches the connection from two sources, or that the recovery flush
	// re-emits onto the Redis stream out of stream order, is written once.
	// spec: §25.5 (eventKey dedup across sources).
	delivered deliveredKeys

	// scope carries the §25.5 read-caller tenant scope resolved at the
	// opsserver boundary. It gates every frame so a tenant-admin observes only
	// its own tenant's events and never a platform-scoped one. A connection
	// whose context carried no resolved scope holds the zero value, which
	// admits nothing. spec: 25.5 (read-endpoint tenant filter).
	scope readerScope

	// tailFailures counts the consecutive Redis stints on this connection whose
	// live tail could not be started. It bounds the retry and, once it reaches
	// maxRedisTailAttempts, moves the connection onto the fall-back rather than
	// re-entering a source it cannot tail. spec: §25.5 (transparent source
	// switch).
	tailFailures int

	// forcedDeg is the degradation envelope a connection reclassified off an
	// untailable Redis source is served under. SourceHealth still reports Redis
	// reachable in that state, so the envelope cannot be derived from the
	// matrix alone. It is nil whenever the session is on the source the matrix
	// selected. spec: §25.5 lines 2768-2780.
	forcedDeg *conventions.Degradation

	// lastDegLine is the last :degradation comment written on this connection.
	// It is what makes the envelope a transition announcement: a stint whose
	// envelope has not changed re-writes nothing. Cleared when the connection
	// returns to a healthy source. spec: §25.5 line 2779.
	lastDegLine string
}

// admits reports whether the SSE caller may observe ev under the §25.5
// tenant-isolation rule. A platform-admin observes every event; a tenant-admin
// sees only its own tenant's events, and a platform-scoped event is dropped for
// it. A session built from a context with no resolved scope admits nothing, so
// a caller that reaches HandleStream without passing the opsserver
// authorization boundary receives no events rather than all of them. The drop
// is silent: the frame is skipped and its eventKey is not marked delivered,
// matching how the event-type/severity filter treats a non-match, so the resume
// position never advances past an event the caller never received. spec: 25.5
// (read-endpoint tenant filter, silent drop); §13 (fail closed on an isolation
// boundary).
func (st *streamSession) admits(ev gwevents.OperationalEvent) bool {
	return st.scope.admits(ev)
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
		src, _, _ := st.s.selectSource()
		if src == dsRedis && st.tailFailures >= maxRedisTailAttempts {
			// SourceHealth classifies Redis reachable, but this connection
			// cannot establish a live tail against it. Re-entering the Redis
			// stint would re-run the full retained-window resume scan, head
			// read, and backlog read for as long as the client stays
			// connected, delivering nothing. Serve the fall-back instead, with
			// the same envelope a poll whose Redis read failed carries.
			src, st.forcedDeg = st.s.tailFallbackSource()
		} else if src != dsRedis {
			st.forcedDeg = nil
		}
		st.announceRecovery(prev, src)
		// The source resolved here is carried into the serve loop so the
		// stint's stay-put check tests the same classification that chose the
		// data path, rather than re-deriving it from the live signal. spec:
		// §25.5 (transparent source switch).
		switch src {
		case dsRedis:
			if st.serveRedis(ctx, src) {
				st.tailFailures++
				if !sleepCtx(ctx, redisTailBackoff(st.tailFailures)) {
					return
				}
			} else {
				st.tailFailures = 0
				st.forcedDeg = nil
			}
		case dsGateway:
			st.serveGateway(ctx, src)
		default:
			st.serveLocal(ctx, src)
		}
		if ctx.Err() != nil {
			return
		}
		prev = src
	}
}

// maxRedisTailAttempts bounds how many times one SSE connection re-enters the
// Redis source after failing to start its live tail before it gives up on that
// source and serves from the fall-back. A tail that cannot be checked out is
// usually a client-level condition (an unsupported client type, an exhausted
// pool) that another attempt will not clear, so the bound is small; the
// connection still returns to Redis whenever a later SourceHealth transition
// moves it off and back. spec: §25.5 (transparent source switch).
const maxRedisTailAttempts = 3

// redisTailBackoffBase is the first pause after a failed tail start. Each
// further consecutive failure doubles it, capped at redisTailBackoffMax, so a
// connection whose tail cannot start neither spins nor stalls.
const (
	redisTailBackoffBase = 250 * time.Millisecond
	redisTailBackoffMax  = 2 * time.Second
)

// redisTailBackoff returns the pause before the attempt-th consecutive retry of
// a Redis stint whose live tail could not be started.
func redisTailBackoff(attempt int) time.Duration {
	d := redisTailBackoffBase
	for i := 1; i < attempt && d < redisTailBackoffMax; i++ {
		d *= 2
	}
	if d > redisTailBackoffMax {
		d = redisTailBackoffMax
	}
	return d
}

// sleepCtx waits out d and reports whether it elapsed rather than the context
// being cancelled.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// tailFallbackSource resolves the source an SSE connection is moved to when the
// Redis stream classifies healthy but its live tail cannot be established, and
// the degradation envelope that connection is being served under.
//
// It mirrors pollRedisUnreachable: the gateway-buffer fan-out serves under the
// case-1 envelope, and with no fan-out source wired this replica's own ring
// serves under the case-4 envelope, since gateway-originated events then have
// nowhere to come from. spec: §25.5 lines 2768-2780 (actualSource names the
// source the response was served from).
func (s *Service) tailFallbackSource() (dataSource, *conventions.Degradation) {
	if s.gateway != nil {
		_, deg, _ := gatewayFallbackState()
		return dsGateway, deg
	}
	_, deg, _ := dualOutageState()
	return dsLocalBuffer, deg
}

// announceRecovery writes :degradation {"level":"healthy"} when the session
// returns to the healthy Redis source from a degraded one, so the consumer
// learns the stream has recovered. It fires once per recovery, and not on the
// initial entry to a healthy source. The degraded envelope is not written here:
// each degraded serve loop announces it on entry to its stint (see
// announceDegradation). A :gap comment on a switch back to Redis is emitted by
// serveRedis when the resume position cannot be honoured. spec: §25.5 (lines
// 2768-2780, transparent switch and recovery).
func (st *streamSession) announceRecovery(prev, src dataSource) {
	if src != dsRedis || prev == dataSource(-1) || prev == dsRedis {
		return
	}
	writeSSEHealthy(st.w)
	st.flusher.Flush()
	// The connection is healthy again, so a later degraded stint announces its
	// envelope afresh even when it is the same envelope this stint carried.
	st.lastDegLine = ""
}

// announceDegradation writes the §25.5 :degradation comment when the envelope
// the connection is being served under changes: on entry to a degraded stint,
// and whenever the envelope's contents change mid-stint (the gateway fan-out
// going unreachable upgrades the case-1 envelope to the dual-outage one, which
// is what the connection is then receiving). A connection already announced
// under the same envelope is written nothing, so the comment stays a transition
// announcement rather than a heartbeat on the wire.
//
// The envelope is re-rendered on every call so the comparison is against the
// current classification rather than a remembered source label. spec: §25.5
// line 2779 (a degraded SSE response carries the canonical degradation envelope
// in a :degradation comment line).
func (st *streamSession) announceDegradation(fanOutDown bool) {
	_, deg, _ := st.s.selectSource()
	if st.forcedDeg != nil {
		// The session was reclassified off an untailable Redis source, which
		// the health matrix still reports reachable, so the envelope it is
		// being served under comes from the reclassification.
		deg = st.forcedDeg
	}
	if fanOutDown {
		_, deg, _ = dualOutageState()
	}
	line, ok := degradationComment(deg)
	if !ok {
		// Not degraded: the next entry into a degraded stint announces afresh.
		st.lastDegLine = ""
		return
	}
	if line == st.lastDegLine {
		return
	}
	st.lastDegLine = line
	fmt.Fprint(st.w, line)
	st.flusher.Flush()
}

// serveRedis serves the SSE stream from the Redis ops:events:stream: it
// resumes the backlog via XRANGE from the carried eventKey, then tails live
// via a bounded XREAD BLOCK on a per-connection cursor with no consumer group.
// It returns when the request is cancelled or SourceHealth moves the active
// source off Redis, at which point run re-selects. The last delivered eventKey
// is tracked so a subsequent switch resumes with no drop.
//
// It reports whether the stint ended because the live tail could not be
// started, which the caller treats as a source failure rather than as an
// ordinary end of stint. spec: §25.5 (XREAD BLOCK live tail, XRANGE resume,
// cross-source cursor translation).
func (st *streamSession) serveRedis(parent context.Context, src dataSource) (tailUnavailable bool) {
	s := st.s
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	// Resolve the resume point through the same cross-source translation the
	// poll path uses: every cursor, and a Last-Event-ID CloudEvents id,
	// carries an eventKey, which the scan translates to a stream position. A
	// read error resolving the point falls back to replaying the whole
	// retained window with the gap flag set.
	// The resume, head, and backlog reads run under the per-request Redis
	// deadline so a connection opened inside the window between a Redis
	// outage starting and the source-health probe observing it fails fast
	// into the fall-back instead of parking on connection retries. The live
	// tail below runs on the unbounded connection context: it blocks by
	// design.
	setupCtx, setupCancel := boundRedisRead(ctx)
	start, gap, rerr := s.redisResumePoint(setupCtx, st.lastKey)
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
	headStreamID, _, haveHead, headErr := s.redis.head(setupCtx)

	lastStreamID := start
	if entries, err := s.redis.ReadRange(setupCtx, start, 0); err == nil {
		for _, e := range entries {
			st.deliverOnce(e.event)
			lastStreamID = e.streamID
		}
	}
	if lastStreamID == "" {
		lastStreamID = streamOrigin
		if haveHead && headErr == nil {
			lastStreamID = headStreamID
		}
	}
	setupCancel()
	st.flusher.Flush()

	// Live tail: each connection runs its own bounded XREAD BLOCK goroutine
	// from its own position, so two subscribers each observe every matching
	// event with no consumer-group competition.
	live := make(chan gwevents.BufferedEvent, 64)
	// tailErr carries the tail's outcome so a tail that never started is
	// distinguishable from one that ended with the connection. Buffered so the
	// goroutine never blocks on a caller that has already returned.
	tailErr := make(chan error, 1)
	go func() { tailErr <- s.redis.Tail(ctx, lastStreamID, live) }()

	ticker := time.NewTicker(sourceCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-parent.Done():
			return false
		case <-ticker.C:
			if s.sourceChanged(src) {
				// The deferred cancel stops the Tail goroutine's bounded block.
				return false
			}
		case ev, open := <-live:
			if !open {
				// The tail closed its channel. Its outcome decides whether the
				// stint simply ended or the source could not be tailed at all;
				// Tail always reports one before returning.
				return <-tailErr != nil
			}
			if st.deliverOnce(ev) {
				st.flusher.Flush()
			}
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
func (st *streamSession) serveGateway(ctx context.Context, src dataSource) {
	// The gateway rings hold gateway-originated events only, so the local ring
	// stays the source of this replica's own events for the duration of the
	// outage: it is merged into every fan-out window and its live publishes are
	// delivered between fan-out ticks. Without it a Redis-only outage would
	// drop escalations, drift, and ops self-health entirely, losing more than
	// the strictly worse dual outage, where the local ring is served. spec:
	// §25.5 (the in-memory buffer is the lenny-ops-origin source).
	sub := st.s.subscribe(st.filter, 64)
	defer st.s.unsubscribe(sub)

	// The envelope is announced on entry, before the first fan-out poll, so a
	// connection learns its view is degraded without waiting on a cross-replica
	// fetch.
	st.announceDegradation(false)

	resumed := false
	for {
		if ctx.Err() != nil {
			return
		}
		if st.s.sourceChanged(src) {
			return
		}
		merged, err := st.s.fetchGatewayBuffer(ctx, st.filter)
		if err != nil {
			// No replica served the buffer query, so this connection is
			// receiving lenny-ops-originated events only. The tick below
			// announces the dual-outage envelope so the consumer stops reading
			// the stream as a cross-replica view, and the local ring keeps
			// serving. spec: §25.5 (dual-outage local-buffer serving).
			merged = nil
		}
		// The envelope is re-checked on every fall-back poll and written only
		// when it changed, so a fan-out that becomes unreachable mid-stint
		// upgrades the entry announcement to the dual-outage envelope the
		// connection is then actually being served under, and a stint whose
		// classification holds writes nothing further.
		st.announceDegradation(err != nil)
		window := st.s.unionLocalOrigin(merged, st.filter)
		if !resumed && err == nil && len(window) > 0 {
			st.seedGatewayResume(window)
			resumed = true
		}
		for _, ev := range window {
			st.deliverOnce(ev)
		}
		st.flusher.Flush()
		if !st.waitGatewayTick(ctx, sub) {
			return
		}
	}
}

// waitGatewayTick waits out one fall-back poll interval, delivering this
// replica's live local publishes as they arrive so a lenny-ops-originated event
// reaches an open connection without waiting for the next fan-out tick. It
// reports false when the connection is done. spec: §25.5 (the in-memory buffer
// is the lenny-ops-origin source during a Redis-down outage).
func (st *streamSession) waitGatewayTick(ctx context.Context, sub *subscription) bool {
	tick := time.NewTimer(gatewayPollInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-tick.C:
			return true
		case ev, open := <-sub.ch:
			if !open {
				return false
			}
			if st.deliverOnce(ev) {
				st.flusher.Flush()
			}
		}
	}
}

// deliverOnce writes ev as an SSE frame unless it was already delivered on this
// connection or the caller's filter or tenant scope excludes it, and reports
// whether a frame was written.
//
// Every source the session serves from writes through it, so the dedup holds
// across a source switch as well as within one stint: the same event reaching
// the connection from the fan-out window and from a live local publish is
// written once, and so is an outage-window event the recovery flush re-emits
// onto the Redis stream after the connection already received it from the local
// ring. Ordering alone cannot decide the latter, because the flush places those
// events at the stream tail carrying keys older than everything around them,
// and a connection that never received them must still be delivered them. spec:
// §25.5 (eventKey dedup across sources, exactly-once across the source switch).
func (st *streamSession) deliverOnce(ev gwevents.BufferedEvent) bool {
	if st.delivered.has(ev.Event.ID) {
		return false
	}
	if !st.filter.Matches(ev.Event) || !st.admits(ev.Event) {
		return false
	}
	writeSSEFrame(st.w, ev)
	st.delivered.add(ev.Event.ID)
	st.markDelivered(ev.Event.ID)
	return true
}

// seedGatewayResume seeds the delivered set from the carried resume position so
// a switch into the gateway-buffer fall-back does not re-deliver events already
// sent from the Redis stream or an earlier gateway stint. Every merged event
// ordering at or before lastKey is marked delivered, so the poll loop emits
// only the continuation. Marking by order rather than by an exact match is what
// makes the ordinary switch exactly-once: the last event delivered from Redis
// is frequently a lenny-ops-originated one that no gateway replica ever
// buffered, so requiring the key itself to be present would replay the whole
// window on every switch.
//
// A :gap comment is emitted when the window cannot honour the resume position
// at either end. Every event newer than lastKey means the events between were
// evicted from every replica's ring, and the whole window is delivered (nothing
// pre-marked). No event at or after lastKey is the §25.5 transition condition:
// the new source has no greater-or-equal eventKey, so the gap is reported while
// the window stays marked delivered and nothing is re-sent. spec: §25.5
// (cross-switch no-drop, exactly-once across the source switch; a :gap when no
// event has a greater-or-equal eventKey).
func (st *streamSession) seedGatewayResume(window []gwevents.BufferedEvent) {
	if st.lastKey == "" {
		return
	}
	seeded := false
	for _, ev := range window {
		if eventKeyAtOrAfter(st.lastKey, ev.Event.ID) {
			st.delivered.add(ev.Event.ID)
			seeded = true
		}
	}
	if seeded && windowHasAtOrAfter(window, st.lastKey) {
		return
	}
	writeSSEGap(st.w, st.lastKey)
	st.s.observeGap()
}

// serveLocal serves the SSE stream from this replica's local ring buffer: the
// lenny-ops-origin source used when no Redis client is wired or during a dual
// Redis + gateway outage. It replays the backlog after
// the carried resume position, then delivers live publishes until the request
// is cancelled or SourceHealth moves the active source off the local buffer.
// spec: §25.5 (line 2768-2780, dual-outage local-buffer serving).
func (st *streamSession) serveLocal(ctx context.Context, src dataSource) {
	sub := st.s.subscribe(st.filter, 64)
	defer st.s.unsubscribe(sub)

	var since uint64
	gap := false
	if st.lastKey != "" {
		since, gap = st.s.localResumePoint(st.lastKey)
	}
	if gap {
		writeSSEGap(st.w, st.lastKey)
		st.s.observeGap()
	}
	backlog := st.s.buffer.Query(since, st.filter, 0)
	for _, ev := range backlog.Events {
		st.deliverOnce(ev)
	}
	st.flusher.Flush()
	// The local ring is a degraded source whenever it is reached from a Redis
	// outage, so the envelope is announced on entry to the stint. A deployment
	// with no Redis wired is not degraded and renders no envelope, so this
	// writes nothing. spec: §25.5 line 2779 (:degradation comment).
	st.announceDegradation(false)

	ticker := time.NewTicker(sourceCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if st.s.sourceChanged(src) {
				return
			}
		case ev, open := <-sub.ch:
			if !open {
				return
			}
			if st.deliverOnce(ev) {
				st.flusher.Flush()
			}
		}
	}
}

// localResumePoint resolves the local ring position an SSE connection carrying
// eventKey resumes after, and reports whether the ring can honour that
// position.
//
// The resolution is by eventKey order rather than by exact presence, matching
// the Redis and gateway-buffer translations. An exact match is the fast path.
// A key the ring never held is the ordinary case on a switch into the local
// ring: the last event delivered from the Redis stream or a gateway replica was
// routinely gateway- or peer-replica-originated and this replica's ring never
// appended it. Resolving that by exact match alone would restart the replay at
// the oldest retained event and re-deliver everything the connection already
// received, so the position is the ring entry immediately before the first
// event ordering at or after the key.
//
// A gap is reported at both ends of the ring, as on the other sources: below
// it the events in between were evicted, and above it no retained event has a
// greater-or-equal eventKey. An empty ring reports no gap. spec: §25.5
// (cross-source cursor translation; cursor transition safety; exactly-once
// across the source switch).
func (s *Service) localResumePoint(eventKey string) (since uint64, gap bool) {
	if id, found := s.buffer.Lookup(eventKey); found {
		return id, false
	}
	_, _, oldestKey, _, found := s.buffer.Bounds()
	if !found {
		return 0, false
	}
	prev := uint64(0)
	for _, ev := range s.buffer.Query(0, gwevents.EventFilter{}, DefaultBufferCapacity).Events {
		if eventKeyLess(eventKey, ev.Event.ID) {
			return prev, eventKeyLess(eventKey, oldestKey)
		}
		prev = ev.ID
	}
	return prev, true
}

// markDelivered advances the last delivered eventKey so a source switch
// resumes with no drop. The kind is left empty (a CloudEvents id) so a switch
// to another source resolves it by eventKey scan. spec: §25.5 (cross-switch
// no-drop ordering).
//
// The position only ever moves forward. The recovery flush re-emits events
// buffered during a Redis outage, which lands them at the head of the stream
// carrying eventKeys that order before entries this connection already
// consumed. Taking such a key as the new resume position would rewind the
// session, and the next source switch would then re-deliver everything after
// it, breaking the exactly-once invariant the resume position exists to hold.
// Every carried position is a canonical eventKey whichever source minted its
// cursor, so the ordering check applies uniformly.
func (st *streamSession) markDelivered(eventKey string) {
	if eventKey == "" {
		return
	}
	if st.lastKey != "" && !eventKeyLess(st.lastKey, eventKey) {
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
