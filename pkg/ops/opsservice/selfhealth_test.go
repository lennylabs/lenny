// SPDX-License-Identifier: MIT

package opsservice

import (
	"sync"
	"testing"
)

func healthyCheck(name string) SelfCheck {
	return func() CheckResult { return CheckResult{Name: name, Status: StatusHealthy} }
}

func degradedCheck(name, detail string) SelfCheck {
	return func() CheckResult { return CheckResult{Name: name, Status: StatusDegraded, Detail: detail} }
}

func unhealthyCheck(name, detail string) SelfCheck {
	return func() CheckResult { return CheckResult{Name: name, Status: StatusUnhealthy, Detail: detail} }
}

// TestSelfHealthAggregatesWorstCheck is the §25.4 aggregate-status
// contract: the replica's self-health is the worst of the individual
// checks — any degraded check makes it degraded, any unhealthy check
// makes it unhealthy.
func TestSelfHealthAggregatesWorstCheck(t *testing.T) {
	cases := []struct {
		name   string
		checks map[string]SelfCheck
		want   HealthStatus
	}{
		{
			name:   "all healthy",
			checks: map[string]SelfCheck{"a": healthyCheck("a"), "b": healthyCheck("b")},
			want:   StatusHealthy,
		},
		{
			name:   "one degraded",
			checks: map[string]SelfCheck{"a": healthyCheck("a"), "b": degradedCheck("b", "slow")},
			want:   StatusDegraded,
		},
		{
			name: "degraded and unhealthy",
			checks: map[string]SelfCheck{
				"a": degradedCheck("a", "slow"),
				"b": unhealthyCheck("b", "down"),
			},
			want: StatusUnhealthy,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := NewSelfHealthMonitor("ops-0", c.checks, nil)
			report := m.Evaluate()
			if report.Status != c.want {
				t.Errorf("aggregate status = %s, want %s", report.Status, c.want)
			}
			if len(report.Checks) != len(c.checks) {
				t.Errorf("report has %d checks, want %d", len(report.Checks), len(c.checks))
			}
		})
	}
}

// TestSelfHealthChecksAreSortedByName confirms the report orders checks
// deterministically so two evaluations produce a stable JSON body.
func TestSelfHealthChecksAreSortedByName(t *testing.T) {
	m := NewSelfHealthMonitor("ops-0", map[string]SelfCheck{
		"redis_consumer_lag": healthyCheck("redis_consumer_lag"),
		"k8s_api":            healthyCheck("k8s_api"),
		"postgres_pool":      healthyCheck("postgres_pool"),
	}, nil)
	report := m.Evaluate()
	for i := 1; i < len(report.Checks); i++ {
		if report.Checks[i-1].Name > report.Checks[i].Name {
			t.Errorf("checks not sorted: %q before %q", report.Checks[i-1].Name, report.Checks[i].Name)
		}
	}
}

// TestSelfHealthFiresChangeHookOnTransition is the §25.4
// ops_health_status_changed contract: the change hook fires when the
// aggregate status transitions and not when it stays the same.
func TestSelfHealthFiresChangeHookOnTransition(t *testing.T) {
	var mu sync.Mutex
	var transitions []string

	degraded := false
	checks := map[string]SelfCheck{
		"pg": func() CheckResult {
			if degraded {
				return CheckResult{Name: "pg", Status: StatusDegraded, Detail: "pool full"}
			}
			return CheckResult{Name: "pg", Status: StatusHealthy}
		},
	}
	m := NewSelfHealthMonitor("ops-0", checks, func(prev, next SelfHealthReport) {
		mu.Lock()
		transitions = append(transitions, prev.StatusText+"->"+next.StatusText)
		mu.Unlock()
	})

	m.Evaluate() // healthy; first evaluation from the healthy baseline — no transition
	m.Evaluate() // still healthy — no transition
	degraded = true
	m.Evaluate() // healthy -> degraded — one transition
	m.Evaluate() // still degraded — no transition
	degraded = false
	m.Evaluate() // degraded -> healthy — one transition

	mu.Lock()
	defer mu.Unlock()
	want := []string{"healthy->degraded", "degraded->healthy"}
	if len(transitions) != len(want) {
		t.Fatalf("transitions = %v, want %v", transitions, want)
	}
	for i := range want {
		if transitions[i] != want[i] {
			t.Errorf("transition[%d] = %q, want %q", i, transitions[i], want[i])
		}
	}
}

// TestSelfHealthReportBeforeEvaluateIsHealthy confirms the GET
// /v1/admin/ops/health endpoint sees a healthy baseline before the
// first self-monitor tick rather than an empty report.
func TestSelfHealthReportBeforeEvaluateIsHealthy(t *testing.T) {
	m := NewSelfHealthMonitor("ops-0", map[string]SelfCheck{"pg": healthyCheck("pg")}, nil)
	r := m.Report()
	if r.Status != StatusHealthy {
		t.Errorf("baseline status = %s, want healthy", r.Status)
	}
	body := m.SelfHealth()
	if body["status"] != "healthy" {
		t.Errorf("SelfHealth() status = %v, want healthy", body["status"])
	}
	if body["replicaId"] != "ops-0" {
		t.Errorf("SelfHealth() replicaId = %v, want ops-0", body["replicaId"])
	}
}

// TestSelfHealthJSONProjectionCarriesDetail confirms the JSON body the
// endpoint serves includes each check's name, status, and failure
// detail.
func TestSelfHealthJSONProjectionCarriesDetail(t *testing.T) {
	m := NewSelfHealthMonitor("ops-0", map[string]SelfCheck{
		"webhook_backlog": degradedCheck("webhook_backlog", "210 pending deliveries"),
	}, nil)
	m.Evaluate()
	body := m.SelfHealth()
	checks, ok := body["checks"].([]map[string]any)
	if !ok || len(checks) != 1 {
		t.Fatalf("checks = %v, want one entry", body["checks"])
	}
	if checks[0]["status"] != "degraded" || checks[0]["detail"] != "210 pending deliveries" {
		t.Errorf("check JSON = %v, want degraded with the backlog detail", checks[0])
	}
}
