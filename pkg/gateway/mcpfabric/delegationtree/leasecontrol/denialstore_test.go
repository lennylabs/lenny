// SPDX-License-Identifier: MIT

package leasecontrol_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/leasecontrol"
)

// fakeDenialStore is an in-memory leasecontrol.DenialStore double that
// records the calls MemoryBudgetSource delegates to it, so the §8.6
// lines 730-733 seam can be exercised without Postgres. F-8.6.5.
type fakeDenialStore struct {
	denied     map[string]bool
	expiry     map[string]time.Time
	coolOff    map[string]time.Duration
	grantCalls int
	cleared    int
	// grantErr, when set, makes the next Grant fail (e.g. to model the
	// §8.6 line 732 in-flight denial caught inside the commit tx).
	grantErr error
	// denyErr / clearErr force the storage-error propagation paths.
	denyErr  error
	clearErr error
}

func newFakeDenialStore() *fakeDenialStore {
	return &fakeDenialStore{
		denied:  map[string]bool{},
		expiry:  map[string]time.Time{},
		coolOff: map[string]time.Duration{},
	}
}

func (f *fakeDenialStore) key(tenant, root string) string { return tenant + "/" + root }

func (f *fakeDenialStore) Deny(_ context.Context, tenant, root string, coolOff time.Duration) error {
	if f.denyErr != nil {
		return f.denyErr
	}
	k := f.key(tenant, root)
	f.denied[k] = true
	f.coolOff[k] = coolOff
	f.expiry[k] = time.Unix(0, 0).Add(coolOff)
	return nil
}

func (f *fakeDenialStore) Denied(_ context.Context, tenant, root string) (bool, time.Time, error) {
	k := f.key(tenant, root)
	return f.denied[k], f.expiry[k], nil
}

func (f *fakeDenialStore) Grant(_ context.Context, _, _ string, _ leasecontrol.Dimensions) error {
	f.grantCalls++
	return f.grantErr
}

func (f *fakeDenialStore) Clear(_ context.Context, tenant, root string) error {
	if f.clearErr != nil {
		return f.clearErr
	}
	f.cleared++
	delete(f.denied, f.key(tenant, root))
	return nil
}

func registerTree(b *leasecontrol.MemoryBudgetSource) {
	b.RegisterTree("root-1", leasecontrol.TreeConfig{
		TenantID:           "acme",
		CurrentTokenBudget: 100_000,
		DeploymentBase:     500_000,
		DeploymentMax:      2_000_000,
		RejectionCoolOff:   90 * time.Second,
	})
}

// spec: §8.6 line 730 — Deny delegates to the durable store rather than
// mutating in-memory state, with the tree's resolved rejectionCoolOff.
func TestDenyDelegatesToStore_spec_8_6_line_730(t *testing.T) {
	store := newFakeDenialStore()
	b := leasecontrol.NewMemoryBudgetSource().WithDenialStore(store)
	registerTree(b)

	if err := b.Deny(context.Background(), "acme", "root-1", "child-9"); err != nil {
		t.Fatalf("Deny: %v", err)
	}
	if !store.denied[store.key("acme", "root-1")] {
		t.Fatalf("Deny did not reach the durable store")
	}
	if got := store.coolOff[store.key("acme", "root-1")]; got != 90*time.Second {
		t.Fatalf("resolved cool-off = %v, want 90s", got)
	}
}

// An unknown tree is a no-op and never reaches the store.
func TestDenyUnknownTreeNoStoreCall_spec_8_6_line_730(t *testing.T) {
	store := newFakeDenialStore()
	b := leasecontrol.NewMemoryBudgetSource().WithDenialStore(store)
	if err := b.Deny(context.Background(), "acme", "missing", "child"); err != nil {
		t.Fatalf("Deny: %v", err)
	}
	if len(store.denied) != 0 {
		t.Fatalf("Deny on unknown tree should not touch the store")
	}
}

// spec: §8.6 line 731 — TreeBudget reads the denial flag from the store,
// not the in-memory cache.
func TestTreeBudgetReadsDenialFromStore_spec_8_6_line_731(t *testing.T) {
	store := newFakeDenialStore()
	b := leasecontrol.NewMemoryBudgetSource().WithDenialStore(store)
	registerTree(b)

	tb, err := b.TreeBudget(context.Background(), "acme", "root-1")
	if err != nil {
		t.Fatalf("TreeBudget: %v", err)
	}
	if tb.ExtensionDenied {
		t.Fatalf("fresh tree should not be denied")
	}

	store.denied[store.key("acme", "root-1")] = true
	store.expiry[store.key("acme", "root-1")] = time.Unix(100, 0)
	tb, err = b.TreeBudget(context.Background(), "acme", "root-1")
	if err != nil {
		t.Fatalf("TreeBudget after deny: %v", err)
	}
	if !tb.ExtensionDenied {
		t.Fatalf("TreeBudget did not surface the store's denial")
	}
	if !tb.CoolOffExpiry.Equal(time.Unix(100, 0)) {
		t.Fatalf("cool-off expiry = %v, want 100s", tb.CoolOffExpiry)
	}
}

