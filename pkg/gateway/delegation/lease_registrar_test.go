// SPDX-License-Identifier: MIT

package delegation_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/delegation/lease"
	"github.com/lennylabs/lenny/pkg/gateway/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/leasecontrol"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// fakeChildRegistrar records the §8.6 lease-extension registration calls
// Delegate makes so the wiring tests can assert the child is bound to its
// root's tree and capped at the parent's own lease. F-15.3.5.
type fakeChildRegistrar struct {
	addSessionCalls []addSessionCall
	parentLeases    map[string]leasecontrol.SessionLease
}

type addSessionCall struct {
	sessionID, rootSessionID, tenantID string
}

func newFakeChildRegistrar() *fakeChildRegistrar {
	return &fakeChildRegistrar{parentLeases: map[string]leasecontrol.SessionLease{}}
}

func (f *fakeChildRegistrar) AddSession(sessionID, rootSessionID, tenantID string) {
	f.addSessionCalls = append(f.addSessionCalls, addSessionCall{sessionID, rootSessionID, tenantID})
}

func (f *fakeChildRegistrar) SetParentLease(sessionID string, parent leasecontrol.SessionLease) {
	f.parentLeases[sessionID] = parent
}

func registrarService(t *testing.T, store sessionstore.Store, reg delegation.LeaseChildRegistrar, childID string) *delegation.Service {
	t.Helper()
	return delegation.NewService(store, delegation.Options{
		Clock:          func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:         func() string { return childID },
		LeaseRegistrar: reg,
	})
}

// spec: §8.6 line 648 — an admitted child is registered with the lease-
// extension budget source under its root's tree and capped at the
// parent's own granted lease, so a later adapter ExtendLease from the
// child resolves the tree instead of failing ErrSessionNotFound. F-15.3.5.
func TestDelegate_RegistersChildLeaseTree_spec_8_6_line_648(t *testing.T) {
	store := memstore.New()
	// Parent is itself a delegated child rooted at sess_root, carrying a
	// granted lease that becomes the child's per-extension ceiling.
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_parent", TenantID: "acme", State: "running",
		UserID: "user_alice", RuntimeRef: "claude", PoolRef: "pool-a",
		IsolationProfile: isolation.ProfileSandboxed,
		ParentSessionID:  "sess_root",
		RootSessionID:    "sess_root",
		DelegationLease: &sessionstore.DelegationLease{
			MaxTokenBudget: 1000, MaxChildrenTotal: 10, MaxTreeSize: 20,
			MaxParallelChildren: 4, PerChildMaxAge: 600,
		},
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed parent: %v", err)
	}
	reg := newFakeChildRegistrar()
	svc := registrarService(t, store, reg, "sess_child")

	if _, err := svc.Delegate(context.Background(), "acme", delegation.Request{
		ParentSessionID:  "sess_parent",
		RuntimeRef:       "gemini",
		PoolRef:          "pool-b",
		IsolationProfile: isolation.ProfileSandboxed,
		LeaseSlice:       lease.LeaseSlice{MaxTokenBudget: 500, MaxChildrenTotal: 5},
	}); err != nil {
		t.Fatalf("Delegate: %v", err)
	}

	if len(reg.addSessionCalls) != 1 {
		t.Fatalf("AddSession calls = %d, want 1", len(reg.addSessionCalls))
	}
	got := reg.addSessionCalls[0]
	// The child inherits the parent's root, not a fresh tree.
	if got != (addSessionCall{"sess_child", "sess_root", "acme"}) {
		t.Fatalf("AddSession(%+v), want {sess_child sess_root acme}", got)
	}
	// The parent-lease ceiling projects the parent's granted lease.
	want := leasecontrol.SessionLease{
		TokenCeiling: 1000, MaxAgeCeiling: 600, ChildrenCeiling: 10,
		ParallelChildrenCeiling: 4, TreeSizeCeiling: 20,
	}
	if got := reg.parentLeases["sess_child"]; got != want {
		t.Fatalf("SetParentLease(%+v), want %+v", got, want)
	}
}

