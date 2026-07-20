// SPDX-License-Identifier: MIT

package events

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gwevents "github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/ops/gateway"
)

// mutableHealth is a SourceHealth whose reachability flips at runtime so a
// test can drive the §25.5 degradation matrix and the transparent source
// switch without a live probe.
type mutableHealth struct {
	redis   atomic.Bool
	gateway atomic.Bool
}

func newMutableHealth(redis, gw bool) *mutableHealth {
	h := &mutableHealth{}
	h.redis.Store(redis)
	h.gateway.Store(gw)
	return h
}

func (h *mutableHealth) RedisAvailable() bool   { return h.redis.Load() }
func (h *mutableHealth) GatewayAvailable() bool { return h.gateway.Load() }

// fakeGatewaySource is a GatewayBufferSource returning a fixed set of
// per-replica buffer pages, so the §25.5 Redis-down fan-out fallback can be
// exercised without a live gateway. Each page is one replica's
// GET /v1/admin/events/buffer body.
type fakeGatewaySource struct {
	pages [][]gwevents.BufferedEvent
	err   error
}

func (f *fakeGatewaySource) FanOutGet(_ context.Context, _ string) ([]gateway.ReplicaResult, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make([]gateway.ReplicaResult, 0, len(f.pages))
	for _, evs := range f.pages {
		body, _ := json.Marshal(gwevents.BufferedEventPage{Events: evs})
		out = append(out, gateway.ReplicaResult{Endpoint: "https://pod", Body: body})
	}
	return out, nil
}

func bufEvt(id, typ string) gwevents.BufferedEvent {
	return gwevents.BufferedEvent{ID: 1, Event: evt(id, typ)}
}

// oneShotGatewaySource returns a fixed set of per-replica pages and closes its
// called channel on the first fan-out, so a serveGateway test can cancel the
// connection after exactly one poll fill instead of waiting the full
// gatewayPollInterval.
type oneShotGatewaySource struct {
	pages  [][]gwevents.BufferedEvent
	called chan struct{}
	once   sync.Once
}

func (o *oneShotGatewaySource) FanOutGet(_ context.Context, _ string) ([]gateway.ReplicaResult, error) {
	out := make([]gateway.ReplicaResult, 0, len(o.pages))
	for _, evs := range o.pages {
		body, _ := json.Marshal(gwevents.BufferedEventPage{Events: evs})
		out = append(out, gateway.ReplicaResult{Endpoint: "https://pod", Body: body})
	}
	o.once.Do(func() { close(o.called) })
	return out, nil
}

// spec: 25.5 (cross-switch no-drop, exactly-once across the source switch) — on
// a switch into the gateway-buffer fall-back, the SSE handler resumes from the
// eventKey it last delivered (over the Redis stream or an earlier gateway
// stint) and streams only the events after it. The pre-fix serveGateway opened
// each stint with a fresh delivered set and never read the carried lastKey, so
// it re-delivered the whole matching buffer window; this fails against that
// code by asserting the events at or before the resume point are not re-sent.
func TestServeGateway_ResumesFromLastKeyNoRedelivery_spec_25_5(t *testing.T) {
	s := New(Options{RedisClient: &fakeStream{}, SourceHealth: newMutableHealth(false, true), Now: ts})
	src := &oneShotGatewaySource{
		pages: [][]gwevents.BufferedEvent{{
			bufEvt("gw-a:1000:1", "dev.lenny.alert_fired"),
			bufEvt("gw-a:1000:2", "dev.lenny.alert_fired"),
			bufEvt("gw-a:1000:3", "dev.lenny.alert_fired"),
		}},
		called: make(chan struct{}),
	}
	s.SetGatewayBufferSource(src)

	rec := httptest.NewRecorder()
	sess := &streamSession{s: s, w: rec, flusher: rec, lastKey: "gw-a:1000:2"}

	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-src.called; cancel() }()
	sess.serveGateway(ctx, dsGateway)

	body := rec.Body.String()
	if strings.Contains(body, "gw-a:1000:1") || strings.Contains(body, "gw-a:1000:2") {
		t.Fatalf("events at or before the resume point were re-delivered:\n%s", body)
	}
	if !strings.Contains(body, "gw-a:1000:3") {
		t.Fatalf("the event after the resume point was not delivered:\n%s", body)
	}
	if strings.Contains(body, ":gap") {
		t.Fatalf("a resume point present in the window must not emit a :gap:\n%s", body)
	}
}

