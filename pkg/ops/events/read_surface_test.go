// SPDX-License-Identifier: MIT

package events

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	gwevents "github.com/lennylabs/lenny/pkg/events"
)

func ts() time.Time { return time.Date(2026, 5, 23, 0, 0, 0, 0, time.UTC) }

// spec: §25.5 lines 2666-2675 — the opaque cursor encodes the source
// kind and the canonical eventKey and round-trips losslessly.
func TestCursor_RoundTrip_spec_25_5_2666(t *testing.T) {
	cases := []struct {
		kind, key string
	}{
		{SourceKindBuffer, "ops:1700000000000000000:7"},
		{SourceKindRedis, "gateway:1700000000000000001:42"},
		{SourceKindMixed, "ops:1:1"},
	}
	for _, c := range cases {
		enc := encodeCursor(c.kind, c.key)
		if enc == "" {
			t.Fatalf("encodeCursor(%q,%q) empty", c.kind, c.key)
		}
		if strings.Contains(enc, ":") {
			t.Errorf("cursor must be opaque (base64), got %q", enc)
		}
		gotKind, gotKey, err := decodeCursor(enc)
		if err != nil {
			t.Fatalf("decodeCursor(%q): %v", enc, err)
		}
		if gotKind != c.kind || gotKey != c.key {
			t.Errorf("round-trip = (%q,%q), want (%q,%q)", gotKind, gotKey, c.kind, c.key)
		}
	}
}

// spec: §25.5 line 2795 — an empty cursor is the start-of-stream;
// a malformed cursor is rejected so HandlePoll returns
// INVALID_EVENT_FILTER.
func TestCursor_EmptyAndMalformed_spec_25_5_2795(t *testing.T) {
	if k, p, err := decodeCursor(""); err != nil || k != "" || p != "" {
		t.Errorf("empty cursor: (%q,%q,%v)", k, p, err)
	}
	if _, _, err := decodeCursor("!!!not-base64!!!"); err == nil {
		t.Error("malformed base64 cursor should error")
	}
	// base64 of a string with no source-kind prefix is rejected.
	noPrefix := base64.RawURLEncoding.EncodeToString([]byte("no-colon-here"))
	if _, _, err := decodeCursor(noPrefix); err == nil {
		t.Error("cursor without source-kind prefix should error")
	}
}

// spec: §25.5 line 2693 / §25.2 line 340 — ?resourceType= and
// ?resourceId= filter the polling page against the CloudEvents subject.
func TestPoll_ResourceFilter_spec_25_5_2693(t *testing.T) {
	s := New(Options{Capacity: 16, Now: ts})
	s.Publish(context.Background(), gwevents.OperationalEvent{Type: "pool_state_changed", Subject: "pool/default-gvisor"})
	s.Publish(context.Background(), gwevents.OperationalEvent{Type: "pool_state_changed", Subject: "pool/default-kata"})
	s.Publish(context.Background(), gwevents.OperationalEvent{Type: "session_failed", Subject: "session/abc"})

	page := poll(t, s, "/v1/admin/events?resourceType=pool&resourceId=default-gvisor")
	if len(page.Items) != 1 {
		t.Fatalf("resource filter: %d items, want 1", len(page.Items))
	}
	if page.Items[0].Event.Subject != "pool/default-gvisor" {
		t.Errorf("subject: %q", page.Items[0].Event.Subject)
	}

	// resourceType alone matches every pool event.
	page = poll(t, s, "/v1/admin/events?resourceType=pool")
	if len(page.Items) != 2 {
		t.Errorf("resourceType-only filter: %d items, want 2", len(page.Items))
	}
}

// spec: §25.2 line 344 — ?since= filters by RFC 3339 timestamp.
func TestPoll_SinceTimestampFilter_spec_25_2_344(t *testing.T) {
	s := New(Options{Capacity: 16, Now: ts})
	early := ts().Add(-time.Hour)
	late := ts().Add(time.Hour)
	s.Publish(context.Background(), gwevents.OperationalEvent{Type: "alert_fired", Time: early})
	s.Publish(context.Background(), gwevents.OperationalEvent{Type: "alert_fired", Time: late})

	page := poll(t, s, "/v1/admin/events?since="+url.QueryEscape(ts().Format(time.RFC3339)))
	if len(page.Items) != 1 {
		t.Fatalf("since filter: %d items, want 1", len(page.Items))
	}
	if !page.Items[0].Event.Time.Equal(late) {
		t.Errorf("since filter kept the wrong event: %v", page.Items[0].Event.Time)
	}
}

