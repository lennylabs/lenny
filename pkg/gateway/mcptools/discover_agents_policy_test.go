// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/delegationpolicystore"
	"github.com/lennylabs/lenny/pkg/gateway/environmentstore"
	"github.com/lennylabs/lenny/pkg/gateway/executor"
	"github.com/lennylabs/lenny/pkg/gateway/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcptools"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
)

// spec: §8.3 line 244 — `lenny/discover_agents` returns only targets
// authorized by the calling session's effective DelegationPolicy. The
// effective policy is the one named by the caller session's resolved
// runtime via DelegationPolicyRef; its tag-based allow/deny rule set
// narrows the discoverable agent set. F-8.5.7.

// policyFilterHarness wires the §8.5 MCP surface with a delegation
// Service, a DelegationPolicy registry, and a runtime registry so the
// discover_agents effective-policy filter can resolve a caller session's
// policy. noEnvironmentPolicy is allow-all so the §10.6 environment
// filter never interferes with the §8.3 policy assertions.
func policyFilterHarness(t *testing.T, parentRuntimeRef string) (*mcp.Server, runtimestore.Store, delegationpolicystore.Store) {
	t.Helper()
	store := memstore.New()
	runtimes := runtimestore.NewMemory()
	policies := delegationpolicystore.NewMemory()
	envs := environmentstore.NewMemory()
	tenants := tenantstore.NewMemory()
	_ = tenants.Create(context.Background(), tenantstore.Tenant{
		ID: "acme", NoEnvironmentPolicy: tenantstore.NoEnvPolicyAllowAll,
	})
	// The caller's parent session — its runtime carries the effective
	// DelegationPolicyRef the discover filter resolves.
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_parent", TenantID: "acme", UserID: "u-alice",
		State: session.StateRunning, RuntimeRef: parentRuntimeRef,
	}); err != nil {
		t.Fatalf("seed parent session: %v", err)
	}
	svc := delegation.NewService(store, delegation.Options{
		Runtimes: runtimes,
		Policies: policies,
		Clock:    func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	})
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:        store,
		Executor:     executor.NewEchoExecutor(),
		Runtimes:     runtimes,
		Delegation:   svc,
		Environments: envs,
		Tenants:      tenants,
		Clock:        func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:       func() string { return "sess_mcp" },
		TenantID:     "acme",
	})
	return srv, runtimes, policies
}

// callerOnParent is the principal bound to the seeded parent session so
// callerSessionID resolves the §8.3 effective policy.
var callerOnParent = authmw.Principal{
	Subject: "alice", TenantID: "acme", SessionID: "sess_parent",
}

func TestDiscoverAgentsFiltersByEffectiveDelegationPolicy_spec_8_3_244(t *testing.T) {
	srv, runtimes, policies := policyFilterHarness(t, "orchestrator")
	ctx := context.Background()
	// The orchestrator runtime names a scoped policy that allows only
	// access:read agents.
	_ = runtimes.Create(ctx, runtimestore.Runtime{
		Name: "orchestrator", Type: runtimestore.TypeAgent,
		DelegationPolicyRef: "scoped",
	})
	_ = runtimes.Create(ctx, runtimestore.Runtime{
		Name: "reader-agent", Type: runtimestore.TypeAgent,
		Labels: map[string]string{"access": "read"},
	})
	_ = runtimes.Create(ctx, runtimestore.Runtime{
		Name: "writer-agent", Type: runtimestore.TypeAgent,
		Labels: map[string]string{"access": "write"},
	})
	if err := policies.Create(ctx, delegationpolicystore.DelegationPolicy{
		TenantID: "acme", Name: "scoped",
		Rules: []delegationpolicystore.Rule{{
			Target: delegationpolicystore.Target{MatchLabels: map[string]string{"access": "read"}},
			Allow:  true,
		}},
	}); err != nil {
		t.Fatalf("create policy: %v", err)
	}

	text := resultText(t, callAs(t, srv.Handler(), callerOnParent, "lenny/discover_agents", `{}`))
	if !strings.Contains(text, "reader-agent") {
		t.Errorf("reader-agent is allowed by the effective policy and must appear: %q", text)
	}
	if strings.Contains(text, "writer-agent") {
		t.Errorf("writer-agent is not allowed by the effective policy and must not appear: %q", text)
	}
	// The orchestrator itself carries no access label, so the allow-list
	// policy (default deny) excludes it too.
	if strings.Contains(text, "orchestrator") {
		t.Errorf("orchestrator carries no matching label and must be excluded under a default-deny policy: %q", text)
	}
}

func TestDiscoverAgentsExplicitDenyOverridesAllow_spec_8_3_244(t *testing.T) {
	srv, runtimes, policies := policyFilterHarness(t, "orchestrator")
	ctx := context.Background()
	_ = runtimes.Create(ctx, runtimestore.Runtime{
		Name: "orchestrator", Type: runtimestore.TypeAgent,
		DelegationPolicyRef: "scoped",
	})
	_ = runtimes.Create(ctx, runtimestore.Runtime{
		Name: "blessed-agent", Type: runtimestore.TypeAgent,
		Labels: map[string]string{"team": "research"},
	})
	_ = runtimes.Create(ctx, runtimestore.Runtime{
		Name: "quarantined-agent", Type: runtimestore.TypeAgent,
		Labels: map[string]string{"team": "research", "quarantined": "true"},
	})
	// Allow team:research broadly, but explicitly deny anything tagged
	// quarantined. The §8.3 deny-overrides-allow precedence must drop the
	// quarantined agent even though the broad allow rule matches it.
	if err := policies.Create(ctx, delegationpolicystore.DelegationPolicy{
		TenantID: "acme", Name: "scoped",
		Rules: []delegationpolicystore.Rule{
			{Target: delegationpolicystore.Target{MatchLabels: map[string]string{"team": "research"}}, Allow: true},
			{Target: delegationpolicystore.Target{MatchLabels: map[string]string{"quarantined": "true"}}, Allow: false},
		},
	}); err != nil {
		t.Fatalf("create policy: %v", err)
	}

	text := resultText(t, callAs(t, srv.Handler(), callerOnParent, "lenny/discover_agents", `{}`))
	if !strings.Contains(text, "blessed-agent") {
		t.Errorf("blessed-agent is allowed and must appear: %q", text)
	}
	if strings.Contains(text, "quarantined-agent") {
		t.Errorf("quarantined-agent is explicitly denied and must not appear: %q", text)
	}
}

