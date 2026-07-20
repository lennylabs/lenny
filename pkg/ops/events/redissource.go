// SPDX-License-Identifier: MIT

package events

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	gwevents "github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/gateway/eventbuffer"
)

// RedisStreamClient is the client surface the §25.5 read-side Redis source
// consumes the ops:events:stream through: XRANGE for polling and SSE backlog
// resume, XREVRANGE to bound the retained window, and a dedicated client per
// live SSE tail. Wrap a go-redis client with NewRedisStreamClient to obtain
// one.
//
// spec: §25.5 — the SSE handler reads Redis via XREAD BLOCK 0 with a
// per-connection XRANGE resume from Last-Event-ID, and polling serves the
// same stream via XRANGE. The read cursor is per-connection and
// independent, with no consumer group.
type RedisStreamClient interface {
	XRangeN(ctx context.Context, stream, start, stop string, count int64) *redis.XMessageSliceCmd
	XRevRangeN(ctx context.Context, stream, start, stop string, count int64) *redis.XMessageSliceCmd
	// TailClient checks out a client dedicated to one live SSE tail. The
	// caller closes it when the tail ends.
	TailClient() (RedisTailClient, error)
}

// RedisTailClient is the client one §25.5 live SSE tail issues its
// XREAD BLOCK 0 on, and closes when the connection ends.
//
// The tail owns a client of its own rather than sharing the read source's,
// because a deadline-free blocked read cannot be interrupted any other way:
// go-redis leaves an in-flight XREAD BLOCK 0 parked in IO wait when the
// request context is cancelled, since it derives the socket read deadline
// from the block argument and the context deadline and neither exists here.
// Closing the tail's own client closes the connection under that read, which
// returns an error and lets the goroutine exit. A shared client cannot be
// closed for one disconnecting connection, and the blocked read occupies its
// connection for the life of the SSE connection either way. spec: §25.5 (the
// SSE handler reads via XREAD BLOCK 0 in a goroutine, one independent read
// cursor per connection).
type RedisTailClient interface {
	XRead(ctx context.Context, a *redis.XReadArgs) *redis.XStreamSliceCmd
	Close() error
}

// NewRedisStreamClient adapts a go-redis client to RedisStreamClient. The
// non-tailing reads run on c's shared connection pool; each live SSE tail
// dials a client of its own from c's options (see RedisTailClient) with a
// single-connection pool, since a tail uses exactly one connection.
//
// It takes the universal client surface so every §17.9.3 Redis topology
// lenny-ops is deployed with reaches the §25.5 stream through the same read
// source: a direct or Sentinel-resolved client, and a Redis Cluster client.
func NewRedisStreamClient(c redis.UniversalClient) RedisStreamClient {
	return redisClientAdapter{UniversalClient: c}
}

// redisClientAdapter is the NewRedisStreamClient adapter. It forwards the
// range reads to the shared client and dials a fresh client per tail.
type redisClientAdapter struct {
	redis.UniversalClient
}

// TailClient dials a client dedicated to one live tail from the shared
// client's connection options, so it reaches the same Redis (including a
// Sentinel-resolved master or a cluster's seed nodes, whose dialer the options
// carry) and honours the same auth and TLS settings. It wraps the dialer to
// keep a handle on the socket the tail blocks on, which is what
// redisTailConn.Close needs. Retries are disabled on the tail client: the tail
// runs its own retry loop, and a client-level retry would re-dial and block
// again after the close that is trying to end the connection.
func (a redisClientAdapter) TailClient() (RedisTailClient, error) {
	tail := &redisTailConn{}
	switch c := a.UniversalClient.(type) {
	case *redis.Client:
		opts := *c.Options()
		opts.PoolSize = 1
		opts.MinIdleConns = 0
		opts.MaxRetries = -1
		opts.Dialer = trackedDialer(opts.Dialer, opts.DialTimeout, opts.TLSConfig, tail)
		tail.client = redis.NewClient(&opts)
	case *redis.ClusterClient:
		opts := *c.Options()
		opts.PoolSize = 1
		opts.MinIdleConns = 0
		opts.MaxRetries = -1
		opts.Dialer = trackedDialer(opts.Dialer, opts.DialTimeout, opts.TLSConfig, tail)
		tail.client = redis.NewClusterClient(&opts)
	default:
		return nil, fmt.Errorf("redis tail client: no per-tail client can be dialled from %T", a.UniversalClient)
	}
	return tail, nil
}

