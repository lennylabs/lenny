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
// returns the HTTP status and the §25.5 degradation envelope's actualSource
// ("" when no envelope is attached, the healthy Redis-served case).
func pollActualSource(t *testing.T, w *opsWiring) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	w.eventStream.HandlePoll(rec, httptest.NewRequest(http.MethodGet, "/v1/admin/events", nil))
	if rec.Code != http.StatusOK {
		return rec.Code, ""
	}
	var body struct {
		Degradation *struct {
			ActualSource string `json:"actualSource"`
		} `json:"degradation"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode poll response: %v; body=%s", err, rec.Body.String())
	}
	if body.Degradation == nil {
		return rec.Code, ""
	}
	return rec.Code, body.Degradation.ActualSource
}

// TestEventStreamWiringFallsBackToGatewayBufferWhenRedisIsUnreachable
// exercises the §25.5 read side through the real lenny-ops composition root:
// the event-stream build step and the gateway-link build step, wired against
// an unreachable Redis and a gateway that answers.
//
// The gateway test server answers the liveness path and refuses every admin
// call, which is what a deployed gateway does to the lenny-ops
// service-account principal: it does not hold the platform-admin role the
// admin API requires. A source-health signal that read that refusal as an
// outage reported the reachable gateway down, which put the read surface in
// the §25.5 dual-outage case and served 503 for the whole Redis outage
// instead of the gateway-buffer fall-back the wiring provides.
//
// spec: §25.5 (degradation matrix — Redis unreachable with the gateway up
// falls back to the gateway event buffer, HTTP 200 with the
// EVENT_STREAM_DEGRADED envelope; both unreachable is the 503 case).
func TestEventStreamWiringFallsBackToGatewayBufferWhenRedisIsUnreachable_spec_25_5(t *testing.T) {
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

	// Port 1 is reserved and never serves Redis, so the ops:events:stream is
	// unreachable for the whole test: the Redis-down half of the §25.5
	// case-1 branch.
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1, DialTimeout: 200 * time.Millisecond})
	defer func() { _ = rdb.Close() }()

	w := &opsWiring{f: eventStreamWiringFlags(gw.URL)}
	w.replicaID = "ops-a"
	w.ctx, w.stop = context.WithCancel(context.Background())
	defer w.stop()
	w.redisClient = rdb

	w.buildEventStreamAndWebhooks()
	defer w.subscriptionCache.Stop()
	w.buildGatewayLink()

	if w.srcHealth == nil {
		t.Fatal("no source-health signal was wired for a Redis-backed event stream; the §25.5 degradation matrix cannot be evaluated")
	}

	// The source-health refresh runs on its own loop, so poll until it has
	// resolved the live state of both sources.
	deadline := time.Now().Add(20 * time.Second)
	var status int
	var src string
	for {
		status, src = pollActualSource(t, w)
		if status == http.StatusOK && src == sourceLabelGatewayBuffer {
			return
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("the wired read surface reported status=%d actualSource=%q during a Redis outage with the "+
		"gateway answering, want 200 with %q: the gateway-buffer fall-back is not reachable through the "+
		"composition root (a 503 means the surface took the dual-outage branch over a reachable gateway)",
		status, src, sourceLabelGatewayBuffer)
}

// sourceLabelGatewayBuffer is the §25.5 actualSource label the read surface
// reports while serving from the gateway event buffer.
const sourceLabelGatewayBuffer = "gateway-buffer"
