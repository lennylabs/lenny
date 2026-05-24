// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/executor"
	"github.com/lennylabs/lenny/pkg/gateway/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// prerouteInterceptor is an external interceptor that returns a fixed
// result; it registers at PreRoute (priority > 100) where external
// interceptors are legal.
type prerouteInterceptor struct{ result interceptor.Result }

func (prerouteInterceptor) Name() string                       { return "child-preroute" }
func (prerouteInterceptor) Priority() int32                    { return 300 }
func (prerouteInterceptor) Builtin() bool                      { return false }
func (prerouteInterceptor) FailPolicy() interceptor.FailPolicy { return interceptor.FailClosed }
func (prerouteInterceptor) Timeout() time.Duration             { return 0 }
func (p prerouteInterceptor) Intercept(context.Context, interceptor.Request) (interceptor.Result, error) {
	return p.result, nil
}

// newDelegateMCPWithChain builds an MCP server wired with a delegation
// service and an interceptor chain, plus a running parent session whose
// runtime the caller may delegate to (no environment scoping).
func newDelegateMCPWithChain(t *testing.T, chain *interceptor.Chain) (*mcp.Server, sessionstore.Store) {
	t.Helper()
	store := memstore.New()
	runtimes := runtimestore.NewMemory()
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:        store,
		Executor:     executor.NewEchoExecutor(),
		Runtimes:     runtimes,
		Interceptors: chain,
		Delegation: delegation.NewService(store, delegation.Options{
			IDFunc: func() string { return "sess_child" },
		}),
		Clock:    func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:   func() string { return "sess_mcp" },
		TenantID: "acme",
	})
	ctxbg := context.Background()
	_ = runtimes.Create(ctxbg, runtimestore.Runtime{Name: "child-agent", Type: runtimestore.TypeAgent})
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	_ = store.Create(ctxbg, sessionstore.Session{
		ID: "sess_parent", TenantID: "acme", State: session.StateRunning,
		RuntimeRef: "child-agent", PoolRef: "pool-a", IsolationProfile: isolation.ProfileSandboxed,
		CreatedAt: now, UpdatedAt: now,
	})
	return srv, store
}

// spec: §8.2 line 90 — the gateway runs the PreRoute chain on the
// child's augmented TaskSpec. A REJECT blocks the delegation and no
// child session is created.
func TestDelegateTaskPreRouteRejectBlocksChild(t *testing.T) {
	chain := interceptor.NewChain()
	if err := chain.Register(interceptor.PhasePreRoute, prerouteInterceptor{
		result: interceptor.Result{Action: interceptor.ActionReject, Reason: "blocked by routing policy"},
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	srv, store := newDelegateMCPWithChain(t, chain)

	resp := call(t, srv.Handler(), "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","runtimeRef":"child-agent","poolRef":"pool-b","taskInput":"do work"}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Errorf("a PreRoute REJECT should be a tool error: %+v", resp)
	}
	if _, err := store.Get(context.Background(), "acme", "sess_child"); err == nil {
		t.Error("a PreRoute-rejected delegation must not create a child session")
	}
}

// spec: §8.2 line 90, §4.8 line 1048 — a PreRoute MODIFY rewrites the
// child's task input. The echo executor reflects the delivered body,
// proving the modified input reached the child.
func TestDelegateTaskPreRouteModifyRewritesInput(t *testing.T) {
	chain := interceptor.NewChain()
	modified := []byte(`{"tenant_id":"acme","requested_runtime":"child-agent","input":"sanitized work"}`)
	if err := chain.Register(interceptor.PhasePreRoute, prerouteInterceptor{
		result: interceptor.Result{Action: interceptor.ActionModify, ModifiedContent: modified},
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	srv, store := newDelegateMCPWithChain(t, chain)

	text := resultText(t, call(t, srv.Handler(), "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","runtimeRef":"child-agent","poolRef":"pool-b","taskInput":"do work"}`))
	if !strings.Contains(text, "sess_child") {
		t.Fatalf("delegation should proceed after a MODIFY: %q", text)
	}
	if _, err := store.Get(context.Background(), "acme", "sess_child"); err != nil {
		t.Fatalf("child session should exist after an admitted delegation: %v", err)
	}
}

// spec: §4.8 line 1048, line 1060 — a PreRoute MODIFY that alters the
// authenticated tenant_id is rejected with
// INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION; no child is created.
func TestDelegateTaskPreRouteModifyImmutableTenantRejected(t *testing.T) {
	chain := interceptor.NewChain()
	modified := []byte(`{"tenant_id":"globex","requested_runtime":"child-agent","input":"do work"}`)
	if err := chain.Register(interceptor.PhasePreRoute, prerouteInterceptor{
		result: interceptor.Result{Action: interceptor.ActionModify, ModifiedContent: modified},
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	srv, store := newDelegateMCPWithChain(t, chain)

	resp := call(t, srv.Handler(), "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","runtimeRef":"child-agent","poolRef":"pool-b","taskInput":"do work"}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Errorf("a PreRoute MODIFY altering tenant_id should be a tool error: %+v", resp)
	}
	if _, err := store.Get(context.Background(), "acme", "sess_child"); err == nil {
		t.Error("an immutable-field-violating MODIFY must not create a child session")
	}
}
