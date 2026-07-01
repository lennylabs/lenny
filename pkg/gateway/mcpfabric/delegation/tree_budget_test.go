// SPDX-License-Identifier: MIT

package delegation_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/delegation/lease"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/treebudget"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// newServiceWithBudget builds a delegation Service whose §12.4 Redis
// budget gate is wired to a fresh miniredis. spec: §8.2 lines 57, 127.
func newServiceWithBudget(t *testing.T, store sessionstore.Store, idFn func() string) (*delegation.Service, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	cl := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = cl.Close() })
	svc := delegation.NewService(store, delegation.Options{
		Clock:              func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:             idFn,
		TreeBudgetReserver: treebudget.New(cl, 0),
	})
	return svc, mr
}

func delegateOnce(svc *delegation.Service) (delegation.Result, error) {
	return svc.Delegate(context.Background(), "acme", delegation.Request{
		ParentSessionID:  "sess_parent",
		RuntimeRef:       "gemini",
		PoolRef:          "pool-b",
		IsolationProfile: isolation.ProfileSandboxed,
	})
}

// spec: §8.2 line 127 / §12.4 line 213 — once the live tree node count
// reaches the parent's maxTreeSize the next delegation is rejected with
// BUDGET_EXHAUSTED, even though each child's static slice is within
// budget. This is the axis the per-call ValidateChildSlice cannot
// enforce (it only compares declared ceilings). F-8.2.18.
func TestDelegate_TreeBudget_TreeSizeExhausted_spec_8_2_127(t *testing.T) {
	store := memstore.New()
	seedParentWithLease(t, store, "sess_parent", &sessionstore.DelegationLease{MaxTreeSize: 2})
	n := 0
	svc, _ := newServiceWithBudget(t, store, func() string {
		n++
		return "child_" + string(rune('a'+n))
	})
	for i := 0; i < 2; i++ {
		if _, err := delegateOnce(svc); err != nil {
			t.Fatalf("delegation %d within tree budget: %v", i, err)
		}
	}
	_, err := delegateOnce(svc)
	var bx *lease.BudgetExceededError
	if !errors.As(err, &bx) {
		t.Fatalf("over-tree-size delegation err = %v, want *lease.BudgetExceededError (BUDGET_EXHAUSTED)", err)
	}
}

// spec: §8.2 line 127 — the per-parent maxParallelChildren counter is
// enforced from the live Redis counter; a parent at its concurrent
// ceiling is rejected with BUDGET_EXHAUSTED.
func TestDelegate_TreeBudget_ParallelChildrenExhausted_spec_8_2_127(t *testing.T) {
	store := memstore.New()
	seedParentWithLease(t, store, "sess_parent", &sessionstore.DelegationLease{MaxParallelChildren: 1})
	n := 0
	svc, _ := newServiceWithBudget(t, store, func() string {
		n++
		return "pc_" + string(rune('a'+n))
	})
	if _, err := delegateOnce(svc); err != nil {
		t.Fatalf("first child: %v", err)
	}
	_, err := delegateOnce(svc)
	var bx *lease.BudgetExceededError
	if !errors.As(err, &bx) {
		t.Fatalf("second concurrent child err = %v, want BUDGET_EXHAUSTED", err)
	}
}

// spec: §12.4 line 213 — a Redis outage fails the admission path closed:
// Delegate returns delegation.ErrBudgetUnavailable (mapped to the
// retryable DELEGATION_BUDGET_UNAVAILABLE) rather than admitting an
// unbudgeted child. F-8.2.18.
func TestDelegate_TreeBudget_FailsClosedOnRedisOutage_spec_12_4_213(t *testing.T) {
	store := memstore.New()
	seedParentWithLease(t, store, "sess_parent", &sessionstore.DelegationLease{MaxTreeSize: 10})
	svc, mr := newServiceWithBudget(t, store, func() string { return "child_x" })
	mr.Close()
	_, err := delegateOnce(svc)
	if !errors.Is(err, delegation.ErrBudgetUnavailable) {
		t.Fatalf("delegation during Redis outage err = %v, want ErrBudgetUnavailable", err)
	}
	// No child row may have been created.
	if _, gerr := store.Get(context.Background(), "acme", "child_x"); gerr == nil {
		t.Fatal("a child row was created despite the fail-closed budget gate")
	}
}

// spec: §8.2 line 127 — a parent with no granted lease (every axis zero)
// imposes no tree-size cap, so the maxTreeMemoryBytes default still
// applies but structural axes are unbounded; delegations are admitted.
func TestDelegate_TreeBudget_UnboundedParentAdmitted_spec_8_2_127(t *testing.T) {
	store := memstore.New()
	seedParentWithLease(t, store, "sess_parent", nil)
	n := 0
	svc, _ := newServiceWithBudget(t, store, func() string {
		n++
		return "ub_" + string(rune('a'+n))
	})
	for i := 0; i < 5; i++ {
		if _, err := delegateOnce(svc); err != nil {
			t.Fatalf("unbounded-parent delegation %d: %v", i, err)
		}
	}
}