// spec: 25.5 (cross-switch no-drop) — when the carried resume position is no
// longer present in the gateway-buffer window, the SSE handler emits a :gap
// comment and observes the gap counter before streaming the window, matching
// the Redis and local-buffer resume paths. The pre-fix serveGateway ignored
// lastKey entirely and emitted no gap, so a consumer could not tell it had lost
// events across the switch; this fails against that code by asserting the gap
// is now signalled.
func TestServeGateway_MissingResumeEmitsGap_spec_25_5(t *testing.T) {
	gaps := 0
	s := New(Options{RedisClient: &fakeStream{}, SourceHealth: newMutableHealth(false, true), Now: ts, OnGap: func() { gaps++ }})
	src := &oneShotGatewaySource{
		pages: [][]gwevents.BufferedEvent{{
			bufEvt("gw-a:1000:5", "dev.lenny.alert_fired"),
		}},
		called: make(chan struct{}),
	}
	s.SetGatewayBufferSource(src)

	rec := httptest.NewRecorder()
	sess := &streamSession{s: s, w: rec, flusher: rec, lastKey: "gw-a:1000:1"}

	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-src.called; cancel() }()
	sess.serveGateway(ctx, dsGateway)

	body := rec.Body.String()
	if !strings.Contains(body, ":gap") {
		t.Fatalf("a missing resume point must emit a :gap comment:\n%s", body)
	}
	if gaps != 1 {
		t.Fatalf("gap counter observed %d times, want 1", gaps)
	}
	if !strings.Contains(body, "gw-a:1000:5") {
		t.Fatalf("the window must still be delivered after a gap:\n%s", body)
	}
}

// spec: 25.3 (cross-replica eventKey dedup, not a content hash) — the merge of
// two per-replica buffer pages collapses a genuine repeat delivery of one
// eventKey while preserving two distinct same-second alert_fired events from
// two replicas, which carry distinct eventKeys. A content-hash-only dedup
// would drop one of the two distinct same-second events (the collision class
// the finding names); this pins that it does not.
func TestMergeReplicaBuffers_DedupsByEventKeyPreservesDistinct_spec_25_3(t *testing.T) {
	// Replica A and replica B each emit a distinct alert_fired in the same
	// second (distinct eventKeys), and both carry a broadcast credential
	// event with the SAME eventKey (a genuine cross-replica repeat).
	a := []gwevents.BufferedEvent{
		bufEvt("gw-a:1000:1", "dev.lenny.alert_fired"),
		bufEvt("broadcast:1000:9", "dev.lenny.credential_rotated"),
	}
	b := []gwevents.BufferedEvent{
		bufEvt("gw-b:1000:1", "dev.lenny.alert_fired"),
		bufEvt("broadcast:1000:9", "dev.lenny.credential_rotated"),
	}
	f := &fakeGatewaySource{pages: [][]gwevents.BufferedEvent{a, b}}
	results, _ := f.FanOutGet(context.Background(), "")

	merged := mergeReplicaBuffers(results)

	keys := map[string]int{}
	for _, ev := range merged {
		keys[ev.Event.ID]++
	}
	if keys["gw-a:1000:1"] != 1 || keys["gw-b:1000:1"] != 1 {
		t.Errorf("two distinct same-second alert_fired events must both survive: got %v", keys)
	}
	if keys["broadcast:1000:9"] != 1 {
		t.Errorf("a genuine repeat of one eventKey must collapse to one: got %d", keys["broadcast:1000:9"])
	}
	if len(merged) != 3 {
		t.Fatalf("merged size = %d, want 3 (two distinct + one collapsed)", len(merged))
	}
}