// trackedDialer wraps dial so every socket it opens is recorded on tail, which
// is what lets Close shut the blocked read's socket down directly.
//
// A nil dial is the ordinary case on a Redis Cluster client: go-redis fills a
// default dialer in Options.init(), but ClusterOptions.init() does not, so a
// §17.9.3 Cluster deployment exposes none. Refusing the tail there would leave
// every SSE connection on that topology with the XRANGE backlog and no live
// XREAD BLOCK 0 tail. The fallback installs the dialer go-redis would have
// installed itself, honouring the same dial timeout and TLS settings, so every
// §17.9.3 topology reaches the §25.5 live tail through the same read source.
// spec: §25.5 (XREAD BLOCK 0 per-connection live tail); §17.9.3 (Redis
// topologies).
func trackedDialer(dial func(context.Context, string, string) (net.Conn, error), dialTimeout time.Duration, tlsCfg *tls.Config, tail *redisTailConn) func(context.Context, string, string) (net.Conn, error) {
	if dial == nil {
		dial = defaultTailDial(dialTimeout, tlsCfg)
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := dial(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		tail.track(conn)
		return conn, nil
	}
}

// defaultTailDialTimeout is the dial timeout the tail falls back to when the
// shared client's options carry none, matching the go-redis default.
const defaultTailDialTimeout = 5 * time.Second

// defaultTailDial builds the dialer go-redis installs in Options.init() when a
// client's options carry none: a plain TCP dial, wrapped in TLS when the client
// is configured for it. Doing the TLS handshake here rather than leaving it to
// the client matches go-redis, which performs it inside the dialer and does not
// wrap the configured one.
func defaultTailDial(dialTimeout time.Duration, tlsCfg *tls.Config) func(context.Context, string, string) (net.Conn, error) {
	if dialTimeout <= 0 {
		dialTimeout = defaultTailDialTimeout
	}
	netDialer := &net.Dialer{Timeout: dialTimeout, KeepAlive: 5 * time.Minute}
	if tlsCfg == nil {
		return netDialer.DialContext
	}
	return (&tls.Dialer{NetDialer: netDialer, Config: tlsCfg}).DialContext
}

// redisTailConn is one live tail's dedicated client. It keeps a handle on
// every socket its client dials so Close can shut the socket down directly.
//
// Closing the client alone does not end an in-flight XREAD BLOCK 0: go-redis
// derives the socket read deadline from the block argument and the context
// deadline, and with neither present the read has none, while the pool
// shutdown that would close the socket waits on the connection that read is
// holding. Closing the socket makes the read fail immediately, after which the
// client shuts down without waiting. spec: §25.5 (XREAD BLOCK 0 live tail).
type redisTailConn struct {
	client redis.UniversalClient

	mu     sync.Mutex
	conns  []net.Conn
	closed bool
}

// track records a dialled socket, or closes it outright when the tail has
// already ended so a late redial cannot resurrect the connection.
func (t *redisTailConn) track(conn net.Conn) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		_ = conn.Close()
		return
	}
	t.conns = append(t.conns, conn)
}

// XRead issues the tail's blocking read on its own connection.
func (t *redisTailConn) XRead(ctx context.Context, a *redis.XReadArgs) *redis.XStreamSliceCmd {
	return t.client.XRead(ctx, a)
}

// Close ends the tail's blocked read and releases its client. Safe to call
// more than once and from another goroutine than the one inside XRead.
func (t *redisTailConn) Close() error {
	t.mu.Lock()
	already := t.closed
	t.closed = true
	conns := t.conns
	t.conns = nil
	t.mu.Unlock()
	for _, conn := range conns {
		_ = conn.Close()
	}
	if already {
		return nil
	}
	return t.client.Close()
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
	// maxWindow caps a single XRANGE scan at the stream's retained length, so
	// a poll, a backlog replay, or a cursor translation reads the whole
	// retained window and never materialises an unbounded slice. It is the
	// MAXLEN the writer side trims the stream to, so a scan that finds no
	// entry at or after a cursor establishes that the cursor was evicted
	// rather than that the scan stopped short of it. spec: §25.5
	// (cross-source cursor translation; gapDetected on an evicted cursor).
	maxWindow int64
}

