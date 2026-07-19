// SPDX-License-Identifier: MIT

package events

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	gwevents "github.com/lennylabs/lenny/pkg/events"
)

// fakeStream is an in-memory RedisStreamClient standing in for a real Redis
// ops:events:stream. It stores entries in stream-ID order and honours the
// XRANGE start/stop bounds (including the "(" exclusive prefix) so the
// §25.5 cursor-translation and gap logic can be exercised deterministically
// at tier 1. XRead is unused by the poll-path tests and returns no data.
type fakeStream struct {
	entries []redis.XMessage
}

func (f *fakeStream) add(id string, ev gwevents.OperationalEvent) {
	body, _ := json.Marshal(ev)
	f.entries = append(f.entries, redis.XMessage{ID: id, Values: map[string]any{"event": string(body)}})
}

// addRaw appends an entry with an arbitrary field map, so a test can inject
// a malformed stream entry (missing or non-decodable "event" field).
func (f *fakeStream) addRaw(id string, values map[string]any) {
	f.entries = append(f.entries, redis.XMessage{ID: id, Values: values})
}

func inRange(id, start, stop string) bool {
	if start != "-" {
		if start[0] == '(' {
			if !streamIDLess(start[1:], id) {
				return false
			}
		} else if streamIDLess(id, start) {
			return false
		}
	}
	if stop != "+" && streamIDLess(stop, id) {
		return false
	}
	return true
}

func (f *fakeStream) XRangeN(ctx context.Context, stream, start, stop string, count int64) *redis.XMessageSliceCmd {
	cmd := redis.NewXMessageSliceCmd(ctx)
	var out []redis.XMessage
	for _, m := range f.entries {
		if inRange(m.ID, start, stop) {
			out = append(out, m)
			if count > 0 && int64(len(out)) >= count {
				break
			}
		}
	}
	cmd.SetVal(out)
	return cmd
}

func (f *fakeStream) XRevRangeN(ctx context.Context, stream, start, stop string, count int64) *redis.XMessageSliceCmd {
	cmd := redis.NewXMessageSliceCmd(ctx)
	var out []redis.XMessage
	for i := len(f.entries) - 1; i >= 0; i-- {
		m := f.entries[i]
		// XREVRANGE bounds are (start=max, stop=min); for the head query
		// start is "+" and stop is "-", so no filtering is needed here.
		out = append(out, m)
		if count > 0 && int64(len(out)) >= count {
			break
		}
	}
	cmd.SetVal(out)
	return cmd
}

func (f *fakeStream) XRead(ctx context.Context, a *redis.XReadArgs) *redis.XStreamSliceCmd {
	cmd := redis.NewXStreamSliceCmd(ctx)
	cmd.SetErr(redis.Nil)
	return cmd
}

func redisService(t *testing.T, f *fakeStream) *Service {
	t.Helper()
	return New(Options{
		RedisClient:  f,
		SourceHealth: StaticSourceHealth{Redis: true, Gateway: true},
		Now:          ts,
	})
}

func evt(key, typ string) gwevents.OperationalEvent {
	return gwevents.OperationalEvent{ID: key, Type: typ, SpecVersion: gwevents.CloudEventsSpecVersion, Time: ts()}
}

// spec: 25.5 (XRANGE polling from the Redis ops:events:stream, opaque
// source-kind cursor) — a poll with an empty cursor serves the retained
// window from the Redis source and returns a redis-kind continuation
// cursor.
func TestRedisPollPage_FirstPageAndResume_spec_25_5(t *testing.T) {
	f := &fakeStream{}
	f.add("1-0", evt("ops:1:1", "dev.lenny.alert_fired"))
	f.add("2-0", evt("ops:2:1", "dev.lenny.alert_fired"))
	f.add("3-0", evt("ops:3:1", "dev.lenny.alert_fired"))
	s := redisService(t, f)

	page := s.pollPage(context.Background(), "", "", gwevents.EventFilter{}, 2, false)
	if page.Pagination.CursorKind != SourceKindRedis {
		t.Fatalf("cursorKind = %q, want %q", page.Pagination.CursorKind, SourceKindRedis)
	}
	if len(page.Items) != 2 {
		t.Fatalf("first page items = %d, want 2", len(page.Items))
	}
	if !page.Pagination.HasMore {
		t.Error("first page should report hasMore with a third entry pending")
	}
	if page.Pagination.GapDetected {
		t.Error("healthy first page must not report a gap")
	}

	// Round-trip the continuation cursor: it must be a redis cursor and the
	// next page must return the remaining entry with no overlap.
	kind, pos, err := decodeCursor(page.Pagination.Cursor)
	if err != nil || kind != SourceKindRedis {
		t.Fatalf("continuation cursor decode = (%q,%q,%v), want redis", kind, pos, err)
	}
	page2 := s.pollPage(context.Background(), kind, pos, gwevents.EventFilter{}, 2, false)
	if len(page2.Items) != 1 {
		t.Fatalf("second page items = %d, want 1", len(page2.Items))
	}
	if page2.Items[0].Event.ID != "ops:3:1" {
		t.Errorf("second page served %q, want the un-paged ops:3:1", page2.Items[0].Event.ID)
	}
	if page2.Pagination.HasMore {
		t.Error("final page must not report hasMore")
	}
}

