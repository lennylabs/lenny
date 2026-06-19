// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
)

// spec: §24.11 line 136 — the production SessionAdmin adapter resolves a
// session by global id, forces a non-terminal session to failed, releases
// its pod via the terminal hook, and is idempotent on a terminal row.
// F-24.11.2.

func seedRunning(t *testing.T, store sessionstore.Store, id, tenant string) {
	t.Helper()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: id, TenantID: tenant, UserID: "alice@" + tenant,
		State: session.StateRunning, PodAssignment: "pod-" + id,
	}); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

func TestSessionAdminAdapter_ForceTerminate_TransitionsAndReleases(t *testing.T) {
	store := memstore.New()
	seedRunning(t, store, "sess_1", "acme")

	var released []string
	var releasedFromStates []session.State
	adapter := sessionAdminAdapter{
		store: store,
		onTerminal: func(_ context.Context, fromState session.State, s sessionstore.Session) {
			released = append(released, s.ID)
			releasedFromStates = append(releasedFromStates, fromState)
		},
	}

	sess, prev, transitioned, err := adapter.ForceTerminate(context.Background(), "sess_1")
	if err != nil {
		t.Fatalf("ForceTerminate: %v", err)
	}
	if !transitioned {
		t.Fatal("transitioned = false, want true for a running session")
	}
	if prev != session.StateRunning {
		t.Errorf("previous state = %q, want running", prev)
	}
	if sess.State != session.StateFailed {
		t.Errorf("state = %q, want failed", sess.State)
	}
	if sess.FailureReason != "FORCE_TERMINATED" {
		t.Errorf("failure reason = %q, want FORCE_TERMINATED", sess.FailureReason)
	}
	if sess.FailureClass != session.FailureClassRuntime {
		t.Errorf("failure class = %q, want runtime_failure", sess.FailureClass)
	}
	if len(released) != 1 || released[0] != "sess_1" {
		t.Errorf("pod-release hook not invoked exactly once: %v", released)
	}
	// spec: §4.6 — the force-terminate forwards the pre-force state (running)
	// so the terminal pod-release path routes the teardown through the §6.2
	// executor recycle path rather than the pre-running by-name reclaim.
	if len(releasedFromStates) != 1 || releasedFromStates[0] != session.StateRunning {
		t.Errorf("terminal hook fromState = %v, want [running]", releasedFromStates)
	}

	// The store reflects the forced terminal state.
	got, _ := store.GetByID(context.Background(), "sess_1")
	if got.State != session.StateFailed {
		t.Errorf("persisted state = %q, want failed", got.State)
	}
}

func TestSessionAdminAdapter_ForceTerminate_IdempotentOnTerminal(t *testing.T) {
	store := memstore.New()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_done", TenantID: "acme", State: session.StateCompleted,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	var released []string
	adapter := sessionAdminAdapter{
		store: store,
		onTerminal: func(_ context.Context, _ session.State, s sessionstore.Session) {
			released = append(released, s.ID)
		},
	}

	sess, prev, transitioned, err := adapter.ForceTerminate(context.Background(), "sess_done")
	if err != nil {
		t.Fatalf("ForceTerminate: %v", err)
	}
	if transitioned {
		t.Error("transitioned = true, want false for an already-terminal session")
	}
	if prev != session.StateCompleted {
		t.Errorf("previous state = %q, want completed", prev)
	}
	if sess.State != session.StateCompleted {
		t.Errorf("state = %q, want unchanged completed", sess.State)
	}
	if len(released) != 0 {
		t.Errorf("the terminal hook must not run on an idempotent no-op: %v", released)
	}
}

func TestSessionAdminAdapter_ForceTerminate_NotFound(t *testing.T) {
	adapter := sessionAdminAdapter{store: memstore.New()}
	_, _, _, err := adapter.ForceTerminate(context.Background(), "missing")
	if err == nil {
		t.Fatal("ForceTerminate on a missing session: want error, got nil")
	}
}

func TestSessionAdminAdapter_GetByID(t *testing.T) {
	store := memstore.New()
	seedRunning(t, store, "sess_1", "acme")
	adapter := sessionAdminAdapter{store: store}
	got, err := adapter.GetByID(context.Background(), "sess_1")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.TenantID != "acme" {
		t.Errorf("tenant = %q, want acme", got.TenantID)
	}
}