// newRedisSource returns a redisSource reading stream from client, scanning at
// most maxWindow entries per range read. An empty stream falls back to the
// §25.5 default ops:events:stream key; a non-positive maxWindow falls back to
// the default stream length.
func newRedisSource(client RedisStreamClient, stream string, maxWindow int64) *redisSource {
	if stream == "" {
		stream = eventbuffer.DefaultStreamKey
	}
	if maxWindow <= 0 {
		maxWindow = eventbuffer.DefaultStreamMaxLen
	}
	return &redisSource{client: client, stream: stream, maxWindow: maxWindow}
}

// redisEntry is one decoded stream entry paired with its Redis stream ID.
// The stream ID is the internal read position this source pages and tails
// from; it never leaves the source, because the opaque pagination cursor
// carries the canonical eventKey so it round-trips at the other sources. The
// event is the transport-neutral BufferedEvent the poll and SSE surfaces
// serve. Its top-level wrapper ID is left zero, because that field is the local
// ring's per-replica sequence and the Redis stream carries no such position:
// one endpoint emits one value domain in items[].id. Callers resume on the
// CloudEvents id (Event.ID, the canonical eventKey) and the opaque pagination
// cursor, both identical across sources.
type redisEntry struct {
	streamID string
	event    gwevents.BufferedEvent
}

// redisReadTimeout bounds every non-tailing Redis read a single poll or SSE
// setup issues (the cursor resolve, the head/oldest bounds, and the backlog
// XRANGE). The source-health probe refreshes on an interval, so a request
// arriving inside the window between a Redis outage starting and the probe
// observing it still selects the Redis source; without a deadline that
// request blocks on the client's connection retries for tens of seconds and
// the caller times out instead of receiving a page. The deadline caps that
// window: the read fails fast, the request serves what it has, and the next
// probe refresh moves the source to the gateway-buffer fall-back. spec:
// §25.5 (the read surface degrades rather than blocking).
const redisReadTimeout = 2 * time.Second

// boundRedisRead derives the per-request Redis read deadline from ctx. The
// live SSE tail is deliberately excluded: it blocks by design.
func boundRedisRead(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, redisReadTimeout)
}

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
		count = rs.maxWindow
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

// resumeByEventKey translates a cursor minted by another source into the
// exclusive Redis stream position to resume after, and reports whether the
// cursor referenced a position the stream no longer retains.
//
// §25.5 locates the continuation point in the new source rather than requiring
// the carried eventKey to be present verbatim. An exact match resumes
// immediately after that entry. A key that is absent but falls inside the
// retained window resumes after the last retained entry whose eventKey orders
// at or before the cursor, so the events after the cursor are delivered without
// replaying the window. That case is the ordinary one on a source switch, where
// the last delivered event originated somewhere that never XADDed it.
//
// The scan covers the whole retained window rather than stopping at the first
// entry ordering after the cursor, because stream order and eventKey order do
// not always agree. The best-effort recovery flush re-emits events buffered
// during a Redis outage with their original eventKeys, so those entries land at
// the stream tail carrying keys older than entries already sitting at earlier
// positions. Stopping at the first greater key would resolve the position to
// somewhere before that out-of-order tail, rewinding a caller that had already
// read past it and re-delivering the whole retained window on every subsequent
// read. Taking the last entry at or before the cursor keeps the position
// forward-only across the flush.
//
// The gap covers both ends of the retained window. A key ordering before the
// oldest retained entry lost the events in between, which were evicted before
// this caller read them. A key ordering after every retained entry is the
// condition §25.5 names directly: no event in the new source has a
// greater-or-equal eventKey, so the source cannot honour the position and the
// caller is told to re-read platform state. Neither gap replays: the resume
// position is still the continuation point, so the flag is a signal rather than
// a redelivery. A stream retaining nothing is not a gap, since an empty source
// carries no evidence either way. spec: §25.5 (cross-source cursor translation;
// gapDetected on an evicted cursor; cursor transition safety when no event has
// a greater-or-equal eventKey).
func (rs *redisSource) resumeByEventKey(ctx context.Context, eventKey string) (start string, gap bool, err error) {
	if eventKey == "" {
		return "", false, nil
	}
	msgs, err := rs.client.XRangeN(ctx, rs.stream, "-", "+", rs.maxWindow).Result()
	if err != nil {
		return "", false, fmt.Errorf("xrange scan %s: %w", rs.stream, err)
	}
	var (
		// prev is the stream ID of the last entry whose eventKey orders at or
		// before the cursor: the position to resume after.
		prev string
		// lowestKey is the smallest retained eventKey, which bounds the window
		// below. It is tracked by order rather than by stream position, since
		// the recovery flush can place an older key at the tail.
		lowestKey string
		// atOrAfter records that some retained entry orders after the cursor,
		// so the stream can honour the position.
		atOrAfter bool
		decoded   bool
	)
	for _, m := range msgs {
		ev, ok := decodeRedisEntry(m)
		if !ok {
			continue
		}
		key := ev.Event.ID
		decoded = true
		if lowestKey == "" || eventKeyLess(key, lowestKey) {
			lowestKey = key
		}
		if key == eventKey {
			return m.ID, false, nil
		}
		if eventKeyLess(eventKey, key) {
			atOrAfter = true
			continue
		}
		prev = m.ID
	}
	if !decoded {
		// An empty stream carries no evidence either way, so it is not a gap.
		return "", false, nil
	}
	if atOrAfter {
		// A cursor ordering before every retained entry lost the events in
		// between, which were evicted before this caller read them.
		return prev, eventKeyLess(eventKey, lowestKey), nil
	}
	// Every retained entry orders before the cursor, so no entry has a
	// greater-or-equal eventKey and the stream cannot honour the position. The
	// next read still starts after the newest entry, so nothing is replayed.
	return prev, true, nil
}