// spec: 25.5 (cross-source cursor translation by eventKey scan) — a cursor
// minted by the in-memory buffer is honoured against the Redis source by
// scanning for the matching eventKey, and the served page is reported as a
// mixed (cross-source) transition.
func TestRedisPollPage_TranslatesBufferCursorByEventKey_spec_25_5(t *testing.T) {
	f := &fakeStream{}
	f.add("1-0", evt("ops:1:1", "dev.lenny.alert_fired"))
	f.add("2-0", evt("ops:2:1", "dev.lenny.alert_fired"))
	f.add("3-0", evt("ops:3:1", "dev.lenny.alert_fired"))
	s := redisService(t, f)

	// The caller resumes with a buffer-minted cursor pointing at ops:2:1.
	page := s.pollPage(context.Background(), SourceKindBuffer, "ops:2:1", gwevents.EventFilter{}, 10, false)
	if page.Pagination.CursorKind != SourceKindMixed {
		t.Errorf("cross-source page cursorKind = %q, want %q", page.Pagination.CursorKind, SourceKindMixed)
	}
	if len(page.Items) != 1 || page.Items[0].Event.ID != "ops:3:1" {
		t.Fatalf("translated resume served %+v, want only ops:3:1", eventKeys(page.Items))
	}
	if page.Pagination.GapDetected {
		t.Error("a resolvable eventKey must not report a gap")
	}
}

// spec: 25.5 (evicted-cursor gap: gapDetected + oldestAvailableCursor) — a
// redis cursor whose stream ID predates the oldest retained entry reports a
// gap and fires the gap counter, and a buffer cursor whose eventKey is no
// longer present does the same.
func TestRedisPollPage_GapOnEvictedCursor_spec_25_5(t *testing.T) {
	f := &fakeStream{}
	f.add("10-0", evt("ops:10:1", "dev.lenny.alert_fired"))
	f.add("11-0", evt("ops:11:1", "dev.lenny.alert_fired"))
	gaps := 0
	s := New(Options{
		RedisClient:  f,
		SourceHealth: StaticSourceHealth{Redis: true, Gateway: true},
		OnGap:        func() { gaps++ },
		Now:          ts,
	})

	// A redis cursor at stream ID 5-0 predates the oldest retained 10-0.
	page := s.pollPage(context.Background(), SourceKindRedis, "5-0", gwevents.EventFilter{}, 10, false)
	if !page.Pagination.GapDetected {
		t.Fatal("an evicted redis cursor must report gapDetected")
	}
	if page.Pagination.OldestAvailableCursor == "" {
		t.Error("a gap must carry oldestAvailableCursor")
	}
	k, pos, _ := decodeCursor(page.Pagination.OldestAvailableCursor)
	if k != SourceKindRedis || pos != "10-0" {
		t.Errorf("oldestAvailableCursor = (%q,%q), want (redis,10-0)", k, pos)
	}

	// A buffer cursor whose eventKey is absent from the stream also gaps.
	page2 := s.pollPage(context.Background(), SourceKindBuffer, "ops:gone:9", gwevents.EventFilter{}, 10, false)
	if !page2.Pagination.GapDetected {
		t.Error("an unresolvable buffer eventKey must report gapDetected")
	}
	if gaps != 2 {
		t.Errorf("gap counter = %d, want 2 (one per gapped poll)", gaps)
	}
}

