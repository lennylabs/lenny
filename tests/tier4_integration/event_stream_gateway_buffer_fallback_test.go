//go:build integration

// SPDX-License-Identifier: MIT

// Tier-4 integration test for the §25.5 read-surface Redis-down /
// gateway-up degradation: when Redis is
// unreachable but the gateway is up, the SSE/polling read surface falls
// back to the gateway's in-memory event buffer (§25.3), fans the query
// across every gateway replica over the lenny-gateway-pods headless
// Service, merges the per-replica pages deduplicated by eventKey, and
// serves the gateway-originated events labelled with the canonical
// degradation envelope actualSource: "gateway-buffer".
//
// The fan-out is driven end to end through the real pkg/ops/gateway.Client
// against two in-process gateway replicas (httptest servers) discovered via
// gateway.StaticDiscovery, so the merge, eventKey dedup, and degradation
// envelope are exercised on the live read path rather than mocked.
package tier4_integration_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gwevents "github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/gateway/eventbuffer"
	opsstream "github.com/lennylabs/lenny/pkg/ops/events"
	"github.com/lennylabs/lenny/pkg/ops/gateway"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// redisDownGatewayUp is the §25.5 Redis-down / gateway-up source-health signal.
type redisDownGatewayUp struct{}

func (redisDownGatewayUp) RedisAvailable() bool   { return false }
func (redisDownGatewayUp) GatewayAvailable() bool { return true }

// bufferReplica serves GET /v1/admin/events/buffer with a fixed page, standing
// in for one gateway pod's §25.3 in-memory event buffer.
func bufferReplica(t *testing.T, events []gwevents.BufferedEvent) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/admin/events/buffer" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(gwevents.BufferedEventPage{Events: events})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func bufEvent(id, typ string) gwevents.BufferedEvent {
	return gwevents.BufferedEvent{
		ID: 1,
		Event: gwevents.OperationalEvent{
			ID:          id,
			Type:        typ,
			SpecVersion: gwevents.CloudEventsSpecVersion,
			Time:        time.Unix(1000, 0).UTC(),
		},
	}
}

// spec: 25.5 (Redis-down gateway-buffer fallback, eventKey dedup); 25.3
// (cross-replica eventKey dedup over the headless Service) — with Redis down
// and two gateway replicas up, a poll of GET /v1/admin/events fans the buffer
// query across both replicas, merges the pages deduped by eventKey, and serves
// the gateway-originated events with the actualSource: "gateway-buffer"
// degradation envelope. The pre-fix read surface served only the local ring
// buffer (lenny-ops-originated events, empty here), so a gateway-originated
// alert never reached the response; this fails against that code and passes
// once the cross-process fan-out fetch exists.
//
// diagnosis: a failure means the §25.5 Redis-down gateway-buffer read-surface fallback is broken:
// during a Redis outage with the gateway up, a poll returns no gateway events
// (the local-buffer-only path), or the merge loses a distinct same-second
// alert from one replica, or fails to collapse a genuine repeat delivery of
// the same eventKey, or omits the degradation envelope.
func TestOpsEventStreamServesGatewayEventsFromGatewayBufferWhenRedisDown(t *testing.T) {
	// Replica A and replica B each hold a distinct same-second alert_fired
	// (distinct eventKeys) plus a broadcast credential_rotated carrying the
	// SAME eventKey across both replicas (a genuine cross-replica repeat).
	repeat := bufEvent("broadcast:1000:9", "dev.lenny.credential_rotated")
	replicaA := bufferReplica(t, []gwevents.BufferedEvent{
		bufEvent("gw-a:1000:1", "dev.lenny.alert_fired"),
		repeat,
	})
	replicaB := bufferReplica(t, []gwevents.BufferedEvent{
		bufEvent("gw-b:1000:1", "dev.lenny.alert_fired"),
		repeat,
	})

	client, err := gateway.NewClient(gateway.Config{
		BaseURL:           "http://gateway.invalid",
		Token:             gateway.StaticToken("test-token"),
		Discovery:         gateway.StaticDiscovery{replicaA.URL, replicaB.URL},
		PerRequestTimeout: 5 * time.Second,
		FanOutTimeout:     2 * time.Second,
	})
	if err != nil {
		t.Fatalf("build gateway client: %v", err)
	}

	svc := opsstream.New(opsstream.Options{SourceHealth: redisDownGatewayUp{}})
	svc.SetGatewayBufferSource(client)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/events", nil)
	svc.HandlePoll(rec, platformAdminReq(req))

	if rec.Code != http.StatusOK {
		t.Fatalf("poll status = %d, want 200", rec.Code)
	}
	var page opsstream.EventPage
	if err := json.NewDecoder(rec.Body).Decode(&page); err != nil {
		t.Fatalf("decode poll body: %v", err)
	}

	if page.Degradation == nil || page.Degradation.ActualSource != "gateway-buffer" {
		t.Fatalf("expected actualSource gateway-buffer degradation envelope, got %+v", page.Degradation)
	}

	got := map[string]int{}
	for _, item := range page.Items {
		got[item.Event.ID]++
	}
	if got["gw-a:1000:1"] != 1 || got["gw-b:1000:1"] != 1 {
		t.Errorf("both distinct same-second alerts must survive the merge: %v", got)
	}
	if got["broadcast:1000:9"] != 1 {
		t.Errorf("the cross-replica repeat must collapse to one by eventKey: got %d", got["broadcast:1000:9"])
	}
	if len(page.Items) != 3 {
		t.Fatalf("served %d events; want 3 (two distinct alerts + one collapsed broadcast)", len(page.Items))
	}
}

