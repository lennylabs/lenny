// SPDX-License-Identifier: MIT

package delegationbudget_test

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/lennylabs/lenny/pkg/gateway/platformtools/delegationbudget"
)

// spec: §16.1 (Metrics), §11.2 (Budgets and Quotas)
func TestBudgetPromMetricsRegisterAndCountReconstructionsByOutcome(t *testing.T) {
	reg := prometheus.NewRegistry()
	m, err := delegationbudget.NewPromMetrics(reg)
	if err != nil {
		t.Fatalf("NewPromMetrics: %v", err)
	}
	m.IncDelegationBudgetReconstruction("success")
	m.IncDelegationBudgetReconstruction("success")
	m.IncDelegationBudgetReconstruction("irrecoverable")

	want := `
# HELP lenny_delegation_budget_reconstruction_total Delegation budget reconstruction events by outcome (success | irrecoverable).
# TYPE lenny_delegation_budget_reconstruction_total counter
lenny_delegation_budget_reconstruction_total{outcome="irrecoverable"} 1
lenny_delegation_budget_reconstruction_total{outcome="success"} 2
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(want), "lenny_delegation_budget_reconstruction_total"); err != nil {
		t.Fatal(err)
	}
}

func TestBudgetPromMetricsRefuseARegistryThatHoldsTheName(t *testing.T) {
	reg := prometheus.NewRegistry()
	if _, err := delegationbudget.NewPromMetrics(reg); err != nil {
		t.Fatalf("first NewPromMetrics: %v", err)
	}
	if _, err := delegationbudget.NewPromMetrics(reg); err == nil {
		t.Fatal("second NewPromMetrics on the same registry returned no error")
	}
}

func TestNilBudgetPromMetricsIgnoreEveryCall(t *testing.T) {
	var m *delegationbudget.PromMetrics
	m.IncDelegationBudgetReconstruction("success")
}