// spec: 25.5 (source selection from the degradation matrix) — the Redis
// source is used only when SourceHealth reports Redis reachable; a
// Redis-down health signal falls back to the local ring buffer so a
// nil-Redis or unreachable-Redis deployment keeps serving.
func TestRedisPrimary_SelectedOnlyWhenRedisAvailable_spec_25_5(t *testing.T) {
	f := &fakeStream{}
	f.add("1-0", evt("ops:1:1", "dev.lenny.alert_fired"))
	s := New(Options{RedisClient: f, SourceHealth: StaticSourceHealth{Redis: false, Gateway: true}, Now: ts})
	if s.redisPrimary() {
		t.Fatal("redisPrimary must be false when SourceHealth reports Redis down")
	}
	// With Redis down, a poll serves from the empty local buffer, not Redis.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/admin/events", nil)
	s.HandlePoll(rec, req)
	var page EventPage
	if err := json.NewDecoder(rec.Body).Decode(&page); err != nil {
		t.Fatalf("decode poll body: %v", err)
	}
	if len(page.Items) != 0 {
		t.Errorf("Redis-down poll served %d items from Redis; expected the local buffer path", len(page.Items))
	}
}

// spec: 25.5 (the read side decodes the emitter's single "event" field) — a
// stream entry that lacks a decodable "event" field is skipped rather than
// surfaced as a malformed CloudEvents record, so a corrupt entry never
// reaches an agent as a poll item.
func TestDecodeRedisEntry_SkipsMalformedEntries_spec_25_5(t *testing.T) {
	f := &fakeStream{}
	f.addRaw("1-0", map[string]any{"notevent": "x"})      // no "event" field
	f.addRaw("2-0", map[string]any{"event": 42})          // wrong type
	f.addRaw("3-0", map[string]any{"event": "{not json"}) // undecodable
	f.add("4-0", evt("ops:4:1", "dev.lenny.alert_fired")) // valid
	s := redisService(t, f)

	page := s.pollPage(context.Background(), "", "", gwevents.EventFilter{}, 10, false)
	if len(page.Items) != 1 || page.Items[0].Event.ID != "ops:4:1" {
		t.Fatalf("malformed entries not skipped; served %v", eventKeys(page.Items))
	}
}

// spec: 25.5 / 25.2 (?sortOrder=desc reverses display order; empty-result
// poll echoes the caller's cursor) — a descending poll returns newest-first,
// and a poll that finds no new events past the cursor echoes it so a repeat
// poll resumes from the same point.
func TestRedisPollPage_DescAndEmptyResumeEcho_spec_25_5(t *testing.T) {
	f := &fakeStream{}
	f.add("1-0", evt("ops:1:1", "dev.lenny.alert_fired"))
	f.add("2-0", evt("ops:2:1", "dev.lenny.alert_fired"))
	s := redisService(t, f)

	desc := s.pollPage(context.Background(), "", "", gwevents.EventFilter{}, 10, true)
	if len(desc.Items) != 2 || desc.Items[0].Event.ID != "ops:2:1" {
		t.Fatalf("desc order = %v, want newest-first", eventKeys(desc.Items))
	}

	// Resume from the head: no new entries, so the page is empty and echoes
	// the caller's cursor without a gap.
	empty := s.pollPage(context.Background(), SourceKindRedis, "2-0", gwevents.EventFilter{}, 10, false)
	if len(empty.Items) != 0 {
		t.Fatalf("resume from head returned %d items, want 0", len(empty.Items))
	}
	if empty.Pagination.GapDetected {
		t.Error("resume from the live head must not report a gap")
	}
	k, pos, _ := decodeCursor(empty.Pagination.Cursor)
	if k != SourceKindRedis || pos != "2-0" {
		t.Errorf("empty poll cursor = (%q,%q), want the echoed (redis,2-0)", k, pos)
	}
}

