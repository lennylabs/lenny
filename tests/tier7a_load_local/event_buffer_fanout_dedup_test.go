// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local concurrency coverage for the §25.5 Redis-down
// gateway-buffer fall-back merge under the race detector. During a Redis
// outage the ops read surface serves every poll and SSE fall-back tick by
// fanning the §25.3 buffer query across all gateway replicas and merging
// the per-replica pages deduplicated by eventKey. Many callers poll that
// fall-back concurrently (a burst of admins plus every open SSE stream
// re-polling on its 2-second tick), so the merge runs from many goroutines
// at once against the same Service. This test pins the invariants that the
// concurrent merge must hold:
//
//   - two distinct same-second alert_fired events from two different
//     replicas both survive every concurrent merge (the dedup key is the
//     eventKey, not a content hash, so a same-(type, timestamp) collision
//     across replicas never collapses two distinct events into one);
//   - a genuine cross-replica repeat delivery carrying the same eventKey
//     collapses to exactly one entry in every concurrent merge;
//   - the merged page is byte-stable across concurrent callers: the same
//     event set, deduped identically, ordered identically (oldest-first by
//     event time, then eventKey), with no data race in Service state under
//     the race detector.
//
// spec: §25.5 (Redis-down gateway-buffer fallback, eventKey dedup), §25.3
// (cross-replica eventKey dedup over the headless Service, not a content
// hash).

package tier7a_load_local_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"

	gwevents "github.com/lennylabs/lenny/pkg/events"
	opsstream "github.com/lennylabs/lenny/pkg/ops/events"
	"github.com/lennylabs/lenny/pkg/ops/gateway"
)

// fanoutRedisDownGatewayUp is the §25.5 Redis-down / gateway-up source-health
// signal that routes the read surface onto the gateway-buffer fall-back.
type fanoutRedisDownGatewayUp struct{}

func (fanoutRedisDownGatewayUp) RedisAvailable() bool   { return false }
func (fanoutRedisDownGatewayUp) GatewayAvailable() bool { return true }

// fanoutBufferReplica serves GET /v1/admin/events/buffer with a fixed page,
// standing in for one gateway pod's §25.3 in-memory event buffer. It is
// read-only and safe to serve concurrently from many poll goroutines.
func fanoutBufferReplica(t *testing.T, events []gwevents.BufferedEvent) *httptest.Server {
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

func fanoutBufEvent(id, typ string, sec int64) gwevents.BufferedEvent {
	return gwevents.BufferedEvent{
		ID: 1,
		Event: gwevents.OperationalEvent{
			ID:          id,
			Type:        typ,
			SpecVersion: gwevents.CloudEventsSpecVersion,
			Time:        time.Unix(sec, 0).UTC(),
		},
	}
}

// spec: 25.5 (Redis-down gateway-buffer fallback, eventKey dedup); 25.3
// (cross-replica eventKey dedup over the headless Service) — under a burst of
// concurrent polls against the Redis-down gateway-buffer fall-back, every
// caller's merge preserves both distinct same-second alerts, collapses the
// cross-replica repeat to one by eventKey, and returns a byte-identical
// oldest-first page. A content-hash dedup would drop one of the two distinct
// same-second alerts; a racy Service would return divergent pages across
// callers or trip the race detector.
//
// diagnosis: a failure means the §25.5 gateway-buffer fall-back merge is not
// safe or not deterministic under concurrent read load: either two distinct
// same-second alert_fired events from two replicas collapse into one (a false
// content-hash collision), or a genuine cross-replica repeat is served twice
// (eventKey dedup missed), or concurrent callers observe divergent pages (a
// data race or non-deterministic ordering in the merge path). Any of these
// breaks the cross-replica dedup contract the read surface depends on when a
// Redis outage fans the buffer query across every gateway replica.
func TestGatewayBufferFanOutDedupIsConcurrentSafeAndDeterministic(t *testing.T) {
	// Replica A and replica B each hold a distinct same-second alert_fired
	// (distinct eventKeys, same type and timestamp) plus a broadcast
	// credential_rotated carrying the SAME eventKey across both replicas (a
	// genuine cross-replica repeat), and one earlier lenny-ops-origin event so
	// ordering has more than one time bucket to sort.
	repeat := fanoutBufEvent("broadcast:1000:9", "dev.lenny.credential_rotated", 1000)
	earlier := fanoutBufEvent("gw-a:0900:1", "dev.lenny.node_drained", 900)
	replicaA := fanoutBufferReplica(t, []gwevents.BufferedEvent{
		earlier,
		fanoutBufEvent("gw-a:1000:1", "dev.lenny.alert_fired", 1000),
		repeat,
	})
	replicaB := fanoutBufferReplica(t, []gwevents.BufferedEvent{
		fanoutBufEvent("gw-b:1000:1", "dev.lenny.alert_fired", 1000),
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

	svc := opsstream.New(opsstream.Options{SourceHealth: fanoutRedisDownGatewayUp{}})
	svc.SetGatewayBufferSource(client)

	// poll issues one GET /v1/admin/events and returns the ordered eventKey
	// sequence and the per-key count from the served page.
	poll := func() ([]string, map[string]int, error) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/v1/admin/events", nil)
		svc.HandlePoll(rec, platformAdminReq(req))
		if rec.Code != http.StatusOK {
			return nil, nil, fmt.Errorf("poll status = %d, want 200", rec.Code)
		}
		var page opsstream.EventPage
		if err := json.NewDecoder(rec.Body).Decode(&page); err != nil {
			return nil, nil, err
		}
		order := make([]string, 0, len(page.Items))
		counts := map[string]int{}
		for _, it := range page.Items {
			order = append(order, it.Event.ID)
			counts[it.Event.ID]++
		}
		return order, counts, nil
	}

	// Establish the canonical merged order once, then assert every concurrent
	// caller reproduces it exactly.
	wantOrder, wantCounts, err := poll()
	if err != nil {
		t.Fatalf("baseline poll: %v", err)
	}
	if wantCounts["gw-a:1000:1"] != 1 || wantCounts["gw-b:1000:1"] != 1 {
		t.Fatalf("both distinct same-second alerts must survive the merge: %v", wantCounts)
	}
	if wantCounts["broadcast:1000:9"] != 1 {
		t.Fatalf("the cross-replica repeat must collapse to one by eventKey: %v", wantCounts)
	}
	if len(wantOrder) != 4 {
		t.Fatalf("merged page = %v; want 4 events (earlier + two distinct alerts + one collapsed broadcast)", wantOrder)
	}

	const goroutines = 32
	const perGoroutine = 8
	var wg sync.WaitGroup
	errCh := make(chan error, goroutines*perGoroutine)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				order, counts, perr := poll()
				if perr != nil {
					errCh <- perr
					return
				}
				if !reflect.DeepEqual(order, wantOrder) {
					errCh <- fmt.Errorf("merged order = %v, want %v", order, wantOrder)
					return
				}
				for _, k := range []string{"gw-a:1000:1", "gw-b:1000:1", "broadcast:1000:9"} {
					if counts[k] != wantCounts[k] {
						errCh <- fmt.Errorf("event %s count = %d, want %d", k, counts[k], wantCounts[k])
						return
					}
				}
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for e := range errCh {
		t.Fatalf("concurrent fan-out merge diverged: %v", e)
	}
}
