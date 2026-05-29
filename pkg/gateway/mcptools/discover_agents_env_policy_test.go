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
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
)

// spec: §10.6 line 601, line 629 — defaultDelegationPolicy is the
// DelegationPolicy applied to sessions created in an environment, and the
// §10.6 effective scope is `(environment definition ∪ cross-environment
// permitted runtimes) ∩ delegation policy`. lenny/discover_agents
// intersects the runtime-level §8.3 policy with the environment default
// policy: a target survives only when every governing policy permits it.
// F-10.6.7.

// envPolicyHarness wires the §8.5 MCP surface with a delegation Service,
// a DelegationPolicy registry, an environment registry, and a parent
// session created in an environment. noEnvironmentPolicy is allow-all and
// the caller is a member of no environment, so the §10.6 transparent
// runtime filter passes every runtime and the assertions isolate the
// delegation-policy intersection. The session's Environment field carries
// the environment whose defaultDelegationPolicy the filter resolves.
func envPolicyHarness(t *testing.T, parentRuntimeRef, envName, envDefaultPolicy string) (*mcp.Server, runtimestore.Store, delegationpolicystore.Store) {
	t.Helper()
	store := memstore.New()
	runtimes := runtimestore.NewMemory()
	policies := delegationpolicystore.NewMemory()
	envs := environmentstore.NewMemory()
	tenants := tenantstore.NewMemory()
	if err := tenants.Create(context.Background(), tenantstore.Tenant{
		ID: "acme", NoEnvironmentPolicy: tenantstore.NoEnvPolicyAllowAll,
	}); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if envName != "" {
		if err := envs.Create(context.Background(), environmentstore.Environment{
			Name: envName, TenantID: "acme", DefaultDelegationPolicy: envDefaultPolicy,
		}); err != nil {
			t.Fatalf("seed environment: %v", err)
		}
	}
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_parent", TenantID: "acme", UserID: "u-alice",
		State: session.StateRunning, RuntimeRef: parentRuntimeRef, Environment: envName,
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

func seedAgent(t *testing.T, runtimes runtimestore.Store, name string, labels map[string]string, policyRef string) {
	t.Helper()
	if err := runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: name, Type: runtimestore.TypeAgent, Labels: labels, DelegationPolicyRef: policyRef,
	}); err != nil {
		t.Fatalf("seed runtime %s: %v", name, err)
	}
}

func TestDiscoverAgentsAppliesEnvironmentDefaultPolicy_spec_10_6_601(t *testing.T) {
	// The parent runtime names no §8.3 policy; the environment's
	// defaultDelegationPolicy alone narrows the discoverable set.
	srv, runtimes, policies := envPolicyHarness(t, "orchestrator", "security-team", "env-scoped")
	ctx := context.Background()
	seedAgent(t, runtimes, "orchestrator", nil, "")
	seedAgent(t, runtimes, "reader-agent", map[string]string{"access": "read"}, "")
	seedAgent(t, runtimes, "writer-agent", map[string]string{"access": "write"}, "")
	if err := policies.Create(ctx, delegationpolicystore.DelegationPolicy{
		TenantID: "acme", Name: "env-scoped",
		Rules: []delegationpolicystore.Rule{{
			Target: delegationpolicystore.Target{MatchLabels: map[string]string{"access": "read"}},
			Allow:  true,
		}},
	}); err != nil {
		t.Fatalf("create env policy: %v", err)
	}

	text := resultText(t, callAs(t, srv.Handler(), callerOnParent, "lenny/discover_agents", `{}`))
	if !strings.Contains(text, "reader-agent") {
		t.Errorf("reader-agent is allowed by the environment default policy and must appear: %q", text)
	}
	if strings.Contains(text, "writer-agent") {
		t.Errorf("writer-agent is denied by the environment default policy and must not appear: %q", text)
	}
}

