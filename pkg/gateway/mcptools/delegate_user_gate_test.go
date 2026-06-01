// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/executor"
	"github.com/lennylabs/lenny/pkg/gateway/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/userstore"
	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// newDelegateMCPWithUsers builds a delegation-capable MCP server whose
// §11.4 user gate is backed by the supplied user store. The parent
// session `sess_parent` is owned by `user_alice`.
func newDelegateMCPWithUsers(t *testing.T, users userstore.Store) (*mcp.Server, sessionstore.Store) {
	t.Helper()
	store := memstore.New()
	runtimes := runtimestore.NewMemory()
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:    store,
		Executor: executor.NewEchoExecutor(),
		Runtimes: runtimes,
		Users:    users,
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
		CreatedAt:  now, UpdatedAt: now,
	})
	return srv, store
}

// spec: §11.4 — hard_disable "also block[s] new delegated tasks". A
// running parent whose owning user is disabled after start cannot spawn
// a child through lenny/delegate_task. F-11.4.1.
func TestDelegateTaskRejectsDisabledOwner_spec_11_4(t *testing.T) {
	users := userstore.NewMemory()
	if err := users.Create(context.Background(), userstore.User{
		Subject: "user_alice", TenantID: "acme", Disabled: true,
	}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	srv, store := newDelegateMCPWithUsers(t, users)

	resp := call(t, srv.Handler(), "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","runtimeRef":"child-agent","poolRef":"pool-b","taskInput":"do work"}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("a disabled owner must block delegation: %+v", resp)
	}
	env := readLennyErrorEnvelope(t, result)
	if env["code"] != "USER_INVALIDATED" {
		t.Errorf("code = %v, want USER_INVALIDATED", env["code"])
	}
	if _, err := store.Get(context.Background(), "acme", "sess_child"); err == nil {
		t.Error("a blocked delegation must not commit a child session")
	}
}

// spec: §11.4 — a fully-revoked owner (deleted_at tombstone) likewise
// cannot spawn new delegated tasks. F-11.4.1.
func TestDelegateTaskRejectsRevokedOwner_spec_11_4(t *testing.T) {
	users := userstore.NewMemory()
	ctx := context.Background()
	if err := users.Create(ctx, userstore.User{Subject: "user_alice", TenantID: "acme"}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := users.SoftDelete(ctx, "acme", "user_alice", time.Now()); err != nil {
		t.Fatalf("soft-delete: %v", err)
	}
	srv, store := newDelegateMCPWithUsers(t, users)

	resp := call(t, srv.Handler(), "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","runtimeRef":"child-agent","poolRef":"pool-b","taskInput":"do work"}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("a revoked owner must block delegation: %+v", resp)
	}
	if _, err := store.Get(ctx, "acme", "sess_child"); err == nil {
		t.Error("a blocked delegation must not commit a child session")
	}
}

// spec: §11.4 — an active owner spawns children normally. F-11.4.1.
func TestDelegateTaskAdmitsActiveOwner_spec_11_4(t *testing.T) {
	users := userstore.NewMemory()
	if err := users.Create(context.Background(), userstore.User{
		Subject: "user_alice", TenantID: "acme",
	}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	srv, store := newDelegateMCPWithUsers(t, users)

	resp := call(t, srv.Handler(), "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","runtimeRef":"child-agent","poolRef":"pool-b","taskInput":"do work"}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] == true {
		t.Fatalf("an active owner must admit delegation: %+v", resp)
	}
	if _, err := store.Get(context.Background(), "acme", "sess_child"); err != nil {
		t.Fatalf("active owner delegation must commit a child session: %v", err)
	}
}

// spec: §11.4 — an owner with no registry row is admitted (the gate
// governs invalidation of known users, not registry membership), the
// same carve-out the REST session-creation gate applies. F-11.4.1.
func TestDelegateTaskAdmitsUnregisteredOwner_spec_11_4(t *testing.T) {
	srv, store := newDelegateMCPWithUsers(t, userstore.NewMemory())

	resp := call(t, srv.Handler(), "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","runtimeRef":"child-agent","poolRef":"pool-b","taskInput":"do work"}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] == true {
		t.Fatalf("an unregistered owner must be admitted: %+v", resp)
	}
	if _, err := store.Get(context.Background(), "acme", "sess_child"); err != nil {
		t.Fatalf("unregistered owner delegation must commit a child session: %v", err)
	}
}