// spec: §25.2 line 243 — ?sortOrder=desc reverses the page order.
func TestPoll_SortOrderDesc_spec_25_2_243(t *testing.T) {
	s := New(Options{Capacity: 16, Now: ts})
	s.Publish(context.Background(), gwevents.OperationalEvent{Type: "alert_fired", Subject: "a/1"})
	s.Publish(context.Background(), gwevents.OperationalEvent{Type: "alert_fired", Subject: "a/2"})

	asc := poll(t, s, "/v1/admin/events")
	desc := poll(t, s, "/v1/admin/events?sortOrder=desc")
	if len(asc.Items) != 2 || len(desc.Items) != 2 {
		t.Fatalf("counts: asc=%d desc=%d", len(asc.Items), len(desc.Items))
	}
	if asc.Items[0].Event.Subject != "a/1" || desc.Items[0].Event.Subject != "a/2" {
		t.Errorf("order: asc[0]=%q desc[0]=%q", asc.Items[0].Event.Subject, desc.Items[0].Event.Subject)
	}
}

// spec: §25.2 line 240 — ?limit= defaults to 100 and caps at 1000.
func TestPoll_LimitBounds_spec_25_2_240(t *testing.T) {
	if got := parseLimit(url.Values{}); got != DefaultPollLimit {
		t.Errorf("default limit = %d, want %d", got, DefaultPollLimit)
	}
	if got := parseLimit(url.Values{"limit": {"5000"}}); got != MaxPollLimit {
		t.Errorf("capped limit = %d, want %d", got, MaxPollLimit)
	}
	if got := parseLimit(url.Values{"limit": {"42"}}); got != 42 {
		t.Errorf("explicit limit = %d, want 42", got)
	}
}

// spec: §25.5 line 2795 — an unrecognized event type or severity, a
// malformed cursor, a bad sortOrder, or a malformed since returns
// INVALID_EVENT_FILTER (400).
func TestPoll_InvalidFilter_spec_25_5_2795(t *testing.T) {
	s := New(Options{Capacity: 16, Now: ts})
	for _, target := range []string{
		"/v1/admin/events?eventType=not_a_real_event",
		"/v1/admin/events?severity=fuchsia",
		"/v1/admin/events?cursor=%21%21%21",
		"/v1/admin/events?sortOrder=sideways",
		"/v1/admin/events?since=not-a-timestamp",
	} {
		rec := httptest.NewRecorder()
		s.HandlePoll(rec, platformAdminReq(httptest.NewRequest(http.MethodGet, target, nil)))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", target, rec.Code)
		}
		var env struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			t.Fatalf("%s: decode error envelope: %v", target, err)
		}
		if env.Error.Code != codeInvalidEventFilter {
			t.Errorf("%s: code %q, want %s", target, env.Error.Code, codeInvalidEventFilter)
		}
	}
}

// spec: §25.5 lines 2666-2675, §25.2 lines 262-275 — a cursor whose
// event has been evicted reports gapDetected and oldestAvailableCursor.
func TestPoll_GapOnEvictedCursor_spec_25_5_2672(t *testing.T) {
	s := New(Options{Capacity: 4, Now: ts})
	// Capture a cursor, then overflow the ring so its event is evicted.
	s.Publish(context.Background(), gwevents.OperationalEvent{Type: "alert_fired"})
	staleKey := s.Query(0, gwevents.EventFilter{}, 0).Events[0].Event.ID
	staleCursor := encodeCursor(SourceKindBuffer, staleKey)
	for i := 0; i < 8; i++ {
		s.Publish(context.Background(), gwevents.OperationalEvent{Type: "alert_fired"})
	}

	page := poll(t, s, "/v1/admin/events?cursor="+url.QueryEscape(staleCursor))
	if !page.Pagination.GapDetected {
		t.Fatal("expected gapDetected for an evicted cursor")
	}
	if page.Pagination.OldestAvailableCursor == "" {
		t.Error("gap response must carry oldestAvailableCursor")
	}
}

