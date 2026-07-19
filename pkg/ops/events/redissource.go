// SPDX-License-Identifier: MIT

package events

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	gwevents "github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/gateway/eventbuffer"
)

// RedisStreamClient is the subset of redis.UniversalClient the §25.5
// read-side Redis source consumes the ops:events:stream with:
// XRANGE for polling and SSE backlog resume, XREVRANGE to bound the
// retained window, and XREAD for the live SSE tail. redis.UniversalClient
// satisfies it; tests substitute a real single-container client.
//
// spec: §25.5 — the SSE handler reads Redis via XREAD BLOCK 0 with a
// per-connection XRANGE resume from Last-Event-ID, and polling serves the
// same stream via XRANGE. The read cursor is per-connection and
// independent, with no consumer group.
type RedisStreamClient interface {
	XRangeN(ctx context.Context, stream, start, stop string, count int64) *redis.XMessageSliceCmd
	XRevRangeN(ctx context.Context, stream, start, stop string, count int64) *redis.XMessageSliceCmd
	XRead(ctx context.Context, a *redis.XReadArgs) *redis.XStreamSliceCmd
}

// redisSource reads the §25.5 platform-scoped Redis stream that every
// replica (gateway, controllers, peer lenny-ops) XADDs operational events
// to. It pairs with the producer side (pkg/gateway/eventbuffer.StreamEmitter):
// the emitter XADDs each event as a single "event" field carrying the
// marshalled CloudEvents record, and this source reads that field back so
// the CloudEvents payload the poll envelope and SSE frame carry decodes
// identically to the buffer-served path.
//
// Unlike the webhook worker's RedisEventSource, which holds one per-process
// shared cursor, redisSource is cursor-free: each poll and each SSE
// connection carries its own read position (a Redis stream ID), so every
// caller gets an independent view with no consumer-group competition.
//
// spec: §25.5 (XREAD BLOCK 0 live tail, XRANGE resume, independent
// per-connection cursor).
type redisSource struct {
	client RedisStreamClient
	stream string
}

// newRedisSource returns a redisSource reading stream from client. An empty
// stream falls back to the §25.5 default ops:events:stream key.
func newRedisSource(client RedisStreamClient, stream string) *redisSource {
	if stream == "" {
		stream = eventbuffer.DefaultStreamKey
	}
	return &redisSource{client: client, stream: stream}
}

// redisEntry is one decoded stream entry paired with its Redis stream ID.
// The stream ID is the source-specific cursor position, carried in the
// opaque pagination cursor; the event is the transport-neutral BufferedEvent
// the poll and SSE surfaces serve. The BufferedEvent's top-level wrapper ID is
// a synthetic per-source position derived from the Redis stream ID (see
// syntheticBufferID): the buffer-served path stamps a per-replica in-memory
// sequence there, and the Redis-served path stamps a monotonic position from
// the stream ID so the /v1/admin/events item shape ({"id":N,"event":{...}})
// stays stable across sources. The wrapper id value is per-source; callers
// resume on the CloudEvents id (Event.ID, the canonical eventKey) and the
// pagination cursor, both identical across sources.
type redisEntry struct {
	streamID string
	event    gwevents.BufferedEvent
}

// maxWindow caps a single XRANGE scan at the §25.5 Tier 1 stream length so
// a poll or a backlog replay never materialises an unbounded slice. The
// stream is MAXLEN-bounded, so this reads the whole retained window at
// most.
const maxWindow = eventbuffer.DefaultStreamMaxLen

// tailBlock bounds each XREAD BLOCK so the live tail sleeps inside Redis
// (waking the instant a new entry is XADDed, delivery-equivalent to BLOCK 0)
// yet re-checks its context on a bounded interval. go-redis v9 does not
// interrupt an in-flight blocked read on a deadline-free context
// cancellation, so a literal BLOCK 0 read stays parked in IO wait after the
// SSE connection disconnects and leaks a goroutine per connection. A bounded
// block gives every read a deadline, so a cancelled tail exits within one
// interval. spec: §25.5 (XREAD BLOCK live tail on a per-connection cursor).
const tailBlock = time.Second

// streamOrigin is the XREAD starting position that reads from the very
// beginning of the stream. The live SSE tail resumes from a concrete stream
// ID rather than the "$" sentinel, which XREAD resolves server-side at read
// time; on an empty stream a fresh tail starts here so a first entry XADDed
// during the backlog-to-tail seam is still delivered. spec: §25.5.
const streamOrigin = "0"

