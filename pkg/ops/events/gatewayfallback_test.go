// SPDX-License-Identifier: MIT

package events

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
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

// recordingGatewaySource records the fan-out paths it was asked for and serves
// one replica holding an empty buffer, so a test can assert what the read side
// asks each gateway pod for. It serves a replica rather than none: a fan-out
// that discovers no replica is the §25.5 dual-outage case, so a no-replica stub
// would fail the fetch before the path assertion is reached.
type recordingGatewaySource struct {
	paths []string
}

func (r *recordingGatewaySource) FanOutGet(_ context.Context, path string) ([]gateway.ReplicaResult, error) {
	r.paths = append(r.paths, path)
	return []gateway.ReplicaResult{{Endpoint: "gw-a", Body: emptyBufferPage()}}, nil
}

// emptyBufferPage is a §25.3 buffer response from a replica that served the
// query and holds no events.
func emptyBufferPage() json.RawMessage {
	return json.RawMessage(`{"events":[]}`)
}

// filteringGatewaySource is a GatewayBufferSource that honours the §25.3
// buffer endpoint's eventType and severity query params the way a real gateway
// replica does, so a narrowing filter pushed to the replicas actually narrows
// the window the read side receives. The plain fake ignores the query string,
// which is what let a filter-driven empty fan-out pass unnoticed.
type filteringGatewaySource struct {
	pages [][]gwevents.BufferedEvent
}

