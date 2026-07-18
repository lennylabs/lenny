// SPDX-License-Identifier: MIT

package gatewaymetrics_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/metrics/gatewaymetrics"
)

func TestSetAlertThresholds_spec_25_13_4737(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Pre-Set baseline reading: the chart Tier 1 defaults are
	// materialized at registration time so scalar(...) is finite.
	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	for _, want := range []string{
		"lenny_gateway_queue_depth_threshold 20",
		"lenny_gateway_latency_threshold_seconds 3",
		"lenny_credential_pool_low_threshold 0.8",
	} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Errorf("/metrics missing default threshold %q\n---\n%s", want, rr.Body.String())
		}
	}
	// Tier-2 / Tier-3 tightening flows through to the gauges via the
	// Set helpers.
	m.SetGatewayQueueDepthThreshold(5)
	m.SetGatewayLatencyThresholdSeconds(1.0)
	m.SetCredentialPoolLowThreshold(0.60)
	rr = httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	for _, want := range []string{
		"lenny_gateway_queue_depth_threshold 5",
		"lenny_gateway_latency_threshold_seconds 1",
		"lenny_credential_pool_low_threshold 0.6",
	} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Errorf("/metrics missing tightened threshold %q\n---\n%s", want, rr.Body.String())
		}
	}
}

// TestAddCredentialLeasesSwept_spec_4_9_1671 asserts the bounded
// expired-lease sweep counter accumulates the rows removed each tick and
// ignores non-positive deltas. The counter is unlabeled per §16.1.1, so a
// scrape exposes a single cumulative sample with no label set.
//
// spec: §4.9 line 1671 (the sweep worker removes expired credential-lease
// rows; lenny_gateway_credential_leases_swept_total counts them).
// diagnosis: a failure means AddCredentialLeasesSwept did not register or
// increment lenny_gateway_credential_leases_swept_total, or its
// non-positive guard let a zero/negative tick move the counter; operators
// would lose visibility into how many expired leases the sweep reclaims.
func TestAddCredentialLeasesSwept_spec_4_9_1671(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// A fresh registry has not emitted the counter yet: it materializes on
	// first Add.
	m.AddCredentialLeasesSwept(0)  // no-op guard: a tick that swept nothing
	m.AddCredentialLeasesSwept(-4) // no-op guard: negative delta rejected
	m.AddCredentialLeasesSwept(3)
	m.AddCredentialLeasesSwept(5)

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	// 3 + 5 = 8; the 0 and -4 ticks must not contribute.
	const want = "lenny_gateway_credential_leases_swept_total 8"
	if !strings.Contains(rr.Body.String(), want) {
		t.Errorf("/metrics missing %q\n---\n%s", want, rr.Body.String())
	}
}
