// SPDX-License-Identifier: MIT

package delegationbudget_test

import (
	"context"
	"testing"

	sessionapi "github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationbudget"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

func mustCreate(t *testing.T, store sessionstore.Store, s sessionstore.Session) {
	t.Helper()
	if err := store.Create(context.Background(), s); err != nil {
		t.Fatalf("create session %q: %v", s.ID, err)
	}
}

// spec: §11.2 line 48 (live pod enumeration) — SessionEnumerator counts
// the non-terminal nodes of a tree (including the root) and sums their
// granted token budgets; terminal nodes are excluded.
func TestSessionEnumeratorCountsAliveNodes_spec_11_2_48(t *testing.T) {
	t.Parallel()
	store := memstore.New()
	mustCreate(t, store, sessionstore.Session{ID: "root", TenantID: "acme", RootSessionID: "root", State: sessionapi.StateRunning})
	mustCreate(t, store, sessionstore.Session{
		ID: "c1", TenantID: "acme", RootSessionID: "root", ParentSessionID: "root",
		State: sessionapi.StateRunning, DelegationLease: &sessionstore.DelegationLease{MaxTokenBudget: 400},
	})
	mustCreate(t, store, sessionstore.Session{
		ID: "c2", TenantID: "acme", RootSessionID: "root", ParentSessionID: "root",
		State: sessionapi.StateCompleted, DelegationLease: &sessionstore.DelegationLease{MaxTokenBudget: 900},
	}) // terminal, excluded

	e := delegationbudget.SessionEnumerator{Sessions: store}
	lt, err := e.LiveTree(context.Background(), "acme", "root")
	if err != nil {
		t.Fatalf("LiveTree: %v", err)
	}
	if !lt.RootExists {
		t.Fatalf("RootExists = false, want true")
	}
	// root + c1 alive; c2 terminal excluded.
	if lt.NodeCount != 2 {
		t.Fatalf("NodeCount = %d, want 2", lt.NodeCount)
	}
	// only c1's lease counts (root has no lease, c2 is terminal).
	if lt.TokenAllocations != 400 {
		t.Fatalf("TokenAllocations = %d, want 400", lt.TokenAllocations)
	}
}

// spec: §11.2 line 48 — a tree with no rows cannot be enumerated;
// RootExists is false (the "coordinating replica lost" half of the
// irrecoverability test).
func TestSessionEnumeratorMissingTreeRootExistsFalse_spec_11_2_48(t *testing.T) {
	t.Parallel()
	e := delegationbudget.SessionEnumerator{Sessions: memstore.New()}
	lt, err := e.LiveTree(context.Background(), "acme", "ghost")
	if err != nil {
		t.Fatalf("LiveTree: %v", err)
	}
	if lt.RootExists || lt.NodeCount != 0 {
		t.Fatalf("missing tree = %+v, want {RootExists:false NodeCount:0}", lt)
	}
}

// spec: §11.2 line 44 — SessionTreeLister returns one TreeRef per
// distinct root of non-terminal sessions; terminal sessions do not
// contribute a tree.
func TestSessionTreeListerDistinctActiveRoots_spec_11_2_44(t *testing.T) {
	t.Parallel()
	store := memstore.New()
	mustCreate(t, store, sessionstore.Session{ID: "root", TenantID: "acme", RootSessionID: "root", State: sessionapi.StateRunning})
	mustCreate(t, store, sessionstore.Session{ID: "c1", TenantID: "acme", RootSessionID: "root", ParentSessionID: "root", State: sessionapi.StateRunning})
	mustCreate(t, store, sessionstore.Session{ID: "doneRoot", TenantID: "acme", RootSessionID: "doneRoot", State: sessionapi.StateCompleted})

	l := delegationbudget.SessionTreeLister{
		Sessions: store,
		Tenants:  func(context.Context) ([]string, error) { return []string{"acme"}, nil },
	}
	refs, err := l.ListActiveTrees(context.Background())
	if err != nil {
		t.Fatalf("ListActiveTrees: %v", err)
	}
	if len(refs) != 1 || refs[0] != (delegationbudget.TreeRef{TenantID: "acme", RootSessionID: "root"}) {
		t.Fatalf("refs = %+v, want one ref for the active tree root", refs)
	}
}

// spec: §11.2 line 48 — MarkBudgetUnrecoverable moves a non-terminal
// root to awaiting_client_action with the reason; a terminal root and a
// missing root are left as no-ops.
func TestSessionMarkerTransitionsAndNoOps_spec_11_2_48(t *testing.T) {
	t.Parallel()
	store := memstore.New()
	mustCreate(t, store, sessionstore.Session{ID: "live", TenantID: "acme", RootSessionID: "live", State: sessionapi.StateRunning})
	mustCreate(t, store, sessionstore.Session{ID: "done", TenantID: "acme", RootSessionID: "done", State: sessionapi.StateCompleted})

	m := delegationbudget.SessionUnrecoverableMarker{Sessions: store}
	ctx := context.Background()

	if err := m.MarkBudgetUnrecoverable(ctx, "acme", "live", delegationbudget.BudgetStateUnrecoverableReason); err != nil {
		t.Fatalf("mark live: %v", err)
	}
	got, err := store.Get(ctx, "acme", "live")
	if err != nil {
		t.Fatalf("get live: %v", err)
	}
	if got.State != sessionapi.StateAwaitingClientAction || got.FailureReason != delegationbudget.BudgetStateUnrecoverableReason {
		t.Fatalf("live = {%s, %s}, want {awaiting_client_action, BUDGET_STATE_UNRECOVERABLE}", got.State, got.FailureReason)
	}

	// Terminal root is left unchanged.
	if err := m.MarkBudgetUnrecoverable(ctx, "acme", "done", delegationbudget.BudgetStateUnrecoverableReason); err != nil {
		t.Fatalf("mark done: %v", err)
	}
	done, _ := store.Get(ctx, "acme", "done")
	if done.State != sessionapi.StateCompleted {
		t.Fatalf("terminal root mutated to %s, want completed", done.State)
	}

	// Missing root is a no-op (no error).
	if err := m.MarkBudgetUnrecoverable(ctx, "acme", "ghost", delegationbudget.BudgetStateUnrecoverableReason); err != nil {
		t.Fatalf("mark missing root must be a no-op, got %v", err)
	}
}
