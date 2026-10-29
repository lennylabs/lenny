// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/treebudget"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// fixedReserver answers every Reserve with err.
type fixedReserver struct{ err error }

func (r fixedReserver) Reserve(context.Context, treebudget.Reservation) (treebudget.Totals, error) {
	return treebudget.Totals{}, r.err
}

func (fixedReserver) Return(context.Context, treebudget.Reservation) error { return nil }

// newBudgetErrorMCP builds a delegate_task server whose tree holds a
// delegated child and whose reserver answers every Reserve with reserveErr.
func newBudgetErrorMCP(t *testing.T, reserveErr error) *mcp.Server {
	t.Helper()
	store := memstore.New()
	runtimes := runtimestore.NewMemory()
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		DevTenantFallback: true,
		Store:             store,
		Executor:          executor.NewEchoExecutor(),
		Runtimes:          runtimes,
		Delegation: delegation.NewService(store, delegation.Options{
			IDFunc:             func() string { return "sess_child" },
			TreeBudgetReserver: fixedReserver{err: reserveErr},
		}),
		Clock:    func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:   func() string { return "sess_mcp" },
		TenantID: "acme",
	})
	ctx := context.Background()
	_ = runtimes.Create(ctx, runtimestore.Runtime{Name: "child-agent", Type: runtimestore.TypeAgent})
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, row := range []sessionstore.Session{
		{ID: "sess_parent", State: session.StateRunning},
		{ID: "sess_old", State: session.StateRunning, ParentSessionID: "sess_parent", RootSessionID: "sess_parent", DelegationDepth: 1},
	} {
		row.TenantID, row.UserID = "acme", "user_alice"
		row.RuntimeRef, row.PoolRef, row.IsolationProfile = "child-agent", "pool-a", isolation.ProfileSandboxed
		row.CreatedAt, row.UpdatedAt = now, now
		if err := store.Create(ctx, row); err != nil {
			t.Fatalf("seed %s: %v", row.ID, err)
		}
	}
	return srv
}

// spec: §8.3 (BUDGET_KEYS_EXPIRED detection and tree cleanup), §12.4, §15.2.1
// A reservation detection maps to the permanent INVALID_STATE_TRANSITION with
// retryable false, and a budget counter outage keeps the retryable
// DELEGATION_BUDGET_UNAVAILABLE, so the two budget errors stay distinct.
func TestDelegateTaskBudgetErrorMapping_spec_8_3(t *testing.T) {
	for _, tc := range []struct {
		name          string
		err           error
		wantCode      string
		wantRetryable bool
	}{
		{name: "keys expired", err: treebudget.ErrBudgetKeysExpired, wantCode: "INVALID_STATE_TRANSITION", wantRetryable: false},
		{name: "counters unavailable", err: treebudget.ErrBudgetUnavailable, wantCode: "DELEGATION_BUDGET_UNAVAILABLE", wantRetryable: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newBudgetErrorMCP(t, tc.err)
			resp := call(t, srv.Handler(), "lenny/delegate_task",
				`{"parentSessionId":"sess_parent","target":"child-agent","poolRef":"pool-b"}`)
			result, _ := resp["result"].(map[string]any)
			env := readLennyErrorEnvelope(t, result)
			if env["code"] != tc.wantCode {
				t.Fatalf("code = %v, want %s", env["code"], tc.wantCode)
			}
			if env["retryable"] != tc.wantRetryable {
				t.Fatalf("retryable = %v, want %v", env["retryable"], tc.wantRetryable)
			}
		})
	}
}