func TestDiscoverAgentsIntersectsRuntimeAndEnvironmentPolicy_spec_10_6_629(t *testing.T) {
	// Two governing policies: the runtime-level rt-policy allows tier:gold,
	// the environment default env-scoped allows access:read. The §10.6 line
	// 629 intersection admits only a target both permit.
	srv, runtimes, policies := envPolicyHarness(t, "orchestrator", "security-team", "env-scoped")
	ctx := context.Background()
	seedAgent(t, runtimes, "orchestrator", nil, "rt-policy")
	// in both → survives
	seedAgent(t, runtimes, "reader-gold", map[string]string{"access": "read", "tier": "gold"}, "")
	// runtime allows (gold) but env denies (write) → excluded
	seedAgent(t, runtimes, "writer-gold", map[string]string{"access": "write", "tier": "gold"}, "")
	// env allows (read) but runtime denies (silver) → excluded
	seedAgent(t, runtimes, "reader-silver", map[string]string{"access": "read", "tier": "silver"}, "")
	if err := policies.Create(ctx, delegationpolicystore.DelegationPolicy{
		TenantID: "acme", Name: "rt-policy",
		Rules: []delegationpolicystore.Rule{{
			Target: delegationpolicystore.Target{MatchLabels: map[string]string{"tier": "gold"}},
			Allow:  true,
		}},
	}); err != nil {
		t.Fatalf("create rt policy: %v", err)
	}
	if err := policies.Create(ctx, delegationpolicystore.DelegationPolicy{
		TenantID: "acme", Name: "env-scoped",
		Rules: []delegationpolicystore.Rule{{
			Target: delegationpolicystore.Target{MatchLabels: map[string]string{"access": "read"}},
			Allow:  true,
		}},
	}); err != nil {
		t.Fatalf("create env policy: %v", err)
	}

	text := resultText(t, callAs(t, srv.Handler(), callerOnParent, "lenny/discover_agents", `{}`))
	if !strings.Contains(text, "reader-gold") {
		t.Errorf("reader-gold satisfies both policies and must appear: %q", text)
	}
	for _, excluded := range []string{"writer-gold", "reader-silver"} {
		if strings.Contains(text, excluded) {
			t.Errorf("%s fails one of the two intersected policies and must not appear: %q", excluded, text)
		}
	}
}

func TestDiscoverAgentsMissingEnvironmentPolicyFailsOpen_spec_10_6_601(t *testing.T) {
	// The environment names a defaultDelegationPolicy that does not resolve
	// (never created). The environment-default layer imposes no restriction,
	// matching the conservative fall-through the runtime-level resolver uses.
	srv, runtimes, _ := envPolicyHarness(t, "orchestrator", "security-team", "ghost-policy")
	seedAgent(t, runtimes, "orchestrator", nil, "")
	seedAgent(t, runtimes, "reader-agent", map[string]string{"access": "read"}, "")
	seedAgent(t, runtimes, "writer-agent", map[string]string{"access": "write"}, "")

	text := resultText(t, callAs(t, srv.Handler(), callerOnParent, "lenny/discover_agents", `{}`))
	for _, want := range []string{"reader-agent", "writer-agent"} {
		if !strings.Contains(text, want) {
			t.Errorf("an unresolved environment default policy must not narrow discovery; %q missing: %q", want, text)
		}
	}
}

func TestDiscoverAgentsEnvironmentWithNoDefaultPolicyUsesRuntimeOnly_spec_10_6_601(t *testing.T) {
	// The session is environment-scoped but the environment names no
	// defaultDelegationPolicy: only the runtime-level §8.3 policy governs.
	srv, runtimes, policies := envPolicyHarness(t, "orchestrator", "security-team", "")
	ctx := context.Background()
	seedAgent(t, runtimes, "orchestrator", nil, "rt-policy")
	seedAgent(t, runtimes, "reader-agent", map[string]string{"access": "read"}, "")
	seedAgent(t, runtimes, "writer-agent", map[string]string{"access": "write"}, "")
	if err := policies.Create(ctx, delegationpolicystore.DelegationPolicy{
		TenantID: "acme", Name: "rt-policy",
		Rules: []delegationpolicystore.Rule{{
			Target: delegationpolicystore.Target{MatchLabels: map[string]string{"access": "read"}},
			Allow:  true,
		}},
	}); err != nil {
		t.Fatalf("create rt policy: %v", err)
	}

	text := resultText(t, callAs(t, srv.Handler(), callerOnParent, "lenny/discover_agents", `{}`))
	if !strings.Contains(text, "reader-agent") {
		t.Errorf("reader-agent is allowed by the runtime policy and must appear: %q", text)
	}
	if strings.Contains(text, "writer-agent") {
		t.Errorf("writer-agent is denied by the runtime policy and must not appear: %q", text)
	}
}
