// SPDX-License-Identifier: MIT

// Coverage for the §12.8 step-4 experiment-sticky-assignment erasure:
// DeleteByUser DELs `t:{tenant_id}:exp:*:sticky:{user_id}` across every
// experiment the user participated in, leaving other users' and other
// tenants' assignments intact.
//
// spec: §12.8 line 786 (Experiment sticky assignment cache), step 4.
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