// spec: §25.5 lines 2666-2675 — a cursor produced by a foreign source
// (redis) is translated by eventKey and the page reports cursorKind
// "mixed".
func TestPoll_MixedCursorKind_spec_25_5_2674(t *testing.T) {
	s := New(Options{Capacity: 16, Now: ts})
	s.Publish(context.Background(), gwevents.OperationalEvent{Type: "alert_fired"})
	s.Publish(context.Background(), gwevents.OperationalEvent{Type: "alert_fired"})
	firstKey := s.Query(0, gwevents.EventFilter{}, 0).Events[0].Event.ID

	// A redis-kind cursor carrying a key still in the buffer.
	redisCursor := encodeCursor(SourceKindRedis, firstKey)
	page := poll(t, s, "/v1/admin/events?cursor="+url.QueryEscape(redisCursor))
	if page.Pagination.CursorKind != SourceKindMixed {
		t.Errorf("cursorKind = %q, want mixed", page.Pagination.CursorKind)
	}
	if len(page.Items) != 1 {
		t.Errorf("mixed-cursor page: %d items, want 1 (the second event)", len(page.Items))
	}
}

// spec: §25.2 lines 261-271 — "Agents MUST NOT parse cursors. A cursor
// produced by one `actualSource` may be invalid at a different source
// — agents treat cursor incompatibility as a gap (see below) and
// reset." and "When the provided cursor cannot be honored (evicted
// from a ring buffer, invalidated by a source transition, or
// otherwise lost), the response includes `"gapDetected": true` and a
// suggested recovery cursor": `oldestAvailableCursor` and
// `"suggestedAction": "resync"`.
//
// A cursor minted at the Redis stream (a foreign actualSource) is
// presented to the poll surface while it is degraded to the gateway
// buffer by a Redis outage. The buffer never recorded that eventKey,
// so honoring the cursor requires a source transition the buffer
// cannot make; the response must report the gap and its recovery hint
// rather than silently resetting to a fresh connect.
func TestPoll_GapDetectedAcrossSourceTransitionDuringRedisOutage_spec_25_2_261(t *testing.T) {
	s := New(Options{
		Capacity:     16,
		Now:          ts,
		SourceHealth: StaticSourceHealth{Redis: false, Gateway: true},
	})
	// A gateway source is wired, so the Redis outage genuinely falls back to
	// the gateway-buffer fan-out rather than resolving to the dual-outage case.
	s.SetGatewayBufferSource(&fakeGatewaySource{})
	s.Publish(context.Background(), gwevents.OperationalEvent{Type: "alert_fired"})

	// A cursor produced by the Redis stream, carrying an eventKey this
	// buffer never held.
	redisCursor := encodeCursor(SourceKindRedis, "redis-replica:1699999999999999999:1")
	page := poll(t, s, "/v1/admin/events?cursor="+url.QueryEscape(redisCursor))

	if page.Degradation == nil || page.Degradation.ActualSource != "gateway-buffer" {
		t.Fatalf("expected the poll to be served from the gateway buffer during the Redis outage, got degradation=%+v", page.Degradation)
	}
	if !page.Pagination.GapDetected {
		t.Fatal("expected gapDetected for a cursor minted at a different actualSource")
	}
	if page.Pagination.OldestAvailableCursor == "" {
		t.Error("gap response must carry oldestAvailableCursor")
	}
	if page.Pagination.SuggestedAction != "resync" {
		t.Errorf("suggestedAction = %q, want resync", page.Pagination.SuggestedAction)
	}
}

// spec: §25.5 lines 2679-2680 — the SSE id: line carries the
// CloudEvents id (eventKey), not the internal buffer sequence.
func TestStream_IDLineIsEventKey_spec_25_5_2679(t *testing.T) {
	s := New(Options{Capacity: 16, Now: ts})
	s.Publish(context.Background(), gwevents.OperationalEvent{Type: "alert_fired"})
	wantKey := s.Query(0, gwevents.EventFilter{}, 0).Events[0].Event.ID

	frames := runStream(s, "/v1/admin/events/stream", nil)
	if len(frames) != 1 {
		t.Fatalf("frames: %d", len(frames))
	}
	if frames[0].id != wantKey {
		t.Errorf("SSE id: %q, want eventKey %q", frames[0].id, wantKey)
	}
}