// ReadRange returns the decoded stream entries after sinceStreamID, capped
// at count (a non-positive count reads the whole retained window). An empty
// sinceStreamID reads from the oldest retained entry inclusive; a non-empty
// one reads exclusive of that position, so a resume never re-delivers the
// event the caller already consumed. It backs both polling and the SSE
// backlog resume from Last-Event-ID. spec: §25.5 (XRANGE resume).
func (rs *redisSource) ReadRange(ctx context.Context, sinceStreamID string, count int64) ([]redisEntry, error) {
	start := "-"
	if sinceStreamID != "" {
		// "(" makes the range start exclusive so an entry is never
		// delivered twice across a resume.
		start = "(" + sinceStreamID
	}
	if count <= 0 {
		count = maxWindow
	}
	msgs, err := rs.client.XRangeN(ctx, rs.stream, start, "+", count).Result()
	if err != nil {
		return nil, fmt.Errorf("xrange %s: %w", rs.stream, err)
	}
	out := make([]redisEntry, 0, len(msgs))
	for _, m := range msgs {
		ev, ok := decodeRedisEntry(m)
		if !ok {
			continue
		}
		out = append(out, redisEntry{streamID: m.ID, event: ev})
	}
	return out, nil
}

// oldest returns the oldest retained entry's stream ID and CloudEvents
// eventKey, and whether the stream holds any entry. It bounds the retained
// window so a poll can decide whether a cursor was evicted. spec: §25.5
// (gapDetected / oldestAvailableCursor on an evicted cursor).
func (rs *redisSource) oldest(ctx context.Context) (streamID, eventKey string, found bool, err error) {
	return rs.bound(ctx, false)
}

// head returns the newest retained entry's stream ID and eventKey. It backs
// the §25.2 headCursor so a poller can tell whether it has reached the live
// head.
func (rs *redisSource) head(ctx context.Context) (streamID, eventKey string, found bool, err error) {
	return rs.bound(ctx, true)
}

// bound returns one boundary entry of the retained window: the newest when
// rev is true (XREVRANGE + -), the oldest otherwise (XRANGE - +).
func (rs *redisSource) bound(ctx context.Context, rev bool) (streamID, eventKey string, found bool, err error) {
	var msgs []redis.XMessage
	if rev {
		msgs, err = rs.client.XRevRangeN(ctx, rs.stream, "+", "-", 1).Result()
	} else {
		msgs, err = rs.client.XRangeN(ctx, rs.stream, "-", "+", 1).Result()
	}
	if err != nil {
		return "", "", false, fmt.Errorf("xrange bound %s: %w", rs.stream, err)
	}
	if len(msgs) == 0 {
		return "", "", false, nil
	}
	ev, ok := decodeRedisEntry(msgs[0])
	if !ok {
		return msgs[0].ID, "", true, nil
	}
	return msgs[0].ID, ev.Event.ID, true, nil
}

// findByEventKey locates the stream ID of the retained entry whose
// CloudEvents id equals eventKey. It backs the §25.5 cross-source cursor
// translation: a cursor minted by the in-memory buffer carries an eventKey,
// and the Redis source resolves it to a stream position by scanning. found
// is false when no retained entry matches (the event was evicted), which
// the caller reports as a gap. spec: §25.5 ("translates by scanning for the
// first event with a matching eventKey").
func (rs *redisSource) findByEventKey(ctx context.Context, eventKey string) (streamID string, found bool, err error) {
	if eventKey == "" {
		return "", false, nil
	}
	msgs, err := rs.client.XRangeN(ctx, rs.stream, "-", "+", maxWindow).Result()
	if err != nil {
		return "", false, fmt.Errorf("xrange scan %s: %w", rs.stream, err)
	}
	for _, m := range msgs {
		ev, ok := decodeRedisEntry(m)
		if ok && ev.Event.ID == eventKey {
			return m.ID, true, nil
		}
	}
	return "", false, nil
}

