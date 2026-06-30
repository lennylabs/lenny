// SPDX-License-Identifier: MIT

package gatewaymetrics_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/metrics/gatewaymetrics"
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

func TestOrphanCleanupAndTreeRecoveryMetricsRegistered_spec_8_10_7(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.IncOrphanCleanupRun()
	m.AddOrphanTasksTerminated(3)
	m.SetOrphanTasksActive(2)
	m.SetOrphanTasksActivePerTenant("acme", 4)
	m.ObserveTreeRecoveryDuration("warm-pool-a", "full_success", 12.5)
	m.IncTreeRecoveryTimeout("warm-pool-a", "level")
	m.IncTreeRecoveryTimeout("warm-pool-a", "tree")

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("/metrics status %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"lenny_orphan_cleanup_runs_total 1",
		"lenny_orphan_tasks_terminated 3",
		"lenny_orphan_tasks_active 2",
		`lenny_orphan_tasks_active_per_tenant{tenant_id="acme"} 4`,
		`lenny_delegation_tree_recovery_duration_seconds_count{outcome="full_success",pool="warm-pool-a"} 1`,
		`lenny_delegation_tree_recovery_timeout_total{pool="warm-pool-a",timeout_type="level"} 1`,
		`lenny_delegation_tree_recovery_timeout_total{pool="warm-pool-a",timeout_type="tree"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics missing %q\n---\n%s", want, body)
		}
	}
}

func TestOrphanMetricsNilSafe(t *testing.T) {
	var m *gatewaymetrics.Metrics
	m.IncOrphanCleanupRun()
	m.AddOrphanTasksTerminated(1)
	m.AddOrphanTasksTerminated(0) // no-op even with a non-nil receiver
	m.SetOrphanTasksActive(0)
	m.SetOrphanTasksActivePerTenant("acme", 0)
	m.ObserveTreeRecoveryDuration("pool", "outcome", 1.0)
	m.IncTreeRecoveryTimeout("pool", "level")
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
}
