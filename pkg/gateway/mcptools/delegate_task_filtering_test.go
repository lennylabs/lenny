// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/environment"
	"github.com/lennylabs/lenny/pkg/gateway/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/environmentstore"
	"github.com/lennylabs/lenny/pkg/gateway/executor"
	"github.com/lennylabs/lenny/pkg/gateway/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcptools"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
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
		environment.Selector{MatchLabels: map[string]string{"team": "security"}}))
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	_ = store.Create(ctxbg, sessionstore.Session{
		ID: "sess_parent", TenantID: "acme", State: session.StateRunning,
		RuntimeRef: "sec-agent", PoolRef: "pool-a", IsolationProfile: isolation.ProfileSandboxed,
		CreatedAt: now, UpdatedAt: now,
	})

	// The caller is a member of security-team via the security-engineers
	// group; the environment's runtimeSelector admits team=security only.
	caller := authmw.Principal{Subject: "alice", TenantID: "acme", Groups: []string{"security-engineers"}}

	// research-agent is outside the caller's environment scope — reject.
	resp := callAs(t, srv.Handler(), caller, "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","runtimeRef":"research-agent","poolRef":"pool-b"}`)
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
		`{"parentSessionId":"sess_parent","runtimeRef":"sec-agent","poolRef":"pool-b"}`))
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
	// team-a declares outbound delegation to team-b; team-b admits the
	// shared tool and accepts inbound delegation from team-a.
	_ = envs.Create(ctxbg, environmentstore.Environment{
		Name: "team-a", TenantID: "acme",
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
		ID: "sess_parent", TenantID: "acme", State: session.StateRunning,
		RuntimeRef: "base-agent", PoolRef: "pool-a", IsolationProfile: isolation.ProfileSandboxed,
		Environment: "team-a", CreatedAt: now, UpdatedAt: now,
	})

	// The caller is a member of no environment, so the within-environment
	// check denies the shared tool; the bilateral team-a <-> team-b
	// declaration makes it reachable cross-environment.
	caller := authmw.Principal{Subject: "alice", TenantID: "acme"}
	text := resultText(t, callAs(t, srv.Handler(), caller, "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","runtimeRef":"shared-tool","poolRef":"pool-b"}`))
	if !strings.Contains(text, "sess_child") {
		t.Errorf("a cross-environment-reachable delegation should proceed: %q", text)
	}
}

func TestDelegateTaskPoolIsolationMonotonicity(t *testing.T) {
	store := memstore.New()
	pools := poolstore.NewMemory()
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:    store,
		Executor: executor.NewEchoExecutor(),
		Pools:    pools,
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
		ID: "sess_parent", TenantID: "acme", State: session.StateRunning,
		RuntimeRef: "base-agent", PoolRef: "pool-a", IsolationProfile: isolation.ProfileSandboxed,
		CreatedAt: now, UpdatedAt: now,
	})

	// §8.3 / §10.6: delegating to a pool whose §5.3 isolation profile is
	// weaker than the parent session's is a monotonicity violation. The
	// resolved child pool profile, not the inherited parent profile, is
	// what the delegation service evaluates.
	resp := call(t, srv.Handler(), "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","runtimeRef":"child-agent","poolRef":"weak-pool"}`)
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

	// A pool at least as restrictive as the parent is admitted.
	text := resultText(t, call(t, srv.Handler(), "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","runtimeRef":"child-agent","poolRef":"strong-pool"}`))
	if !strings.Contains(text, "sess_child") {
		t.Errorf("delegation to a stronger-isolation pool should proceed: %q", text)
	}
}
