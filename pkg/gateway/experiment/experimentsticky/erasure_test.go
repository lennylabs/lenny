// SPDX-License-Identifier: MIT

// Coverage for the §12.8 step-4 experiment-sticky-assignment erasure:
// DeleteByUser DELs `t:{tenant_id}:exp:*:sticky:{user_id}` across every
// experiment the user participated in, leaving other users' and other
// tenants' assignments intact.
//
// spec: §12.8, step 4.
package experimentsticky

import (
	"context"
	"testing"
)

func TestDeleteByUser_PurgesAllUserExperiments_spec_12_8_786(t *testing.T) {
	c, _ := newCacheT(t)
	ctx := context.Background()
	mustPut(t, c, "acme", "exp-1", "alice", "A")
	mustPut(t, c, "acme", "exp-2", "alice", "B")
	mustPut(t, c, "acme", "exp-1", "bob", "A")
	mustPut(t, c, "globex", "exp-1", "alice", "A")

	n, err := c.DeleteByUser(ctx, "acme", "alice")
	if err != nil {
		t.Fatalf("DeleteByUser: %v", err)
	}
	if n != 2 {
		t.Fatalf("deleted = %d, want 2", n)
	}

	if _, ok, _ := c.Get(ctx, "acme", "exp-1", "alice"); ok {
		t.Fatal("acme/exp-1/alice survived erasure")
	}
	if _, ok, _ := c.Get(ctx, "acme", "exp-2", "alice"); ok {
		t.Fatal("acme/exp-2/alice survived erasure")
	}
	if _, ok, _ := c.Get(ctx, "acme", "exp-1", "bob"); !ok {
		t.Fatal("acme/exp-1/bob erased — bob is not the target")
	}
	if _, ok, _ := c.Get(ctx, "globex", "exp-1", "alice"); !ok {
		t.Fatal("globex/exp-1/alice erased — cross-tenant leak")
	}
}

func TestDeleteByUser_NoAssignments_spec_12_8_786(t *testing.T) {
	c, _ := newCacheT(t)
	n, err := c.DeleteByUser(context.Background(), "acme", "carol")
	if err != nil {
		t.Fatalf("DeleteByUser: %v", err)
	}
	if n != 0 {
		t.Fatalf("deleted = %d, want 0", n)
	}
}

func TestDeleteByUser_RejectsEmptyArgs_spec_12_8_786(t *testing.T) {
	c, _ := newCacheT(t)
	if _, err := c.DeleteByUser(context.Background(), "", "alice"); err == nil {
		t.Fatal("empty tenant should error")
	}
	if _, err := c.DeleteByUser(context.Background(), "acme", ""); err == nil {
		t.Fatal("empty user should error")
	}
}

// spec: 12.8 (Compliance Interfaces: Tenant deletion lifecycle, Phase 4).
// DeleteByTenant removes every sticky assignment of every experiment and
// user in the tenant, leaves the other tenant's assignments, records no
// invalidation observation, and is idempotent.
func TestDeleteByTenant_PurgesTenantStickyKeysOnly_spec_12_8(t *testing.T) {
	rec := &countingRecorder{}
	c, mr := newCacheT(t, WithInvalidationRecorder(rec))
	ctx := context.Background()
	mustPut(t, c, "acme", "exp-1", "alice", "A")
	mustPut(t, c, "acme", "exp-2", "bob", "B")
	mustPut(t, c, "globex", "exp-1", "alice", "A")
	// A non-sticky key under the acme prefix is outside the sticky cache.
	if err := mr.Set("t:{acme}:exp:exp-1:config", "x"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	n, err := c.DeleteByTenant(ctx, "acme")
	if err != nil || n != 2 {
		t.Fatalf("DeleteByTenant = (%d, %v), want (2, nil)", n, err)
	}
	if _, ok, _ := c.Get(ctx, "acme", "exp-1", "alice"); ok {
		t.Fatal("acme/exp-1/alice survived tenant deletion")
	}
	if _, ok, _ := c.Get(ctx, "acme", "exp-2", "bob"); ok {
		t.Fatal("acme/exp-2/bob survived tenant deletion")
	}
	if _, ok, _ := c.Get(ctx, "globex", "exp-1", "alice"); !ok {
		t.Fatal("globex/exp-1/alice erased: cross-tenant deletion")
	}
	if !mr.Exists("t:{acme}:exp:exp-1:config") {
		t.Fatal("a non-sticky acme key was deleted")
	}
	if got := rec.count; got != 0 {
		t.Fatalf("invalidation observations = %d, want 0", got)
	}
	if n, err := c.DeleteByTenant(ctx, "acme"); err != nil || n != 0 {
		t.Fatalf("repeated DeleteByTenant = (%d, %v), want (0, nil)", n, err)
	}
}

// spec: 12.8 (Compliance Interfaces: Tenant deletion lifecycle, Phase 4).
func TestDeleteByTenant_RejectsEmptyTenant_spec_12_8(t *testing.T) {
	c, _ := newCacheT(t)
	if _, err := c.DeleteByTenant(context.Background(), " "); err == nil {
		t.Fatal("empty tenant should error")
	}
}

// spec: 12.8 (Compliance Interfaces: Tenant deletion lifecycle, Phase 4).
// A Redis error is wrapped and returned.
func TestDeleteByTenant_SurfacesRedisError_spec_12_8(t *testing.T) {
	c, mr := newCacheT(t)
	mr.SetError("LOADING redis is loading")
	if _, err := c.DeleteByTenant(context.Background(), "acme"); err == nil {
		t.Fatal("DeleteByTenant swallowed a Redis error")
	}
}
