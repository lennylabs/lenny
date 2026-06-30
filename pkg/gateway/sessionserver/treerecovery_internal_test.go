// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// spec: §8.10 line 1027 — a node whose individual resume window elapsed
// transitions to `expired`; a node lost to a level/tree budget
// transitions to `failed`. The terminal marker maps the recovery reason
// onto those two terminal states.
func TestTerminalMarkerReasonMapping_spec_8_10_1027(t *testing.T) {
	cases := []struct {
		reason string
		want   session.State
	}{
		{"node resume window exceeded", session.StateExpired},
		{"level recovery deadline exceeded", session.StateFailed},
		{"tree recovery deadline exceeded", session.StateFailed},
		{"pool exhausted", session.StateFailed},
	}
	for _, tc := range cases {
		store := memstore.New()
		srv := New(store, Options{})
		ctx := context.Background()
		row := sessionstore.Session{ID: "n1", TenantID: "acme", State: session.StateRunning}
		if err := store.Create(ctx, row); err != nil {
			t.Fatalf("seed: %v", err)
		}
		sessionTerminalMarker{s: srv}.FailNode(ctx, row, tc.reason)
		got, err := store.Get(ctx, "acme", "n1")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.State != tc.want {
			t.Fatalf("reason %q → state %q, want %q", tc.reason, got.State, tc.want)
		}
	}
}

// nodeNeedsRecovery is a no-op (no node recoverable) when the pod
// registry is unwired, so the recovery degrades to a no-op on a gateway
// without pod binding (dev / unit-test).
func TestNodeNeedsRecoveryNilRegistry(t *testing.T) {
	srv := New(memstore.New(), Options{})
	if srv.podRegistry != nil {
		t.Skip("test assumes the bare server has no pod registry")
	}
	if srv.nodeNeedsRecovery(sessionstore.Session{ID: "n1", State: session.StateRunning}) {
		t.Fatal("nodeNeedsRecovery should be false without a pod registry")
	}
}

// The resume path's tree-recovery trigger is plumbed end to end: New
// builds the orchestrator, recoverDelegationTree spawns the detached
// traversal over the resumed tree, and the completion hook fires. With
// no pod registry every descendant is non-recoverable, so the pass is a
// safe no-op that leaves the running children untouched.
func TestRecoverDelegationTreePlumbing_spec_8_10_1016(t *testing.T) {
	store := memstore.New()
	srv := New(store, Options{})
	if srv.treeRecovery == nil {
		t.Fatal("New must build the tree-recovery orchestrator")
	}
	ctx := context.Background()
	rows := []sessionstore.Session{
		{ID: "root", TenantID: "acme", RootSessionID: "root", State: session.StateRunning},
		{ID: "child", TenantID: "acme", ParentSessionID: "root", RootSessionID: "root", DelegationDepth: 1, State: session.StateRunning},
	}
	for _, r := range rows {
		if err := store.Create(ctx, r); err != nil {
			t.Fatalf("seed %s: %v", r.ID, err)
		}
	}
	done := make(chan string, 1)
	srv.treeRecoveryHook = func(rootID string) { done <- rootID }

	srv.recoverDelegationTree(ctx, "acme", "root")

	select {
	case got := <-done:
		if got != "root" {
			t.Fatalf("hook fired for %q, want root", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("tree-recovery goroutine did not complete")
	}

	// No pod registry → the running child is left running, not
	// terminated.
	child, err := store.Get(ctx, "acme", "child")
	if err != nil {
		t.Fatalf("get child: %v", err)
	}
	if child.State != session.StateRunning {
		t.Fatalf("child state = %q, want running (untouched)", child.State)
	}
}

// A nil orchestrator and an empty root are no-ops that still fire the
// hook so the resume handler can call the trigger unconditionally.
func TestRecoverDelegationTreeEmptyRootNoOp(t *testing.T) {
	srv := New(memstore.New(), Options{})
	done := make(chan string, 1)
	srv.treeRecoveryHook = func(rootID string) { done <- rootID }
	srv.recoverDelegationTree(context.Background(), "acme", "")
	select {
	case <-done:
		t.Fatal("empty root must not spawn a recovery goroutine")
	case <-time.After(200 * time.Millisecond):
		// expected: no hook, no goroutine.
	}
}
