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
	w.eventStream.HandlePoll(rec, httptest.NewRequest(http.MethodGet, "/v1/admin/events", nil))
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
// unreachable Redis and a gateway that answers the liveness path but serves no
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
		if r.URL.Path == gatewayLivenessPath {
			w.WriteHeader(http.StatusOK)
			return
		}
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
