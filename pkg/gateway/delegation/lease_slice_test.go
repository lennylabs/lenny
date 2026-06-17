// SPDX-License-Identifier: MIT

package delegation_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/delegation/lease"
	"github.com/lennylabs/lenny/pkg/gateway/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// seedParentWithLease seeds a running parent carrying a granted §8.2
// lease_slice so the delegation budget gate has a ceiling to validate
// against. spec: §8.2 lines 38-48. F-8.2.2.
func seedParentWithLease(t *testing.T, store sessionstore.Store, id string, granted *sessionstore.DelegationLease) {
	t.Helper()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	row := sessionstore.Session{
		ID: id, TenantID: "acme", State: session.StateRunning,
		UserID:     "user_alice",
		RuntimeRef: "claude", PoolRef: "pool-a",
		IsolationProfile: isolation.ProfileSandboxed,
		DelegationLease:  granted,
		CreatedAt:        now, UpdatedAt: now,
	}
	if err := store.Create(context.Background(), row); err != nil {
		t.Fatalf("seed parent %s: %v", id, err)
	}
}

func delegateWithSlice(svc *delegation.Service, slice lease.LeaseSlice) (delegation.Result, error) {
	return svc.Delegate(context.Background(), "acme", delegation.Request{
		ParentSessionID:  "sess_parent",
		RuntimeRef:       "gemini",
		PoolRef:          "pool-b",
		IsolationProfile: isolation.ProfileSandboxed,
		LeaseSlice:       slice,
	})
}

// spec: §8.2 lines 38-48 — a child lease_slice that stays within the
// parent's granted budget is admitted, and the resolved slice is stamped
// on the child row so the child's own descendants validate against it.
func TestDelegate_LeaseSlice_WithinParentBudget_Admitted_Spec82(t *testing.T) {
	store := memstore.New()
	seedParentWithLease(t, store, "sess_parent", &sessionstore.DelegationLease{
		MaxTokenBudget: 1000, MaxChildrenTotal: 10, MaxTreeSize: 20,
		MaxParallelChildren: 4, PerChildMaxAge: 600,
	})
	svc := newService(t, store, func() string { return "sess_child" })

	res, err := delegateWithSlice(svc, lease.LeaseSlice{
		MaxTokenBudget: 500, MaxChildrenTotal: 5, MaxTreeSize: 10,
		MaxParallelChildren: 2, PerChildMaxAge: 300,
	})
	if err != nil {
		t.Fatalf("Delegate within budget: unexpected error %v", err)
	}
	if res.Child.DelegationLease == nil {
		t.Fatal("child DelegationLease not stamped")
	}
	if got := res.Child.DelegationLease.MaxTokenBudget; got != 500 {
		t.Fatalf("child MaxTokenBudget = %d, want 500", got)
	}
	// The stamped slice must survive a store round-trip so the child's
	// descendants read the same ceiling.
	reloaded, err := store.Get(context.Background(), "acme", "sess_child")
	if err != nil {
		t.Fatalf("reload child: %v", err)
	}
	if reloaded.DelegationLease == nil || reloaded.DelegationLease.MaxChildrenTotal != 5 {
		t.Fatalf("reloaded child lease = %+v, want MaxChildrenTotal 5", reloaded.DelegationLease)
	}
}

// spec: §8.2 lines 38-48, 127 — a child lease_slice that exceeds the
// parent's granted budget on any axis is rejected with
// *lease.BudgetExceededError (mapped to BUDGET_EXHAUSTED) before the
// child row is created.
func TestDelegate_LeaseSlice_ExceedsParentBudget_BudgetExceeded_Spec82(t *testing.T) {
	cases := []struct {
		name  string
		slice lease.LeaseSlice
	}{
		{"tokens", lease.LeaseSlice{MaxTokenBudget: 2000}},
		{"children", lease.LeaseSlice{MaxChildrenTotal: 50}},
		{"treeSize", lease.LeaseSlice{MaxTreeSize: 99}},
		{"parallel", lease.LeaseSlice{MaxParallelChildren: 9}},
		{"age", lease.LeaseSlice{PerChildMaxAge: 9999}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := memstore.New()
			seedParentWithLease(t, store, "sess_parent", &sessionstore.DelegationLease{
				MaxTokenBudget: 1000, MaxChildrenTotal: 10, MaxTreeSize: 20,
				MaxParallelChildren: 4, PerChildMaxAge: 600,
			})
			svc := newService(t, store, func() string { return "sess_child" })

			_, err := delegateWithSlice(svc, tc.slice)
			var budgetErr *lease.BudgetExceededError
			if !errors.As(err, &budgetErr) {
				t.Fatalf("Delegate over budget: got %v, want *lease.BudgetExceededError", err)
			}
			// The rejection must occur before the child row exists.
			if _, gerr := store.Get(context.Background(), "acme", "sess_child"); gerr == nil {
				t.Fatal("child row created despite BUDGET_EXHAUSTED rejection")
			}
		})
	}
}

// spec: §8.2 lines 38-48 — a parent with no granted slice (root /
// standalone, or a child whose lease declared no slice) imposes no
// budget binding, so ValidateChildSlice admits any child slice.
func TestDelegate_LeaseSlice_ParentNoBudget_AdmitsChild_Spec82(t *testing.T) {
	store := memstore.New()
	seedParentWithLease(t, store, "sess_parent", nil)
	svc := newService(t, store, func() string { return "sess_child" })

	res, err := delegateWithSlice(svc, lease.LeaseSlice{MaxTokenBudget: 1_000_000})
	if err != nil {
		t.Fatalf("Delegate against unbounded parent: unexpected error %v", err)
	}
	if res.Child.DelegationLease == nil || res.Child.DelegationLease.MaxTokenBudget != 1_000_000 {
		t.Fatalf("child lease = %+v, want MaxTokenBudget 1000000", res.Child.DelegationLease)
	}
}

// spec: §8.2 lines 38-48 — a delegation that declares no lease_slice
// leaves the child's DelegationLease nil (no budget binding), so the
// store omits the column rather than persisting an all-zero slice.
func TestDelegate_LeaseSlice_NoChildSlice_NilLease_Spec82(t *testing.T) {
	store := memstore.New()
	seedParentWithLease(t, store, "sess_parent", &sessionstore.DelegationLease{MaxTokenBudget: 1000})
	svc := newService(t, store, func() string { return "sess_child" })

	res, err := delegateWithSlice(svc, lease.LeaseSlice{})
	if err != nil {
		t.Fatalf("Delegate with no slice: unexpected error %v", err)
	}
	if res.Child.DelegationLease != nil {
		t.Fatalf("child DelegationLease = %+v, want nil", res.Child.DelegationLease)
	}
}
