// SPDX-License-Identifier: MIT

//go:build contract

package rest_mcp_consistency_test

import (
	"context"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/errorclassify"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/treebudget"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

const (
	budgetRootID  = "sess_budget_root"
	budgetChildID = "sess_budget_child"
)

// countingCleanup forwards each BUDGET_KEYS_EXPIRED cleanup to the session
// server and counts the calls.
type countingCleanup struct {
	inner *sessionserver.Server
	mu    sync.Mutex
	calls int
}

func (c *countingCleanup) HandleBudgetKeysExpired(ctx context.Context, tenantID, rootID string) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	c.inner.HandleBudgetKeysExpired(ctx, tenantID, rootID)
}

// newBudgetDelegateMCP stands up the platform MCP server with the
// delegation service over a real treebudget.Reserver on miniredis, and a tree
// whose root already holds a delegated child but whose budget keys are gone.
func newBudgetDelegateMCP(t *testing.T) (string, *miniredis.Miniredis, *countingCleanup) {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	store := memstore.New()
	runtimes := runtimestore.NewMemory()
	if err := runtimes.Create(ctx, runtimestore.Runtime{Name: "worker", Type: runtimestore.TypeAgent}); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	for _, row := range []sessionstore.Session{
		{ID: budgetRootID, RootSessionID: budgetRootID},
		{ID: budgetChildID, ParentSessionID: budgetRootID, RootSessionID: budgetRootID, DelegationDepth: 1},
	} {
		row.TenantID, row.UserID, row.State = "acme", "alice@acme.com", session.StateRunning
		row.RuntimeRef, row.PoolRef, row.IsolationProfile = "echo", "pool-a", isolation.ProfileSandboxed
		row.CreatedAt, row.UpdatedAt = now, now
		if err := store.Create(ctx, row); err != nil {
			t.Fatalf("seed %s: %v", row.ID, err)
		}
	}
	mr := miniredis.RunT(t)
	cl := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = cl.Close() })
	exec := executor.NewEchoExecutor()
	srv := sessionserver.New(store, sessionserver.Options{DevTenantHeader: true, Executor: exec})
	t.Cleanup(srv.Close)
	cleanup := &countingCleanup{inner: srv}
	mcpSrv := mcp.NewServer()
	mcptools.Register(mcpSrv, mcptools.Deps{
		DevTenantFallback: true,
		Store:             store, SessionService: srv, Executor: exec, Runtimes: runtimes,
		Delegation: delegation.NewService(store, delegation.Options{
			Clock:              func() time.Time { return now },
			IDFunc:             func() string { return "sess_budget_new" },
			TreeBudgetReserver: treebudget.New(cl, 0),
			BudgetKeysExpired:  cleanup,
		}),
		Clock: func() time.Time { return now }, TenantID: "acme",
	})
	m := httptest.NewServer(mcpSrv.Handler())
	t.Cleanup(m.Close)
	return m.URL + "/mcp", mr, cleanup
}

// wantTriple is the (code, category, retryable) the shared classifier
// assigns code.
func wantTriple(code string) restErrorTriple {
	cat, retryable := errorclassify.Classify(code)
	return restErrorTriple{Code: code, Category: string(cat), Retryable: retryable}
}

// spec: §8.3 (BUDGET_KEYS_EXPIRED detection and tree cleanup), §15.2.1
// (REST/MCP Consistency Contract), §12.4
// diagnosis: lenny/delegate_task on a tree whose budget keys expired did not
// return the permanent INVALID_STATE_TRANSITION with retryable false and the
// classifier's category, its triple differs from the REST
// INVALID_STATE_TRANSITION triple, or the reservation detection ran no
// cleanup. The DELEGATION_BUDGET_UNAVAILABLE case pins that a counter outage
// keeps the retryable code, so the two budget errors stay distinct on the
// wire.
func TestDelegateTaskBudgetErrorsOnTheWire_spec_8_3(t *testing.T) {
	args := map[string]any{"parentSessionId": budgetRootID, "target": "worker", "poolRef": "pool-b"}

	t.Run("keys expired", func(t *testing.T) {
		url, mr, cleanup := newBudgetDelegateMCP(t)
		got := decodeMCPError(t, mcpCall(t, url, "lenny/delegate_task", args))
		want := wantTriple("INVALID_STATE_TRANSITION")
		if got != want || got.Retryable {
			t.Fatalf("MCP triple = %+v, want %+v with retryable false", got, want)
		}
		if keys := mr.Keys(); len(keys) != 0 {
			t.Fatalf("a detection wrote budget keys %v", keys)
		}
		cleanup.mu.Lock()
		calls := cleanup.calls
		cleanup.mu.Unlock()
		if calls != 1 {
			t.Fatalf("cleanup calls = %d, want 1", calls)
		}

		// REST parity: the REST surface's INVALID_STATE_TRANSITION carries
		// the same triple.
		viaREST := newParitySurface(t, session.StateInputRequired)
		_, raw := postJSON(t, viaREST.rest.URL+"/v1/sessions/"+paritySessionID+"/interrupt", "acme", nil)
		if rest := decodeRESTError(t, raw); rest != got {
			t.Fatalf("REST triple = %+v, MCP triple = %+v, want equal", rest, got)
		}
	})

	t.Run("counters unavailable", func(t *testing.T) {
		url, mr, _ := newBudgetDelegateMCP(t)
		mr.Close()
		got := decodeMCPError(t, mcpCall(t, url, "lenny/delegate_task", args))
		want := wantTriple("DELEGATION_BUDGET_UNAVAILABLE")
		if got != want || !got.Retryable {
			t.Fatalf("MCP triple = %+v, want %+v with retryable true", got, want)
		}
	})
}
