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
// terminal-side-effects pipeline calls the BudgetForget hook with the
// session id so the LLM-proxy enforcer's per-session map does not grow
// without bound.
func TestTerminalPipelineForgetsBudgetAccounting_spec_11_2(t *testing.T) {
	ctx := context.Background()
	store := memstore.New()
	var forgotten []string
	srv := sessionserver.New(store, sessionserver.Options{
		BudgetForget: func(sessionID string) { forgotten = append(forgotten, sessionID) },
	})
	row := sessionstore.Session{
		ID: "s_1", TenantID: "acme", State: session.StateCompleted,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := store.Create(ctx, row); err != nil {
		t.Fatalf("create: %v", err)
	}
	srv.OnSessionTerminal(ctx, session.StateRunning, row)

	if len(forgotten) != 1 || forgotten[0] != "s_1" {
		t.Fatalf("BudgetForget calls = %v, want [s_1]", forgotten)
	}
}

// A nil BudgetForget hook leaves the terminal pipeline unaffected.
func TestTerminalPipelineNilBudgetForgetIsSafe_spec_11_2(t *testing.T) {
	ctx := context.Background()
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{})
	row := sessionstore.Session{
		ID: "s_1", TenantID: "acme", State: session.StateCompleted,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := store.Create(ctx, row); err != nil {
		t.Fatalf("create: %v", err)
	}
	srv.OnSessionTerminal(ctx, session.StateRunning, row) // must not panic
}
