// SPDX-License-Identifier: MIT

package deadlock_test

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/deadlock"
)

// TestDeadlockPromMetricsCarryTheirLabelSets pins the label sets: the
// detection counter and the duration histogram are labelled by pool alone,
// and the resolution counter by pool and resolution.
//
// spec: §16.1 (Metrics), §8.8 (TaskRecord and TaskResult Schema)
func TestDeadlockPromMetricsCarryTheirLabelSets(t *testing.T) {
	reg := prometheus.NewRegistry()
	m, err := deadlock.NewPromMetrics(reg)
	if err != nil {
		t.Fatalf("NewPromMetrics: %v", err)
	}
	m.IncDelegationDeadlockDetected("pool-a")
	m.ObserveDelegationDeadlockResolution("pool-a", "client_input", 12)
	m.ObserveDelegationDeadlockResolution("pool-a", "child_cancel", 20)
	m.ObserveDelegationDeadlockResolution("pool-b", "timeout", 120)

	want := `
# HELP lenny_delegation_deadlock_detected_total Subtree deadlock detections (§8.8 heuristic), by the deadlocked root's pool.
# TYPE lenny_delegation_deadlock_detected_total counter
lenny_delegation_deadlock_detected_total{pool="pool-a"} 1
# HELP lenny_delegation_deadlock_resolution_total Subtree deadlock resolutions by pool and resolution (client_input | child_cancel | timeout).
# TYPE lenny_delegation_deadlock_resolution_total counter
lenny_delegation_deadlock_resolution_total{pool="pool-a",resolution="child_cancel"} 1
lenny_delegation_deadlock_resolution_total{pool="pool-a",resolution="client_input"} 1
lenny_delegation_deadlock_resolution_total{pool="pool-b",resolution="timeout"} 1
`
	if err := testutil.GatherAndCompare(
		reg, strings.NewReader(want),
		"lenny_delegation_deadlock_detected_total",
		"lenny_delegation_deadlock_resolution_total",
	); err != nil {
		t.Fatal(err)
	}
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	counts := map[string]uint64{}
	for _, mf := range mfs {
		if mf.GetName() != "lenny_delegation_deadlock_duration_seconds" {
			continue
		}
		for _, metric := range mf.GetMetric() {
			labels := metric.GetLabel()
			if len(labels) != 1 || labels[0].GetName() != "pool" {
				t.Fatalf("duration histogram labels = %v, want pool alone", labels)
			}
			counts[labels[0].GetValue()] = metric.GetHistogram().GetSampleCount()
		}
	}
	if counts["pool-a"] != 2 || counts["pool-b"] != 1 {
		t.Fatalf("duration histogram counts = %v, want pool-a 2 and pool-b 1", counts)
	}
}

func TestDeadlockPromMetricsRefuseARegistryThatHoldsTheNames(t *testing.T) {
	reg := prometheus.NewRegistry()
	if _, err := deadlock.NewPromMetrics(reg); err != nil {
		t.Fatalf("first NewPromMetrics: %v", err)
	}
	if _, err := deadlock.NewPromMetrics(reg); err == nil {
		t.Fatal("second NewPromMetrics on the same registry returned no error")
	}
}

func TestNilDeadlockPromMetricsIgnoreEveryCall(t *testing.T) {
	var m *deadlock.PromMetrics
	m.IncDelegationDeadlockDetected("pool-a")
	m.ObserveDelegationDeadlockResolution("pool-a", "timeout", 1)
}