// Tail streams live events to out via a bounded XREAD BLOCK from
// lastStreamID, closing out when ctx is cancelled or Redis returns a
// terminal error. Each SSE connection runs its own Tail with its own
// starting position, so the per-connection cursor is independent and no
// consumer group is created. The block interval bounds cancellation latency
// (see tailBlock): go-redis v9 does not interrupt a blocked read on a
// deadline-free context cancellation, so a bounded block is what lets a
// disconnected connection's goroutine exit instead of parking in IO wait
// forever. A transient Redis error is retried after a short pause rather
// than closing the channel, matching the §25.5 live-tail resilience the SSE
// surface depends on. spec: §25.5 (XREAD BLOCK per-connection live tail).
func (rs *redisSource) Tail(ctx context.Context, lastStreamID string, out chan<- gwevents.BufferedEvent) {
	defer close(out)
	lastID := lastStreamID
	if lastID == "" {
		// "$" tails only entries that arrive after this call, so a backlog
		// already replayed by ReadRange is not re-delivered.
		lastID = "$"
	}
	for {
		if ctx.Err() != nil {
			return
		}
		res, err := rs.client.XRead(ctx, &redis.XReadArgs{
			Streams: []string{rs.stream, lastID},
			Block:   tailBlock,
			Count:   maxWindow,
		}).Result()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if err == redis.Nil {
				// The block elapsed with no new entry; loop to re-check the
				// context and block again.
				continue
			}
			// A transient Redis error: pause briefly and retry so a blip
			// does not tear down the live connection.
			select {
			case <-ctx.Done():
				return
			case <-time.After(50 * time.Millisecond):
			}
			continue
		}
		for _, stream := range res {
			for _, m := range stream.Messages {
				lastID = m.ID
				ev, ok := decodeRedisEntry(m)
				if !ok {
					continue
				}
				select {
				case out <- ev:
				case <-ctx.Done():
					return
				}
			}
		}
	}
}

// redisPollPage serves the §25.5 polling page from the Redis
// ops:events:stream. It resolves the incoming cursor's source: a redis
// cursor reads directly by stream ID (O(1) resume); a buffer or mixed
// cursor is translated by scanning for the matching eventKey. A cursor
// whose position was evicted (a redis stream ID older than the oldest
// retained entry, or an eventKey no longer present) reports gapDetected
// with oldestAvailableCursor set to the oldest retained entry, matching the
// buffer-served gap semantics. spec: §25.5 (XRANGE polling, cross-source
// cursor translation, evicted-cursor gap).
func (s *Service) redisPollPage(ctx context.Context, cursorKind, position string, filter gwevents.EventFilter, limit int, desc bool) EventPage {
	start, gap, err := s.redisResumePoint(ctx, cursorKind, position)
	if err != nil {
		// A Redis read error mid-poll: report no new events with the caller's
		// cursor echoed so a retry resumes from the same point rather than
		// silently losing position.
		return EventPage{Items: []gwevents.BufferedEvent{}, Pagination: Pagination{CursorKind: SourceKindRedis, Cursor: encodeCursor(SourceKindRedis, position)}}
	}

	// Fetch one past the page so hasMore reflects a further raw entry. The
	// cursor advances by raw stream position; the filter narrows the items
	// shown on the page, so a filtered page may hold fewer than limit items
	// while still advancing.
	entries, err := s.redis.ReadRange(ctx, start, int64(limit)+1)
	if err != nil {
		return EventPage{Items: []gwevents.BufferedEvent{}, Pagination: Pagination{CursorKind: SourceKindRedis, Cursor: encodeCursor(SourceKindRedis, position)}}
	}
	hasMore := len(entries) > limit
	if hasMore {
		entries = entries[:limit]
	}

	items := make([]gwevents.BufferedEvent, 0, len(entries))
	for _, e := range entries {
		if filter.Matches(e.event.Event) {
			items = append(items, e.event)
		}
	}
	if desc {
		items = reversed(items)
	}

	page := EventPage{
		Items: items,
		Pagination: Pagination{
			HasMore:    hasMore,
			CursorKind: redisServedKind(cursorKind),
		},
	}
	if headStreamID, _, found, herr := s.redis.head(ctx); herr == nil && found {
		page.Pagination.HeadCursor = encodeCursor(SourceKindRedis, headStreamID)
	}
	if n := len(entries); n > 0 {
		page.Pagination.Cursor = encodeCursor(SourceKindRedis, entries[n-1].streamID)
	} else if position != "" && !gap {
		page.Pagination.Cursor = encodeCursor(cursorKind, position)
	}
	if gap {
		page.Pagination.GapDetected = true
		page.Pagination.GapReason = "cursor could not be resolved against the current Redis stream: evicted, or minted at a different actualSource"
		if oldestStreamID, _, found, oerr := s.redis.oldest(ctx); oerr == nil && found {
			page.Pagination.OldestAvailableCursor = encodeCursor(SourceKindRedis, oldestStreamID)
		}
		page.Pagination.SuggestedAction = "resync"
		s.observeGap()
	}
	return page
}

