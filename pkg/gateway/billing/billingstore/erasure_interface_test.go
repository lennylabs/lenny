// SPDX-License-Identifier: MIT

package billingstore_test

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/billing/billingstore"
)

// spec: §12.1 line 5 — the erasure primitives are mandatory methods on
// the Store interface, enforced at compile time. The bare ledger must
// satisfy billingstore.Store including the erasure pair.
func TestMemorySatisfiesStore_spec_12_1(t *testing.T) {
	var _ billingstore.Store = (*billingstore.Memory)(nil)
}

// spec: §12.8 erasure verification — CountUser reports how many events
// still carry a user id, the input to the post-pseudonymization check.
func TestMemoryCountUser_spec_12_8(t *testing.T) {
	store := billingstore.NewMemory()
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if _, err := store.Append(ctx, billed("acme", "alice@acme", 10)); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	if _, err := store.Append(ctx, billed("acme", "bob@acme", 10)); err != nil {
		t.Fatalf("Append: %v", err)
	}
	n, err := store.CountUser(ctx, "acme", "alice@acme")
	if err != nil {
		t.Fatalf("CountUser: %v", err)
	}
	if n != 2 {
		t.Fatalf("CountUser = %d, want 2", n)
	}
	// After pseudonymization no event carries the original id.
	if _, err := store.PseudonymizeUser(ctx, "acme", "alice@acme", erasureSalt); err != nil {
		t.Fatalf("PseudonymizeUser: %v", err)
	}
	if n, _ := store.CountUser(ctx, "acme", "alice@acme"); n != 0 {
		t.Fatalf("CountUser after pseudonymize = %d, want 0", n)
	}
}

// spec: §12.1 line 5, §12.8 — billing user erasure pseudonymizes rather
// than deletes, so DeleteByUser is a no-op that returns (0, nil) and
// leaves the events in place for financial reconciliation.
func TestMemoryDeleteByUserIsNoOp_spec_12_1(t *testing.T) {
	store := billingstore.NewMemory()
	ctx := context.Background()
	if _, err := store.Append(ctx, billed("acme", "alice@acme", 10)); err != nil {
		t.Fatalf("Append: %v", err)
	}
	n, err := store.DeleteByUser(ctx, "acme", "alice@acme")
	if err != nil {
		t.Fatalf("DeleteByUser: %v", err)
	}
	if n != 0 {
		t.Fatalf("DeleteByUser removed %d events, want 0 (billing pseudonymizes)", n)
	}
	if c, _ := store.CountUser(ctx, "acme", "alice@acme"); c != 1 {
		t.Fatalf("event count after DeleteByUser = %d, want 1 (append-only)", c)
	}
}

// spec: §12.1 line 5, §12.8 Phase 4 — DeleteByTenant removes every event
// the tenant owns on teardown and leaves other tenants untouched.
func TestMemoryDeleteByTenant_spec_12_1(t *testing.T) {
	store := billingstore.NewMemory()
	ctx := context.Background()
	if _, err := store.Append(ctx, billed("acme", "alice@acme", 10)); err != nil {
		t.Fatalf("Append acme: %v", err)
	}
	if _, err := store.Append(ctx, billed("acme", "bob@acme", 10)); err != nil {
		t.Fatalf("Append acme: %v", err)
	}
	if _, err := store.Append(ctx, billed("globex", "carol@globex", 10)); err != nil {
		t.Fatalf("Append globex: %v", err)
	}
	n, err := store.DeleteByTenant(ctx, "acme")
	if err != nil {
		t.Fatalf("DeleteByTenant: %v", err)
	}
	if n != 2 {
		t.Fatalf("DeleteByTenant removed %d events, want 2", n)
	}
	if got, _ := store.Since(ctx, "acme", 0, 0); len(got) != 0 {
		t.Fatalf("acme has %d events after teardown, want 0", len(got))
	}
	if got, _ := store.Since(ctx, "globex", 0, 0); len(got) != 1 {
		t.Fatalf("globex has %d events, want 1 (untouched)", len(got))
	}
}
