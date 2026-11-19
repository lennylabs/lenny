// SPDX-License-Identifier: MIT

package gatewaymetrics_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/replica/gatewaymetrics"
)

func TestSetMaxOrphanTasksPerTenant_spec_8_10(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(rr.Body.String(), "lenny_max_orphan_tasks_per_tenant 0") {
		t.Errorf("startup /metrics missing zero gauge\n---\n%s", rr.Body.String())
	}
	m.SetMaxOrphanTasksPerTenant(100)
	rr = httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(rr.Body.String(), "lenny_max_orphan_tasks_per_tenant 100") {
		t.Errorf("/metrics missing configured gauge after SetMaxOrphanTasksPerTenant\n---\n%s", rr.Body.String())
	}
}

func TestOrphanSessionReconcilerMetrics_spec_10_1_51(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.IncOrphanSessionReconciliation()
	m.IncOrphanSessionReconciliation()
	m.SetAgentPodStateMirrorLag("warm-pool-a", 42)
	m.SetAgentPodStateMirrorLag("warm-pool-b", 7.5)

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("/metrics status %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"lenny_orphan_session_reconciliations_total 2",
		`lenny_agent_pod_state_mirror_lag_seconds{pool="warm-pool-a"} 42`,
		`lenny_agent_pod_state_mirror_lag_seconds{pool="warm-pool-b"} 7.5`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics missing %q\n---\n%s", want, body)
		}
	}
}

func TestOrphanSessionMetricsNilSafe(t *testing.T) {
	var m *gatewaymetrics.Metrics
	m.IncOrphanSessionReconciliation()
	m.SetAgentPodStateMirrorLag("pool", 1.0)
	m.ClearAgentPodStateMirrorLag()
}

// TestClearAgentPodStateMirrorLagDeletesEveryPoolSeries_spec_10_1_4 pins the
// clear a replica runs when it stops being the gateway leader: every per-pool
// lag series it published is gone from the exposition, the other orphan
// metrics are untouched, and a later publish starts a fresh series.
//
// spec: §10.1.4, §12.5
func TestClearAgentPodStateMirrorLagDeletesEveryPoolSeries_spec_10_1_4(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	scrape := func() string {
		rr := httptest.NewRecorder()
		m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
		return rr.Body.String()
	}
	m.IncOrphanSessionReconciliation()
	m.SetAgentPodStateMirrorLag("warm-pool-a", 42)
	m.SetAgentPodStateMirrorLag("warm-pool-b", 7.5)

	m.ClearAgentPodStateMirrorLag()
	body := scrape()
	if strings.Contains(body, "lenny_agent_pod_state_mirror_lag_seconds{") {
		t.Errorf("/metrics still carries a lag series after the clear\n---\n%s", body)
	}
	if !strings.Contains(body, "lenny_orphan_session_reconciliations_total 1") {
		t.Errorf("the clear touched the reconciliation counter\n---\n%s", body)
	}
	m.ClearAgentPodStateMirrorLag() // a clear with no series is a no-op

	m.SetAgentPodStateMirrorLag("warm-pool-b", 3)
	body = scrape()
	if !strings.Contains(body, `lenny_agent_pod_state_mirror_lag_seconds{pool="warm-pool-b"} 3`) ||
		strings.Contains(body, `pool="warm-pool-a"`) {
		t.Errorf("a publish after the clear = \n%s\nwant only the fresh warm-pool-b series", body)
	}
}