// spec: §8.9 line 1010 — a child of the tree root inherits the root's id
// as its tree key, and a root parent carrying no granted lease yields a
// zero parent-lease ceiling (the tree's effective-max still applies). F-15.3.5.
func TestDelegate_ChildOfRoot_ZeroParentCeiling_spec_8_9_line_1010(t *testing.T) {
	store := memstore.New()
	// Root parent: no ParentSessionID, no granted DelegationLease.
	seedParent(t, store, "sess_root", "", "claude", "pool-a", isolation.ProfileSandboxed)
	reg := newFakeChildRegistrar()
	svc := registrarService(t, store, reg, "sess_child")

	if _, err := svc.Delegate(context.Background(), "acme", delegation.Request{
		ParentSessionID:  "sess_root",
		RuntimeRef:       "gemini",
		PoolRef:          "pool-b",
		IsolationProfile: isolation.ProfileSandboxed,
	}); err != nil {
		t.Fatalf("Delegate: %v", err)
	}

	if len(reg.addSessionCalls) != 1 {
		t.Fatalf("AddSession calls = %d, want 1", len(reg.addSessionCalls))
	}
	// rootSessionID resolves to the root parent's own id.
	if got := reg.addSessionCalls[0]; got != (addSessionCall{"sess_child", "sess_root", "acme"}) {
		t.Fatalf("AddSession(%+v), want {sess_child sess_root acme}", got)
	}
	if got := reg.parentLeases["sess_child"]; got != (leasecontrol.SessionLease{}) {
		t.Fatalf("SetParentLease(%+v), want zero ceiling", got)
	}
}

// spec: §8.6 — end-to-end against the production *MemoryBudgetSource:
// before this wiring the first ExtendLease from a delegated child failed
// TenantOf with ErrSessionNotFound (the finding's cited failure point).
// After Delegate registers the child, TenantOf and TreeBudget resolve the
// child to its root's tree. F-15.3.5.
func TestDelegate_RealBudgetSource_ResolvesChild_spec_8_6(t *testing.T) {
	store := memstore.New()
	seedParent(t, store, "sess_root", "", "claude", "pool-a", isolation.ProfileSandboxed)

	budgets := leasecontrol.NewMemoryBudgetSource()
	// The session server registers the root tree; simulate that here so
	// the delegation half resolves against a populated source.
	budgets.RegisterTree("sess_root", leasecontrol.TreeConfig{
		TenantID:           "acme",
		CurrentTokenBudget: 100_000,
		DeploymentBase:     500_000,
		DeploymentMax:      2_000_000,
	})
	svc := registrarService(t, store, budgets, "sess_child")

	if _, err := svc.Delegate(context.Background(), "acme", delegation.Request{
		ParentSessionID:  "sess_root",
		RuntimeRef:       "gemini",
		PoolRef:          "pool-b",
		IsolationProfile: isolation.ProfileSandboxed,
	}); err != nil {
		t.Fatalf("Delegate: %v", err)
	}

	tenant, err := budgets.TenantOf(context.Background(), "sess_child")
	if err != nil {
		t.Fatalf("TenantOf(sess_child) = %v, want nil (no longer ErrSessionNotFound)", err)
	}
	if tenant != "acme" {
		t.Fatalf("TenantOf(sess_child) = %q, want acme", tenant)
	}
	tb, err := budgets.TreeBudget(context.Background(), "acme", "sess_child")
	if err != nil {
		t.Fatalf("TreeBudget(sess_child): %v", err)
	}
	if tb.RootSessionID != "sess_root" {
		t.Fatalf("TreeBudget.RootSessionID = %q, want sess_root", tb.RootSessionID)
	}
}

// A nil registrar (the in-process gateway with no GatewayControl
// listener) leaves Delegate's behaviour unchanged and panics nowhere.
// F-15.3.5.
func TestDelegate_NilLeaseRegistrar_NoPanic(t *testing.T) {
	store := memstore.New()
	seedParent(t, store, "sess_root", "", "claude", "pool-a", isolation.ProfileSandboxed)
	svc := registrarService(t, store, nil, "sess_child")

	if _, err := svc.Delegate(context.Background(), "acme", delegation.Request{
		ParentSessionID:  "sess_root",
		RuntimeRef:       "gemini",
		PoolRef:          "pool-b",
		IsolationProfile: isolation.ProfileSandboxed,
	}); err != nil {
		t.Fatalf("Delegate with nil registrar: %v", err)
	}
}