// redisResumePoint resolves the incoming cursor to a Redis stream position
// to resume after. It returns the exclusive start stream ID ("" reads from
// the oldest retained entry) and whether the cursor referenced an evicted
// position (a gap). A redis cursor reads by stream ID; any other source's
// cursor is translated by eventKey scan. spec: §25.5 (cross-source cursor
// translation).
func (s *Service) redisResumePoint(ctx context.Context, cursorKind, position string) (start string, gap bool, err error) {
	if position == "" {
		return "", false, nil
	}
	if cursorKind == SourceKindRedis {
		oldestStreamID, _, found, oerr := s.redis.oldest(ctx)
		if oerr != nil {
			return "", false, oerr
		}
		if found && streamIDLess(position, oldestStreamID) {
			// The cursor's stream ID predates the oldest retained entry: the
			// events after it were evicted before this poll read them.
			return "", true, nil
		}
		return position, false, nil
	}
	// A cursor minted by another source (the in-memory buffer) carries an
	// eventKey; translate it to a stream position by scanning.
	streamID, found, ferr := s.redis.findByEventKey(ctx, position)
	if ferr != nil {
		return "", false, ferr
	}
	if !found {
		return "", true, nil
	}
	return streamID, false, nil
}

// redisServedKind reports the §25.5 cursorKind for a page served from the
// Redis stream in response to a cursor produced by cursorKind. A redis (or
// empty) cursor stays "redis"; a cursor from another source served here
// spans a transition and is reported as "mixed". spec: §25.5.
func redisServedKind(cursorKind string) string {
	if cursorKind == "" || cursorKind == SourceKindRedis {
		return SourceKindRedis
	}
	return SourceKindMixed
}

