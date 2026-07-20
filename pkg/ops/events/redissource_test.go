// SPDX-License-Identifier: MIT

package events

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
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

// streamIDLess orders two Redis "ms-seq" stream IDs, which the in-memory
// fakeStream needs to honour XRANGE bounds. The read path itself orders by
// eventKey, so this lives with the fake rather than in the source.
func streamIDLess(a, b string) bool {
	ams, aseq := splitStreamID(a)
	bms, bseq := splitStreamID(b)
	if ams != bms {
		return ams < bms
	}
	return aseq < bseq
}

// splitStreamID splits a Redis "ms-seq" stream ID into its numeric parts for
// streamIDLess. An unparseable component yields zero.
func splitStreamID(id string) (ms, seq uint64) {
	msPart, seqPart, found := strings.Cut(id, "-")
	ms, _ = strconv.ParseUint(msPart, 10, 64)
	if found {
		seq, _ = strconv.ParseUint(seqPart, 10, 64)
	}
	return ms, seq
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

// TailClient hands the live tail a client that parks like a real BLOCK 0 read
// until the tail closes it, so a tail over the fake neither spins nor delivers.
func (f *fakeStream) TailClient() (RedisTailClient, error) { return newParkedTailClient(), nil }

// parkedTailClient models a real Redis connection under XREAD BLOCK 0: the
// read parks indefinitely and, matching go-redis, does not observe a
// deadline-free context cancellation. Only Close ends it. It is what lets a
// tier-1 test pin that the tail closes its own client rather than relying on
// the context to unblock the read.
type parkedTailClient struct {
	closed chan struct{}
	once   sync.Once
	// blocks records the XREAD BLOCK argument of every read it served.
	blocks chan time.Duration
}

func newParkedTailClient() *parkedTailClient {
	return &parkedTailClient{closed: make(chan struct{}), blocks: make(chan time.Duration, 4)}
}

func (c *parkedTailClient) XRead(ctx context.Context, a *redis.XReadArgs) *redis.XStreamSliceCmd {
	select {
	case c.blocks <- a.Block:
	default:
	}
	<-c.closed
	cmd := redis.NewXStreamSliceCmd(ctx)
	cmd.SetErr(errors.New("redis: client is closed"))
	return cmd
}

func (c *parkedTailClient) Close() error {
	c.once.Do(func() { close(c.closed) })
	return nil
}

// pollActiveSource serves one poll page from the source the live
// SourceHealth signal selects, the way HandlePoll resolves it, so a poll-path
// test exercises source selection as well as the page it produces. The context
// carries a platform-admin read scope, the grant a caller needs to observe the
// whole window through the §25.5 read-endpoint tenant filter.
func pollActiveSource(s *Service, ctx context.Context, cursorKind, position string, filter gwevents.EventFilter, limit int, desc bool) EventPage {
	return pollActiveSourceCursor(s, ctx, eventCursor{Kind: cursorKind, EventKey: position}, filter, limit, desc)
}

// pollActiveSourceCursor is pollActiveSource driven by a fully decoded cursor,
// so a test can carry the Redis stream ID a redis-kind cursor holds.
func pollActiveSourceCursor(s *Service, ctx context.Context, cur eventCursor, filter gwevents.EventFilter, limit int, desc bool) EventPage {
	src, deg, _ := s.selectSource()
	page, _, _ := s.pollPage(WithReaderScope(ctx, "alice@acme.com", "", true), src, deg, cur, filter, limit, desc)
	return page
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

	page := pollActiveSource(s, context.Background(), "", "", gwevents.EventFilter{}, 2, false)
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
	cur, err := decodeCursor(page.Pagination.Cursor)
	if err != nil || cur.Kind != SourceKindRedis {
		t.Fatalf("continuation cursor decode = (%+v,%v), want redis", cur, err)
	}
	page2 := pollActiveSourceCursor(s, context.Background(), cur, gwevents.EventFilter{}, 2, false)
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
	page := pollActiveSource(s, context.Background(), SourceKindBuffer, "ops:2:1", gwevents.EventFilter{}, 10, false)
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
// redis cursor whose eventKey orders before the oldest retained entry reports a
// gap and fires the gap counter, and a buffer cursor whose eventKey orders
// before the oldest retained event does the same.
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

	// A redis cursor whose eventKey predates the oldest retained ops:10:1.
	page := pollActiveSource(s, context.Background(), SourceKindRedis, "ops:5:1", gwevents.EventFilter{}, 10, false)
	if !page.Pagination.GapDetected {
		t.Fatal("an evicted redis cursor must report gapDetected")
	}
	if page.Pagination.OldestAvailableCursor == "" {
		t.Error("a gap must carry oldestAvailableCursor")
	}
	oldest, _ := decodeCursor(page.Pagination.OldestAvailableCursor)
	if oldest.Kind != SourceKindRedis || oldest.EventKey != "ops:10:1" {
		t.Errorf("oldestAvailableCursor = (%q,%q), want (redis,ops:10:1)", oldest.Kind, oldest.EventKey)
	}

	// A buffer cursor whose eventKey orders before the oldest retained event
	// also gaps: the events between it and the window were evicted.
	page2 := pollActiveSource(s, context.Background(), SourceKindBuffer, "ops:5:1", gwevents.EventFilter{}, 10, false)
	if !page2.Pagination.GapDetected {
		t.Error("a buffer eventKey older than the retained window must report gapDetected")
	}
	if gaps != 2 {
		t.Errorf("gap counter = %d, want 2 (one per gapped poll)", gaps)
	}
}

// spec: 25.5 (cross-source cursor translation: the continuation point is the
// first event ordering at or after the carried eventKey) — a cursor whose
// eventKey is absent from the Redis stream but orders inside its retained
// window is the ordinary source-switch case: the last event the caller
// consumed came from a source that never XADDed it. The read resumes at the
// next event in order, with no gap and no replay. The pre-fix resume required
// the key verbatim and treated every miss as an eviction, so it reported a gap
// and re-served the whole retained window; this fails against that code.
func TestRedisPollPage_ContinuesFromAbsentKeyInsideWindow_spec_25_5(t *testing.T) {
	f := &fakeStream{}
	f.add("10-0", evt("gw:10:1", "dev.lenny.alert_fired"))
	f.add("12-0", evt("gw:12:1", "dev.lenny.alert_fired"))
	gaps := 0
	s := New(Options{
		RedisClient:  f,
		SourceHealth: StaticSourceHealth{Redis: true, Gateway: true},
		OnGap:        func() { gaps++ },
		Now:          ts,
	})

	// ops:11:1 is a lenny-ops event emitted while Redis was down, so it never
	// reached the stream, but it orders between the two retained entries.
	page := pollActiveSource(s, context.Background(), SourceKindBuffer, "ops:11:1", gwevents.EventFilter{}, 10, false)
	if page.Pagination.GapDetected {
		t.Error("a cursor ordering inside the retained window must not report a gap")
	}
	if gaps != 0 {
		t.Errorf("gap counter = %d, want 0 for an ordinary source switch", gaps)
	}
	if len(page.Items) != 1 || page.Items[0].Event.ID != "gw:12:1" {
		t.Fatalf("resumed page = %v, want only gw:12:1 (the continuation, not the whole window)", eventKeys(page.Items))
	}
}

// spec: 25.5 (cursor transition safety: a gap is reported when no event in the
// new source has a greater-or-equal eventKey) — a cursor ordering after every
// retained entry cannot be located in this source, so the page reports the gap
// and serves nothing. The pre-fix translation reported a gap only for a cursor
// ordering before the oldest retained entry, so a caller ahead of the whole
// window resumed silently and neither the :gap signal nor the gap counter
// fired; this fails against that code.
func TestRedisPollPage_GapWhenNoEntryOrdersAtOrAfterCursor_spec_25_5(t *testing.T) {
	f := &fakeStream{}
	f.add("10-0", evt("gw:10:1", "dev.lenny.alert_fired"))
	gaps := 0
	s := New(Options{
		RedisClient:  f,
		SourceHealth: StaticSourceHealth{Redis: true, Gateway: true},
		OnGap:        func() { gaps++ },
		Now:          ts,
	})

	page := pollActiveSource(s, context.Background(), SourceKindBuffer, "ops:99:1", gwevents.EventFilter{}, 10, false)
	if !page.Pagination.GapDetected {
		t.Error("a cursor ordering after every retained entry must report gapDetected")
	}
	if gaps != 1 {
		t.Errorf("gap counter = %d, want 1", gaps)
	}
	if len(page.Items) != 0 {
		t.Fatalf("served %v, want an empty page: the caller already holds the whole window", eventKeys(page.Items))
	}
}

// spec: 25.5 (cross-source cursor translation; cursor transition safety) — the
// translation resolves a carried eventKey to a resume position and reports a
// gap at both ends of the retained window: below it the events in between were
// evicted, above it no retained entry has a greater-or-equal eventKey. A cursor
// inside the window, present or absent, is an ordinary continuation. The pre-fix
// predicate reported a gap on the below-window case alone, so the
// after-the-window row fails against it.
func TestResumeByEventKey_GapAtBothEndsOfTheRetainedWindow_spec_25_5(t *testing.T) {
	newStream := func() *fakeStream {
		f := &fakeStream{}
		f.add("10-0", evt("gw:10:1", "dev.lenny.alert_fired"))
		f.add("12-0", evt("gw:12:1", "dev.lenny.alert_fired"))
		return f
	}
	for _, tc := range []struct {
		name      string
		empty     bool
		cursor    string
		wantStart string
		wantGap   bool
	}{
		{name: "before the window", cursor: "gw:5:1", wantStart: "", wantGap: true},
		{name: "absent inside the window", cursor: "ops:11:1", wantStart: "10-0", wantGap: false},
		{name: "exactly on a retained entry", cursor: "gw:10:1", wantStart: "10-0", wantGap: false},
		{name: "on the newest retained entry", cursor: "gw:12:1", wantStart: "12-0", wantGap: false},
		{name: "after the window", cursor: "ops:99:1", wantStart: "12-0", wantGap: true},
		{name: "empty cursor", cursor: "", wantStart: "", wantGap: false},
		{name: "empty stream", empty: true, cursor: "ops:99:1", wantStart: "", wantGap: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newStream()
			if tc.empty {
				f = &fakeStream{}
			}
			rs := newRedisSource(f, "ops:events:stream", 0)
			start, gap, err := rs.resumeByEventKey(context.Background(), tc.cursor)
			if err != nil {
				t.Fatalf("resume: %v", err)
			}
			if start != tc.wantStart {
				t.Errorf("resume start = %q, want %q", start, tc.wantStart)
			}
			if gap != tc.wantGap {
				t.Errorf("gap = %v, want %v", gap, tc.wantGap)
			}
		})
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
	s.HandlePoll(rec, platformAdminReq(req))
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

	page := pollActiveSource(s, context.Background(), "", "", gwevents.EventFilter{}, 10, false)
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

	desc := pollActiveSource(s, context.Background(), "", "", gwevents.EventFilter{}, 10, true)
	if len(desc.Items) != 2 || desc.Items[0].Event.ID != "ops:2:1" {
		t.Fatalf("desc order = %v, want newest-first", eventKeys(desc.Items))
	}

	// Resume from the head: no new entries, so the page is empty and echoes
	// the caller's cursor without a gap.
	const headKey = "ops:2:1"
	empty := pollActiveSource(s, context.Background(), SourceKindRedis, headKey, gwevents.EventFilter{}, 10, false)
	if len(empty.Items) != 0 {
		t.Fatalf("resume from head returned %d items, want 0", len(empty.Items))
	}
	if empty.Pagination.GapDetected {
		t.Error("resume from the live head must not report a gap")
	}
	echoed, _ := decodeCursor(empty.Pagination.Cursor)
	if echoed.Kind != SourceKindRedis || echoed.EventKey != headKey {
		t.Errorf("empty poll cursor = (%q,%q), want the echoed (redis,%s)", echoed.Kind, echoed.EventKey, headKey)
	}
}

// spec: 25.5 (the poll envelope served from the Redis ops:events:stream keeps
// the buffer-served item encoding); 25.3 (each event is assigned a monotonic
// uint64 ID, per source rather than globally ordered, used for cursor-based
// polling) — the /v1/admin/events item is {"id":N,"event":{...}} whichever
// source is active, and the CloudEvents record under "event" is byte-identical
// across sources. The wrapper id is the serving source's own monotonic
// position: the local ring's sequence on the buffer path, the Redis stream
// position on the Redis path. It rises with the page on both, which is what a
// caller ordering on it relies on. A regression that replaced either path's
// position with a source-independent synthetic identity, such as a hash of the
// eventKey, would break the monotonicity assertion below.
func TestPollEnvelopeItemIDCarriesTheServingSourcePosition_spec_25_5_25_3(t *testing.T) {
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
	redisItems := decodeItems(t, itemsJSON(t, pollActiveSource(redisService(t, f), context.Background(), "", "", gwevents.EventFilter{}, 10, false)))

	// Buffer-served page: the same events through the local ring buffer, whose
	// wrapper ids are the monotonic 1,2,3.
	buf := New(Options{Now: ts})
	for _, e := range events {
		if _, err := buf.Publish(context.Background(), e); err != nil {
			t.Fatalf("publish %s: %v", e.ID, err)
		}
	}
	bufferItems := decodeItems(t, itemsJSON(t, pollActiveSource(buf, context.Background(), "", "", gwevents.EventFilter{}, 10, false)))

	if len(redisItems) != 3 || len(bufferItems) != 3 {
		t.Fatalf("items: redis=%d buffer=%d, want 3 each", len(redisItems), len(bufferItems))
	}
	var prevRedisID, prevBufferID uint64
	for i := range redisItems {
		// Each item on each source carries a top-level wrapper id (the frozen
		// {"id":N,"event":{...}} shape) and the CloudEvents record under "event".
		for _, side := range []struct {
			name string
			item map[string]json.RawMessage
		}{{"redis", redisItems[i]}, {"buffer", bufferItems[i]}} {
			if _, ok := side.item["id"]; !ok {
				t.Errorf("%s item %d dropped its top-level wrapper id: %v", side.name, i, side.item)
			}
			if _, ok := side.item["event"]; !ok {
				t.Errorf("%s item %d missing the CloudEvents event payload: %v", side.name, i, side.item)
			}
		}
		// The CloudEvents payload is byte-identical across sources.
		if string(redisItems[i]["event"]) != string(bufferItems[i]["event"]) {
			t.Errorf("item %d CloudEvents record diverges by source:\n redis  = %s\n buffer = %s", i, redisItems[i]["event"], bufferItems[i]["event"])
		}
		// The wrapper id is the serving source's own monotonic position, so it
		// rises with the page on each source independently.
		var redisID, bufferID uint64
		if err := json.Unmarshal(redisItems[i]["id"], &redisID); err != nil {
			t.Errorf("redis item %d wrapper id did not decode as a number: %v", i, err)
		}
		if err := json.Unmarshal(bufferItems[i]["id"], &bufferID); err != nil {
			t.Errorf("buffer item %d wrapper id did not decode as a number: %v", i, err)
		}
		if redisID == 0 || bufferID == 0 {
			t.Errorf("item %d wrapper id is zero on a served record: redis=%d buffer=%d", i, redisID, bufferID)
		}
		if i > 0 {
			if redisID <= prevRedisID {
				t.Errorf("redis item %d wrapper id = %d, want greater than the previous item's %d; §25.3 assigns a monotonic per-source id", i, redisID, prevRedisID)
			}
			if bufferID <= prevBufferID {
				t.Errorf("buffer item %d wrapper id = %d, want greater than the previous item's %d; §25.3 assigns a monotonic per-source id", i, bufferID, prevBufferID)
			}
		}
		prevRedisID, prevBufferID = redisID, bufferID
	}
}

// decodeItems unmarshals a raw items array into a slice of field maps so a
// test can inspect each item's top-level fields (the wrapper id and the
// CloudEvents record) independently.
func decodeItems(t *testing.T, raw []byte) []map[string]json.RawMessage {
	t.Helper()
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		t.Fatalf("decode items: %v", err)
	}
	return items
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

// spec: 25.5 (the opaque cursor carries the canonical eventKey; cross-source
// cursor translation) — a redis-kind ?cursor= as a Redis poll mints it
// resumes the SSE backlog at the continuation point: only events after the
// carried eventKey are replayed and no spurious :gap comment is emitted. A
// pre-cancelled request context writes the backlog replay then returns
// before the live tail.
func TestHandleStreamRedis_RedisCursorResumes_spec_25_5(t *testing.T) {
	f := &fakeStream{}
	f.add("1-0", evt("ops:1:1", "dev.lenny.alert_fired"))
	f.add("2-0", evt("ops:2:1", "dev.lenny.alert_fired"))
	f.add("3-0", evt("ops:3:1", "dev.lenny.alert_fired"))
	s := redisService(t, f)

	// A redis-kind cursor carrying the eventKey of the second entry, as a
	// Redis poll mints it.
	cursor := encodeCursor(SourceKindRedis, "ops:2:1")
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // backlog-only: replay the resume window, then return.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/admin/events/stream?cursor="+cursor, nil).WithContext(ctx)
	s.HandleStream(rec, platformAdminReq(req))

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

// tailClientStream is a fakeStream that hands out one recording tail client a
// test can inspect, so the XREAD the live tail issues and the close that ends
// it are both observable.
type tailClientStream struct {
	fakeStream
	tail *parkedTailClient
}

func (t *tailClientStream) TailClient() (RedisTailClient, error) { return t.tail, nil }

// spec: 25.5 ("The SSE handler ... reads from the Redis stream via XREAD BLOCK
// 0 in a goroutine") — the per-connection live tail issues the blocking read
// the spec names, with no client-side block bound, and ends it by closing the
// connection it owns. go-redis does not interrupt a deadline-free blocked read
// on context cancellation, so a tail that relied on the context alone would
// park a goroutine per disconnected SSE connection; the parked fake here
// ignores the context exactly as go-redis does, so a tail that does not close
// its own client never returns. A tail that substitutes a bounded block for
// BLOCK 0 fails the argument assertion.
func TestRedisTail_IssuesBlockZeroAndClosesItsClientOnCancel_spec_25_5(t *testing.T) {
	c := newParkedTailClient()
	rs := newRedisSource(&tailClientStream{tail: c}, "", 0)
	ctx, cancel := context.WithCancel(context.Background())

	out := make(chan gwevents.BufferedEvent)
	done := make(chan struct{})
	go func() {
		for range out {
		}
		close(done)
	}()
	go func() { _ = rs.Tail(ctx, "", out) }()

	select {
	case got := <-c.blocks:
		if got != 0 {
			t.Fatalf("XREAD Block = %v; §25.5 states the per-connection live tail as XREAD BLOCK 0", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Tail never issued an XREAD")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Tail did not exit within 3s of cancellation; it must close its own client to end the blocked read")
	}
	select {
	case <-c.closed:
	default:
		t.Error("Tail left its dedicated client open after the connection was cancelled")
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

func (s *startRecordingStream) TailClient() (RedisTailClient, error) {
	return &startRecordingTail{parked: newParkedTailClient(), firstStart: s.firstStart}, nil
}

// startRecordingTail records the starting stream ID of each XREAD and then
// parks like a real BLOCK 0 read until the tail closes it.
type startRecordingTail struct {
	parked     *parkedTailClient
	firstStart chan string
}

func (s *startRecordingTail) XRead(ctx context.Context, a *redis.XReadArgs) *redis.XStreamSliceCmd {
	if len(a.Streams) == 2 {
		select {
		case s.firstStart <- a.Streams[1]:
		default:
		}
	}
	return s.parked.XRead(ctx, a)
}

func (s *startRecordingTail) Close() error { return s.parked.Close() }

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
		s.HandleStream(rec, platformAdminReq(req))
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

// spec: 25.5 (the opaque cursor carries the canonical eventKey so it
// round-trips across sources; cross-source cursor translation) — a cursor
// minted while the Redis stream served the poll must resolve at the two other
// sources, which locate a position by scanning for the first event ordering at
// or after the carried eventKey. Encoding the Redis stream ID instead leaves
// both of them comparing a "ms-seq" string against eventKeys of the form
// {replicaID}:{emittedAt}:{nonce}: parseEventKey rejects it, the comparison
// degrades to a byte order in which the stream ID sorts before the whole
// retained window, and every poll across a Redis-down transition reports a
// spurious gap and re-serves the window. The pre-fix Redis source minted the
// stream ID; this fails against that code on both legs.
func TestRedisMintedCursorTranslatesAtOtherSources_spec_25_5(t *testing.T) {
	base := ts()
	local := func(id string) gwevents.OperationalEvent {
		e := evt(id, "dev.lenny.alert_fired")
		e.Time = base.Add(1000 * time.Second)
		return e
	}

	f := &fakeStream{}
	f.add("1-0", local("ops:1000:1"))
	f.add("2-0", local("ops:1000:2"))
	s := New(Options{RedisClient: f, SourceHealth: newMutableHealth(true, true), Now: ts})
	// The same events are in this replica's ring: the fan-out emitter writes
	// each event to both the local ring and the shared stream.
	for _, id := range []string{"ops:1000:1", "ops:1000:2", "ops:1000:3"} {
		if _, err := s.Publish(context.Background(), local(id)); err != nil {
			t.Fatalf("publish %s: %v", id, err)
		}
	}

	page := pollActiveSource(s, context.Background(), "", "", gwevents.EventFilter{}, 10, false)
	minted, err := decodeCursor(page.Pagination.Cursor)
	if err != nil {
		t.Fatalf("decode the Redis-minted cursor: %v", err)
	}
	kind, position := minted.Kind, minted.EventKey
	if kind != SourceKindRedis {
		t.Fatalf("cursorKind = %q, want %q", kind, SourceKindRedis)
	}
	if position != "ops:1000:2" {
		t.Errorf("Redis-minted cursor position = %q, want the canonical eventKey ops:1000:2", position)
	}

	// The local ring is the dual-outage source: it resolves the same cursor.
	buffered := s.bufferPollPage(kind, position, gwevents.EventFilter{}, 10, false)
	if buffered.Pagination.GapDetected {
		t.Errorf("the local ring reported a gap for a Redis-minted cursor: %+v", buffered.Pagination)
	}
	if got := eventKeys(buffered.Items); len(got) != 1 || got[0] != "ops:1000:3" {
		t.Errorf("local-ring continuation = %v, want only ops:1000:3", got)
	}

	// The gateway-buffer fan-out is the Redis-down source: it resolves the
	// same cursor against a window that brackets it.
	gwEvt := func(id string, sec int64) gwevents.BufferedEvent {
		e := evt(id, "dev.lenny.alert_fired")
		e.Time = base.Add(time.Duration(sec) * time.Second)
		return gwevents.BufferedEvent{ID: 1, Event: e}
	}
	s.SetGatewayBufferSource(&fakeGatewaySource{pages: [][]gwevents.BufferedEvent{{
		gwEvt("gw:0500:1", 500),
		gwEvt("gw:2000:1", 2000),
	}}})
	fanned, _ := s.gatewayPollPage(context.Background(), kind, position, gwevents.EventFilter{}, 10, false)
	if fanned.Pagination.GapDetected {
		t.Errorf("the gateway-buffer fan-out reported a gap for a Redis-minted cursor: %+v", fanned.Pagination)
	}
	got := eventKeys(fanned.Items)
	if len(got) != 2 || got[0] != "ops:1000:3" || got[1] != "gw:2000:1" {
		t.Errorf("fan-out continuation = %v, want [ops:1000:3 gw:2000:1] with no replay of the window", got)
	}
}

// spec: 25.5 (cross-source cursor translation, exactly-once across the source
// switch) — the recovery flush re-emits the events a replica buffered during a
// Redis outage with their original eventKeys, so they land at the stream tail
// carrying keys older than entries already at earlier positions while the
// gateway keeps XADDing fresh keys the instant Redis is reachable. The retained
// window is then [pre-outage, post-recovery, flushed], and stream order no
// longer agrees with eventKey order. Cursor translation must stay forward-only
// over such a window: a caller resuming from a position it already read must
// not be sent back before the out-of-order tail. The pre-fix scan stopped at
// the first entry ordering after the cursor, so it resolved every cursor at or
// after the flushed keys to the last pre-outage position and replayed the whole
// window on the next read.
func TestResumeByEventKey_DoesNotRewindOverAnOutOfOrderRecoveryTail_spec_25_5(t *testing.T) {
	// Stream order: two pre-outage entries, two post-recovery gateway entries
	// with fresh keys, then the flush re-emitting two outage-window lenny-ops
	// events whose keys order before the gateway ones.
	f := &fakeStream{}
	f.add("10-0", evt("gw:10:1", "dev.lenny.alert_fired"))
	f.add("11-0", evt("gw:11:1", "dev.lenny.alert_fired"))
	f.add("30-0", evt("gw:30:1", "dev.lenny.alert_fired"))
	f.add("31-0", evt("gw:31:1", "dev.lenny.alert_fired"))
	f.add("40-0", evt("ops:20:1", "dev.lenny.escalation_created"))
	f.add("41-0", evt("ops:21:1", "dev.lenny.escalation_created"))
	rs := newRedisSource(f, "ops:events:stream", 0)

	for _, tc := range []struct {
		name      string
		cursor    string
		wantStart string
	}{
		// The cursor a poller mints from the last raw entry of a page that ran
		// to the end of the flushed tail. Resuming must stay at that position.
		{name: "on the flushed tail entry", cursor: "ops:21:1", wantStart: "41-0"},
		{name: "on the first flushed entry", cursor: "ops:20:1", wantStart: "40-0"},
		// A cursor on a post-recovery gateway entry: the flushed entries order
		// before it, so the position stays at the gateway entry rather than
		// rewinding to the last pre-outage one.
		{name: "on a post-recovery entry", cursor: "gw:31:1", wantStart: "31-0"},
		// A foreign cursor with no exact match, between the flushed keys:
		// resume after the last entry ordering at or before it.
		{name: "absent between the flushed keys", cursor: "ops:20:5", wantStart: "40-0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start, gap, err := rs.resumeByEventKey(context.Background(), tc.cursor)
			if err != nil {
				t.Fatalf("resume: %v", err)
			}
			if start != tc.wantStart {
				t.Errorf("resume start = %q, want %q (the position rewound behind the out-of-order recovery tail)", start, tc.wantStart)
			}
			if gap {
				t.Errorf("resume reported a gap for a cursor inside the retained window")
			}
		})
	}
}

// spec: 25.5 (evicted-cursor gap) — the below-window gap is decided by eventKey
// order rather than by stream position, so an out-of-order recovery tail does
// not make an in-window cursor look evicted. The lowest retained key here sits
// at the stream tail, which a position-ordered bound would miss.
func TestResumeByEventKey_GapBoundIsTheLowestRetainedKey_spec_25_5(t *testing.T) {
	f := &fakeStream{}
	f.add("30-0", evt("gw:30:1", "dev.lenny.alert_fired"))
	f.add("40-0", evt("ops:20:1", "dev.lenny.escalation_created"))
	rs := newRedisSource(f, "ops:events:stream", 0)

	if _, gap, err := rs.resumeByEventKey(context.Background(), "ops:25:1"); err != nil || gap {
		t.Errorf("resume gap = %v (err %v) for a cursor above the lowest retained key; want no gap", gap, err)
	}
	if _, gap, err := rs.resumeByEventKey(context.Background(), "ops:5:1"); err != nil || !gap {
		t.Errorf("resume gap = %v (err %v) for a cursor below every retained key; want a gap", gap, err)
	}
}

// rangeCall is one XRANGE the read source issued, recorded so a test can tell
// a positioned read from a scan of the whole retained window.
type rangeCall struct {
	start string
	stop  string
	count int64
}

// recordingStream is a fakeStream that records every XRANGE issued against it.
type recordingStream struct {
	fakeStream
	mu    sync.Mutex
	calls []rangeCall
}

func (r *recordingStream) XRangeN(ctx context.Context, stream, start, stop string, count int64) *redis.XMessageSliceCmd {
	r.mu.Lock()
	r.calls = append(r.calls, rangeCall{start: start, stop: stop, count: count})
	r.mu.Unlock()
	return r.fakeStream.XRangeN(ctx, stream, start, stop, count)
}

// windowScans reports how many recorded XRANGEs scanned the whole retained
// window ("-" to "+" for more than one entry), which is what an eventKey
// translation costs. The one-entry bound probes that back headCursor and
// oldestAvailableCursor are excluded: they are positioned reads.
func (r *recordingStream) windowScans() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, c := range r.calls {
		if c.start == "-" && c.stop == "+" && c.count > 1 {
			n++
		}
	}
	return n
}

// reset drops the recorded calls so a test can measure one request in
// isolation.
func (r *recordingStream) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = nil
}

// spec: 25.5 (a redis cursor reads Redis by stream ID; a foreign-source cursor
// is translated by eventKey) — a cursor the Redis source minted resumes by the
// stream ID it carries, so the steady-state poll loop costs one positioned
// lookup rather than a scan of the whole retained window.
//
// Every cursor used to carry the canonical eventKey alone, so resolving one the
// Redis source had itself minted an instant earlier read and JSON-decoded the
// entire retained stream. This test fails against that code: the continuation
// poll issues an XRANGE from "-" to "+" over the whole window.
func TestRedisResumePoint_SameSourceCursorResumesByStreamID_spec_25_5(t *testing.T) {
	f := &recordingStream{}
	f.add("1-0", evt("ops:1:1", "dev.lenny.alert_fired"))
	f.add("2-0", evt("ops:2:1", "dev.lenny.alert_fired"))
	f.add("3-0", evt("ops:3:1", "dev.lenny.alert_fired"))
	s := New(Options{RedisClient: f, SourceHealth: StaticSourceHealth{Redis: true, Gateway: true}, Now: ts})

	page := pollActiveSource(s, context.Background(), "", "", gwevents.EventFilter{}, 2, false)
	cur, err := decodeCursor(page.Pagination.Cursor)
	if err != nil {
		t.Fatalf("decode the Redis-minted cursor: %v", err)
	}
	if cur.Kind != SourceKindRedis || cur.StreamID != "2-0" || cur.EventKey != "ops:2:1" {
		t.Fatalf("Redis-minted cursor = %+v, want kind redis at stream ID 2-0 carrying ops:2:1", cur)
	}

	f.reset()
	page2 := pollActiveSourceCursor(s, context.Background(), cur, gwevents.EventFilter{}, 2, false)
	if got := f.windowScans(); got != 0 {
		t.Errorf("resuming a redis-kind cursor issued %d whole-window scan(s); a same-source resume reads by stream ID", got)
	}
	if page2.Pagination.GapDetected {
		t.Errorf("a retained same-source cursor reported a gap: %+v", page2.Pagination)
	}
	if got := eventKeys(page2.Items); len(got) != 1 || got[0] != "ops:3:1" {
		t.Fatalf("continuation items = %v, want only ops:3:1", got)
	}

	// A poll that finds nothing new echoes the caller's position verbatim, so
	// the repeat poll is still a positioned read rather than a scan.
	head, err := decodeCursor(page2.Pagination.Cursor)
	if err != nil {
		t.Fatalf("decode the head cursor: %v", err)
	}
	f.reset()
	idle := pollActiveSourceCursor(s, context.Background(), head, gwevents.EventFilter{}, 2, false)
	if len(idle.Items) != 0 {
		t.Fatalf("an idle poll served %d items, want 0", len(idle.Items))
	}
	echoed, err := decodeCursor(idle.Pagination.Cursor)
	if err != nil || echoed != head {
		t.Fatalf("idle poll echoed %+v (err %v), want the caller's %+v", echoed, err, head)
	}
	if got := f.windowScans(); got != 0 {
		t.Errorf("an idle poll issued %d whole-window scan(s)", got)
	}
}

// spec: 25.5 (a redis cursor whose stream ID the stream no longer retains, and
// a foreign-source cursor, are translated by eventKey) — the positioned resume
// is a fast path rather than a replacement: a trimmed stream ID and a cursor
// minted at another source both still resolve through the eventKey scan.
func TestRedisResumePoint_TrimmedAndForeignCursorsFallBackToTheKeyScan_spec_25_5(t *testing.T) {
	f := &recordingStream{}
	f.add("10-0", evt("ops:10:1", "dev.lenny.alert_fired"))
	f.add("11-0", evt("ops:11:1", "dev.lenny.alert_fired"))
	s := New(Options{RedisClient: f, SourceHealth: StaticSourceHealth{Redis: true, Gateway: true}, Now: ts})

	// A redis cursor whose stream ID was trimmed away, but whose eventKey is
	// still retained: the scan resolves it and the read continues after it.
	f.reset()
	trimmed := eventCursor{Kind: SourceKindRedis, StreamID: "1-0", EventKey: "ops:10:1"}
	page := pollActiveSourceCursor(s, context.Background(), trimmed, gwevents.EventFilter{}, 10, false)
	if f.windowScans() == 0 {
		t.Error("a trimmed redis cursor must fall back to the eventKey scan")
	}
	if got := eventKeys(page.Items); len(got) != 1 || got[0] != "ops:11:1" {
		t.Fatalf("trimmed-cursor items = %v, want only ops:11:1", got)
	}

	// A cursor minted at the local ring carries no stream ID at all.
	f.reset()
	foreign := eventCursor{Kind: SourceKindBuffer, EventKey: "ops:10:1"}
	page2 := pollActiveSourceCursor(s, context.Background(), foreign, gwevents.EventFilter{}, 10, false)
	if f.windowScans() == 0 {
		t.Error("a foreign-source cursor must be translated by the eventKey scan")
	}
	if got := eventKeys(page2.Items); len(got) != 1 || got[0] != "ops:11:1" {
		t.Fatalf("foreign-cursor items = %v, want only ops:11:1", got)
	}
}
