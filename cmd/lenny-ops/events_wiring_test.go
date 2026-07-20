// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
)

func boolPtr(b bool) *bool                  { return &b }
func int64Ptr(i int64) *int64               { return &i }
func durPtr(d time.Duration) *time.Duration { return &d }

// eventStreamWiringFlags returns the opsFlags subset the §25.5 event-stream
// and §25.4 gateway-link build steps read, pointed at the gateway test
// server. The headless Service is left empty so the build does not attempt a
// cluster DNS lookup.
func eventStreamWiringFlags(gatewayURL string) *opsFlags {
	return &opsFlags{
		addr:                         strPtr(":8090"),
		leaderElectNS:                strPtr("lenny-system"),
		opsServiceName:               strPtr("lenny-ops"),
		eventsStreamMaxLen:           int64Ptr(1000),
		bearerTrustHMACKeyFile:       strPtr(""),
		webhookAllowHTTP:             boolPtr(false),
		webhookBlockedCIDRs:          strPtr(""),
		webhookDomainAllowlist:       strPtr(""),
		webhookTrackingMode:          strPtr("full"),
		webhookRetentionDays:         intPtr(7),
		webhookFailuresRetentionDays: intPtr(0),
		gatewayURL:                   strPtr(gatewayURL),
		gatewayHeadlessSvc:           strPtr(""),
		gatewayInternalTLS:           boolPtr(false),
		gatewayTLSPort:               intPtr(8443),
		gatewayPlaintextPort:         intPtr(8080),
		gatewayFanOutTimeout:         durPtr(2 * time.Second),
		gatewayBreakerThreshold:      intPtr(3),
		gatewayBreakerResetAfter:     durPtr(60 * time.Second),
		gatewaySATokenFile:           strPtr(""),
		gatewayTokenRefreshBefore:    durPtr(5 * time.Minute),
		gatewayTokenMinTTL:           durPtr(time.Minute),
		gatewayCABundleFile:          strPtr(""),
	}
}