// spec: §8.6 line 732 — ApplyGrant runs the store's in-transaction
// re-check and, on a not-denied tree, applies the per-session in-memory
// delta afterwards.
func TestApplyGrantDelegatesGateThenAppliesDelta_spec_8_6_line_732(t *testing.T) {
	store := newFakeDenialStore()
	b := leasecontrol.NewMemoryBudgetSource().WithDenialStore(store)
	registerTree(b)

	nl, err := b.ApplyGrant(context.Background(), "acme", "root-1", "root-1",
		leasecontrol.Dimensions{Tokens: 50_000})
	if err != nil {
		t.Fatalf("ApplyGrant: %v", err)
	}
	if store.grantCalls != 1 {
		t.Fatalf("store.Grant calls = %d, want 1", store.grantCalls)
	}
	if nl.Tokens != 150_000 {
		t.Fatalf("new token limit = %d, want 150000", nl.Tokens)
	}
}

// When the store's in-transaction re-check finds the flag set, ApplyGrant
// returns ErrExtensionDenied and the in-memory delta is NOT applied.
func TestApplyGrantStoreDeniedSkipsDelta_spec_8_6_line_732(t *testing.T) {
	store := newFakeDenialStore()
	store.grantErr = leasecontrol.ErrExtensionDenied
	b := leasecontrol.NewMemoryBudgetSource().WithDenialStore(store)
	registerTree(b)

	_, err := b.ApplyGrant(context.Background(), "acme", "root-1", "child-2",
		leasecontrol.Dimensions{Tokens: 50_000})
	if !errors.Is(err, leasecontrol.ErrExtensionDenied) {
		t.Fatalf("ApplyGrant err = %v, want ErrExtensionDenied", err)
	}
	// The child's view must be unchanged: no delta was applied.
	tb, err := b.TreeBudget(context.Background(), "acme", "child-2")
	if err != nil {
		// child-2 is not a registered session; read the root view instead.
		tb, err = b.TreeBudget(context.Background(), "acme", "root-1")
		if err != nil {
			t.Fatalf("TreeBudget: %v", err)
		}
	}
	if tb.Current.Tokens != 100_000 {
		t.Fatalf("denied grant leaked a delta: current = %d, want 100000", tb.Current.Tokens)
	}
}

// spec: §8.6 line 735 / §15.1 line 868 — ClearSubtreeDenial delegates to
// the store for a known tree and reports found=true; an unknown tree
// reports found=false without a store call.
func TestClearSubtreeDenialDelegates_spec_8_6_line_735(t *testing.T) {
	store := newFakeDenialStore()
	b := leasecontrol.NewMemoryBudgetSource().WithDenialStore(store)
	registerTree(b)

	found, err := b.ClearSubtreeDenial(context.Background(), "root-1", "child-3")
	if err != nil {
		t.Fatalf("ClearSubtreeDenial: %v", err)
	}
	if !found {
		t.Fatalf("known tree should report found=true")
	}
	if store.cleared != 1 {
		t.Fatalf("store.Clear calls = %d, want 1", store.cleared)
	}

	found, err = b.ClearSubtreeDenial(context.Background(), "missing", "child")
	if err != nil {
		t.Fatalf("ClearSubtreeDenial unknown: %v", err)
	}
	if found {
		t.Fatalf("unknown tree should report found=false")
	}
}

// A storage error from the store propagates through ClearSubtreeDenial
// with found=true so the admin handler surfaces the failure.
func TestClearSubtreeDenialStorageErrorPropagates_spec_8_6_line_735(t *testing.T) {
	store := newFakeDenialStore()
	store.clearErr = errors.New("boom")
	b := leasecontrol.NewMemoryBudgetSource().WithDenialStore(store)
	registerTree(b)

	found, err := b.ClearSubtreeDenial(context.Background(), "root-1", "child")
	if err == nil {
		t.Fatalf("expected storage error to propagate")
	}
	if !found {
		t.Fatalf("storage error should still report found=true")
	}
}

// With no store injected the in-memory denial behavior is unchanged: a
// Deny is observable through TreeBudget and gates ApplyGrant.
func TestNilDenialStoreKeepsInMemoryBehavior_spec_8_6_line_734(t *testing.T) {
	b := leasecontrol.NewMemoryBudgetSource()
	registerTree(b)

	if err := b.Deny(context.Background(), "acme", "root-1", "root-1"); err != nil {
		t.Fatalf("Deny: %v", err)
	}
	tb, err := b.TreeBudget(context.Background(), "acme", "root-1")
	if err != nil {
		t.Fatalf("TreeBudget: %v", err)
	}
	if !tb.ExtensionDenied {
		t.Fatalf("in-memory Deny not reflected in TreeBudget")
	}
	if _, err := b.ApplyGrant(context.Background(), "acme", "root-1", "root-1",
		leasecontrol.Dimensions{Tokens: 1}); !errors.Is(err, leasecontrol.ErrExtensionDenied) {
		t.Fatalf("in-memory denial did not gate ApplyGrant: %v", err)
	}
}
