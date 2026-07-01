// SPDX-License-Identifier: MIT

package leasestore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/storage/leasestore"
)

// newStore returns a leasestore wired to a fresh miniredis instance.
func newStore(t *testing.T) *leasestore.Store {
	t.Helper()
	mr := miniredis.RunT(t)
	cl := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = cl.Close() })
	return leasestore.New(cl)
}

// spec: §12.1 line 5 — the LeaseStore role interface exposes the
// mandatory erasure pair so a substitute backend that omits either
// method cannot compile in. The var _ check in the package enforces
// this; this test pins it as an explicit, named assertion.
func TestStoreSatisfiesLeaseStore_spec_12_1(t *testing.T) {
	t.Parallel()
	var _ leasestore.LeaseStore = (*leasestore.Store)(nil)
}

// spec: §12.1 line 5 / §12.8 step 1 — DeleteByUser is a no-op for the
// session-coordination lease (leases are session-keyed and TTL-bound),
// returning (0, nil).
func TestDeleteByUserNoOp_spec_12_8_step1(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := context.Background()
	if _, err := s.Acquire(ctx, "acme", "sess-1", "replica-a", time.Minute); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	n, err := s.DeleteByUser(ctx, "acme", "alice@acme.com")
	if err != nil || n != 0 {
		t.Fatalf("DeleteByUser = (%d, %v), want (0, nil)", n, err)
	}
	// The lease must remain — DeleteByUser does not touch coordination state.
	if _, err := s.Get(ctx, "acme", "sess-1"); err != nil {
		t.Fatalf("lease should survive DeleteByUser: %v", err)
	}
}

// spec: §12.8 line 753 — empty arguments must never be treated as
// "delete everything"; the erasure primitives reject them.
func TestErasureRejectsEmptyScope_spec_12_8_line753(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := context.Background()
	if _, err := s.DeleteByUser(ctx, "", "alice@acme.com"); !errors.Is(err, leasestore.ErrEmptyScope) {
		t.Errorf("DeleteByUser(emptyTenant) err = %v, want ErrEmptyScope", err)
	}
	if _, err := s.DeleteByUser(ctx, "acme", ""); !errors.Is(err, leasestore.ErrEmptyScope) {
		t.Errorf("DeleteByUser(emptyUser) err = %v, want ErrEmptyScope", err)
	}
	if _, err := s.DeleteByTenant(ctx, ""); !errors.Is(err, leasestore.ErrEmptyScope) {
		t.Errorf("DeleteByTenant(empty) err = %v, want ErrEmptyScope", err)
	}
}

// spec: §12.1 line 5 / §12.8 Phase 4 — DeleteByTenant removes every
// session lease under the tenant prefix and returns the count, leaving
// other tenants untouched. A second call is idempotent.
func TestDeleteByTenantScopedAndIdempotent_spec_12_8_phase4(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := context.Background()
	for _, sid := range []string{"sess-1", "sess-2"} {
		if _, err := s.Acquire(ctx, "acme", sid, "replica-a", time.Minute); err != nil {
			t.Fatalf("Acquire acme/%s: %v", sid, err)
		}
	}
	if _, err := s.Acquire(ctx, "globex", "sess-9", "replica-b", time.Minute); err != nil {
		t.Fatalf("Acquire globex: %v", err)
	}

	n, err := s.DeleteByTenant(ctx, "acme")
	if err != nil {
		t.Fatalf("DeleteByTenant: %v", err)
	}
	if n != 2 {
		t.Errorf("DeleteByTenant count = %d, want 2", n)
	}
	for _, sid := range []string{"sess-1", "sess-2"} {
		if _, err := s.Get(ctx, "acme", sid); !errors.Is(err, leasestore.ErrNotFound) {
			t.Errorf("acme/%s should be erased, Get err = %v", sid, err)
		}
	}
	// Other tenant's lease is untouched.
	if _, err := s.Get(ctx, "globex", "sess-9"); err != nil {
		t.Errorf("globex lease should survive: %v", err)
	}
	// Idempotent: a second sweep removes nothing.
	if n2, err := s.DeleteByTenant(ctx, "acme"); err != nil || n2 != 0 {
		t.Errorf("second DeleteByTenant = (%d, %v), want (0, nil)", n2, err)
	}
}
