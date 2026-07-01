// SPDX-License-Identifier: MIT

package gatewaymetrics_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/metrics/gatewaymetrics"
)

// spec: §4.6.1 (onPoolExhausted: queue); §16.1 (metric catalog) — the
// bounded synchronous claim-wait queue publishes three per-pool series:
// the FIFO depth gauge, the residency-wait histogram, and the
// timeout counter the gateway start path drives while a queued request
// waits for a pod or exhausts maxQueueWaitSeconds.
//
// diagnosis: a failure means the onPoolExhausted: queue observability is
// not scrapable or carries the wrong label set, so an operator cannot
// see queue backpressure or distinguish a satisfied wait from a
// timed-out one on the pool-exhaustion dashboards.
func TestPodClaimQueueMetricsExpose_spec_4_6_1(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.SetPodClaimQueueDepth("default", 3)
	m.ObservePodClaimQueueWait("default", 0.5)
	m.ObservePodClaimQueueWait("default", 1.5)
	m.IncPodClaimTimeout("default")
	m.IncPodClaimTimeout("default")
	m.IncPodClaimTimeout("emergency")

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`lenny_pod_claim_queue_depth{pool="default"} 3`,
		`lenny_pod_claim_queue_wait_seconds_count{pool="default"} 2`,
		`lenny_pod_claim_timeout_total{pool="default"} 2`,
		`lenny_pod_claim_timeout_total{pool="emergency"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics output missing %q\n---\n%s", want, body)
		}
	}
}

// The pod-claim-queue emitters are nil-receiver-safe like the rest of
// the *Metrics surface, so the gateway start path can wire them
// unconditionally when no metrics sink is configured.
//
// diagnosis: a failure means a queued claim wait panics on a gateway
// built without metrics, taking down the start path.
func TestPodClaimQueueMetricsNilReceiverSafe_spec_4_6_1(t *testing.T) {
	var m *gatewaymetrics.Metrics
	m.SetPodClaimQueueDepth("default", 1)
	m.ObservePodClaimQueueWait("default", 1.0)
	m.IncPodClaimTimeout("default")
}