// bufEventAt builds a gateway-buffer event with an explicit emission instant so
// a test can place a cursor before, inside, or after the retained window.
func bufEventAt(id, typ string, sec int64) gwevents.BufferedEvent {
	ev := bufEvent(id, typ)
	ev.Event.Time = time.Unix(sec, 0).UTC()
	return ev
}

// spec: 25.5 (cross-source cursor translation with gapDetected and
// oldestAvailableCursor on a miss) — a poll served from the Redis-down
// gateway-buffer fan-out with a cursor whose eventKey has aged out of every
// replica's ring reports the canonical gap envelope and fires the
// lenny_ops_events_stream_gaps_total counter, so a caller resyncs instead of
// silently re-consuming the window. A cursor that is merely absent from the
// window while ordering inside it is the ordinary source-switch case and
// continues without a gap. The pre-fix fan-out poll path flagged neither: it
// restarted from the head of the window with gapDetected false and never fired
// the counter, so this fails against that code.
//
// diagnosis: a failure means the §25.5 gap contract is not honoured on the
// gateway-buffer fall-back read path — an aged-out cursor is served as though
// continuity held (the caller re-processes delivered events and silently misses
// the evicted ones), or an ordinary cross-source switch is misreported as an
// eviction and replays the window.
func TestOpsEventStreamGatewayBufferPollReportsGapOnAgedOutCursor(t *testing.T) {
	replica := bufferReplica(t, []gwevents.BufferedEvent{
		bufEventAt("gw-a:2000:1", "dev.lenny.alert_fired", 2000),
		bufEventAt("gw-a:2002:1", "dev.lenny.alert_fired", 2002),
	})
	client, err := gateway.NewClient(gateway.Config{
		BaseURL:           "http://gateway.invalid",
		Token:             gateway.StaticToken("test-token"),
		Discovery:         gateway.StaticDiscovery{replica.URL},
		PerRequestTimeout: 5 * time.Second,
		FanOutTimeout:     2 * time.Second,
	})
	if err != nil {
		t.Fatalf("build gateway client: %v", err)
	}

	var gaps atomic.Int64
	svc := opsstream.New(opsstream.Options{
		SourceHealth: redisDownGatewayUp{},
		OnGap:        func() { gaps.Add(1) },
	})
	svc.SetGatewayBufferSource(client)

	// A cursor minted before the retained window: the events between it and the
	// oldest retained entry are gone.
	aged := mixedCursor("gw-a:1000:1")
	page := pollFallback(t, svc, aged)
	if !page.Pagination.GapDetected {
		t.Fatalf("an aged-out cursor must report pagination.gapDetected: true, got %+v", page.Pagination)
	}
	if page.Pagination.OldestAvailableCursor == "" {
		t.Error("a detected gap must carry pagination.oldestAvailableCursor so the caller can resync")
	}
	if page.Pagination.SuggestedAction != "resync" {
		t.Errorf("gap suggestedAction = %q, want resync", page.Pagination.SuggestedAction)
	}
	if n := gaps.Load(); n != 1 {
		t.Errorf("lenny_ops_events_stream_gaps_total fired %d times, want 1 for the one gapped poll", n)
	}

	// A cursor absent from the window but ordering inside it continues from the
	// next event with no gap: the caller's last event came from a source no
	// gateway replica buffers.
	inside := mixedCursor("ops:2001:1")
	cont := pollFallback(t, svc, inside)
	if cont.Pagination.GapDetected {
		t.Fatalf("an ordinary cross-source switch must not report a gap: %+v", cont.Pagination)
	}
	if len(cont.Items) != 1 || cont.Items[0].Event.ID != "gw-a:2002:1" {
		t.Fatalf("continuation served %d items (%+v), want only gw-a:2002:1", len(cont.Items), cont.Items)
	}
	if n := gaps.Load(); n != 1 {
		t.Errorf("gap counter fired again on a continuation poll: %d", n)
	}
}

