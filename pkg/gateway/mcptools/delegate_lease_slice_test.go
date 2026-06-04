// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/executor"
	"github.com/lennylabs/lenny/pkg/gateway/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// newDelegateMCPWithParentLease builds a delegate_task MCP server whose
// `sess_parent` carries the granted §8.2 lease_slice `granted`, so a
// child lease_slice can be validated against a real parent ceiling.
// spec: §8.2 lines 38-48. F-8.2.2.
func newDelegateMCPWithParentLease(t *testing.T, granted *sessionstore.DelegationLease) (*mcp.Server, sessionstore.Store) {
	t.Helper()
	store := memstore.New()
	runtimes := runtimestore.NewMemory()
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:    store,
		Executor: executor.NewEchoExecutor(),
		Runtimes: runtimes,
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
		ID: "sess_parent", TenantID: "acme", UserID: "user_alice",
		State:      session.StateRunning,
		RuntimeRef: "child-agent", PoolRef: "pool-a", IsolationProfile: isolation.ProfileSandboxed,
		DelegationLease: granted,
		CreatedAt:       now, UpdatedAt: now,
	})
	return srv, store
}

// spec: §8.2 lines 38-48, 127 — a `leaseSlice` exceeding the parent's
// granted budget is rejected with the canonical BUDGET_EXHAUSTED code,
// and no child session is committed.
func TestDelegateTaskLeaseSliceOverBudget_spec_8_2(t *testing.T) {
	srv, store := newDelegateMCPWithParentLease(t, &sessionstore.DelegationLease{
		MaxTokenBudget: 1000, MaxChildrenTotal: 10,
	})

	resp := call(t, srv.Handler(), "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","target":"child-agent","poolRef":"pool-b","leaseSlice":{"maxTokenBudget":5000}}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("over-budget leaseSlice must be a tool error: %+v", resp)
	}
	env := readLennyErrorEnvelope(t, result)
	if env["code"] != "BUDGET_EXHAUSTED" {
		t.Errorf("code = %v, want BUDGET_EXHAUSTED", env["code"])
	}
	if _, err := store.Get(context.Background(), "acme", "sess_child"); err == nil {
		t.Error("over-budget delegation must not commit a child session")
	}
}

// spec: §8.2 lines 38-48 — a `leaseSlice` within the parent's granted
// budget is admitted and the resolved slice is stamped on the child.
func TestDelegateTaskLeaseSliceWithinBudget_spec_8_2(t *testing.T) {
	srv, store := newDelegateMCPWithParentLease(t, &sessionstore.DelegationLease{
		MaxTokenBudget: 1000, MaxChildrenTotal: 10,
	})

	resp := call(t, srv.Handler(), "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","target":"child-agent","poolRef":"pool-b","leaseSlice":{"maxTokenBudget":400,"maxChildrenTotal":5}}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] == true {
		t.Fatalf("within-budget leaseSlice must be admitted: %+v", resp)
	}
	child, err := store.Get(context.Background(), "acme", "sess_child")
	if err != nil {
		t.Fatalf("within-budget delegation must commit a child: %v", err)
	}
	if child.DelegationLease == nil || child.DelegationLease.MaxTokenBudget != 400 {
		t.Fatalf("child lease = %+v, want MaxTokenBudget 400", child.DelegationLease)
	}
}