// Tail streams live events to out via XREAD BLOCK 0 from lastStreamID, closing
// out when it returns. Each SSE connection runs its own Tail on its own client
// from its own starting position, so the per-connection cursor is independent
// and no consumer group is created.
//
// It returns nil when the tail ran and ended because ctx was cancelled, and a
// non-nil error when the tail could never be established. The caller must be
// able to tell the two apart: a closed channel alone would make an ordinary
// disconnect and a client that cannot be checked out look identical, and the
// SSE source loop would re-enter the same unstartable source immediately,
// re-running the full retained-window scans on every iteration. spec: §25.5
// (the SSE source loop switches on a SourceHealth transition or a closed
// connection).
//
// The tail's client is closed when ctx is cancelled, which is what ends the
// blocked read: go-redis does not interrupt a deadline-free XREAD on context
// cancellation, so without the close a disconnected connection's goroutine
// would park in IO wait indefinitely (see RedisTailClient). A transient Redis
// error is retried after a short pause rather than closing the channel,
// matching the §25.5 live-tail resilience the SSE surface depends on. spec:
// §25.5 (XREAD BLOCK 0 per-connection live tail).
func (rs *redisSource) Tail(ctx context.Context, lastStreamID string, out chan<- gwevents.BufferedEvent) error {
	defer close(out)

	client, err := rs.client.TailClient()
	if err != nil {
		return fmt.Errorf("check out tail client for %s: %w", rs.stream, err)
	}
	defer func() { _ = client.Close() }()

	// Close the tail's client on cancellation so the in-flight blocked read
	// returns. The watchdog exits with the tail, so it never outlives it.
	stopped := make(chan struct{})
	defer close(stopped)
	go func() {
		select {
		case <-ctx.Done():
			_ = client.Close()
		case <-stopped:
		}
	}()

	lastID := lastStreamID
	if lastID == "" {
		// "$" tails only entries that arrive after this call, so a backlog
		// already replayed by ReadRange is not re-delivered.
		lastID = "$"
	}
	for {
		if ctx.Err() != nil {
			return nil
		}
		res, err := client.XRead(ctx, &redis.XReadArgs{
			Streams: []string{rs.stream, lastID},
			Block:   0,
			Count:   rs.maxWindow,
		}).Result()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			// A transient Redis error: pause briefly and retry so a blip
			// does not tear down the live connection.
			select {
			case <-ctx.Done():
				return nil
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
					return nil
				}
			}
		}
	}
}