// handleStreamRedis serves the §25.5 SSE stream from the Redis
// ops:events:stream: it resumes the backlog via XRANGE from the resume
// point, then tails live via XREAD BLOCK 0 on a per-connection cursor with
// no consumer group. The resume point honors the cursor's source kind: a
// redis cursor resumes directly by stream ID, while any other cursor (or a
// Last-Event-ID header, always a CloudEvents id) translates by eventKey
// scan. A resume point that has been evicted emits the :gap comment and
// replays from the oldest retained entry. spec: §25.5 (XREAD BLOCK 0 live
// tail, XRANGE resume, cross-source cursor translation, independent
// per-connection cursor).
func (s *Service) handleStreamRedis(w http.ResponseWriter, flusher http.Flusher, r *http.Request, filter gwevents.EventFilter, cursorKind, resumeKey string) {
	ctx := r.Context()

	// Resolve the resume point through the same cross-source translation the
	// poll path uses: a redis cursor reads by stream ID, any other cursor
	// (or a Last-Event-ID CloudEvents id) translates by eventKey scan. A
	// read error resolving the point falls back to replaying the whole
	// retained window with the gap flag set.
	start, gap, rerr := s.redisResumePoint(ctx, cursorKind, resumeKey)
	if rerr != nil {
		start = ""
		gap = true
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	if _, deg, _ := s.streamState(); deg != nil {
		writeSSEDegradation(w, deg)
	}
	if gap {
		writeSSEGap(w, resumeKey)
		s.observeGap()
	}

	// Capture a concrete live-tail resume position BEFORE scanning the
	// backlog, so the tail is contiguous with the scan and drops no event in
	// the seam between the backlog read and the blocking XREAD. Relying on the
	// XREAD "$" sentinel would reopen that seam: "$" resolves server-side at
	// read time, so an entry XADDed after the backlog scan returns but before
	// the read blocks would be neither in the backlog nor after "$", and would
	// be lost. This mirrors the buffer path's subscribe-before-scan guarantee.
	// spec: §25.5 (independent per-connection cursor, contiguous backlog-to-
	// live-tail).
	headStreamID, _, haveHead, headErr := s.redis.head(ctx)

	// Replay the backlog after the resume point (or from the oldest retained
	// entry on a gap), tracking the last stream ID so the live tail continues
	// from exactly where the backlog ended with no overlap.
	lastStreamID := start
	if entries, err := s.redis.ReadRange(ctx, start, 0); err == nil {
		for _, e := range entries {
			if filter.Matches(e.event.Event) {
				writeSSEFrame(w, e.event)
			}
			lastStreamID = e.streamID
		}
	}
	if lastStreamID == "" {
		// Fresh connection with no resume cursor and an empty backlog: resume
		// the tail from the head captured before the scan, or from the stream
		// origin when the stream was empty, never the "$" sentinel.
		lastStreamID = streamOrigin
		if haveHead && headErr == nil {
			lastStreamID = headStreamID
		}
	}
	flusher.Flush()

	// Live tail: each connection runs its own XREAD BLOCK 0 goroutine from
	// its own position, so two subscribers each observe every matching event
	// with no consumer-group competition.
	live := make(chan gwevents.BufferedEvent, 64)
	go s.redis.Tail(ctx, lastStreamID, live)
	for {
		select {
		case <-ctx.Done():
			return
		case ev, open := <-live:
			if !open {
				return
			}
			if !filter.Matches(ev.Event) {
				continue
			}
			writeSSEFrame(w, ev)
			flusher.Flush()
		}
	}
}

// decodeRedisEntry rebuilds a BufferedEvent from one stream entry. The
// StreamEmitter stores the marshalled CloudEvents record under the single
// "event" field; this reads that field back into the same OperationalEvent,
// so the CloudEvents payload every poll item and SSE frame carries decodes
// byte-identically to the buffer-served path. The top-level wrapper
// BufferedEvent.ID is a synthetic per-source position derived from the Redis
// stream ID (see syntheticBufferID), so a Redis-served poll item keeps the
// same {"id":N,"event":{...}} shape the buffer path emits without inventing a
// value that clashes with the buffer's monotonic sequence. Ordering and the
// resume key rest on the Redis stream position (carried in the opaque
// pagination cursor) and the CloudEvents id (Event.ID, the canonical
// eventKey), both identical across sources. spec: §25.5.
func decodeRedisEntry(m redis.XMessage) (gwevents.BufferedEvent, bool) {
	raw, ok := m.Values["event"]
	if !ok {
		return gwevents.BufferedEvent{}, false
	}
	var body []byte
	switch v := raw.(type) {
	case string:
		body = []byte(v)
	case []byte:
		body = v
	default:
		return gwevents.BufferedEvent{}, false
	}
	var ev gwevents.OperationalEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		return gwevents.BufferedEvent{}, false
	}
	return gwevents.BufferedEvent{ID: syntheticBufferID(m.ID), Event: ev}, true
}

// syntheticBufferID packs a Redis "ms-seq" stream ID into a monotonic uint64
// so a Redis-served poll item carries a non-zero top-level wrapper position of
// the same shape the local ring buffer stamps. The high bits carry the
// millisecond timestamp and the low 12 bits the intra-millisecond sequence, so
// the value increases in stream order (Redis assigns seq densely from zero
// within a millisecond and up to 4096 entries per millisecond fit the low
// bits). The wrapper id is informational: cross-source resume keys on the
// CloudEvents id and the opaque pagination cursor rather than this value. A
// malformed ID yields zero. spec: §25.5.
func syntheticBufferID(streamID string) uint64 {
	ms, seq := parseStreamID(streamID)
	return ms<<12 | (seq & 0xFFF)
}

// streamIDLess reports whether stream ID a orders strictly before b. A Redis
// stream ID is "millisecondsTime-sequenceNumber"; the comparison is on the
// (ms, seq) pair. A malformed ID sorts as zero so an unparseable cursor is
// treated as older than any real entry (fail toward reporting a gap rather
// than silently skipping events). spec: §25.5 (evicted-cursor gap).
func streamIDLess(a, b string) bool {
	ams, aseq := parseStreamID(a)
	bms, bseq := parseStreamID(b)
	if ams != bms {
		return ams < bms
	}
	return aseq < bseq
}

// parseStreamID splits a Redis "ms-seq" stream ID into its numeric parts. A
// missing sequence defaults to zero; an unparseable component yields zero.
func parseStreamID(id string) (ms, seq uint64) {
	msPart, seqPart, found := strings.Cut(id, "-")
	ms, _ = strconv.ParseUint(msPart, 10, 64)
	if found {
		seq, _ = strconv.ParseUint(seqPart, 10, 64)
	}
	return ms, seq
}
