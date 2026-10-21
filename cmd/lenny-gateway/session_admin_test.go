// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// spec: §24.11 — the production SessionAdmin adapter resolves a
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

	sess, prev, transitioned, err := adapter.ForceTerminate(context.Background(), "acme", "sess_1")
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

	sess, prev, transitioned, err := adapter.ForceTerminate(context.Background(), "acme", "sess_done")
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
	_, _, _, err := adapter.ForceTerminate(context.Background(), "acme", "missing")
	if !errors.Is(err, sessionstore.ErrNotFound) {
		t.Fatalf("ForceTerminate on a missing session: err = %v, want ErrNotFound", err)
	}
}

// replicaLagStore is a session store whose global GetByID cannot see any
// row, as a read pool that has not yet replicated a freshly inserted row
// cannot. Update and the other methods address the primary (the embedded
// memstore).
type replicaLagStore struct{ *memstore.Store }

func (replicaLagStore) GetByID(context.Context, string) (sessionstore.Session, error) {
	return sessionstore.Session{}, sessionstore.ErrNotFound
}

// TestSessionAdminAdapter_ForceTerminate_ReadsNoReplica_spec_12_8 pins
// that the adapter performs no global lookup: a row the read pool has
// not seen is still terminated under the caller's tenant through the
// locked update, and the terminal pipeline runs. An adapter that resolves
// the row with GetByID first returns ErrNotFound here.
// spec: §12.8 (Lifecycle fence and drain: Drain), §24.11 (Session investigation).
func TestSessionAdminAdapter_ForceTerminate_ReadsNoReplica_spec_12_8(t *testing.T) {
	primary := memstore.New()
	seedRunning(t, primary, "sess_lag", "acme")
	var released []string
	var claimed []string
	ended := 0
	adapter := sessionAdminAdapter{
		store: replicaLagStore{primary},
		onTerminal: func(_ context.Context, _ session.State, s sessionstore.Session) {
			released = append(released, s.ID)
		},
		beginTerminal: func(id string) func() {
			claimed = append(claimed, id)
			return func() { ended++ }
		},
	}
	sess, prev, transitioned, err := adapter.ForceTerminate(context.Background(), "acme", "sess_lag")
	if err != nil {
		t.Fatalf("ForceTerminate: %v", err)
	}
	if !transitioned || prev != session.StateRunning {
		t.Fatalf("transitioned=%v prev=%q, want true and running", transitioned, prev)
	}
	if sess.State != session.StateFailed || sess.FailureClass != session.FailureClassRuntime ||
		sess.FailureReason != "FORCE_TERMINATED" {
		t.Errorf("row = %q/%q/%q, want failed/runtime_failure/FORCE_TERMINATED",
			sess.State, sess.FailureClass, sess.FailureReason)
	}
	if len(released) != 1 || released[0] != "sess_lag" {
		t.Errorf("terminal pipeline runs = %v, want [sess_lag]", released)
	}
	if len(claimed) != 1 || claimed[0] != "sess_lag" || ended != 1 {
		t.Errorf("terminal claim = %v ended %d times, want [sess_lag] ended once", claimed, ended)
	}
}

// TestSessionAdminAdapter_ForceTerminate_OtherTenantNotFound_spec_12_8
// pins the tenant scope: called with another tenant's id, the adapter
// returns ErrNotFound, leaves the row unchanged, and runs no terminal
// pipeline. spec: §12.8 (Lifecycle fence and drain: Drain), §24.11.
func TestSessionAdminAdapter_ForceTerminate_OtherTenantNotFound_spec_12_8(t *testing.T) {
	store := memstore.New()
	seedRunning(t, store, "sess_a", "acme")
	ran := false
	adapter := sessionAdminAdapter{
		store:      store,
		onTerminal: func(context.Context, session.State, sessionstore.Session) { ran = true },
	}
	_, _, transitioned, err := adapter.ForceTerminate(context.Background(), "globex", "sess_a")
	if !errors.Is(err, sessionstore.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if transitioned || ran {
		t.Errorf("transitioned=%v terminal pipeline ran=%v, want neither", transitioned, ran)
	}
	got, _ := store.GetByID(context.Background(), "sess_a")
	if got.State != session.StateRunning {
		t.Errorf("acme row state = %q, want running (unchanged)", got.State)
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
