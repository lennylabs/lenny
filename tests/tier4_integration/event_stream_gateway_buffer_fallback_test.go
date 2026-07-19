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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	gwevents "github.com/lennylabs/lenny/pkg/events"
	opsstream "github.com/lennylabs/lenny/pkg/ops/events"
	"github.com/lennylabs/lenny/pkg/ops/gateway"
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
	svc.HandlePoll(rec, req)

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