// spec: 25.5 (Redis-down gateway-buffer fallback, eventKey dedup) — with Redis
// unreachable and the gateway up, a poll serves the
// gateway-originated events fanned from the gateway event buffer, deduped by
// eventKey, and attaches the degradation envelope with actualSource
// "gateway-buffer". The pre-fix read surface served only the local ring
// buffer (lenny-ops-originated events), so a gateway-originated alert never
// reached the response; this asserts it now does.
func TestHandlePoll_RedisDownServesGatewayBuffer_spec_25_5(t *testing.T) {
	health := newMutableHealth(false, true)
	s := New(Options{RedisClient: &fakeStream{}, SourceHealth: health, Now: ts})
	s.SetGatewayBufferSource(&fakeGatewaySource{pages: [][]gwevents.BufferedEvent{
		{bufEvt("gw-a:1000:1", "dev.lenny.alert_fired")},
		{bufEvt("gw-b:1000:1", "dev.lenny.pool_state_changed")},
	}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/admin/events", nil)
	s.HandlePoll(rec, req)

	var page EventPage
	if err := json.NewDecoder(rec.Body).Decode(&page); err != nil {
		t.Fatalf("decode poll body: %v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("Redis-down poll served %d gateway events; want 2 from the gateway buffer", len(page.Items))
	}
	if page.Degradation == nil || page.Degradation.ActualSource != sourceGatewayBuffer {
		t.Fatalf("expected a gateway-buffer degradation envelope, got %+v", page.Degradation)
	}
	if page.Pagination.CursorKind != SourceKindMixed {
		t.Errorf("cursorKind = %q, want %q", page.Pagination.CursorKind, SourceKindMixed)
	}
}

// spec: 25.5 (source selection from the degradation matrix, dual-outage case)
// — a Redis-down state selects the gateway buffer only when a gateway source is
// wired. With none wired, gateway-originated events have nowhere to fetch from,
// so the read surface serves this replica's ring under the case-4
// lenny-ops-local-buffer envelope with gateway-events unavailable, and polling
// returns 503 EVENT_STREAM_UNAVAILABLE. Reporting actualSource gateway-buffer
// while serving the local ring would announce a cross-replica view the caller
// is not receiving.
func TestSelectSource_GatewayLabelFallsToLocalWhenUnwired_spec_25_5(t *testing.T) {
	s := New(Options{RedisClient: &fakeStream{}, SourceHealth: newMutableHealth(false, true), Now: ts})
	src, deg, dualDown := s.selectSource()
	if src != dsLocalBuffer {
		t.Fatalf("no gateway source wired: selectSource = %v, want dsLocalBuffer", src)
	}
	if !dualDown {
		t.Error("no gateway source wired during a Redis outage must resolve to the dual-outage case")
	}
	if deg == nil || deg.ActualSource != sourceOpsLocalBuffer {
		t.Fatalf("degradation envelope = %+v; want actualSource %q", deg, sourceOpsLocalBuffer)
	}
	if len(deg.UnavailableFields) != 1 || deg.UnavailableFields[0] != "gateway-events" {
		t.Errorf("unavailableFields = %v; want [gateway-events]", deg.UnavailableFields)
	}

	rec := httptest.NewRecorder()
	s.HandlePoll(rec, httptest.NewRequest(http.MethodGet, "/v1/admin/events", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("poll status = %d; want 503 EVENT_STREAM_UNAVAILABLE", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), codeEventStreamUnavailable) {
		t.Errorf("poll body = %s; want %s", rec.Body.String(), codeEventStreamUnavailable)
	}

	s.SetGatewayBufferSource(&fakeGatewaySource{})
	if src, _, dual := s.selectSource(); src != dsGateway || dual {
		t.Fatalf("gateway source wired: selectSource = %v (dualDown=%t), want dsGateway", src, dual)
	}
}

// spec: 25.5 (transparent Redis to gateway-buffer switch and recovery) — an
// open SSE connection announces each source transition: entering the
// gateway-buffer fall-back writes the :degradation envelope; returning to the
// healthy Redis source writes :degradation {"level":"healthy"}. The initial
// entry into a healthy source writes nothing.
func TestStreamTransition_AnnouncesDegradeAndRecovery_spec_25_5(t *testing.T) {
	health := newMutableHealth(false, true)
	s := New(Options{RedisClient: &fakeStream{}, SourceHealth: health, Now: ts})
	s.SetGatewayBufferSource(&fakeGatewaySource{})
	rec := httptest.NewRecorder()
	sess := &streamSession{s: s, w: rec, flusher: rec}

	// Entering the gateway fall-back from the start announces the degradation.
	_, degGw, _ := s.selectSource()
	sess.writeTransition(dataSource(-1), dsGateway, degGw)
	if !strings.Contains(rec.Body.String(), sourceGatewayBuffer) {
		t.Fatalf("gateway entry did not announce the degradation envelope:\n%s", rec.Body.String())
	}

	// Redis recovers: switching back to Redis announces recovery.
	health.redis.Store(true)
	rec2 := httptest.NewRecorder()
	sess.w = rec2
	sess.flusher = rec2
	sess.writeTransition(dsGateway, dsRedis, nil)
	if !strings.Contains(rec2.Body.String(), "\"level\":\"healthy\"") {
		t.Fatalf("switch back to Redis did not announce recovery:\n%s", rec2.Body.String())
	}

	// A fresh healthy start (no prior source) announces nothing.
	rec3 := httptest.NewRecorder()
	sess.w = rec3
	sess.flusher = rec3
	sess.writeTransition(dataSource(-1), dsRedis, nil)
	if strings.TrimSpace(rec3.Body.String()) != "" {
		t.Fatalf("healthy start must announce nothing, got:\n%s", rec3.Body.String())
	}
}

// errBodyGatewaySource returns one healthy replica page alongside a failed
// replica (Err set), an empty-body replica, and a malformed-JSON replica, so
// the merge's best-effort skip of failed, empty, and unparseable replicas can
// be exercised without a live gateway.
type errBodyGatewaySource struct {
	good []gwevents.BufferedEvent
}

func (f *errBodyGatewaySource) FanOutGet(_ context.Context, _ string) ([]gateway.ReplicaResult, error) {
	goodBody, _ := json.Marshal(gwevents.BufferedEventPage{Events: f.good})
	return []gateway.ReplicaResult{
		{Endpoint: "https://pod-a", Err: context.DeadlineExceeded},
		{Endpoint: "https://pod-b", Body: nil},
		{Endpoint: "https://pod-c", Body: []byte("{not json")},
		{Endpoint: "https://pod-d", Body: goodBody},
	}, nil
}

// spec: 25.3 (cross-replica eventKey dedup, best-effort fan-out) — a fan-out in
// which some replicas fail, return an empty body, or return unparseable JSON is
// best-effort: the failed, empty, and malformed replicas are skipped and the
// merge proceeds with the replicas that did respond. This pins that a partial
// gateway outage still serves the events from the healthy replicas rather than
// dropping the whole page.
func TestMergeReplicaBuffers_SkipsFailedEmptyAndMalformedReplicas_spec_25_3(t *testing.T) {
	f := &errBodyGatewaySource{good: []gwevents.BufferedEvent{
		bufEvt("gw-d:1000:1", "dev.lenny.alert_fired"),
	}}
	results, _ := f.FanOutGet(context.Background(), "")

	merged := mergeReplicaBuffers(results)
	if len(merged) != 1 || merged[0].Event.ID != "gw-d:1000:1" {
		t.Fatalf("merge over a partial outage = %v, want the single healthy replica's event", merged)
	}
}

// spec: 25.3 (oldest-first ordering by event time) — the merge orders events
// oldest-first by event time, falling back to the eventKey only as a stable tie
// break for same-time events. This pins that two events with distinct times are
// ordered by time regardless of eventKey ordering.
func TestMergeReplicaBuffers_OrdersByEventTime_spec_25_3(t *testing.T) {
	early := bufEvt("gw-b:2000:1", "dev.lenny.alert_fired")
	late := bufEvt("gw-a:1000:1", "dev.lenny.alert_fired")
	early.Event.Time = ts().Add(-time.Hour)
	late.Event.Time = ts()
	// Feed the later event first so a stable sort must reorder by time.
	f := &fakeGatewaySource{pages: [][]gwevents.BufferedEvent{{late, early}}}
	results, _ := f.FanOutGet(context.Background(), "")

	merged := mergeReplicaBuffers(results)
	if len(merged) != 2 || merged[0].Event.ID != "gw-b:2000:1" || merged[1].Event.ID != "gw-a:1000:1" {
		t.Fatalf("merge ordering = %v, want the earlier-time event first", merged)
	}
}

// spec: 25.5 (Redis-down gateway-buffer fallback, filter narrowing) —
// fetchGatewayBuffer returns an error when no gateway source is wired and when
// the fan-out itself fails, and it renders the event-type and severity filter
// dimensions into the per-replica query so each pod narrows before responding.
func TestFetchGatewayBuffer_ErrorsAndFilterQuery_spec_25_5(t *testing.T) {
	// No gateway source wired: the fetch fails closed rather than serving an
	// empty page as if the buffer were empty.
	s := New(Options{RedisClient: &fakeStream{}, SourceHealth: newMutableHealth(false, true), Now: ts})
	if _, err := s.fetchGatewayBuffer(context.Background(), gwevents.EventFilter{}); err == nil {
		t.Fatal("fetchGatewayBuffer with no gateway source wired must return an error")
	}

	// A fan-out failure propagates as an error.
	s.SetGatewayBufferSource(&fakeGatewaySource{err: context.DeadlineExceeded})
	if _, err := s.fetchGatewayBuffer(context.Background(), gwevents.EventFilter{}); err == nil {
		t.Fatal("fetchGatewayBuffer must propagate a fan-out failure")
	}

	// The event-type and severity dimensions ride to each replica as query
	// params; the resource and time dimensions are applied locally.
	q := bufferFilterQuery(gwevents.EventFilter{EventType: "alert_fired", Severity: "critical"})
	if !strings.Contains(q, "eventType=alert_fired") || !strings.Contains(q, "severity=critical") {
		t.Fatalf("buffer filter query = %q, want eventType and severity params", q)
	}
}

// spec: 25.5 (Redis-down gateway-buffer fallback, eventKey resume) — the poll
// page resumes after the cursor's eventKey, pages at the limit with hasMore,
// and on a fan-out failure returns an empty page echoing the caller's cursor so
// a retry resumes from the same position rather than losing it.
func TestGatewayPollPage_ResumeLimitAndFetchError_spec_25_5(t *testing.T) {
	s := New(Options{RedisClient: &fakeStream{}, SourceHealth: newMutableHealth(false, true), Now: ts})
	s.SetGatewayBufferSource(&fakeGatewaySource{pages: [][]gwevents.BufferedEvent{{
		bufEvt("gw-a:1000:1", "dev.lenny.alert_fired"),
		bufEvt("gw-a:1000:2", "dev.lenny.alert_fired"),
		bufEvt("gw-a:1000:3", "dev.lenny.alert_fired"),
	}}})

	// Resume after gw-a:1000:1 with a limit of 1: the page is the single event
	// after the cursor, with hasMore true and a continuation cursor.
	page := s.gatewayPollPage(context.Background(), SourceKindMixed, "gw-a:1000:1", gwevents.EventFilter{}, 1, false)
	if len(page.Items) != 1 || page.Items[0].Event.ID != "gw-a:1000:2" {
		t.Fatalf("resumed page = %v, want [gw-a:1000:2]", page.Items)
	}
	if !page.Pagination.HasMore {
		t.Fatalf("resumed page must report hasMore with events remaining after the limit")
	}

	// A fan-out failure returns an empty page echoing the caller's cursor.
	s.SetGatewayBufferSource(&fakeGatewaySource{err: context.DeadlineExceeded})
	errPage := s.gatewayPollPage(context.Background(), SourceKindMixed, "gw-a:1000:2", gwevents.EventFilter{}, 10, false)
	if len(errPage.Items) != 0 {
		t.Fatalf("fan-out failure must serve an empty page, got %d items", len(errPage.Items))
	}
	if errPage.Pagination.Cursor != encodeCursor(SourceKindMixed, "gw-a:1000:2") {
		t.Fatalf("fan-out failure cursor = %q, want the echoed caller cursor", errPage.Pagination.Cursor)
	}
}

// spec: 25.5 (cross-source cursor translation with gapDetected and
// oldestAvailableCursor on a miss) — a cursor whose eventKey has aged out of
// every gateway replica's ring reports gapDetected with oldestAvailableCursor
// and a resync action, and fires the gap counter, the same way the Redis and
// local-buffer poll paths do. The pre-fix gatewayPollPage silently restarted
// the window from its head with gapDetected false and no counter fire, so a
// caller re-consumed events it had already processed while the envelope
// asserted continuity; this fails against that code.
func TestGatewayPollPage_GapOnAgedOutCursor_spec_25_5(t *testing.T) {
	gaps := 0
	s := New(Options{RedisClient: &fakeStream{}, SourceHealth: newMutableHealth(false, true), Now: ts, OnGap: func() { gaps++ }})
	s.SetGatewayBufferSource(&fakeGatewaySource{pages: [][]gwevents.BufferedEvent{{
		bufEvt("gw-a:2000:1", "dev.lenny.alert_fired"),
		bufEvt("gw-a:2000:2", "dev.lenny.alert_fired"),
	}}})

	page := s.gatewayPollPage(context.Background(), SourceKindMixed, "gw-a:1000:1", gwevents.EventFilter{}, 10, false)
	if !page.Pagination.GapDetected {
		t.Fatal("a cursor older than every retained gateway-buffer entry must report gapDetected")
	}
	if page.Pagination.OldestAvailableCursor != encodeCursor(SourceKindMixed, "gw-a:2000:1") {
		t.Errorf("oldestAvailableCursor = %q, want the oldest retained merged entry", page.Pagination.OldestAvailableCursor)
	}
	if page.Pagination.SuggestedAction != "resync" {
		t.Errorf("suggestedAction = %q, want resync", page.Pagination.SuggestedAction)
	}
	if gaps != 1 {
		t.Errorf("gap counter fired %d times, want 1", gaps)
	}
	if len(page.Items) != 2 {
		t.Errorf("a gapped page must still serve the retained window, got %d items", len(page.Items))
	}
}

// spec: 25.5 (cross-source cursor translation) — a cursor absent from the
// merged gateway window but ordering inside it is the ordinary source-switch
// case (the caller's last event was a lenny-ops-originated one no gateway
// replica buffers), so the page continues from the next event in order with no
// gap. The eviction test runs over the raw merged window, so narrowing the
// filter is not misread as an eviction either.
func TestGatewayPollPage_ContinuesWithoutGapOnOrdinarySwitch_spec_25_5(t *testing.T) {
	gaps := 0
	s := New(Options{RedisClient: &fakeStream{}, SourceHealth: newMutableHealth(false, true), Now: ts, OnGap: func() { gaps++ }})
	s.SetGatewayBufferSource(&fakeGatewaySource{pages: [][]gwevents.BufferedEvent{{
		bufEvt("gw-a:1000:1", "dev.lenny.alert_fired"),
		bufEvt("gw-a:1000:3", "dev.lenny.pool_state_changed"),
	}}})

	page := s.gatewayPollPage(context.Background(), SourceKindMixed, "ops:1000:2", gwevents.EventFilter{}, 10, false)
	if page.Pagination.GapDetected || gaps != 0 {
		t.Fatal("a cursor ordering inside the retained window must not report a gap")
	}
	if len(page.Items) != 1 || page.Items[0].Event.ID != "gw-a:1000:3" {
		t.Fatalf("served %v, want only the continuation gw-a:1000:3", page.Items)
	}

	// A filtered page whose cursor event does not match the filter is still a
	// continuation rather than an eviction.
	filtered := s.gatewayPollPage(context.Background(), SourceKindMixed, "gw-a:1000:1", gwevents.EventFilter{EventType: "pool_state_changed"}, 10, false)
	if filtered.Pagination.GapDetected || gaps != 0 {
		t.Fatalf("narrowing the filter must not be reported as a gap: %+v", filtered.Pagination)
	}
}

// spec: 25.5 (the in-memory buffer stays the canonical source of
// lenny-ops-originated events) — during a Redis-down / gateway-up outage the
// poll page serves the union of the gateway fan-out and this replica's local
// ring, deduped by eventKey. lenny-ops-originated events exist nowhere else for
// the duration of the outage: no gateway replica buffers them and their XADD is
// failing. The pre-fix poll served the fan-out alone, so a Redis-only outage
// dropped them entirely, losing more than the strictly worse dual outage; this
// fails against that code.
func TestGatewayPollPage_UnionsLocalOriginEvents_spec_25_5(t *testing.T) {
	s := New(Options{RedisClient: &fakeStream{}, SourceHealth: newMutableHealth(false, true), Now: ts})
	s.SetGatewayBufferSource(&fakeGatewaySource{pages: [][]gwevents.BufferedEvent{{
		bufEvt("gw-a:1000:1", "dev.lenny.alert_fired"),
	}}})
	if _, err := s.Publish(context.Background(), evt("ops:1000:2", "dev.lenny.escalation_created")); err != nil {
		t.Fatalf("publish local event: %v", err)
	}

	page := s.gatewayPollPage(context.Background(), SourceKindMixed, "", gwevents.EventFilter{}, 10, false)
	got := eventKeys(page.Items)
	if len(got) != 2 {
		t.Fatalf("served %v, want both the gateway event and the local-origin one", got)
	}
	found := false
	for _, k := range got {
		if k == "ops:1000:2" {
			found = true
		}
	}
	if !found {
		t.Fatalf("served %v; the lenny-ops-originated event must stay observable during a Redis-only outage", got)
	}
}

// spec: 25.5 (the in-memory buffer stays the canonical source of
// lenny-ops-originated events) — an SSE connection served from the
// gateway-buffer fall-back stays subscribed to this replica's local publishes,
// so a lenny-ops-originated event emitted during the outage reaches the open
// connection rather than waiting for Redis to recover. The pre-fix serveGateway
// never subscribed, so the event was never delivered; this fails against that
// code.
func TestServeGateway_DeliversLiveLocalOriginEvents_spec_25_5(t *testing.T) {
	s := New(Options{RedisClient: &fakeStream{}, SourceHealth: newMutableHealth(false, true), Now: ts})
	src := &oneShotGatewaySource{
		pages:  [][]gwevents.BufferedEvent{{bufEvt("gw-a:1000:1", "dev.lenny.alert_fired")}},
		called: make(chan struct{}),
	}
	s.SetGatewayBufferSource(src)

	rec := &syncRecorder{hdr: http.Header{}}
	sess := &streamSession{s: s, w: rec, flusher: rec}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		sess.serveGateway(ctx, dsGateway)
	}()

	// After the first fan-out fill, publish a lenny-ops-originated event; it
	// must reach the connection between fan-out ticks.
	<-src.called
	if _, err := s.Publish(ctx, evt("ops:1000:2", "dev.lenny.escalation_created")); err != nil {
		t.Fatalf("publish local event: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(rec.String(), "ops:1000:2") {
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatalf("the lenny-ops-originated event never reached the open fall-back connection:\n%s", rec.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
}

// syncRecorder is a streaming ResponseWriter+Flusher safe to read while a
// handler goroutine writes to it.
type syncRecorder struct {
	mu  sync.Mutex
	buf strings.Builder
	hdr http.Header
}

func (r *syncRecorder) Header() http.Header { return r.hdr }
func (r *syncRecorder) WriteHeader(int)     {}
func (r *syncRecorder) Flush()              {}
func (r *syncRecorder) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf.Write(p)
}

func (r *syncRecorder) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf.String()
}

// timedEvt builds an event carrying an explicit emission time so a test can
// order the local ring against the gateway fan-out window deterministically.
func timedEvt(id, typ string, at time.Time) gwevents.OperationalEvent {
	e := evt(id, typ)
	e.Time = at
	return e
}

// spec: 25.5 (gapDetected and oldestAvailableCursor when the cursor aged out
// of every replica's ring) — during the Redis-down gateway-buffer fall-back,
// the eviction boundary is the oldest event the gateway replicas still
// retain. This replica's local ring holds only lenny-ops-originated events
// and outlives the gateway rings, so measuring the boundary against the
// union of the two puts it before every genuinely aged-out cursor. The
// pre-fix gatewayPollPage compared the cursor against the post-union window
// and reported no gap here, silently skipping the evicted gateway events;
// this test fails against that code.
func TestGatewayPollPage_EvictionGapMeasuredAgainstFanOutWindow_spec_25_5(t *testing.T) {
	base := ts()
	gaps := 0
	s := New(Options{SourceHealth: newMutableHealth(false, true), Now: ts, OnGap: func() { gaps++ }})
	// An old lenny-ops-originated event, retained locally long after the
	// gateway rings rolled past it.
	if _, err := s.Publish(context.Background(), timedEvt("ops:1000:1", "dev.lenny.alert_fired", base)); err != nil {
		t.Fatalf("publish local event: %v", err)
	}
	s.SetGatewayBufferSource(&fakeGatewaySource{pages: [][]gwevents.BufferedEvent{{
		{ID: 1, Event: timedEvt("gw-a:5000:1", "dev.lenny.alert_fired", base.Add(5*time.Second))},
		{ID: 2, Event: timedEvt("gw-a:6000:1", "dev.lenny.alert_fired", base.Add(6*time.Second))},
	}}})

	// A cursor newer than the local event but older than everything the
	// gateway replicas still hold: the gateway events in between were evicted.
	page := s.gatewayPollPage(context.Background(), SourceKindMixed, "gw-a:2000:1", gwevents.EventFilter{}, 10, false)

	if !page.Pagination.GapDetected {
		t.Fatalf("gapDetected = false for a cursor older than the whole gateway window; items = %v", eventKeys(page.Items))
	}
	if gaps != 1 {
		t.Errorf("OnGap fired %d times, want 1", gaps)
	}
	if page.Pagination.SuggestedAction != "resync" {
		t.Errorf("suggestedAction = %q, want resync", page.Pagination.SuggestedAction)
	}
	_, oldest, err := decodeCursor(page.Pagination.OldestAvailableCursor)
	if err != nil {
		t.Fatalf("decode oldestAvailableCursor: %v", err)
	}
	if oldest != "gw-a:5000:1" {
		t.Errorf("oldestAvailableCursor = %q, want the oldest retained gateway event gw-a:5000:1", oldest)
	}
}

// spec: 25.5 (cross-source cursor translation; continuation at the first
// greater-or-equal eventKey) — a cursor inside the retained gateway window is
// not an eviction, so the fall-back continues after it with no gap even
// though this replica's local ring holds older events.
func TestGatewayPollPage_CursorInsideFanOutWindowIsNotAGap_spec_25_5(t *testing.T) {
	base := ts()
	s := New(Options{SourceHealth: newMutableHealth(false, true), Now: ts})
	if _, err := s.Publish(context.Background(), timedEvt("ops:1000:1", "dev.lenny.alert_fired", base)); err != nil {
		t.Fatalf("publish local event: %v", err)
	}
	s.SetGatewayBufferSource(&fakeGatewaySource{pages: [][]gwevents.BufferedEvent{{
		{ID: 1, Event: timedEvt("gw-a:5000:1", "dev.lenny.alert_fired", base.Add(5*time.Second))},
		{ID: 2, Event: timedEvt("gw-a:6000:1", "dev.lenny.alert_fired", base.Add(6*time.Second))},
	}}})

	page := s.gatewayPollPage(context.Background(), SourceKindMixed, "gw-a:5000:1", gwevents.EventFilter{}, 10, false)
	if page.Pagination.GapDetected {
		t.Fatalf("gapDetected = true for a cursor inside the retained gateway window")
	}
	if got := eventKeys(page.Items); len(got) != 1 || got[0] != "gw-a:6000:1" {
		t.Errorf("items = %v, want the single continuation event gw-a:6000:1", got)
	}
}
