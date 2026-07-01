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