// redisPollPage serves the §25.5 polling page from the Redis
// ops:events:stream. Every incoming cursor carries the canonical eventKey
// whichever source minted it, so one translation resolves them all: the scan
// stops at the first retained entry ordering at or after that key. A cursor
// ordering before the oldest retained entry reports gapDetected with
// oldestAvailableCursor set to that entry's eventKey, matching the
// buffer-served gap semantics. spec: §25.5 (XRANGE polling, cross-source
// cursor translation, evicted-cursor gap).
// A Redis read error is returned rather than served as an empty page: the
// source-health probe refreshes on an interval, so a request arriving inside
// the window between Redis failing and the probe observing it still selects the
// Redis source, and an empty page carrying the caller's cursor and no
// degradation envelope is byte-indistinguishable from a healthy idle poll. The
// caller reclassifies the request onto the source that can serve it. spec:
// §25.5 (actualSource names the source the response was served from).
func (s *Service) redisPollPage(ctx context.Context, cursorKind, position string, filter gwevents.EventFilter, limit int, desc bool) (EventPage, error) {
	ctx, cancel := boundRedisRead(ctx)
	defer cancel()
	start, gap, err := s.redisResumePoint(ctx, position)
	if err != nil {
		return EventPage{}, fmt.Errorf("resolve redis cursor: %w", err)
	}

	// Fetch one past the page so hasMore reflects a further raw entry. The
	// cursor advances by raw stream position; the filter narrows the items
	// shown on the page, so a filtered page may hold fewer than limit items
	// while still advancing.
	entries, err := s.redis.ReadRange(ctx, start, int64(limit)+1)
	if err != nil {
		return EventPage{}, fmt.Errorf("read redis page: %w", err)
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
	if _, headKey, found, herr := s.redis.head(ctx); herr == nil && found {
		page.Pagination.HeadCursor = encodeCursor(SourceKindRedis, headKey)
	}
	if n := len(entries); n > 0 {
		page.Pagination.Cursor = encodeCursor(SourceKindRedis, entries[n-1].event.Event.ID)
	} else if position != "" && !gap {
		page.Pagination.Cursor = encodeCursor(cursorKind, position)
	}
	if gap {
		page.Pagination.GapDetected = true
		page.Pagination.GapReason = "cursor could not be resolved against the current Redis stream: evicted, or minted at a different actualSource"
		if _, oldestKey, found, oerr := s.redis.oldest(ctx); oerr == nil && found {
			page.Pagination.OldestAvailableCursor = encodeCursor(SourceKindRedis, oldestKey)
		}
		page.Pagination.SuggestedAction = "resync"
		s.observeGap()
	}
	return page, nil
}

// redisResumePoint resolves the incoming cursor to a Redis stream position to
// resume after. It returns the exclusive start stream ID ("" reads from the
// oldest retained entry) and whether the cursor referenced an evicted position
// (a gap).
//
// Every cursor, whichever source minted it, carries the canonical eventKey, so
// one translation serves them all: scan for the first retained entry ordering
// at or after that key (see resumeByEventKey). A Redis-minted cursor is not a
// special case — encoding the Redis stream ID instead would make the cursor
// untranslatable at the gateway-buffer and local-ring sources, which compare
// positions as eventKeys and would read a stream ID as ordering before their
// whole retained window. spec: §25.5 (opaque cursor carries the canonical
// eventKey; cross-source cursor translation).
func (s *Service) redisResumePoint(ctx context.Context, position string) (start string, gap bool, err error) {
	if position == "" {
		return "", false, nil
	}
	return s.redis.resumeByEventKey(ctx, position)
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

// decodeRedisEntry rebuilds a BufferedEvent from one stream entry. The
// StreamEmitter stores the marshalled CloudEvents record under the single
// "event" field; this reads that field back into the same OperationalEvent,
// so the CloudEvents payload every poll item and SSE frame carries decodes
// byte-identically to the buffer-served path. The top-level wrapper
// BufferedEvent.ID is a synthetic per-source position derived from the Redis
// BufferedEvent.ID is left zero: it is the local ring's per-replica sequence,
// and the Redis stream holds no such position. Stamping a value derived from
// the stream ID there would put a second value domain behind the same
// items[].id field, so the same client paging across a source switch would see
// the field jump between a small ring sequence and a millisecond-derived
// integer. Ordering and the resume key rest on the opaque pagination cursor and
// the CloudEvents id (Event.ID, the canonical eventKey), both identical across
// sources. spec: §25.5.
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
	return gwevents.BufferedEvent{Event: ev}, true
}