func (f *filteringGatewaySource) FanOutGet(_ context.Context, path string) ([]gateway.ReplicaResult, error) {
	q := url.Values{}
	if i := strings.Index(path, "?"); i >= 0 {
		parsed, err := url.ParseQuery(path[i+1:])
		if err != nil {
			return nil, err
		}
		q = parsed
	}
	replicaFilter := gwevents.EventFilter{EventType: q.Get("eventType"), Severity: q.Get("severity")}
	out := make([]gateway.ReplicaResult, 0, len(f.pages))
	for _, evs := range f.pages {
		kept := make([]gwevents.BufferedEvent, 0, len(evs))
		for _, ev := range evs {
			if replicaFilter.Matches(ev.Event) {
				kept = append(kept, ev)
			}
		}
		body, _ := json.Marshal(gwevents.BufferedEventPage{Events: kept})
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
	sess := &streamSession{s: s, w: rec, flusher: rec, lastKey: "gw-a:1000:2", scope: readerScope{platformAdmin: true}}

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
	sess := &streamSession{s: s, w: rec, flusher: rec, lastKey: "gw-a:1000:1", scope: readerScope{platformAdmin: true}}

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
	s.HandlePoll(rec, platformAdminReq(req))

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
	s.HandlePoll(rec, platformAdminReq(httptest.NewRequest(http.MethodGet, "/v1/admin/events", nil)))
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

// spec: 25.5 (transparent Redis to gateway-buffer switch and recovery; the
// canonical degradation envelope is embedded in a periodic :degradation comment
// line) — an open SSE connection re-emits the :degradation envelope on every
// fall-back poll for the life of a degraded stint, so a consumer that attaches
// mid-outage learns its view is degraded, and announces its return to the
// healthy Redis source with :degradation {"level":"healthy"} exactly once. The
// initial entry into a healthy source writes nothing.
func TestStreamTransition_AnnouncesDegradeAndRecovery_spec_25_5(t *testing.T) {
	health := newMutableHealth(false, true)
	s := New(Options{RedisClient: &fakeStream{}, SourceHealth: health, Now: ts})
	s.SetGatewayBufferSource(&fakeGatewaySource{})
	rec := httptest.NewRecorder()
	sess := &streamSession{s: s, w: rec, flusher: rec, scope: readerScope{platformAdmin: true}}

	// Serving the gateway fall-back carries the envelope on every poll tick,
	// with the classification unchanged, so the consumer keeps learning its view
	// is degraded for as long as the outage lasts.
	sess.announceDegradation(false)
	sess.announceDegradation(false)
	if got := strings.Count(rec.Body.String(), sourceGatewayBuffer); got != 2 {
		t.Fatalf("the gateway fall-back carried the degradation envelope %d times over two poll ticks, want one per tick:\n%s", got, rec.Body.String())
	}

	// Redis recovers: switching back to Redis announces recovery.
	health.redis.Store(true)
	rec2 := httptest.NewRecorder()
	sess.w = rec2
	sess.flusher = rec2
	sess.announceRecovery(dsGateway, dsRedis)
	if !strings.Contains(rec2.Body.String(), "\"level\":\"healthy\"") {
		t.Fatalf("switch back to Redis did not announce recovery:\n%s", rec2.Body.String())
	}

	// A fresh healthy start (no prior source) announces nothing.
	rec3 := httptest.NewRecorder()
	sess.w = rec3
	sess.flusher = rec3
	sess.announceRecovery(dataSource(-1), dsRedis)
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

// spec: 25.5 (Redis-down gateway-buffer fallback) — fetchGatewayBuffer returns
// an error when no gateway source is wired and when the fan-out itself fails,
// and it queries every replica for the unnarrowed window so the eviction
// boundary the caller derives from it describes what the replicas retain.
func TestFetchGatewayBuffer_ErrorsAndUnnarrowedQuery_spec_25_5(t *testing.T) {
	// No gateway source wired: the fetch fails closed rather than serving an
	// empty page as if the buffer were empty.
	s := New(Options{RedisClient: &fakeStream{}, SourceHealth: newMutableHealth(false, true), Now: ts})
	if _, err := s.fetchGatewayBuffer(context.Background()); err == nil {
		t.Fatal("fetchGatewayBuffer with no gateway source wired must return an error")
	}

	// A fan-out failure propagates as an error.
	s.SetGatewayBufferSource(&fakeGatewaySource{err: context.DeadlineExceeded})
	if _, err := s.fetchGatewayBuffer(context.Background()); err == nil {
		t.Fatal("fetchGatewayBuffer must propagate a fan-out failure")
	}

	// The per-replica request carries no filter query.
	rec := &recordingGatewaySource{}
	s.SetGatewayBufferSource(rec)
	if _, err := s.fetchGatewayBuffer(context.Background()); err != nil {
		t.Fatalf("fetchGatewayBuffer: %v", err)
	}
	if got := rec.paths[0]; got != gatewayBufferPath {
		t.Fatalf("fan-out path = %q, want the unnarrowed %q", got, gatewayBufferPath)
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
	page, _ := s.gatewayPollPage(context.Background(), SourceKindMixed, "gw-a:1000:1", gwevents.EventFilter{}, 1, false)
	if len(page.Items) != 1 || page.Items[0].Event.ID != "gw-a:1000:2" {
		t.Fatalf("resumed page = %v, want [gw-a:1000:2]", page.Items)
	}
	if !page.Pagination.HasMore {
		t.Fatalf("resumed page must report hasMore with events remaining after the limit")
	}

	// A fan-out failure serves no gateway-buffer page at all: the request
	// carries no gateway-originated events, so it is the dual-outage case.
	s.SetGatewayBufferSource(&fakeGatewaySource{err: context.DeadlineExceeded})
	if _, err := s.gatewayPollPage(context.Background(), SourceKindMixed, "gw-a:1000:2", gwevents.EventFilter{}, 10, false); err == nil {
		t.Fatal("a fan-out failure must not serve a gateway-buffer page")
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

	page, _ := s.gatewayPollPage(context.Background(), SourceKindMixed, "gw-a:1000:1", gwevents.EventFilter{}, 10, false)
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

	page, _ := s.gatewayPollPage(context.Background(), SourceKindMixed, "ops:1000:2", gwevents.EventFilter{}, 10, false)
	if page.Pagination.GapDetected || gaps != 0 {
		t.Fatal("a cursor ordering inside the retained window must not report a gap")
	}
	if len(page.Items) != 1 || page.Items[0].Event.ID != "gw-a:1000:3" {
		t.Fatalf("served %v, want only the continuation gw-a:1000:3", page.Items)
	}

	// A filtered page whose cursor event does not match the filter is still a
	// continuation rather than an eviction.
	filtered, _ := s.gatewayPollPage(context.Background(), SourceKindMixed, "gw-a:1000:1", gwevents.EventFilter{EventType: "pool_state_changed"}, 10, false)
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

	page, _ := s.gatewayPollPage(context.Background(), SourceKindMixed, "", gwevents.EventFilter{}, 10, false)
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
	sess := &streamSession{s: s, w: rec, flusher: rec, scope: readerScope{platformAdmin: true}}

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
	page, _ := s.gatewayPollPage(context.Background(), SourceKindMixed, "gw-a:2000:1", gwevents.EventFilter{}, 10, false)

	if !page.Pagination.GapDetected {
		t.Fatalf("gapDetected = false for a cursor older than the whole gateway window; items = %v", eventKeys(page.Items))
	}
	if gaps != 1 {
		t.Errorf("OnGap fired %d times, want 1", gaps)
	}
	if page.Pagination.SuggestedAction != "resync" {
		t.Errorf("suggestedAction = %q, want resync", page.Pagination.SuggestedAction)
	}
	oldestCur, err := decodeCursor(page.Pagination.OldestAvailableCursor)
	oldest := oldestCur.EventKey
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

	page, _ := s.gatewayPollPage(context.Background(), SourceKindMixed, "gw-a:5000:1", gwevents.EventFilter{}, 10, false)
	if page.Pagination.GapDetected {
		t.Fatalf("gapDetected = true for a cursor inside the retained gateway window")
	}
	if got := eventKeys(page.Items); len(got) != 1 || got[0] != "gw-a:6000:1" {
		t.Errorf("items = %v, want the single continuation event gw-a:6000:1", got)
	}
}

// spec: 25.5 (cursor transition safety: a gap is reported when no event in the
// new source has a greater-or-equal eventKey) — a poll whose cursor orders
// after every event the fan-out window retains cannot be located in that
// window, so the page reports the gap with oldestAvailableCursor and serves
// nothing. The window is not replayed: the caller already holds it. The pre-fix
// predicate reported a gap only below the window, so this position resumed
// silently; this fails against that code.
func TestGatewayPollPage_GapWhenNoEventOrdersAtOrAfterCursor_spec_25_5(t *testing.T) {
	gaps := 0
	s := New(Options{RedisClient: &fakeStream{}, SourceHealth: newMutableHealth(false, true), Now: ts, OnGap: func() { gaps++ }})
	s.SetGatewayBufferSource(&fakeGatewaySource{pages: [][]gwevents.BufferedEvent{{
		bufEvt("gw-a:1000:1", "dev.lenny.alert_fired"),
		bufEvt("gw-a:1000:2", "dev.lenny.alert_fired"),
	}}})

	page, _ := s.gatewayPollPage(context.Background(), SourceKindMixed, "ops:9000:1", gwevents.EventFilter{}, 10, false)
	if !page.Pagination.GapDetected {
		t.Fatal("a cursor ordering after the whole fan-out window must report gapDetected")
	}
	if gaps != 1 {
		t.Errorf("gap counter = %d, want 1", gaps)
	}
	if page.Pagination.OldestAvailableCursor == "" {
		t.Error("a reported gap must carry oldestAvailableCursor")
	}
	if got := eventKeys(page.Items); len(got) != 0 {
		t.Errorf("items = %v, want none: a caller ahead of the window is not re-served it", got)
	}
}

// spec: 25.5 (cursor transition safety) — a mid-connection switch into the
// gateway-buffer fall-back whose carried resume position orders after every
// event the replicas retain emits a :gap comment, since the new source has no
// greater-or-equal eventKey to continue from. Nothing is re-sent: the whole
// window is already behind the resume position. The pre-fix seed treated that
// position as honoured and emitted no gap; this fails against that code.
func TestServeGateway_ResumeAheadOfWindowEmitsGap_spec_25_5(t *testing.T) {
	gaps := 0
	s := New(Options{RedisClient: &fakeStream{}, SourceHealth: newMutableHealth(false, true), Now: ts, OnGap: func() { gaps++ }})
	src := &oneShotGatewaySource{
		pages: [][]gwevents.BufferedEvent{{
			bufEvt("gw-a:1000:1", "dev.lenny.alert_fired"),
			bufEvt("gw-a:1000:2", "dev.lenny.alert_fired"),
		}},
		called: make(chan struct{}),
	}
	s.SetGatewayBufferSource(src)

	rec := httptest.NewRecorder()
	sess := &streamSession{s: s, w: rec, flusher: rec, lastKey: "ops:9000:1", scope: readerScope{platformAdmin: true}}

	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-src.called; cancel() }()
	sess.serveGateway(ctx, dsGateway)

	body := rec.Body.String()
	if !strings.Contains(body, ":gap") {
		t.Fatalf("a resume position ahead of the whole window must emit a :gap comment:\n%s", body)
	}
	if gaps != 1 {
		t.Fatalf("gap counter observed %d times, want 1", gaps)
	}
	if strings.Contains(body, "gw-a:1000:1") || strings.Contains(body, "gw-a:1000:2") {
		t.Fatalf("the window is behind the resume position and must not be delivered:\n%s", body)
	}
}

// spec: 25.5 (cross-source cursor translation; exactly-once across the source
// switch) — a switch into this replica's local ring resolves the carried
// position by eventKey order, so a position the ring never held (the ordinary
// case: the last event delivered came from the Redis stream or a gateway
// replica) resumes at the continuation point. The pre-fix serveLocal looked the
// key up verbatim and, on the miss, replayed the whole retained ring from its
// oldest event; this fails against that code by asserting the already-delivered
// events are not re-sent.
func TestServeLocal_ResumesByEventKeyOrder_spec_25_5(t *testing.T) {
	gaps := 0
	s := New(Options{SourceHealth: newMutableHealth(false, false), Now: ts, OnGap: func() { gaps++ }})
	for _, key := range []string{"ops:1000:1", "ops:2000:1", "ops:3000:1"} {
		if _, err := s.Publish(context.Background(), evt(key, "dev.lenny.alert_fired")); err != nil {
			t.Fatalf("publish %s: %v", key, err)
		}
	}

	rec := httptest.NewRecorder()
	// gw:2500:1 is a gateway-originated event this replica's ring never held,
	// ordering between the second and third local events.
	sess := &streamSession{s: s, w: rec, flusher: rec, lastKey: "gw:2500:1", scope: readerScope{platformAdmin: true}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	sess.serveLocal(ctx, dsLocalBuffer)

	body := rec.Body.String()
	if strings.Contains(body, "ops:1000:1") || strings.Contains(body, "ops:2000:1") {
		t.Fatalf("events at or before the resume point were replayed:\n%s", body)
	}
	if !strings.Contains(body, "ops:3000:1") {
		t.Fatalf("the event after the resume point was not delivered:\n%s", body)
	}
	if strings.Contains(body, ":gap") || gaps != 0 {
		t.Fatalf("a resume position inside the retained ring must not report a gap:\n%s", body)
	}
}

// spec: 25.5 (cursor transition safety: a :gap when no event in the new source
// has a greater-or-equal eventKey; gapDetected on an evicted cursor) — the
// local ring reports a gap at both ends of its retained window, and replays
// only for the evicted position, where the caller has genuinely lost events.
func TestServeLocal_GapAtBothEndsOfTheRing_spec_25_5(t *testing.T) {
	for _, tc := range []struct {
		name       string
		lastKey    string
		wantReplay bool
	}{
		{name: "before the ring", lastKey: "ops:500:1", wantReplay: true},
		{name: "after the ring", lastKey: "ops:9000:1", wantReplay: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gaps := 0
			s := New(Options{SourceHealth: newMutableHealth(false, false), Now: ts, OnGap: func() { gaps++ }})
			for _, key := range []string{"ops:1000:1", "ops:2000:1"} {
				if _, err := s.Publish(context.Background(), evt(key, "dev.lenny.alert_fired")); err != nil {
					t.Fatalf("publish %s: %v", key, err)
				}
			}
			rec := httptest.NewRecorder()
			sess := &streamSession{s: s, w: rec, flusher: rec, lastKey: tc.lastKey, scope: readerScope{platformAdmin: true}}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			sess.serveLocal(ctx, dsLocalBuffer)

			body := rec.Body.String()
			if !strings.Contains(body, ":gap") || gaps != 1 {
				t.Fatalf("a position the ring cannot honour must emit one :gap (counter=%d):\n%s", gaps, body)
			}
			replayed := strings.Contains(body, "ops:1000:1")
			if replayed != tc.wantReplay {
				t.Fatalf("ring replayed = %v, want %v:\n%s", replayed, tc.wantReplay, body)
			}
		})
	}
}

// refusingGatewaySource is a GatewayBufferSource whose replicas all refuse the
// §25.3 buffer query, the way a gateway admin gate refuses a principal that
// does not hold the platform-admin role. The fan-out itself succeeds; every
// per-replica result carries the refusal.
type refusingGatewaySource struct {
	replicas int
	called   chan struct{}
	once     sync.Once
}

func (r *refusingGatewaySource) FanOutGet(_ context.Context, _ string) ([]gateway.ReplicaResult, error) {
	out := make([]gateway.ReplicaResult, 0, r.replicas)
	for i := 0; i < r.replicas; i++ {
		out = append(out, gateway.ReplicaResult{
			Endpoint: "https://pod",
			Err:      &gateway.HTTPError{Status: http.StatusForbidden, Body: []byte("admin endpoint requires the platform-admin role")},
		})
	}
	if r.called != nil {
		r.once.Do(func() { close(r.called) })
	}
	return out, nil
}

// spec: 25.5 (actualSource names the source the response was served from; both
// sources unreachable returns 503 EVENT_STREAM_UNAVAILABLE) — when every
// gateway replica refuses or fails the §25.3 buffer query, the fall-back has no
// gateway-originated events at all. The pre-fix poll skipped every errored
// replica and returned an empty 200 labelled gateway-buffer, so a refused
// principal was indistinguishable from a quiet gateway; this fails against that
// code by requiring the dual-outage 503.
func TestHandlePoll_AllReplicasRefused_IsUnavailableNotDegradedOK_spec_25_5(t *testing.T) {
	s := New(Options{RedisClient: &fakeStream{}, SourceHealth: newMutableHealth(false, true), Now: ts})
	s.SetGatewayBufferSource(&refusingGatewaySource{replicas: 2})
	if _, err := s.Publish(context.Background(), evt("ops:1000:1", "dev.lenny.escalation_created")); err != nil {
		t.Fatalf("publish local event: %v", err)
	}

	rec := httptest.NewRecorder()
	s.HandlePoll(rec, platformAdminReq(httptest.NewRequest(http.MethodGet, "/v1/admin/events", nil)))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503: no replica served the buffer query, so the response carries no gateway events\n%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), codeEventStreamUnavailable) {
		t.Errorf("body = %s, want the §25.5 %s error code", rec.Body.String(), codeEventStreamUnavailable)
	}
}

// spec: 25.5 (dual-outage local-buffer serving) — an SSE connection in the
// gateway-buffer fall-back whose replicas all refuse the query announces the
// lenny-ops-local-buffer degradation, so the consumer learns it is receiving
// this replica's events only rather than a cross-replica view. The pre-fix
// serve loop silently skipped the failed fetch and kept the gateway-buffer
// label; this fails against that code.
func TestServeGateway_AllReplicasRefused_AnnouncesLocalBufferDegradation_spec_25_5(t *testing.T) {
	s := New(Options{RedisClient: &fakeStream{}, SourceHealth: newMutableHealth(false, true), Now: ts})
	src := &refusingGatewaySource{replicas: 2, called: make(chan struct{})}
	s.SetGatewayBufferSource(src)
	if _, err := s.Publish(context.Background(), evt("ops:1000:1", "dev.lenny.escalation_created")); err != nil {
		t.Fatalf("publish local event: %v", err)
	}

	rec := httptest.NewRecorder()
	sess := &streamSession{s: s, w: rec, flusher: rec, scope: readerScope{platformAdmin: true}}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-src.called; cancel() }()
	sess.serveGateway(ctx, dsGateway)

	body := rec.Body.String()
	if !strings.Contains(body, sourceOpsLocalBuffer) {
		t.Fatalf("a fall-back serving no gateway replica must announce the %s degradation:\n%s", sourceOpsLocalBuffer, body)
	}
	if !strings.Contains(body, "ops:1000:1") {
		t.Fatalf("the local ring must keep serving lenny-ops-originated events:\n%s", body)
	}
}

// spec: 25.5 — a fan-out where at least one replica answered is still the
// gateway-buffer fall-back: the merge is best-effort across the replicas that
// did respond, so one failed pod does not escalate the request to the
// dual-outage case.
func TestFetchGatewayBuffer_OneReplicaAnsweringIsStillServed_spec_25_5(t *testing.T) {
	s := New(Options{RedisClient: &fakeStream{}, SourceHealth: newMutableHealth(false, true), Now: ts})
	s.SetGatewayBufferSource(&partialGatewaySource{})
	merged, err := s.fetchGatewayBuffer(context.Background())
	if err != nil {
		t.Fatalf("a fan-out with one answering replica must serve: %v", err)
	}
	if got := eventKeys(merged); len(got) != 1 || got[0] != "gw-a:1000:1" {
		t.Fatalf("merged = %v, want the answering replica's event", got)
	}
}

// partialGatewaySource answers from one replica and fails on the other.
type partialGatewaySource struct{}

func (partialGatewaySource) FanOutGet(_ context.Context, _ string) ([]gateway.ReplicaResult, error) {
	body, _ := json.Marshal(gwevents.BufferedEventPage{Events: []gwevents.BufferedEvent{bufEvt("gw-a:1000:1", "dev.lenny.alert_fired")}})
	return []gateway.ReplicaResult{
		{Endpoint: "https://pod-a", Body: body},
		{Endpoint: "https://pod-b", Err: &gateway.HTTPError{Status: http.StatusInternalServerError}},
	}, nil
}

// spec: 25.5 (gapDetected and oldestAvailableCursor only when the cursor aged
// out of every replica's ring) — a narrowing ?eventType= that no gateway
// replica matches is not an eviction. The eviction boundary is derived from the
// window the replicas retain, so the fan-out query carries no filter and a poll
// for a lenny-ops-originated event type continues from its cursor. The pre-fix
// read side pushed the filter to each replica and took the boundary from that
// narrowed page, so an empty fan-out reported a gap and replayed the whole local
// window on every poll; this fails against that code.
func TestGatewayPollPage_NarrowedFilterMatchingNoGatewayEventIsNotAGap_spec_25_5(t *testing.T) {
	base := ts()
	gaps := 0
	s := New(Options{SourceHealth: newMutableHealth(false, true), Now: ts, OnGap: func() { gaps++ }})
	s.SetGatewayBufferSource(&filteringGatewaySource{pages: [][]gwevents.BufferedEvent{{
		{ID: 1, Event: timedEvt("gw-a:1000:1", "dev.lenny.alert_fired", base.Add(time.Second))},
	}}})
	for _, e := range []gwevents.OperationalEvent{
		timedEvt("ops:2000:1", "dev.lenny.escalation_created", base.Add(2*time.Second)),
		timedEvt("ops:3000:1", "dev.lenny.escalation_created", base.Add(3*time.Second)),
	} {
		if _, err := s.Publish(context.Background(), e); err != nil {
			t.Fatalf("publish local event %s: %v", e.ID, err)
		}
	}

	filter := gwevents.EventFilter{EventType: "escalation_created"}
	page, err := s.gatewayPollPage(context.Background(), SourceKindMixed, "ops:2000:1", filter, 10, false)
	if err != nil {
		t.Fatalf("gatewayPollPage: %v", err)
	}
	if page.Pagination.GapDetected || gaps != 0 {
		t.Fatalf("a filter no gateway replica matches was reported as an evicted cursor: %+v (gap counter %d)", page.Pagination, gaps)
	}
	if got := eventKeys(page.Items); len(got) != 1 || got[0] != "ops:3000:1" {
		t.Fatalf("items = %v, want only the continuation ops:3000:1; the window before the cursor must not be re-served", got)
	}

	// A repeat poll from the position the response returned serves nothing and
	// still reports no gap, so a caller polling in a loop does not receive the
	// same window on every request.
	repeat, err := s.gatewayPollPage(context.Background(), SourceKindMixed, "ops:3000:1", filter, 10, false)
	if err != nil {
		t.Fatalf("repeat gatewayPollPage: %v", err)
	}
	if repeat.Pagination.GapDetected || gaps != 0 {
		t.Fatalf("repeat poll reported a gap: %+v (gap counter %d)", repeat.Pagination, gaps)
	}
	if got := eventKeys(repeat.Items); len(got) != 0 {
		t.Fatalf("repeat poll re-served %v, want nothing new", got)
	}
}

// spec: 25.5 (gapDetected and oldestAvailableCursor only when the cursor aged
// out of every replica's ring) — a gateway that retains nothing (a freshly
// restarted or idle replica) carries no evidence about the carried cursor, so
// the poll continues from it against the local ring instead of reporting a gap
// and replaying the window. It is the same rule the Redis source and the SSE
// resume seed apply to an empty source window; the pre-fix poll path was the
// one place that read an empty window as an eviction, and this fails against
// that code.
func TestGatewayPollPage_EmptyFanOutWindowIsNotAGap_spec_25_5(t *testing.T) {
	base := ts()
	gaps := 0
	s := New(Options{SourceHealth: newMutableHealth(false, true), Now: ts, OnGap: func() { gaps++ }})
	s.SetGatewayBufferSource(&fakeGatewaySource{pages: [][]gwevents.BufferedEvent{{}}})
	for _, e := range []gwevents.OperationalEvent{
		timedEvt("ops:2000:1", "dev.lenny.escalation_created", base.Add(2*time.Second)),
		timedEvt("ops:3000:1", "dev.lenny.escalation_created", base.Add(3*time.Second)),
	} {
		if _, err := s.Publish(context.Background(), e); err != nil {
			t.Fatalf("publish local event %s: %v", e.ID, err)
		}
	}

	page, err := s.gatewayPollPage(context.Background(), SourceKindMixed, "ops:2000:1", gwevents.EventFilter{}, 10, false)
	if err != nil {
		t.Fatalf("gatewayPollPage: %v", err)
	}
	if page.Pagination.GapDetected || gaps != 0 {
		t.Fatalf("an empty gateway window was reported as an evicted cursor: %+v (gap counter %d)", page.Pagination, gaps)
	}
	if got := eventKeys(page.Items); len(got) != 1 || got[0] != "ops:3000:1" {
		t.Fatalf("items = %v, want only the continuation ops:3000:1", got)
	}
}

// spec: 25.5 (delivery in eventKey order across a source switch; continuation
// at the first greater-or-equal eventKey) — the merged fan-out window is
// ordered by the same relation the cursor scan resolves against, so a
// same-instant nonce tie does not re-deliver the event the caller already
// consumed.
//
// The eventKey's trailing nonce is numeric, so a byte comparison of two
// same-emittedAt keys from one replica orders "gw-a:2000:10" before
// "gw-a:2000:9" while eventKeyLess orders nonce 9 first. gatewayResumeIndex is
// a single forward scan that assumes the window is already in eventKeyLess
// order; against the byte-ordered window it stops at index 0 and serves nonce 9
// again. This test fails against that ordering.
func TestGatewayPollPage_SameInstantNonceTieDoesNotRedeliver_spec_25_5(t *testing.T) {
	at := ts()
	// Two events emitted by one gateway replica in the same instant: the tie
	// break is the numeric nonce alone.
	nine := gwevents.BufferedEvent{Event: timedEvt("gw-a:2000:9", "dev.lenny.alert_fired", at)}
	ten := gwevents.BufferedEvent{Event: timedEvt("gw-a:2000:10", "dev.lenny.alert_fired", at)}

	s := New(Options{SourceHealth: newMutableHealth(false, true), Now: ts})
	// Feed the higher nonce first so the merge has to order them.
	s.SetGatewayBufferSource(&fakeGatewaySource{pages: [][]gwevents.BufferedEvent{{ten, nine}}})

	results, _ := (&fakeGatewaySource{pages: [][]gwevents.BufferedEvent{{ten, nine}}}).FanOutGet(context.Background(), "")
	if got := eventKeys(mergeReplicaBuffers(results)); len(got) != 2 || got[0] != "gw-a:2000:9" || got[1] != "gw-a:2000:10" {
		t.Fatalf("merged order = %v, want eventKey order [gw-a:2000:9 gw-a:2000:10]", got)
	}

	page, err := s.gatewayPollPage(context.Background(), SourceKindMixed, "gw-a:2000:9", gwevents.EventFilter{}, 10, false)
	if err != nil {
		t.Fatalf("gatewayPollPage: %v", err)
	}
	if page.Pagination.GapDetected {
		t.Fatalf("a cursor inside the window was reported as a gap: %+v", page.Pagination)
	}
	if got := eventKeys(page.Items); len(got) != 1 || got[0] != "gw-a:2000:10" {
		t.Fatalf("items = %v, want only the continuation gw-a:2000:10", got)
	}
}
