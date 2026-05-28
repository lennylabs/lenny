// SPDX-License-Identifier: MIT

package health_test

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/health"
)

// spec: §4.0 / §25.3 — the centralized runbook-link table resolves
// known components to their runbook references and returns the empty
// string for unknown components.
func TestRunbookFor_KnownComponents(t *testing.T) {
	cases := map[string]string{
		"postgres":              "postgres-failover",
		"redis":                 "redis-failure",
		"circuit_breaker_cache": "redis-failure",
		"warm_pool":             "warm-pool-exhaustion",
		"unknown_component":     "",
	}
	for comp, want := range cases {
		if got := health.RunbookFor(comp); got != want {
			t.Errorf("RunbookFor(%q): want %q, got %q", comp, want, got)
		}
	}
}

// spec: §4.0 — RegisterRunbook installs a new (component → runbook)
// link so out-of-tree backends can publish their own runbook routing.
func TestRegisterRunbook(t *testing.T) {
	health.RegisterRunbook("custom_backend", "custom-runbook")
	if got := health.RunbookFor("custom_backend"); got != "custom-runbook" {
		t.Errorf("RunbookFor(custom_backend): want custom-runbook, got %q", got)
	}
}

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

// TestAggregatorBackfillsRunbookRefFromIssue asserts that when a
// Checker stamps Issue but leaves RunbookRef empty, the aggregator
// resolves the runbook from §17.7's issueRunbooks table so the §25.3
// response carries the Path B pointer.
// spec: §25.7 line 3234.
func TestAggregatorBackfillsRunbookRefFromIssue_spec_25_7_3234(t *testing.T) {
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
	if comp.RunbookRef != "warm-pool-exhaustion" {
		t.Errorf("RunbookRef: %q, want warm-pool-exhaustion", comp.RunbookRef)
	}
	if comp.Issue != "WARM_POOL_EXHAUSTED" {
		t.Errorf("Issue: %q, want WARM_POOL_EXHAUSTED", comp.Issue)
	}
}

// TestAggregatorPreservesExplicitRunbookRef ensures that a checker
// that already stamped RunbookRef wins over the back-fill, so
// out-of-tree probes that point at a non-spec runbook keep control.
// spec: §25.7 line 3234.
func TestAggregatorPreservesExplicitRunbookRef_spec_25_7_3234(t *testing.T) {
	agg := health.NewAggregator()
	agg.Register(health.CheckerFunc{
		ComponentName: "warm_pool",
		Fn: func(context.Context) health.Component {
			return health.Component{
				Name:       "warm_pool",
				Status:     health.StatusUnhealthy,
				Issue:      "WARM_POOL_EXHAUSTED",
				RunbookRef: "custom-warm-pool-runbook",
			}
		},
	})
	report := agg.Report(context.Background())
	if got := report.Components[0].RunbookRef; got != "custom-warm-pool-runbook" {
		t.Errorf("RunbookRef: %q, want explicit override preserved", got)
	}
}

// TestComponentEndpointBackfillsRunbookRefFromIssue covers the
// single-component lookup path (Aggregator.Component) so the
// /v1/admin/health/{component} response also carries the Path B
// runbook pointer when only Issue is stamped.
// spec: §25.7 line 3234.
func TestComponentEndpointBackfillsRunbookRefFromIssue_spec_25_7_3234(t *testing.T) {
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
	if comp.RunbookRef != "gateway-replica-failure" {
		t.Errorf("RunbookRef: %q, want gateway-replica-failure", comp.RunbookRef)
	}
}
