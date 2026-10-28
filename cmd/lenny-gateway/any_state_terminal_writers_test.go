// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionbudget"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/session/state"
)

// seedInState creates one row of tenant acme in the given state.
func seedInState(t *testing.T, store sessionstore.Store, id string, st session.State) {
	t.Helper()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: id, TenantID: "acme", UserID: "alice@acme.com",
		State: st, PodAssignment: "pod-" + id,
	}); err != nil {
		t.Fatalf("seed %s in %s: %v", id, st, err)
	}
}

// assertAnyStateEdge fails the test when the written edge is not one the
// session transition table admits.
func assertAnyStateEdge(t *testing.T, from, to session.State) {
	t.Helper()
	if err := state.IsValid(state.State(from), state.State(to)); err != nil {
		t.Errorf("IsValid(%q, %q) = %v: the writer wrote an edge the transition table does not admit", from, to, err)
	}
}

// TestForceTerminateFromRecoveryAndPreRunningStatesWritesFailed_spec_7_2
// drives the operator force-terminate writer from each non-terminal state
// the §7.2 per-state block lists no → failed edge for. Each write lands
// failed with FORCE_TERMINATED, forwards the pre-force state to the terminal
// pipeline, and takes an edge the transition table admits under the
// any-state terminal-write paragraph.
//
// spec: §7.2 (Terminal writes from any non-terminal state); §24.11
func TestForceTerminateFromRecoveryAndPreRunningStatesWritesFailed_spec_7_2(t *testing.T) {
	for _, from := range []session.State{
		session.StateResumePending,
		session.StateAwaitingClientAction,
		session.StateResuming,
		session.StateFinalizing,
		session.StateReady,
		session.StateStarting,
		session.StateSuspended,
		session.StateInputRequired,
	} {
		from := from
		t.Run(string(from), func(t *testing.T) {
			store := memstore.New()
			seedInState(t, store, "sess_"+string(from), from)
			var hookFrom []session.State
			adapter := sessionAdminAdapter{
				store: store,
				onTerminal: func(_ context.Context, fs session.State, _ sessionstore.Session) {
					hookFrom = append(hookFrom, fs)
				},
			}
			got, prev, transitioned, err := adapter.ForceTerminate(context.Background(), "acme", "sess_"+string(from))
			if err != nil {
				t.Fatalf("ForceTerminate: %v", err)
			}
			if !transitioned || prev != from {
				t.Fatalf("transitioned=%v prev=%q, want true and %q", transitioned, prev, from)
			}
			if got.State != session.StateFailed || got.FailureReason != "FORCE_TERMINATED" {
				t.Fatalf("row = %q/%q, want failed/FORCE_TERMINATED", got.State, got.FailureReason)
			}
			if len(hookFrom) != 1 || hookFrom[0] != from {
				t.Fatalf("terminal hook fromState = %v, want [%s]", hookFrom, from)
			}
			assertAnyStateEdge(t, prev, got.State)
		})
	}
}

// TestBudgetTerminatorFromRecoveryAndPausedStatesWritesExpired_spec_7_2
// drives the budget terminator, which also carries credential-lease expiry
// and fallback exhaustion, from non-running states. Each write lands expired
// with the caller's reason, runs the terminal pipeline once with the
// pre-terminal state, and takes an edge the transition table admits under
// the any-state terminal-write paragraph.
//
// spec: §7.2 (Terminal writes from any non-terminal state); §11.2
func TestBudgetTerminatorFromRecoveryAndPausedStatesWritesExpired_spec_7_2(t *testing.T) {
	for _, from := range []session.State{
		session.StateResuming,
		session.StateResumePending,
		session.StateSuspended,
		session.StateAwaitingClientAction,
		session.StateInputRequired,
	} {
		from := from
		t.Run(string(from), func(t *testing.T) {
			ctx := context.Background()
			store := memstore.New()
			id := "s_" + string(from)
			seedInState(t, store, id, from)
			var hookFrom []session.State
			term := &budgetSessionTerminator{
				store: store,
				onTerminal: func(_ context.Context, fs session.State, _ sessionstore.Session) {
					hookFrom = append(hookFrom, fs)
				},
			}
			term.terminate(ctx, id, sessionbudget.ReasonBudgetExhausted)
			got, err := store.GetByID(ctx, id)
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			if got.State != session.StateExpired || got.FailureReason != sessionbudget.ReasonBudgetExhausted {
				t.Fatalf("row = %q/%q, want expired/%s", got.State, got.FailureReason, sessionbudget.ReasonBudgetExhausted)
			}
			if len(hookFrom) != 1 || hookFrom[0] != from {
				t.Fatalf("terminal hook fromState = %v, want [%s]", hookFrom, from)
			}
			assertAnyStateEdge(t, from, got.State)
		})
	}
}