// spec: 25.5 (the in-memory buffer stays the canonical source of
// lenny-ops-originated events during a Redis-down / gateway-up outage) — with
// Redis unreachable, a lenny-ops-originated event exists only in this replica's
// local ring: no gateway replica buffers it and its XADD to the shared stream
// is failing. The poll page and an open SSE connection must both carry it
// alongside the gateway-originated events fetched from the fan-out. The pre-fix
// read surface served the fan-out alone during this outage, so a Redis-only
// outage dropped lenny-ops events entirely, observably losing more than the
// strictly worse dual outage; this fails against that code.
//
// diagnosis: a failure means the §25.5 read surface drops lenny-ops-originated
// events (escalations, drift, lock changes, ops self-health) for the duration
// of a Redis-only outage — either the poll page omits them or an open SSE
// connection in the gateway-buffer fall-back never receives them.
func TestOpsEventStreamServesLocalOriginEventsDuringRedisOnlyOutage(t *testing.T) {
	replica := bufferReplica(t, []gwevents.BufferedEvent{
		bufEventAt("gw-a:3000:1", "dev.lenny.alert_fired", 3000),
	})
	client, err := gateway.NewClient(gateway.Config{
		BaseURL:           "http://gateway.invalid",
		Token:             gateway.StaticToken("test-token"),
		Discovery:         gateway.StaticDiscovery{replica.URL},
		PerRequestTimeout: 5 * time.Second,
		FanOutTimeout:     2 * time.Second,
	})
	if err != nil {
		t.Fatalf("build gateway client: %v", err)
	}

	svc := opsstream.New(opsstream.Options{SourceHealth: redisDownGatewayUp{}, ReplicaID: "ops-1"})
	svc.SetGatewayBufferSource(client)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// An open SSE connection in the fall-back.
	rec := newFallbackSink()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "/v1/admin/events/stream", nil)
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.HandleStream(rec, platformAdminReq(req))
	}()
	waitFor(t, rec, "gw-a:3000:1", 5*time.Second, "the gateway-originated event from the fan-out")

	// lenny-ops emits its own event while Redis is down.
	local := gwevents.OperationalEvent{
		Type:        "dev.lenny.escalation_created",
		SpecVersion: gwevents.CloudEventsSpecVersion,
		Time:        time.Unix(3001, 0).UTC(),
	}
	if _, err := svc.Publish(ctx, local); err != nil {
		t.Fatalf("publish local-origin event: %v", err)
	}

	waitFor(t, rec, "dev.lenny.escalation_created", 5*time.Second, "the lenny-ops-originated event on the open fall-back connection")

	page := pollFallback(t, svc, "")
	types := map[string]int{}
	for _, item := range page.Items {
		types[item.Event.Type]++
	}
	if types["dev.lenny.alert_fired"] != 1 {
		t.Errorf("poll page lost the gateway-originated event: %v", types)
	}
	if types["dev.lenny.escalation_created"] != 1 {
		t.Errorf("poll page dropped the lenny-ops-originated event during a Redis-only outage: %v", types)
	}

	cancel()
	<-done
}

