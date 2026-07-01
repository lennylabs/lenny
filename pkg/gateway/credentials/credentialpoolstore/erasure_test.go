// SPDX-License-Identifier: MIT

package credentialpoolstore_test

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
)

// spec: §12.1 line 5 — credential pools are tenant-scoped configuration
// with no user_id, so DeleteByUser removes nothing and returns (0, nil)
// while still satisfying the mandatory-erasure interface contract.
func TestMemoryDeleteByUserIsNoOp_spec_12_1(t *testing.T) {
	store := credentialpoolstore.NewMemory()
	if err := store.Create(context.Background(), samplePool("acme", "primary")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	n, err := store.DeleteByUser(context.Background(), "acme", "alice@acme.com")
	if err != nil {
		t.Fatalf("DeleteByUser: %v", err)
	}
	if n != 0 {
		t.Fatalf("DeleteByUser removed %d pools, want 0 (pools are tenant-scoped)", n)
	}
	// The pool is untouched.
	if _, err := store.Get(context.Background(), "acme", "primary"); err != nil {
		t.Fatalf("pool should survive a user erasure: %v", err)
	}
}

// spec: §12.1 line 5 — empty scope ids are rejected.
func TestMemoryDeleteByUserEmptyRejected_spec_12_1(t *testing.T) {
	store := credentialpoolstore.NewMemory()
	if _, err := store.DeleteByUser(context.Background(), "", "alice@acme.com"); err == nil {
		t.Fatal("DeleteByUser with empty tenant id should error")
	}
	if _, err := store.DeleteByUser(context.Background(), "acme", ""); err == nil {
		t.Fatal("DeleteByUser with empty user id should error")
	}
}

// spec: §12.1 line 5, §12.8 Phase 4 — DeleteByTenant removes every pool
// the tenant owns and leaves other tenants untouched.
func TestMemoryDeleteByTenant_spec_12_1(t *testing.T) {
	store := credentialpoolstore.NewMemory()
	if err := store.Create(context.Background(), samplePool("acme", "primary")); err != nil {
		t.Fatalf("Create acme/primary: %v", err)
	}
	if err := store.Create(context.Background(), samplePool("acme", "secondary")); err != nil {
		t.Fatalf("Create acme/secondary: %v", err)
	}
	if err := store.Create(context.Background(), samplePool("globex", "primary")); err != nil {
		t.Fatalf("Create globex/primary: %v", err)
	}

	n, err := store.DeleteByTenant(context.Background(), "acme")
	if err != nil {
		t.Fatalf("DeleteByTenant: %v", err)
	}
	if n != 2 {
		t.Fatalf("DeleteByTenant removed %d pools, want 2", n)
	}
	if _, err := store.Get(context.Background(), "acme", "primary"); err == nil {
		t.Fatal("acme pool should be erased")
	}
	if _, err := store.Get(context.Background(), "globex", "primary"); err != nil {
		t.Fatalf("globex pool should survive acme teardown: %v", err)
	}
}

// spec: §12.1 line 5 — DeleteByTenant rejects the empty tenant id and is
// idempotent on a tenant with no pools.
func TestMemoryDeleteByTenantEmptyAndIdempotent_spec_12_1(t *testing.T) {
	store := credentialpoolstore.NewMemory()
	if _, err := store.DeleteByTenant(context.Background(), ""); err == nil {
		t.Fatal("DeleteByTenant with empty tenant id should error")
	}
	n, err := store.DeleteByTenant(context.Background(), "ghost")
	if err != nil {
		t.Fatalf("DeleteByTenant on unknown tenant: %v", err)
	}
	if n != 0 {
		t.Fatalf("DeleteByTenant on unknown tenant removed %d, want 0", n)
	}
}
