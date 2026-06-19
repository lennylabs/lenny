// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/sessionbudget"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
)

// TestBudgetTerminatorExpiresRunningSession_spec_7_1 proves F-11.2.21: an
// over-budget session is force-transitioned to the §7.1 `expired`
// terminal with the §8.8 expired:budget reason and the terminal pipeline
// runs.
func TestBudgetTerminatorExpiresRunningSession_spec_7_1(t *testing.T) {
	ctx := context.Background()
	store := memstore.New()
	if err := store.Create(ctx, sessionstore.Session{
		ID: "s_1", TenantID: "acme", State: session.StateRunning,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	var terminalSess sessionstore.Session
	var terminalFromState session.State
	term := &budgetSessionTerminator{
		store: store,
		onTerminal: func(_ context.Context, fromState session.State, s sessionstore.Session) {
			terminalSess = s
			terminalFromState = fromState
		},
	}
	term.terminate(ctx, "s_1", sessionbudget.ReasonBudgetExhausted)

	got, err := store.GetByID(ctx, "s_1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.State != session.StateExpired {
		t.Fatalf("state = %q, want expired", got.State)
	}
	if got.FailureReason != sessionbudget.ReasonBudgetExhausted {
		t.Fatalf("failure reason = %q, want %q", got.FailureReason, sessionbudget.ReasonBudgetExhausted)
	}
	if terminalSess.ID != "s_1" {
		t.Fatalf("onTerminal not invoked with the expired session, got %q", terminalSess.ID)
	}
	// spec: §4.6 — a budget terminate runs against a running session, so the
	// pre-terminal state forwarded is running; the terminal pod-release path
	// must keep it on the §6.2 executor recycle path, not the by-name reclaim.
	if terminalFromState != session.StateRunning {
		t.Fatalf("onTerminal fromState = %q, want running", terminalFromState)
	}
}

// An already-terminal session is a no-op: no state change, no terminal
// pipeline re-run (idempotent against a concurrent natural transition).
func TestBudgetTerminatorIdempotentOnTerminal_spec_11_2(t *testing.T) {
	ctx := context.Background()
	store := memstore.New()
	if err := store.Create(ctx, sessionstore.Session{
		ID: "s_1", TenantID: "acme", State: session.StateCompleted,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	onTerminalCalls := 0
	term := &budgetSessionTerminator{
		store:      store,
		onTerminal: func(context.Context, session.State, sessionstore.Session) { onTerminalCalls++ },
	}
	term.terminate(ctx, "s_1", sessionbudget.ReasonBudgetExhausted)

	got, _ := store.GetByID(ctx, "s_1")
	if got.State != session.StateCompleted {
		t.Fatalf("a terminal session must not be re-transitioned, got %q", got.State)
	}
	if onTerminalCalls != 0 {
		t.Fatalf("onTerminal must not fire for an already-terminal session, got %d", onTerminalCalls)
	}
}

// An unknown session id is a no-op (best-effort): no panic, no terminal
// pipeline.
func TestBudgetTerminatorUnknownSession_spec_11_2(t *testing.T) {
	ctx := context.Background()
	called := false
	term := &budgetSessionTerminator{
		store:      memstore.New(),
		onTerminal: func(context.Context, session.State, sessionstore.Session) { called = true },
	}
	term.terminate(ctx, "missing", sessionbudget.ReasonBudgetExhausted)
	if called {
		t.Fatalf("onTerminal must not fire for an unknown session")
	}
}
