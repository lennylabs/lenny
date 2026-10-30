// SPDX-License-Identifier: MIT

package gatewaymetrics_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/metrics/gatewaymetrics"
)

// TestDelegationDeadlockMetricsCarrySpecLabels_spec_16_1 pins the §16.1
// label sets: the detection counter and the duration histogram are labelled
// by `pool` alone, and the resolution counter by `pool` and `resolution`.
// No series carries a `tenant_id` label or a `resolution` label on the
// histogram.
//
// spec: §16.1 (Metrics), §8.8 (Subtree deadlock detection)
func TestDelegationDeadlockMetricsCarrySpecLabels_spec_16_1(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.IncDelegationDeadlockDetected("pool-a")
	m.ObserveDelegationDeadlockResolution("pool-a", "client_input", 12)
	m.ObserveDelegationDeadlockResolution("pool-a", "child_cancel", 20)
	m.ObserveDelegationDeadlockResolution("pool-b", "timeout", 120)

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rr.Body.String()
	for _, want := range []string{
		`lenny_delegation_deadlock_detected_total{pool="pool-a"} 1`,
		`lenny_delegation_deadlock_resolution_total{pool="pool-a",resolution="client_input"} 1`,
		`lenny_delegation_deadlock_resolution_total{pool="pool-a",resolution="child_cancel"} 1`,
		`lenny_delegation_deadlock_resolution_total{pool="pool-b",resolution="timeout"} 1`,
		`lenny_delegation_deadlock_duration_seconds_count{pool="pool-a"} 2`,
		`lenny_delegation_deadlock_duration_seconds_count{pool="pool-b"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics missing %s", want)
		}
	}
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "lenny_delegation_deadlock_") {
			continue
		}
		if strings.Contains(line, "tenant_id=") {
			t.Errorf("deadlock series carries tenant_id: %s", line)
		}
		if strings.HasPrefix(line, "lenny_delegation_deadlock_duration_seconds") && strings.Contains(line, "resolution=") {
			t.Errorf("duration histogram carries resolution: %s", line)
		}
	}
}

// TestDelegationDeadlockMetricsNilReceiver_spec_16_1 pins that the deadlock
// recorders are no-ops on a nil metric set, as the detector runs without
// metrics in tests and in a gateway built without them.
//
// spec: §16.1 (Metrics)
func TestDelegationDeadlockMetricsNilReceiver_spec_16_1(t *testing.T) {
	var m *gatewaymetrics.Metrics
	m.IncDelegationDeadlockDetected("pool-a")
	m.ObserveDelegationDeadlockResolution("pool-a", "timeout", 1)
}