// mixedCursor builds the opaque §25.5 cursor a caller sends back: the
// base64url of source_kind and the canonical eventKey. Agents treat it as
// opaque; the test mints one so it can place a cursor before or inside the
// retained window.
func mixedCursor(eventKey string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(opsstream.SourceKindMixed + ":" + eventKey))
}

// pollFallback drives GET /v1/admin/events through the Service and decodes the
// §25.5 poll envelope.
func pollFallback(t *testing.T, svc *opsstream.Service, cursor string) opsstream.EventPage {
	t.Helper()
	target := "/v1/admin/events"
	if cursor != "" {
		target += "?cursor=" + cursor
	}
	rec := httptest.NewRecorder()
	svc.HandlePoll(rec, platformAdminReq(httptest.NewRequest(http.MethodGet, target, nil)))
	if rec.Code != http.StatusOK {
		t.Fatalf("poll status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var page opsstream.EventPage
	if err := json.NewDecoder(rec.Body).Decode(&page); err != nil {
		t.Fatalf("decode poll body: %v", err)
	}
	return page
}

// fallbackSink is a streaming ResponseWriter+Flusher readable while the SSE
// handler writes to it from another goroutine.
type fallbackSink struct {
	mu  sync.Mutex
	buf strings.Builder
	hdr http.Header
}

func newFallbackSink() *fallbackSink { return &fallbackSink{hdr: http.Header{}} }

func (s *fallbackSink) Header() http.Header { return s.hdr }
func (s *fallbackSink) WriteHeader(int)     {}
func (s *fallbackSink) Flush()              {}
func (s *fallbackSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *fallbackSink) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func waitFor(t *testing.T, sink *fallbackSink, want string, timeout time.Duration, what string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(sink.String(), want) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s (%q) on the SSE stream:\n%s", what, want, sink.String())
}

// spec: 25.5 (gapDetected and oldestAvailableCursor when the cursor aged out
// of every replica's ring) — the eviction boundary on the gateway-buffer
// fall-back is the oldest event the gateway replicas still retain, taken
// before this replica's local ring is unioned into the served window. The
// local ring holds only lenny-ops-originated events and, on a low-emission
// replica, outlives the gateway rings, so a boundary measured against the
// union sits before every genuinely aged-out cursor and the gap is never
// reported. The pre-fix fan-out poll path measured against the union and
// served an aged-out cursor with gapDetected false, silently skipping the
// evicted gateway events; this fails against that code.
//
// diagnosis: a failure means an aged-out cursor is served as though continuity
// held whenever this replica's local ring retains an event older than the
// gateway window — the caller silently misses the gateway events evicted from
// every replica's ring, or resyncs from the wrong oldestAvailableCursor.
func TestOpsEventStreamGatewayBufferGapMeasuredAgainstFanOutNotLocalRing(t *testing.T) {
	replica := bufferReplica(t, []gwevents.BufferedEvent{
		bufEventAt("gw-a:5000:1", "dev.lenny.alert_fired", 5000),
		bufEventAt("gw-a:6000:1", "dev.lenny.alert_fired", 6000),
	})
	client, err := gateway.NewClient(gateway.Config{
		BaseURL:           "http://gateway.invalid",
		Token:             gateway.StaticToken("test-token"),
		Discovery:         gateway.StaticDiscovery{replica.URL},
		PerRequestTimeout: 5 * time.Second,
		FanOutTimeout:     2 * time.Second,
	})
	if err != nil {
		t.Fatalf("build gateway client: %v", err)
	}

	var gaps atomic.Int64
	svc := opsstream.New(opsstream.Options{
		SourceHealth: redisDownGatewayUp{},
		ReplicaID:    "ops-1",
		OnGap:        func() { gaps.Add(1) },
	})
	svc.SetGatewayBufferSource(client)

	// A lenny-ops-originated event older than the whole gateway window, still
	// retained in this replica's ring.
	if _, err := svc.Publish(context.Background(), gwevents.OperationalEvent{
		ID:          "ops-1:1000:1",
		Type:        "dev.lenny.escalation_created",
		SpecVersion: gwevents.CloudEventsSpecVersion,
		Time:        time.Unix(1000, 0).UTC(),
	}); err != nil {
		t.Fatalf("publish local-origin event: %v", err)
	}

	// A cursor newer than the local event but older than everything the
	// gateway replicas still hold.
	page := pollFallback(t, svc, mixedCursor("gw-a:2000:1"))
	if !page.Pagination.GapDetected {
		t.Fatalf("an aged-out cursor must report gapDetected even when the local ring retains an older event: %+v", page.Pagination)
	}
	if n := gaps.Load(); n != 1 {
		t.Errorf("lenny_ops_events_stream_gaps_total fired %d times, want 1", n)
	}
	if page.Pagination.SuggestedAction != "resync" {
		t.Errorf("gap suggestedAction = %q, want resync", page.Pagination.SuggestedAction)
	}
	oldest := decodeCursorKey(t, page.Pagination.OldestAvailableCursor)
	if oldest != "gw-a:5000:1" {
		t.Errorf("oldestAvailableCursor = %q, want the oldest retained gateway event gw-a:5000:1", oldest)
	}
}

// decodeCursorKey unwraps the opaque §25.5 cursor to its source position so a
// test can assert which retained event a gap response points the caller at.
func decodeCursorKey(t *testing.T, cursor string) string {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		t.Fatalf("decode cursor %q: %v", cursor, err)
	}
	kind, position, found := strings.Cut(string(raw), ":")
	if !found {
		t.Fatalf("cursor %q carries no source position", string(raw))
	}
	// A redis-kind cursor carries the Redis stream ID ahead of the canonical
	// eventKey so a same-source resume is a positioned read.
	if kind == "redis" {
		if _, key, ok := strings.Cut(position, "|"); ok {
			return key
		}
	}
	return position
}

// flippableHealth is a SourceHealth whose Redis reachability is toggled by the
// test so one poll is served from the Redis stream and the next from the
// gateway-buffer fall-back.
type flippableHealth struct{ redis atomic.Bool }

func (h *flippableHealth) RedisAvailable() bool   { return h.redis.Load() }
func (h *flippableHealth) GatewayAvailable() bool { return true }

// spec: 25.5 (the opaque cursor carries the canonical eventKey and round-trips
// across sources; cross-source cursor translation) — a caller that round-trips
// the cursor it was just handed must continue where it left off across the
// Redis-down transition the fall-back exists for. The gateway-buffer source
// resolves a position by scanning for the first event ordering at or after the
// carried eventKey, so a cursor encoding the Redis stream ID instead is
// untranslatable there: the "ms-seq" string fails to parse as an eventKey, the
// comparison degrades to a byte order in which it sorts before the whole
// retained window, and the poll reports a spurious gap, fires the gap counter,
// and re-serves every event it already delivered. The pre-fix Redis source
// minted the stream ID; this fails against that code.
//
// diagnosis: a failure means every poll spanning a Redis-to-gateway-buffer
// transition reports gapDetected and duplicates the whole retained window to
// the caller, instead of continuing at the next event.
func TestOpsEventStreamRedisMintedCursorContinuesInGatewayBufferFallback(t *testing.T) {
	rd := containers.StartRedis(t, containers.RedisOptions{})
	ctx := context.Background()

	// The gateway replica's window brackets the Redis-served window: the
	// StreamEmitter writes every gateway-originated event to both the shared
	// stream and the per-replica ring.
	replica := bufferReplica(t, []gwevents.BufferedEvent{
		bufEventAt("gw-a:0500:1", "dev.lenny.alert_fired", 500),
		bufEventAt("gw-a:3000:1", "dev.lenny.alert_fired", 3000),
	})
	client, err := gateway.NewClient(gateway.Config{
		BaseURL:           "http://gateway.invalid",
		Token:             gateway.StaticToken("test-token"),
		Discovery:         gateway.StaticDiscovery{replica.URL},
		PerRequestTimeout: 5 * time.Second,
		FanOutTimeout:     2 * time.Second,
	})
	if err != nil {
		t.Fatalf("build gateway client: %v", err)
	}

	health := &flippableHealth{}
	health.redis.Store(true)
	var gaps atomic.Int64
	svc := opsstream.New(opsstream.Options{
		RedisClient:  opsstream.NewRedisStreamClient(rd.Client),
		SourceHealth: health,
		ReplicaID:    "ops-1",
		OnGap:        func() { gaps.Add(1) },
	})
	svc.SetGatewayBufferSource(client)

	// Seed the shared stream through the production emitter.
	emitter := eventbuffer.NewStreamEmitter(eventbuffer.StreamEmitterOptions{
		Client:    rd.Client,
		Buffer:    eventbuffer.NewEventBuffer(0),
		ReplicaID: "ops-1",
	})
	for _, e := range []gwevents.OperationalEvent{
		{ID: "gw-a:0500:1", Type: "dev.lenny.alert_fired", SpecVersion: gwevents.CloudEventsSpecVersion, Time: time.Unix(500, 0).UTC()},
		{ID: "ops-1:1000:1", Type: "dev.lenny.escalation_created", SpecVersion: gwevents.CloudEventsSpecVersion, Time: time.Unix(1000, 0).UTC()},
	} {
		if err := emitter.Emit(ctx, e); err != nil {
			t.Fatalf("seed stream with %s: %v", e.ID, err)
		}
	}

	// Poll with Redis up and keep the cursor the response hands back.
	first := pollFallback(t, svc, "")
	if first.Degradation != nil {
		t.Fatalf("the first poll must be served from the Redis stream, got %+v", first.Degradation)
	}
	delivered := map[string]int{}
	for _, item := range first.Items {
		delivered[item.Event.ID]++
	}
	if delivered["gw-a:0500:1"] != 1 || delivered["ops-1:1000:1"] != 1 {
		t.Fatalf("first poll served %v, want both seeded events once", delivered)
	}
	if first.Pagination.Cursor == "" {
		t.Fatal("the Redis-served poll returned no continuation cursor")
	}

	// Redis drops out. The same cursor, round-tripped verbatim, must continue
	// in the gateway-buffer fall-back.
	health.redis.Store(false)
	second := pollFallback(t, svc, first.Pagination.Cursor)

	if second.Degradation == nil || second.Degradation.ActualSource != "gateway-buffer" {
		t.Fatalf("the second poll must be served from the gateway-buffer fall-back, got %+v", second.Degradation)
	}
	if second.Pagination.GapDetected {
		t.Fatalf("round-tripping the Redis-minted cursor into the fall-back reported a gap: %+v", second.Pagination)
	}
	if n := gaps.Load(); n != 0 {
		t.Errorf("lenny_ops_events_stream_gaps_total fired %d times on a clean cross-source continuation", n)
	}
	for _, item := range second.Items {
		if delivered[item.Event.ID] > 0 {
			t.Errorf("event %s was delivered twice across the source transition", item.Event.ID)
		}
		delivered[item.Event.ID]++
	}
	if delivered["gw-a:3000:1"] != 1 {
		t.Fatalf("the fall-back did not serve the continuation event gw-a:3000:1: %v", delivered)
	}
}

// spec: 25.5 (actualSource names the source the response was served from;
// EVENT_STREAM_UNAVAILABLE when neither source can serve gateway-originated
// events) — a Redis-down fall-back whose fan-out discovers no gateway replica
// is the dual-outage case, not a healthy but empty gateway-buffer page.
// Discovery resolving an empty endpoint set (a stale or empty
// lenny-gateway-pods endpoints list, a gateway Deployment scaled to zero)
// leaves nothing in a position to answer the §25.3 buffer query, so polling
// returns 503 EVENT_STREAM_UNAVAILABLE and the SSE stint announces the
// lenny-ops-local-buffer envelope with gateway-events unavailable.
//
// The source-health probe does not cover this: it reaches the gateway over its
// ClusterIP, a different resolution path from the headless-Service discovery
// the fan-out uses, so it reports the gateway reachable while the fan-out can
// reach nothing. The pre-fix fetch exempted a zero-result fan-out from the
// "no replica served" check, so the poll answered 200 carrying the case-1
// EVENT_STREAM_DEGRADED envelope with actualSource gateway-buffer and zero
// events; this fails against that code.
//
// diagnosis: a failure means the §25.5 read surface labels a response
// gateway-buffer when no gateway replica was reachable to serve it — a caller
// reads the empty page as "the gateway buffer holds nothing" rather than "the
// gateway-originated events are unobservable", and the SSE surface withholds
// the unavailableFields the dual-outage envelope owes it.
func TestOpsEventStreamReportsDualOutageWhenFanOutDiscoversNoGatewayReplica(t *testing.T) {
	client, err := gateway.NewClient(gateway.Config{
		BaseURL:           "http://gateway.invalid",
		Token:             gateway.StaticToken("test-token"),
		Discovery:         gateway.StaticDiscovery{},
		PerRequestTimeout: 5 * time.Second,
		FanOutTimeout:     2 * time.Second,
	})
	if err != nil {
		t.Fatalf("build gateway client: %v", err)
	}

	svc := opsstream.New(opsstream.Options{SourceHealth: redisDownGatewayUp{}, ReplicaID: "ops-1"})
	svc.SetGatewayBufferSource(client)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/events", nil)
	svc.HandlePoll(rec, platformAdminReq(req))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("poll status = %d (body %s), want 503 EVENT_STREAM_UNAVAILABLE: no gateway replica "+
			"was discovered, so the response carries no gateway-originated events and must not be "+
			"labelled a gateway-buffer page", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "EVENT_STREAM_UNAVAILABLE") {
		t.Errorf("poll body = %s, want the §25.5 EVENT_STREAM_UNAVAILABLE error code", rec.Body.String())
	}

	// The SSE surface still serves lenny-ops-originated events, under the
	// dual-outage envelope rather than the gateway-buffer one.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sink := newFallbackSink()
	sreq, _ := http.NewRequestWithContext(ctx, http.MethodGet, "/v1/admin/events/stream", nil)
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.HandleStream(sink, platformAdminReq(sreq))
	}()
	waitFor(t, sink, `"actualSource":"lenny-ops-local-buffer"`, 5*time.Second,
		"the §25.5 dual-outage degradation comment on a fan-out that discovered no replica")
	if !strings.Contains(sink.String(), `"unavailableFields":["gateway-events"]`) {
		t.Errorf("SSE degradation comment = %s, want unavailableFields [gateway-events]", sink.String())
	}
	// The stint announces the case-1 envelope on entry, before the first
	// fan-out, and upgrades it on the first tick. What the connection must not
	// be left reading is a gateway-buffer classification: the dual-outage
	// envelope has to be the last word.
	out := sink.String()
	if last := strings.LastIndex(out, `"actualSource":"lenny-ops-local-buffer"`); last < strings.LastIndex(out, `"actualSource":"gateway-buffer"`) {
		t.Errorf("the connection was left on the gateway-buffer classification while no replica was "+
			"discovered; the dual-outage envelope must be the standing one: %s", out)
	}

	cancel()
	<-done
}
