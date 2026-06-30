// SPDX-License-Identifier: MIT

package health_test

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/operability/health"
	"github.com/lennylabs/lenny/pkg/ops/conventions"
)

// TestRunbookForIssue_RequiredByPathB pins the §25.7 Path B
// (lines 3217–3231) lookup. The eight codes named at §17.7 line 741
// must each resolve to the runbook the spec quotes verbatim.
// spec: §25.7 lines 3222-3231; §17.7 line 741.
func TestRunbookForIssue_RequiredByPathB_spec_17_7_741(t *testing.T) {
	required := map[string]string{
		"WARM_POOL_EXHAUSTED":       "warm-pool-exhaustion",
		"WARM_POOL_LOW":             "warm-pool-exhaustion",
		"CREDENTIAL_POOL_EXHAUSTED": "credential-pool-exhaustion",
		"POSTGRES_UNREACHABLE":      "postgres-failover",
		"REDIS_UNREACHABLE":         "redis-failure",
		"MINIO_UNREACHABLE":         "minio-failure",
		"CERT_EXPIRY_IMMINENT":      "cert-manager-outage",
		"CIRCUIT_BREAKER_OPEN":      "gateway-replica-failure",
	}
	for issue, want := range required {
		if got := health.RunbookForIssue(issue); got != want {
			t.Errorf("RunbookForIssue(%q): want %q, got %q", issue, want, got)
		}
	}
	if got := health.RunbookForIssue("UNKNOWN_ISSUE"); got != "" {
		t.Errorf("RunbookForIssue(UNKNOWN_ISSUE): want empty, got %q", got)
	}
}

// TestRegisterIssueRunbook lets a backend register a new issue → runbook
// link without re-opening the central table.
// spec: §17.7 line 741.
func TestRegisterIssueRunbook(t *testing.T) {
	health.RegisterIssueRunbook("CUSTOM_ISSUE", "custom-issue-runbook")
	if got := health.RunbookForIssue("CUSTOM_ISSUE"); got != "custom-issue-runbook" {
		t.Errorf("RunbookForIssue(CUSTOM_ISSUE): want custom-issue-runbook, got %q", got)
	}
}

// TestAggregatorBackfillsActionsFromIssue asserts that when a Checker
// stamps a ranked Issue but leaves the remediation hint empty, the
// aggregator resolves the §25.3 suggestedActions array from the catalog,
// each carrying the §25.7 Path B runbook pointer.
// spec: §25.3 lines 459-501; §25.7 line 3234.
func TestAggregatorBackfillsActionsFromIssue_spec_25_3_459(t *testing.T) {
	agg := health.NewAggregator()
	agg.Register(health.CheckerFunc{
		ComponentName: "warm_pool",
		Fn: func(context.Context) health.Component {
			return health.Component{
				Name:   "warm_pool",
				Status: health.StatusUnhealthy,
				Issue:  "WARM_POOL_EXHAUSTED",
				Detail: "pool empty",
			}
		},
	})
	report := agg.Report(context.Background())
	if len(report.Components) != 1 {
		t.Fatalf("components: %d, want 1", len(report.Components))
	}
	comp := report.Components[0]
	// WARM_POOL_EXHAUSTED is a ranked issue: the singular field is empty
	// and the ordered alternatives carry the Path B runbook.
	if comp.SuggestedAction != nil {
		t.Errorf("SuggestedAction: %+v, want nil (ranked issue uses the array form)", comp.SuggestedAction)
	}
	if len(comp.SuggestedActions) < 2 {
		t.Fatalf("SuggestedActions: %d, want the ranked alternatives", len(comp.SuggestedActions))
	}
	if comp.SuggestedActions[0].Runbook != "warm-pool-exhaustion" {
		t.Errorf("primary runbook: %q, want warm-pool-exhaustion", comp.SuggestedActions[0].Runbook)
	}
	if comp.SuggestedActions[0].Action != "SCALE_WARM_POOL" {
		t.Errorf("primary action: %q, want SCALE_WARM_POOL", comp.SuggestedActions[0].Action)
	}
	// The pool name flows into the executable endpoint.
	if comp.SuggestedActions[0].Endpoint != "PUT /v1/admin/pools/warm_pool/warm-count" {
		t.Errorf("endpoint: %q, want the warm_pool target substituted", comp.SuggestedActions[0].Endpoint)
	}
}

// TestAggregatorPreservesExplicitAction ensures that a checker that
// already populated the remediation hint wins over the catalog back-fill,
// so out-of-tree probes keep control.
// spec: §25.3 lines 459-501.
func TestAggregatorPreservesExplicitAction_spec_25_3_459(t *testing.T) {
	agg := health.NewAggregator()
	agg.Register(health.CheckerFunc{
		ComponentName: "warm_pool",
		Fn: func(context.Context) health.Component {
			return health.Component{
				Name:   "warm_pool",
				Status: health.StatusUnhealthy,
				Issue:  "WARM_POOL_EXHAUSTED",
				SuggestedAction: &conventions.SuggestedAction{
					Action:  "CUSTOM",
					Runbook: "custom-warm-pool-runbook",
				},
			}
		},
	})
	report := agg.Report(context.Background())
	comp := report.Components[0]
	if comp.SuggestedAction == nil || comp.SuggestedAction.Runbook != "custom-warm-pool-runbook" {
		t.Errorf("SuggestedAction: %+v, want explicit override preserved", comp.SuggestedAction)
	}
	if len(comp.SuggestedActions) != 0 {
		t.Errorf("SuggestedActions: %d, want none when the checker set the singular form", len(comp.SuggestedActions))
	}
}

// TestComponentEndpointBackfillsActionsFromIssue covers the
// single-component lookup path (Aggregator.Component) so the
// /v1/admin/health/{component} response also carries the structured
// remediation hint when only Issue is stamped.
// spec: §25.3 lines 459-501.
func TestComponentEndpointBackfillsActionsFromIssue_spec_25_3_459(t *testing.T) {
	agg := health.NewAggregator()
	agg.Register(health.CheckerFunc{
		ComponentName: "circuit_breaker_cache",
		Fn: func(context.Context) health.Component {
			return health.Component{
				Name:   "circuit_breaker_cache",
				Status: health.StatusUnhealthy,
				Issue:  "CIRCUIT_BREAKER_OPEN",
			}
		},
	})
	comp, ok := agg.Component(context.Background(), "circuit_breaker_cache")
	if !ok {
		t.Fatal("component not found")
	}
	if len(comp.SuggestedActions) < 2 {
		t.Fatalf("SuggestedActions: %d, want the ranked alternatives", len(comp.SuggestedActions))
	}
	if comp.SuggestedActions[0].Runbook != "gateway-replica-failure" {
		t.Errorf("primary runbook: %q, want gateway-replica-failure", comp.SuggestedActions[0].Runbook)
	}
}
