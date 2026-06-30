// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/environment"
	"github.com/lennylabs/lenny/pkg/gateway/environmentstore"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// spec: §10.6 environment scoping of lenny/delegate_task targets.

func TestDelegateTaskEnvironmentScope(t *testing.T) {
	store := memstore.New()
	runtimes := runtimestore.NewMemory()
	envs := environmentstore.NewMemory()
	tenants := tenantstore.NewMemory()
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:        store,
		Executor:     executor.NewEchoExecutor(),
		Runtimes:     runtimes,
		Environments: envs,
		Tenants:      tenants,
		Delegation: delegation.NewService(store, delegation.Options{
			IDFunc: func() string { return "sess_child" },
		}),
		Clock:    func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:   func() string { return "sess_mcp" },
		TenantID: "acme",
	})

	ctxbg := context.Background()
	_ = runtimes.Create(ctxbg, runtimestore.Runtime{
		Name: "sec-agent", Type: runtimestore.TypeAgent,
		Labels: map[string]string{"team": "security"},
	})
	_ = runtimes.Create(ctxbg, runtimestore.Runtime{
		Name: "research-agent", Type: runtimestore.TypeAgent,
		Labels: map[string]string{"team": "research"},
	})
	_ = tenants.Create(ctxbg, tenantstore.Tenant{ID: "acme"})
	_ = envs.Create(ctxbg, securityEnv(
		environment.Selector{MatchLabels: map[string]string{"team": "security"}},
	))
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	_ = store.Create(ctxbg, sessionstore.Session{
		ID: "sess_parent", TenantID: "acme", UserID: "user_alice",
		State:      session.StateRunning,
		RuntimeRef: "sec-agent", PoolRef: "pool-a", IsolationProfile: isolation.ProfileSandboxed,
		CreatedAt: now, UpdatedAt: now,
	})

	// The caller is a member of security-team via the security-engineers
	// group; the environment's runtimeSelector admits team=security only.
	caller := authmw.Principal{Subject: "alice", TenantID: "acme", Groups: []string{"security-engineers"}}

	// research-agent is outside the caller's environment scope — reject.
	resp := callAs(t, srv.Handler(), caller, "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","target":"research-agent","poolRef":"pool-b"}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Errorf("delegation to an out-of-scope runtime must be a tool error: %+v", resp)
	}
	// §10.6: the rejection carries the target_not_in_scope reason.
	if content, _ := result["content"].([]any); len(content) > 0 {
		c0, _ := content[0].(map[string]any)
		if text, _ := c0["text"].(string); !strings.Contains(text, "target_not_in_scope") {
			t.Errorf("scope rejection should carry the §10.6 target_not_in_scope reason: %q", text)
		}
	}
	if _, err := store.Get(ctxbg, "acme", "sess_child"); err == nil {
		t.Error("a rejected delegation must not create a child session")
	}

	// sec-agent is within the caller's environment scope — the
	// delegation proceeds.
	text := resultText(t, callAs(t, srv.Handler(), caller, "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","target":"sec-agent","poolRef":"pool-b"}`))
	if !strings.Contains(text, "sess_child") {
		t.Errorf("delegation to an in-scope runtime should proceed: %q", text)
	}
}

func TestDelegateTaskCrossEnvironmentReachable(t *testing.T) {
	store := memstore.New()
	runtimes := runtimestore.NewMemory()
	envs := environmentstore.NewMemory()
	tenants := tenantstore.NewMemory()
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:        store,
		Executor:     executor.NewEchoExecutor(),
		Runtimes:     runtimes,
		Environments: envs,
		Tenants:      tenants,
		Delegation: delegation.NewService(store, delegation.Options{
			IDFunc: func() string { return "sess_child" },
		}),
		Clock:    func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:   func() string { return "sess_mcp" },
		TenantID: "acme",
	})

	ctxbg := context.Background()
	sharedSel := environment.Selector{MatchLabels: map[string]string{"shared": "true"}}
	_ = runtimes.Create(ctxbg, runtimestore.Runtime{
		Name: "shared-tool", Type: runtimestore.TypeAgent,
		Labels: map[string]string{"shared": "true"},
	})
	_ = tenants.Create(ctxbg, tenantstore.Tenant{ID: "acme"})
	// team-a admits only its own team=a runtimes and declares outbound
	// delegation to team-b; team-b admits the shared tool and accepts
	// inbound delegation from team-a.
	_ = envs.Create(ctxbg, environmentstore.Environment{
		Name: "team-a", TenantID: "acme",
		Members: []environmentstore.Member{
			{
				Identity: environmentstore.Identity{Type: "oidc-group", Value: "team-a-members"},
				Role:     environment.RoleCreator,
			},
		},
		RuntimeSelector: environment.Selector{MatchLabels: map[string]string{"team": "a"}},
		CrossEnvOutbound: []environmentstore.CrossEnvRule{
			{Environment: "team-b", Runtimes: sharedSel},
		},
	})
	_ = envs.Create(ctxbg, environmentstore.Environment{
		Name: "team-b", TenantID: "acme", RuntimeSelector: sharedSel,
		CrossEnvInbound: []environmentstore.CrossEnvRule{
			{Environment: "team-a", Runtimes: sharedSel},
		},
	})
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	_ = store.Create(ctxbg, sessionstore.Session{
		ID: "sess_parent", TenantID: "acme", UserID: "user_alice",
		State:      session.StateRunning,
		RuntimeRef: "base-agent", PoolRef: "pool-a", IsolationProfile: isolation.ProfileSandboxed,
		Environment: "team-a", CreatedAt: now, UpdatedAt: now,
	})

	// The caller is a genuine member of team-a (the parent session's
	// environment), but shared-tool is outside team-a's runtimeSelector,
	// so the within-environment check denies it; the bilateral
	// team-a <-> team-b declaration makes it reachable cross-environment.
	caller := authmw.Principal{Subject: "alice", TenantID: "acme", Groups: []string{"team-a-members"}}
	text := resultText(t, callAs(t, srv.Handler(), caller, "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","target":"shared-tool","poolRef":"pool-b"}`))
	if !strings.Contains(text, "sess_child") {
		t.Errorf("a cross-environment-reachable delegation should proceed: %q", text)
	}

	// §10.6 security: a caller who is not a member of team-a cannot
	// borrow its cross-environment reach by delegating from a session
	// tagged with that environment.
	outsider := authmw.Principal{Subject: "mallory", TenantID: "acme", Groups: []string{"outsiders"}}
	resp := callAs(t, srv.Handler(), outsider, "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","target":"shared-tool","poolRef":"pool-b"}`)
	if result, _ := resp["result"].(map[string]any); result["isError"] != true {
		t.Errorf("a non-member must not borrow the parent environment's cross-environment reach: %+v", resp)
	}
}

// spec: §8.2 line 50 — `lenny/delegate_task` rejects `type: mcp`
// targets with `target_not_an_agent`. The MCP shim is the primary
// rejection site; the delegation Service enforces it again as
// defence-in-depth. F-8.2.8 / F-8.5.4.
func TestDelegateTaskRejectsTypeMCPTarget_spec_8_2_F_8_2_8(t *testing.T) {
	store := memstore.New()
	runtimes := runtimestore.NewMemory()
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:    store,
		Runtimes: runtimes,
		Delegation: delegation.NewService(store, delegation.Options{
			IDFunc:   func() string { return "sess_child" },
			Runtimes: runtimes,
		}),
		Clock:    func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:   func() string { return "sess_mcp" },
		TenantID: "acme",
	})
	ctxbg := context.Background()
	_ = runtimes.Create(ctxbg, runtimestore.Runtime{
		Name: "fs-mcp", Type: runtimestore.TypeMCP, Image: "lenny/fs-mcp@sha256:abc",
	})
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	_ = store.Create(ctxbg, sessionstore.Session{
		ID: "sess_parent", TenantID: "acme", UserID: "user_alice",
		State:      session.StateRunning,
		RuntimeRef: "claude", PoolRef: "pool-a", IsolationProfile: isolation.ProfileSandboxed,
		CreatedAt: now, UpdatedAt: now,
	})
	resp := call(t, srv.Handler(), "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","target":"fs-mcp","poolRef":"pool-b"}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("delegation to a type:mcp runtime must be a tool error: %+v", resp)
	}
	content, _ := result["content"].([]any)
	c0, _ := content[0].(map[string]any)
	if text, _ := c0["text"].(string); !strings.Contains(text, "target_not_an_agent") {
		t.Errorf("rejection should carry target_not_an_agent reason: %q", text)
	}
	if _, gerr := store.Get(ctxbg, "acme", "sess_child"); gerr == nil {
		t.Error("a target_not_an_agent rejection must not create a child session")
	}
}

// auditedEvent is one event a fakeDelegationAuditor recorded.
type auditedEvent struct {
	eventType string
	detail    map[string]any
}

// fakeDelegationAuditor records the §11.7 delegation events emitted.
type fakeDelegationAuditor struct{ events []auditedEvent }

func (f *fakeDelegationAuditor) EmitDelegationEvent(_ context.Context, eventType string, detail map[string]any) {
	f.events = append(f.events, auditedEvent{eventType, detail})
}

func TestDelegateTaskPoolIsolationMonotonicity(t *testing.T) {
	store := memstore.New()
	pools := poolstore.NewMemory()
	audit := &fakeDelegationAuditor{}
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:    store,
		Executor: executor.NewEchoExecutor(),
		Pools:    pools,
		Audit:    audit,
		Delegation: delegation.NewService(store, delegation.Options{
			IDFunc: func() string { return "sess_child" },
		}),
		Clock:    func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:   func() string { return "sess_mcp" },
		TenantID: "acme",
	})

	ctxbg := context.Background()
	_ = pools.Create(ctxbg, poolstore.Pool{
		Name: "weak-pool", IsolationProfile: isolation.ProfileStandard,
		AllowStandardIsolation: true,
	})
	_ = pools.Create(ctxbg, poolstore.Pool{
		Name: "strong-pool", IsolationProfile: isolation.ProfileMicrovm,
	})
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	_ = store.Create(ctxbg, sessionstore.Session{
		ID: "sess_parent", TenantID: "acme", UserID: "user_alice",
		State:      session.StateRunning,
		RuntimeRef: "base-agent", PoolRef: "pool-a", IsolationProfile: isolation.ProfileSandboxed,
		CreatedAt: now, UpdatedAt: now,
	})

	// §8.3 / §10.6: delegating to a pool whose §5.3 isolation profile is
	// weaker than the parent session's is a monotonicity violation. The
	// resolved child pool profile, not the inherited parent profile, is
	// what the delegation service evaluates.
	resp := call(t, srv.Handler(), "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","target":"child-agent","poolRef":"weak-pool"}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Errorf("delegation to a weaker-isolation pool must be rejected: %+v", resp)
	}
	// §10.6: the rejection carries the ISOLATION_MONOTONICITY_VIOLATED reason.
	if content, _ := result["content"].([]any); len(content) > 0 {
		c0, _ := content[0].(map[string]any)
		if text, _ := c0["text"].(string); !strings.Contains(text, "ISOLATION_MONOTONICITY_VIOLATED") {
			t.Errorf("isolation rejection should carry the §10.6 reason: %q", text)
		}
	}
	if _, err := store.Get(ctxbg, "acme", "sess_child"); err == nil {
		t.Error("a monotonicity-violating delegation must not create a child session")
	}
	// §10.6: the violation emits a delegation.isolation_violation event.
	if len(audit.events) != 1 || audit.events[0].eventType != "delegation.isolation_violation" {
		t.Fatalf("isolation violation should emit one audit event: %+v", audit.events)
	}
	if audit.events[0].detail["cross_environment"] != false {
		t.Errorf("a same-environment violation should record cross_environment=false: %+v",
			audit.events[0].detail)
	}
	// spec: §11.7 lines 99-101 — the audit payload uses the snake_case
	// field names (parent_isolation / target_isolation /
	// matched_policy_rule) that SIEM consumers pivot on. F-8.5.18.
	detail := audit.events[0].detail
	if got, _ := detail["parent_isolation"].(string); got != string(isolation.ProfileSandboxed) {
		t.Errorf("parent_isolation = %v, want %s", detail["parent_isolation"], isolation.ProfileSandboxed)
	}
	if got, _ := detail["target_isolation"].(string); got != string(isolation.ProfileStandard) {
		t.Errorf("target_isolation = %v, want %s", detail["target_isolation"], isolation.ProfileStandard)
	}
	if _, ok := detail["matched_policy_rule"]; !ok {
		t.Errorf("matched_policy_rule must be present (empty until §8.3 DelegationPolicy enforcement lands): %+v", detail)
	}
	if _, legacy := detail["parentProfile"]; legacy {
		t.Errorf("legacy camelCase parentProfile must not appear: %+v", detail)
	}
	if _, legacy := detail["childProfile"]; legacy {
		t.Errorf("legacy camelCase childProfile must not appear: %+v", detail)
	}

	// A pool at least as restrictive as the parent is admitted, and a
	// successful delegation emits no isolation-violation event.
	text := resultText(t, call(t, srv.Handler(), "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","target":"child-agent","poolRef":"strong-pool"}`))
	if !strings.Contains(text, "sess_child") {
		t.Errorf("delegation to a stronger-isolation pool should proceed: %q", text)
	}
	if len(audit.events) != 1 {
		t.Errorf("a successful delegation must not emit an isolation-violation event: %+v", audit.events)
	}
}
