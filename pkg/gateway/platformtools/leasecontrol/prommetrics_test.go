// SPDX-License-Identifier: MIT

package leasecontrol_test

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/lennylabs/lenny/pkg/gateway/platformtools/leasecontrol"
)

// spec: §16.1 (Metrics), §8.6 (Lease Extension)
func TestLeasePromMetricsCountExtensionsByTenantAndOutcome(t *testing.T) {
	reg := prometheus.NewRegistry()
	m, err := leasecontrol.NewPromMetrics(reg)
	if err != nil {
		t.Fatalf("NewPromMetrics: %v", err)
	}
	m.IncDelegationLeaseExtension("acme", "approved")
	m.IncDelegationLeaseExtension("acme", "approved")
	m.IncDelegationLeaseExtension("acme", "denied")
	m.IncDelegationLeaseExtension("globex", "capped")

	want := `
# HELP lenny_delegation_lease_extension_total §8.6 delegation lease-extension decisions by tenant and §8.6 outcome.
# TYPE lenny_delegation_lease_extension_total counter
lenny_delegation_lease_extension_total{outcome="approved",tenant_id="acme"} 2
lenny_delegation_lease_extension_total{outcome="capped",tenant_id="globex"} 1
lenny_delegation_lease_extension_total{outcome="denied",tenant_id="acme"} 1
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(want), "lenny_delegation_lease_extension_total"); err != nil {
		t.Fatal(err)
	}
}

func TestLeasePromMetricsRefuseARegistryThatHoldsTheName(t *testing.T) {
	reg := prometheus.NewRegistry()
	if _, err := leasecontrol.NewPromMetrics(reg); err != nil {
		t.Fatalf("first NewPromMetrics: %v", err)
	}
	if _, err := leasecontrol.NewPromMetrics(reg); err == nil {
		t.Fatal("second NewPromMetrics on the same registry returned no error")
	}
}

func TestNilLeasePromMetricsIgnoreEveryCall(t *testing.T) {
	var m *leasecontrol.PromMetrics
	m.IncDelegationLeaseExtension("acme", "approved")
}
