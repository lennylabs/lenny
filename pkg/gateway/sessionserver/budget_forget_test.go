// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// TestTerminalPipelineForgetsBudgetAccounting_spec_11_2 proves the §11.2
// mid-session budget accounting is reclaimed when a session settles: the
// terminal-side-effects pipeline calls the metering budgets' ForgetBudget with the
// session id so the LLM-proxy enforcer's per-session map does not grow
// without bound.
func TestTerminalPipelineForgetsBudgetAccounting_spec_11_2(t *testing.T) {
	ctx := context.Background()
	store := memstore.New()
	var forgotten []string
	srv := sessionserver.New(store, sessionserver.Options{
		DevTenantHeader: true,
		MeteringBudgets: forgetBudgets(func(sessionID string) { forgotten = append(forgotten, sessionID) }),
	})
	row := sessionstore.Session{
		UserID: testOwner,
		ID:     "s_1", TenantID: "acme", State: session.StateCompleted,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := store.Create(ctx, row); err != nil {
		t.Fatalf("create: %v", err)
	}
	srv.OnSessionTerminal(ctx, session.StateRunning, row)
	if err := srv.WaitTerminalWork(context.Background()); err != nil {
		t.Fatalf("WaitTerminalWork: %v", err)
	}

	if len(forgotten) != 1 || forgotten[0] != "s_1" {
		t.Fatalf("ForgetBudget calls = %v, want [s_1]", forgotten)
	}
}

// Nil metering budgets leave the terminal pipeline unaffected.
func TestTerminalPipelineNilBudgetForgetIsSafe_spec_11_2(t *testing.T) {
	ctx := context.Background()
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{DevTenantHeader: true})
	row := sessionstore.Session{
		UserID: testOwner,
		ID:     "s_1", TenantID: "acme", State: session.StateCompleted,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := store.Create(ctx, row); err != nil {
		t.Fatalf("create: %v", err)
	}
	srv.OnSessionTerminal(ctx, session.StateRunning, row) // must not panic
	if err := srv.WaitTerminalWork(context.Background()); err != nil {
		t.Fatalf("WaitTerminalWork: %v", err)
	}
}

// forgetBudgets is a seam.MeteringBudgets whose ForgetBudget calls the
// function; the terminal pipeline calls no other operation.
type forgetBudgets func(sessionID string)

func (f forgetBudgets) ForgetBudget(sessionID string) { f(sessionID) }
func (forgetBudgets) TrackedBudgetSessions() []string { return nil }
func (forgetBudgets) RaiseBudget(string, int64)       {}
func (forgetBudgets) TerminateForBudget(string)       {}