// spec: 25.5 (stream-ID ordering underpinning eviction detection).
func TestStreamIDLess(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"5-0", "10-0", true},
		{"10-0", "5-0", false},
		{"10-0", "10-0", false},
		{"10-0", "10-1", true},
		{"10-2", "10-1", false},
		{"", "1-0", true},
	}
	for _, c := range cases {
		if got := streamIDLess(c.a, c.b); got != c.want {
			t.Errorf("streamIDLess(%q,%q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// spec: 25.5 (the poll envelope served from the Redis ops:events:stream is
// byte-identical to the buffer-served path) — the same events served through
// the local ring buffer and through Redis marshal to the identical items array
// on the wire. The buffer-internal wrapper sequence id (which the buffer path
// stamps as 1,2,3 and the Redis path cannot reproduce) is projected away from
// the poll item on both paths, so a consumer polling /v1/admin/events sees the
// same bytes regardless of which source is active. A regression that leaks the
// wrapper id would make the buffer path emit "id":1,2,3 and the Redis path
// "id":0, and this test would fail.
func TestPollEnvelope_ItemsByteIdenticalAcrossSources_spec_25_5(t *testing.T) {
	events := []gwevents.OperationalEvent{
		evt("ops:1:1", "dev.lenny.alert_fired"),
		evt("ops:2:1", "dev.lenny.alert_fired"),
		evt("ops:2:5", "dev.lenny.alert_fired"),
	}

	// Redis-served page.
	f := &fakeStream{}
	f.add("1-0", events[0])
	f.add("2-0", events[1])
	f.add("2-5", events[2])
	redisItems := itemsJSON(t, redisService(t, f).pollPage(context.Background(), "", "", gwevents.EventFilter{}, 10, false))

	// Buffer-served page: the same events through the local ring buffer, whose
	// wrapper ids are the monotonic 1,2,3 — which must not reach the wire.
	buf := New(Options{Now: ts})
	for _, e := range events {
		if _, err := buf.Publish(context.Background(), e); err != nil {
			t.Fatalf("publish %s: %v", e.ID, err)
		}
	}
	bufferItems := itemsJSON(t, buf.pollPage(context.Background(), "", "", gwevents.EventFilter{}, 10, false))

	if string(redisItems) != string(bufferItems) {
		t.Errorf("poll items diverge by source:\n redis  = %s\n buffer = %s", redisItems, bufferItems)
	}

	// Structurally, no item may carry a top-level wrapper id, and each must
	// carry the CloudEvents record under "event".
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(redisItems, &items); err != nil {
		t.Fatalf("decode items: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("items = %d, want 3", len(items))
	}
	for i, it := range items {
		if _, leaked := it["id"]; leaked {
			t.Errorf("item %d retains the buffer-internal wrapper id on the wire: %s", i, redisItems)
		}
		if _, ok := it["event"]; !ok {
			t.Errorf("item %d missing the CloudEvents event payload: %s", i, redisItems)
		}
	}
}

// itemsJSON marshals a poll page and returns the raw JSON of its items array,
// so a test can compare the wire encoding across sources independent of the
// source-specific pagination cursor.
func itemsJSON(t *testing.T, page EventPage) []byte {
	t.Helper()
	raw, err := json.Marshal(page)
	if err != nil {
		t.Fatalf("marshal page: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal page: %v", err)
	}
	return m["items"]
}

// spec: 25.5 (SSE resume honors the cursor source_kind) — a redis-kind
// ?cursor= (the kind a Redis poll mints, whose position is a Redis stream
// ID) resumes the SSE backlog directly by stream ID: only events after the
// cursor are replayed and no spurious :gap comment is emitted. A
// pre-cancelled request context writes the backlog replay then returns
// before the live tail.
func TestHandleStreamRedis_RedisCursorResumesByStreamID_spec_25_5(t *testing.T) {
	f := &fakeStream{}
	f.add("1-0", evt("ops:1:1", "dev.lenny.alert_fired"))
	f.add("2-0", evt("ops:2:1", "dev.lenny.alert_fired"))
	f.add("3-0", evt("ops:3:1", "dev.lenny.alert_fired"))
	s := redisService(t, f)

	// A redis-kind cursor at stream ID 2-0, as a Redis poll would mint it.
	cursor := encodeCursor(SourceKindRedis, "2-0")
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // backlog-only: replay the resume window, then return.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/admin/events/stream?cursor="+cursor, nil).WithContext(ctx)
	s.HandleStream(rec, req)

	out := rec.Body.String()
	if strings.Contains(out, ":gap") {
		t.Errorf("redis cursor resume emitted a spurious :gap; it was mis-translated as an eventKey:\n%s", out)
	}
	if strings.Contains(out, "ops:1:1") || strings.Contains(out, "ops:2:1") {
		t.Errorf("redis cursor resume replayed pre-cursor events (full-window replay):\n%s", out)
	}
	if !strings.Contains(out, "ops:3:1") {
		t.Errorf("redis cursor resume did not replay the event after the cursor:\n%s", out)
	}
}

// blockingStream models a real Redis XREAD BLOCK against go-redis v9: a
// bounded block returns redis.Nil after the block elapses (or sooner when it
// observes the request context), while BLOCK 0 blocks indefinitely and does
// NOT observe a deadline-free context cancellation, matching go-redis v9,
// which leaves an in-flight blocked read parked in IO wait after the context
// is cancelled. It lets a tier-1 test pin that the live tail issues a bounded
// block so a disconnected connection's goroutine exits rather than leaking.
type blockingStream struct {
	fakeStream
	reads   chan time.Duration
	release chan struct{}
}

func (b *blockingStream) XRead(ctx context.Context, a *redis.XReadArgs) *redis.XStreamSliceCmd {
	select {
	case b.reads <- a.Block:
	default:
	}
	cmd := redis.NewXStreamSliceCmd(ctx)
	if a.Block <= 0 {
		// A literal BLOCK 0: block until the test releases this fake at
		// cleanup, ignoring ctx cancellation as go-redis v9 does.
		<-b.release
		cmd.SetErr(redis.Nil)
		return cmd
	}
	// A bounded block: wake when the block elapses or the read's context is
	// cancelled, then report no new entry so the tail re-checks its context.
	select {
	case <-time.After(a.Block):
	case <-ctx.Done():
	}
	cmd.SetErr(redis.Nil)
	return cmd
}

// spec: 25.5 (XREAD BLOCK per-connection live tail) — the live SSE tail
// issues a bounded XREAD BLOCK so a deadline-free context cancellation tears
// the per-connection goroutine down within one block interval. go-redis v9
// does not interrupt a blocked read on ctx cancellation, so a zero block
// would leave the tail parked in IO wait and leak a goroutine per
// disconnected SSE connection.
func TestRedisTail_BoundedBlockExitsOnCancel_spec_25_5(t *testing.T) {
	b := &blockingStream{reads: make(chan time.Duration, 4), release: make(chan struct{})}
	t.Cleanup(func() { close(b.release) })
	rs := newRedisSource(b, "")
	ctx, cancel := context.WithCancel(context.Background())

	out := make(chan gwevents.BufferedEvent)
	done := make(chan struct{})
	go func() {
		// Drain until Tail closes out on exit.
		for range out {
		}
		close(done)
	}()
	go rs.Tail(ctx, "", out)

	select {
	case got := <-b.reads:
		if got <= 0 {
			t.Fatalf("XREAD Block = %v; a bounded block is required so the tail exits on ctx cancel (BLOCK 0 leaks the goroutine)", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Tail never issued an XREAD")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Tail did not exit within 3s of context cancellation")
	}
}

// startRecordingStream records the starting stream ID the live SSE tail
// passes to XREAD, so a test can assert the tail resumes from a concrete
// position rather than the "$" sentinel. XRead blocks on ctx so the handler's
// live loop exits deterministically on cancel.
type startRecordingStream struct {
	fakeStream
	firstStart chan string
}

func (s *startRecordingStream) XRead(ctx context.Context, a *redis.XReadArgs) *redis.XStreamSliceCmd {
	if len(a.Streams) == 2 {
		select {
		case s.firstStart <- a.Streams[1]:
		default:
		}
	}
	<-ctx.Done()
	cmd := redis.NewXStreamSliceCmd(ctx)
	cmd.SetErr(redis.Nil)
	return cmd
}

// spec: 25.5 (contiguous backlog-to-live-tail seam) — on a fresh SSE
// connection with no resume cursor against an empty stream, the live tail must
// resume from a concrete stream origin ("0"), not the XREAD "$" sentinel. "$"
// resolves server-side at read time, so an event XADDed between the empty
// backlog scan and the blocking read would be neither in the backlog nor after
// "$", and would be dropped. Resuming from "0" reads from the beginning of the
// stream and delivers it. This mirrors the buffer path's subscribe-before-scan
// guarantee. A regression that lets the tail fall back to "$" fails here.
func TestHandleStreamRedis_EmptyStreamTailsFromConcreteOrigin_spec_25_5(t *testing.T) {
	rs := &startRecordingStream{firstStart: make(chan string, 1)}
	s := New(Options{RedisClient: rs, SourceHealth: StaticSourceHealth{Redis: true, Gateway: true}, Now: ts})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/admin/events/stream", nil).WithContext(ctx)
	done := make(chan struct{})
	go func() {
		s.HandleStream(rec, req)
		close(done)
	}()

	select {
	case start := <-rs.firstStart:
		if start == "$" {
			t.Errorf("live tail resumed from the $ sentinel on an empty fresh connect; an event XADDed in the backlog-to-tail seam would be dropped")
		}
		if start != streamOrigin {
			t.Errorf("live tail start = %q, want the concrete stream origin %q", start, streamOrigin)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("live tail never issued an XREAD")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("handler did not exit within 3s of context cancellation")
	}
}

func eventKeys(items []gwevents.BufferedEvent) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Event.ID
	}
	return out
}
