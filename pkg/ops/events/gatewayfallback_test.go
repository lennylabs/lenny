// SPDX-License-Identifier: MIT

package events

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

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
// unreachable and the gateway up (degradation case 1), a poll serves the
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

// spec: 25.5 (source selection from the degradation matrix) — a Redis-down /
// gateway-up state selects the gateway buffer only when a gateway source is
// wired; with none wired the read surface falls through to the local ring
// buffer rather than 503-ing or serving nothing.
func TestSelectSource_GatewayLabelFallsToLocalWhenUnwired_spec_25_5(t *testing.T) {
	s := New(Options{RedisClient: &fakeStream{}, SourceHealth: newMutableHealth(false, true), Now: ts})
	if src, _, _ := s.selectSource(); src != dsLocalBuffer {
		t.Fatalf("no gateway source wired: selectSource = %v, want dsLocalBuffer", src)
	}
	s.SetGatewayBufferSource(&fakeGatewaySource{})
	if src, _, _ := s.selectSource(); src != dsGateway {
		t.Fatalf("gateway source wired: selectSource = %v, want dsGateway", src)
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
