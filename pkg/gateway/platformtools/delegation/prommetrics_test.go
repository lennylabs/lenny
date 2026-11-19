// SPDX-License-Identifier: MIT

package delegation_test

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/lennylabs/lenny/pkg/gateway/platformtools/delegation"
)

// spec: §16.1 (Metrics), §8.2 (Delegation Mechanism)
func TestDelegationPromMetricsRegisterAndEmitWithTheirLabels(t *testing.T) {
	reg := prometheus.NewRegistry()
	m, err := delegation.NewPromMetrics(reg)
	if err != nil {
		t.Fatalf("NewPromMetrics: %v", err)
	}
	m.ObserveDelegationDepth("pool-a", 3)
	m.ObserveDelegationDepth("pool-a", 1)
	m.IncDelegationWouldHaveBlocked("pool-a", "acme", "platform", "enforce")
	m.IncDelegationWouldHaveBlocked("pool-a", "acme", "runtime", "warn")

	want := `
# HELP lenny_delegation_would_have_blocked_total Self-recursion would-have-blocked counter by layer and cycle-detection mode (§8.2).
# TYPE lenny_delegation_would_have_blocked_total counter
lenny_delegation_would_have_blocked_total{layer="platform",mode="enforce",pool="pool-a",tenant_id="acme"} 1
lenny_delegation_would_have_blocked_total{layer="runtime",mode="warn",pool="pool-a",tenant_id="acme"} 1
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(want), "lenny_delegation_would_have_blocked_total"); err != nil {
		t.Fatal(err)
	}
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() != "lenny_delegation_depth" {
			continue
		}
		h := mf.GetMetric()[0].GetHistogram()
		if h.GetSampleCount() != 2 || h.GetSampleSum() != 4 {
			t.Fatalf("depth histogram count=%d sum=%v, want 2 and 4", h.GetSampleCount(), h.GetSampleSum())
		}
		if got := len(h.GetBucket()); got != 8 {
			t.Fatalf("depth histogram has %d buckets, want 8", got)
		}
		return
	}
	t.Fatal("lenny_delegation_depth not gathered")
}

func TestDelegationPromMetricsRefuseARegistryThatHoldsTheNames(t *testing.T) {
	reg := prometheus.NewRegistry()
	if _, err := delegation.NewPromMetrics(reg); err != nil {
		t.Fatalf("first NewPromMetrics: %v", err)
	}
	if _, err := delegation.NewPromMetrics(reg); err == nil {
		t.Fatal("second NewPromMetrics on the same registry returned no error")
	}
}

func TestNilDelegationPromMetricsIgnoreEveryCall(t *testing.T) {
	var m *delegation.PromMetrics
	m.ObserveDelegationDepth("pool-a", 1)
	m.IncDelegationWouldHaveBlocked("pool-a", "acme", "policy", "enforce")
}