func TestDiscoverAgentsNoEffectivePolicyReturnsAll_spec_8_3_244(t *testing.T) {
	// The parent runtime names no DelegationPolicyRef, so no policy
	// narrowing applies and discovery reflects only the §10.6 scope
	// (allow-all here): every agent is visible.
	srv, runtimes, _ := policyFilterHarness(t, "orchestrator")
	ctx := context.Background()
	_ = runtimes.Create(ctx, runtimestore.Runtime{
		Name: "orchestrator", Type: runtimestore.TypeAgent,
	})
	_ = runtimes.Create(ctx, runtimestore.Runtime{
		Name: "reader-agent", Type: runtimestore.TypeAgent,
		Labels: map[string]string{"access": "read"},
	})
	_ = runtimes.Create(ctx, runtimestore.Runtime{
		Name: "writer-agent", Type: runtimestore.TypeAgent,
		Labels: map[string]string{"access": "write"},
	})

	text := resultText(t, callAs(t, srv.Handler(), callerOnParent, "lenny/discover_agents", `{}`))
	for _, want := range []string{"reader-agent", "writer-agent"} {
		if !strings.Contains(text, want) {
			t.Errorf("with no effective policy every agent is visible; %q missing: %q", want, text)
		}
	}
}

func TestDiscoverAgentsUnboundCallerSkipsPolicyFilter_spec_8_3_244(t *testing.T) {
	// A caller with no session binding (no principal SessionID, no
	// sessionId arg) cannot resolve an effective policy; the filter
	// fails open to the §10.6 environment scope rather than denying.
	srv, runtimes, policies := policyFilterHarness(t, "orchestrator")
	ctx := context.Background()
	_ = runtimes.Create(ctx, runtimestore.Runtime{
		Name: "orchestrator", Type: runtimestore.TypeAgent,
		DelegationPolicyRef: "scoped",
	})
	_ = runtimes.Create(ctx, runtimestore.Runtime{
		Name: "reader-agent", Type: runtimestore.TypeAgent,
		Labels: map[string]string{"access": "read"},
	})
	_ = runtimes.Create(ctx, runtimestore.Runtime{
		Name: "writer-agent", Type: runtimestore.TypeAgent,
		Labels: map[string]string{"access": "write"},
	})
	_ = policies.Create(ctx, delegationpolicystore.DelegationPolicy{
		TenantID: "acme", Name: "scoped",
		Rules: []delegationpolicystore.Rule{{
			Target: delegationpolicystore.Target{MatchLabels: map[string]string{"access": "read"}},
			Allow:  true,
		}},
	})

	unbound := authmw.Principal{Subject: "alice", TenantID: "acme"}
	text := resultText(t, callAs(t, srv.Handler(), unbound, "lenny/discover_agents", `{}`))
	// No policy narrowing without a resolvable session.
	if !strings.Contains(text, "writer-agent") {
		t.Errorf("an unbound caller must not have the policy filter applied; writer-agent missing: %q", text)
	}
}

func TestDiscoverAgentsResolvesPolicyViaSessionIDArg_spec_8_3_244(t *testing.T) {
	// The transport-fallback sessionId argument resolves the effective
	// policy when the principal carries no SessionID claim.
	srv, runtimes, policies := policyFilterHarness(t, "orchestrator")
	ctx := context.Background()
	_ = runtimes.Create(ctx, runtimestore.Runtime{
		Name: "orchestrator", Type: runtimestore.TypeAgent,
		DelegationPolicyRef: "scoped",
	})
	_ = runtimes.Create(ctx, runtimestore.Runtime{
		Name: "reader-agent", Type: runtimestore.TypeAgent,
		Labels: map[string]string{"access": "read"},
	})
	_ = runtimes.Create(ctx, runtimestore.Runtime{
		Name: "writer-agent", Type: runtimestore.TypeAgent,
		Labels: map[string]string{"access": "write"},
	})
	_ = policies.Create(ctx, delegationpolicystore.DelegationPolicy{
		TenantID: "acme", Name: "scoped",
		Rules: []delegationpolicystore.Rule{{
			Target: delegationpolicystore.Target{MatchLabels: map[string]string{"access": "read"}},
			Allow:  true,
		}},
	})

	unbound := authmw.Principal{Subject: "alice", TenantID: "acme"}
	text := resultText(t, callAs(t, srv.Handler(), unbound, "lenny/discover_agents",
		`{"sessionId":"sess_parent"}`))
	if !strings.Contains(text, "reader-agent") {
		t.Errorf("reader-agent is allowed by the policy resolved via sessionId arg: %q", text)
	}
	if strings.Contains(text, "writer-agent") {
		t.Errorf("writer-agent must be filtered when the policy is resolved via sessionId arg: %q", text)
	}
}
