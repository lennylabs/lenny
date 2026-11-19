// SPDX-License-Identifier: MIT

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/replica/gatewaymetrics"
)

// TestPlatformToolsMetricsRegisterBesideTheGatewayMetrics checks that the
// platform tools seam's metric sets register on the gateway's metric
// registry without colliding with a gateway metric, and that /metrics then
// serves the unlabelled series they expose at zero, as it did when the
// gateway metrics registered them.
//
// spec: §16.1 (Metrics)
func TestPlatformToolsMetricsRegisterBesideTheGatewayMetrics(t *testing.T) {
	gw, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("gatewaymetrics.New: %v", err)
	}
	m, err := newPlatformToolsMetrics(gw.Registerer())
	if err != nil {
		t.Fatalf("newPlatformToolsMetrics: %v", err)
	}
	if m.delegation == nil || m.deadlock == nil || m.tools == nil ||
		m.leaseExtension == nil || m.orphanCleanup == nil || m.budgetReconstruction == nil {
		t.Fatalf("a metric set is nil: %+v", m)
	}
	m.leaseExtension.IncDelegationLeaseExtension("acme", "approved")

	rr := httptest.NewRecorder()
	gw.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rr.Body.String()
	for _, want := range []string{
		"lenny_elicitation_pending 0",
		"lenny_elicitation_timeout_total 0",
		"lenny_elicitation_suppressed_total 0",
		"lenny_orphan_cleanup_runs_total 0",
		"lenny_orphan_tasks_terminated 0",
		"lenny_orphan_tasks_active 0",
		`lenny_delegation_lease_extension_total{outcome="approved",tenant_id="acme"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics missing %q", want)
		}
	}

	if _, err := newPlatformToolsMetrics(gw.Registerer()); err == nil {
		t.Fatal("a second registration on the same registry returned no error")
	}
}
