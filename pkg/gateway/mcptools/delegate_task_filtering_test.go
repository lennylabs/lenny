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
	if result, _ := resp["result"].(map[string]any); result["isError"] != true {
		t.Errorf("delegation to an out-of-scope runtime must be a tool error: %+v", resp)
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