// spec: §25.5 line 2684 — when the SSE resume point has been evicted,
// the handler emits a :gap comment line before the backlog.
func TestStream_GapCommentOnEvictedResume_spec_25_5_2684(t *testing.T) {
	s := New(Options{Capacity: 16, Now: ts})
	s.Publish(context.Background(), gwevents.OperationalEvent{Type: "alert_fired"})

	body := runStreamBody(s, "/v1/admin/events/stream", http.Header{"Last-Event-Id": {"ops:0:0"}})
	if !strings.Contains(body, ":gap ") {
		t.Errorf("expected a :gap comment line, body=%q", body)
	}
}

// spec: §25.5 line 2685 — the SSE stream applies the canonical filter
// set (resourceType/resourceId) to the backlog.
func TestStream_ResourceFilter_spec_25_5_2685(t *testing.T) {
	s := New(Options{Capacity: 16, Now: ts})
	s.Publish(context.Background(), gwevents.OperationalEvent{Type: "pool_state_changed", Subject: "pool/a"})
	s.Publish(context.Background(), gwevents.OperationalEvent{Type: "pool_state_changed", Subject: "pool/b"})

	frames := runStream(s, "/v1/admin/events/stream?resourceType=pool&resourceId=b", nil)
	if len(frames) != 1 {
		t.Fatalf("filtered backlog: %d frames", len(frames))
	}
}

// spec: §25.5 line 2795 — a malformed filter on the SSE endpoint is
// rejected with INVALID_EVENT_FILTER before the stream opens.
func TestStream_InvalidFilter_spec_25_5_2795(t *testing.T) {
	s := New(Options{Capacity: 16, Now: ts})
	w := flushRec{httptest.NewRecorder()}
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/events/stream?severity=mauve", nil)
	s.HandleStream(w, platformAdminReq(req))
	if w.Code != http.StatusBadRequest {
		t.Errorf("status %d, want 400", w.Code)
	}
}

// --- helpers ---

func poll(t *testing.T, s *Service, target string) EventPage {
	t.Helper()
	rec := httptest.NewRecorder()
	s.HandlePoll(rec, platformAdminReq(httptest.NewRequest(http.MethodGet, target, nil)))
	if rec.Code != http.StatusOK {
		t.Fatalf("poll %s: status %d (%s)", target, rec.Code, rec.Body.String())
	}
	var page EventPage
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("poll %s: decode: %v", target, err)
	}
	return page
}

type sseFrame struct{ id, typ, data string }

// flushRec is a flushable ResponseRecorder for SSE handler tests.
type flushRec struct{ *httptest.ResponseRecorder }

func (flushRec) Flush() {}

// runStreamBody drives HandleStream until its ~20ms cancel and returns
// the raw response body.
func runStreamBody(s *Service, target string, hdr http.Header) string {
	r := flushRec{httptest.NewRecorder()}
	req := httptest.NewRequest(http.MethodGet, target, nil)
	for k, vs := range hdr {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	ctx, cancel := context.WithCancel(req.Context())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	s.HandleStream(r, platformAdminReq(req.WithContext(ctx)))
	return r.Body.String()
}

func runStream(s *Service, target string, hdr http.Header) []sseFrame {
	return parseFrames(runStreamBody(s, target, hdr))
}

func parseFrames(body string) []sseFrame {
	var out []sseFrame
	var cur sseFrame
	for _, line := range strings.Split(body, "\n") {
		switch {
		case line == "" && cur != (sseFrame{}):
			out = append(out, cur)
			cur = sseFrame{}
		case strings.HasPrefix(line, "id: "):
			cur.id = strings.TrimPrefix(line, "id: ")
		case strings.HasPrefix(line, "event: "):
			cur.typ = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			cur.data = strings.TrimPrefix(line, "data: ")
		}
	}
	if cur != (sseFrame{}) {
		out = append(out, cur)
	}
	return out
}