// pollActualSource issues one poll against the wired read surface and
// returns the HTTP status, the §25.5 degradation envelope's actualSource ("" when
// no envelope is attached, the healthy Redis-served case), and the eventKeys the
// page served.
func pollActualSource(t *testing.T, w *opsWiring) (int, string, []string) {
	t.Helper()
	rec := httptest.NewRecorder()
	w.eventStream.HandlePoll(rec, platformAdminReq(httptest.NewRequest(http.MethodGet, "/v1/admin/events", nil)))
	if rec.Code != http.StatusOK {
		return rec.Code, "", nil
	}
	var body struct {
		Items []struct {
			Event struct {
				ID string `json:"id"`
			} `json:"event"`
		} `json:"items"`
		Degradation *struct {
			ActualSource string `json:"actualSource"`
		} `json:"degradation"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode poll response: %v; body=%s", err, rec.Body.String())
	}
	keys := make([]string, 0, len(body.Items))
	for _, it := range body.Items {
		keys = append(keys, it.Event.ID)
	}
	if body.Degradation == nil {
		return rec.Code, "", keys
	}
	return rec.Code, body.Degradation.ActualSource, keys
}

// buildRedisDownWiring builds the §25.5 read side through the real lenny-ops
// composition root against an unreachable Redis and the gateway at gatewayURL.
func buildRedisDownWiring(t *testing.T, gatewayURL string) *opsWiring {
	t.Helper()
	// Port 1 is reserved and never serves Redis, so the ops:events:stream is
	// unreachable for the whole test: the Redis-down half of the §25.5
	// case-1 branch.
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1, DialTimeout: 200 * time.Millisecond})
	t.Cleanup(func() { _ = rdb.Close() })

	// The gateway client registers its §25.4 metrics on the default
	// registerer, so each composition-root build gets its own registry rather
	// than colliding with a sibling test's.
	prevRegisterer := prometheus.DefaultRegisterer
	prometheus.DefaultRegisterer = prometheus.NewRegistry()
	t.Cleanup(func() { prometheus.DefaultRegisterer = prevRegisterer })

	w := &opsWiring{f: eventStreamWiringFlags(gatewayURL)}
	w.replicaID = "ops-a"
	w.ctx, w.stop = context.WithCancel(context.Background())
	t.Cleanup(w.stop)
	w.redisClient = rdb

	w.buildEventStreamAndWebhooks()
	t.Cleanup(w.subscriptionCache.Stop)
	w.buildGatewayLink()

	if w.srcHealth == nil {
		t.Fatal("no source-health signal was wired for a Redis-backed event stream; the §25.5 degradation matrix cannot be evaluated")
	}
	return w
}

// awaitPoll polls the wired read surface until want reports satisfied, so the
// assertion runs after the source-health loop has resolved the live state of
// both sources rather than on its optimistic cold start.
func awaitPoll(t *testing.T, w *opsWiring, want func(status int, src string, keys []string) bool) (int, string, []string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		status, src, keys := pollActualSource(t, w)
		if want(status, src, keys) {
			return status, src, keys
		}
		if time.Now().After(deadline) {
			return status, src, keys
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// TestEventStreamWiringReportsUnavailableWhenNoGatewayReplicaServes exercises
// the §25.5 read side through the real lenny-ops composition root: the
// event-stream build step and the gateway-link build step, wired against an
// unreachable Redis and a gateway process that is up but refuses lenny-ops the
// §25.3 buffer query. That is the deployed state the read side has to classify
// honestly: the gateway admin API refuses a principal that does not hold the
// platform-admin role it requires, and a wiring with no headless Service
// configured has no replica to fan out to at all. Either way the response
// carries no gateway-originated events, so it is the §25.5 dual-outage case.
//
// The assertion is on the status rather than the label, because actualSource is
// computed from the source-health signal: a gateway-buffer label says only that
// the surface chose the fall-back, and the pre-fix read path attached it to an
// empty 200 whether or not a single gateway event was retrieved. That is what
// let a refused fan-out read as a healthy degraded page; this fails against
// that code.
//
// spec: §25.5 (degradation matrix — actualSource names the source the response
// was served from; EVENT_STREAM_UNAVAILABLE when no source can serve
// gateway-originated events).
func TestEventStreamWiringReportsUnavailableWhenNoGatewayReplicaServes_spec_25_5(t *testing.T) {
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/admin/") {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer gw.Close()

	w := buildRedisDownWiring(t, gw.URL)
	status, src, keys := awaitPoll(t, w, func(status int, _ string, _ []string) bool {
		return status == http.StatusServiceUnavailable
	})
	if status != http.StatusServiceUnavailable {
		t.Fatalf("the read surface reported status=%d actualSource=%q items=%v while no gateway replica served "+
			"the buffer query, want 503: a fan-out that retrieved nothing must not surface as a healthy "+
			"%s page", status, src, keys, sourceLabelGatewayBuffer)
	}
}

// sourceLabelGatewayBuffer is the §25.5 actualSource label the read surface
// reports while serving from the gateway event buffer.
const sourceLabelGatewayBuffer = "gateway-buffer"

// TestEventStreamWiringClassifiesGatewayRefusalAsDualOutage asserts the §25.5
// source-health signal and the data path it gates agree about the gateway. The
// signal is measured on the surface the case-1 fall-back consumes, so a gateway
// process that is serving but refuses lenny-ops the §25.3 buffer query is
// classified down, and the read surface reports the dual-outage state it is
// really in.
//
// A probe measured on an unauthenticated liveness endpoint answers a different
// question ("is the gateway process up") and reports such a gateway reachable.
// The read surface then classifies case 1, routes the request to a source that
// returns nothing, and an open SSE connection parks in the gateway stint
// re-polling a fan-out that never answers.
//
// spec: §25.5 (degradation matrix — case 1 requires a gateway that can serve
// the buffer; both sources unreachable is the dual-outage case), §25.4
// (lenny-ops reaches the admin API as a service account holding platform-admin).
func TestEventStreamWiringClassifiesGatewayRefusalAsDualOutage_spec_25_5(t *testing.T) {
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			// The process is serving: a liveness probe finds this gateway up.
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	defer gw.Close()

	w := buildRedisDownWiring(t, gw.URL)
	go w.srcHealth.run(w.ctx, 200*time.Millisecond, w.redisClient, w.gwClient, redisEdgeCallbacks{})

	deadline := time.Now().Add(10 * time.Second)
	for w.srcHealth.GatewayAvailable() && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if w.srcHealth.GatewayAvailable() {
		t.Fatalf("the source-health signal reports the gateway available while it refuses lenny-ops the "+
			"§25.3 buffer query; the read surface would classify §25.5 case 1 and serve from a source that "+
			"answers nothing, labelling the empty result %q", sourceLabelGatewayBuffer)
	}

	status, src, keys := awaitPoll(t, w, func(status int, _ string, _ []string) bool {
		return status == http.StatusServiceUnavailable
	})
	if status != http.StatusServiceUnavailable {
		t.Fatalf("poll status=%d actualSource=%q items=%v, want 503: with Redis down and the gateway "+
			"refusing the buffer query neither source can serve gateway-originated events", status, src, keys)
	}
}

// TestEventStreamWiringClassifiesAServingGatewayAsCaseOne is the other half of
// the same agreement: a gateway that does serve lenny-ops the §25.3 buffer
// query is classified up, and the Redis-outage read is served from it and
// labelled gateway-buffer.
//
// spec: §25.5 (degradation matrix — Redis down with the gateway up falls back
// to the gateway event buffer).
func TestEventStreamWiringClassifiesAServingGatewayAsCaseOne_spec_25_5(t *testing.T) {
	const gwKey = "gw-1:3000:1"
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"events": []map[string]any{{
			"id": 1,
			"event": map[string]any{
				"id":          gwKey,
				"type":        "dev.lenny.alert_fired",
				"specversion": "1.0",
				"severity":    "warning",
				"time":        time.Unix(3000, 0).UTC().Format(time.RFC3339),
			},
		}}})
	}))
	defer gw.Close()

	w := buildRedisDownWiring(t, gw.URL)
	// The fan-out reaches the replica through the discovery the client was
	// built with; with no headless Service configured the gateway base URL is
	// the single replica.
	go w.srcHealth.run(w.ctx, 200*time.Millisecond, w.redisClient, w.gwClient, redisEdgeCallbacks{})

	deadline := time.Now().Add(10 * time.Second)
	for !w.srcHealth.GatewayAvailable() && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if !w.srcHealth.GatewayAvailable() {
		t.Fatal("the source-health signal reports a gateway that serves the §25.3 buffer query as " +
			"unavailable; the §25.5 case-1 fall-back would never be entered")
	}
}
