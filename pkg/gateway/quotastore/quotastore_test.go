// SPDX-License-Identifier: MIT

package quotastore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/quotastore"
	"github.com/lennylabs/lenny/pkg/quota"
)

func newCounter(t *testing.T) *quotastore.Counter {
	t.Helper()
	mr := miniredis.RunT(t)
	cl := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = cl.Close() })
	return quotastore.New(cl)
}

// spec: §12.1 line 5 — the QuotaStore role interface exposes the
// mandatory erasure pair, enforced at compile time.
func TestCounterSatisfiesQuotaStore_spec_12_1(t *testing.T) {
	t.Parallel()
	var _ quotastore.QuotaStore = (*quotastore.Counter)(nil)
}

// spec: §12.8 line 753 — the erasure primitives reject empty scope.
func TestErasureRejectsEmptyScope_spec_12_8_line753(t *testing.T) {
	t.Parallel()
	c := newCounter(t)
	ctx := context.Background()
	if _, err := c.DeleteByUser(ctx, "", "alice@acme.com"); !errors.Is(err, quotastore.ErrEmptyScope) {
		t.Errorf("DeleteByUser(emptyTenant) err = %v, want ErrEmptyScope", err)
	}
	if _, err := c.DeleteByUser(ctx, "acme", ""); !errors.Is(err, quotastore.ErrEmptyScope) {
		t.Errorf("DeleteByUser(emptyUser) err = %v, want ErrEmptyScope", err)
	}
	if _, err := c.DeleteByTenant(ctx, ""); !errors.Is(err, quotastore.ErrEmptyScope) {
		t.Errorf("DeleteByTenant(empty) err = %v, want ErrEmptyScope", err)
	}
}

// spec: §12.1 line 5 / §12.8 step 6 — DeleteByUser removes every
// per-user counter (both fixed-window and sliding-window buckets) for
// the user, leaving the other user's counters intact.
func TestDeleteByUserScoped_spec_12_8_step6(t *testing.T) {
	t.Parallel()
	c := newCounter(t)
	ctx := context.Background()
	at := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)

	if _, err := c.Add(ctx, "acme", "alice@acme.com", quota.ResetDaily, at, 100); err != nil {
		t.Fatalf("Add alice daily: %v", err)
	}
	if _, err := c.SlidingAdd(ctx, "acme", "alice@acme.com", time.Hour, time.Minute, at, 50); err != nil {
		t.Fatalf("SlidingAdd alice: %v", err)
	}
	if _, err := c.Add(ctx, "acme", "bob@acme.com", quota.ResetDaily, at, 70); err != nil {
		t.Fatalf("Add bob: %v", err)
	}

	n, err := c.DeleteByUser(ctx, "acme", "alice@acme.com")
	if err != nil {
		t.Fatalf("DeleteByUser: %v", err)
	}
	if n < 2 {
		t.Errorf("DeleteByUser count = %d, want >= 2 (daily + sliding bucket)", n)
	}
	if got, _ := c.Usage(ctx, "acme", "alice@acme.com", quota.ResetDaily, at); got != 0 {
		t.Errorf("alice daily usage after erase = %d, want 0", got)
	}
	if got, _ := c.SlidingUsage(ctx, "acme", "alice@acme.com", time.Hour, time.Minute, at); got != 0 {
		t.Errorf("alice sliding usage after erase = %d, want 0", got)
	}
	// Bob's counter is untouched.
	if got, _ := c.Usage(ctx, "acme", "bob@acme.com", quota.ResetDaily, at); got != 70 {
		t.Errorf("bob daily usage = %d, want 70 (unaffected)", got)
	}
}

// spec: §12.1 line 5 / §12.8 Phase 4 — DeleteByTenant removes every
// token-usage counter for the tenant across users, leaving other
// tenants untouched, and is idempotent.
func TestDeleteByTenantScopedAndIdempotent_spec_12_8_phase4(t *testing.T) {
	t.Parallel()
	c := newCounter(t)
	ctx := context.Background()
	at := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)

	if _, err := c.Add(ctx, "acme", "alice@acme.com", quota.ResetDaily, at, 100); err != nil {
		t.Fatalf("Add acme/alice: %v", err)
	}
	if _, err := c.Add(ctx, "acme", "bob@acme.com", quota.ResetHourly, at, 30); err != nil {
		t.Fatalf("Add acme/bob: %v", err)
	}
	if _, err := c.Add(ctx, "globex", "carol@globex.com", quota.ResetDaily, at, 40); err != nil {
		t.Fatalf("Add globex/carol: %v", err)
	}

	n, err := c.DeleteByTenant(ctx, "acme")
	if err != nil {
		t.Fatalf("DeleteByTenant: %v", err)
	}
	if n != 2 {
		t.Errorf("DeleteByTenant count = %d, want 2", n)
	}
	if got, _ := c.Usage(ctx, "acme", "alice@acme.com", quota.ResetDaily, at); got != 0 {
		t.Errorf("acme/alice usage after tenant erase = %d, want 0", got)
	}
	if got, _ := c.Usage(ctx, "globex", "carol@globex.com", quota.ResetDaily, at); got != 40 {
		t.Errorf("globex/carol usage = %d, want 40 (unaffected)", got)
	}
	if n2, err := c.DeleteByTenant(ctx, "acme"); err != nil || n2 != 0 {
		t.Errorf("second DeleteByTenant = (%d, %v), want (0, nil)", n2, err)
	}
}

// TestAddHierarchicalScopesAreIndependent proves the §11.2 per-user,
// per-tenant, and global windows accumulate independently: a second
// user's tokens lift the tenant and global rollups but never the first
// user's per-user window. spec: §11.2 (global ⊇ tenant ⊇ user).
func TestAddHierarchicalScopesAreIndependent(t *testing.T) {
	t.Parallel()
	c := newCounter(t)
	ctx := context.Background()
	at := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	if _, err := c.AddHierarchical(ctx, "acme", "alice", quota.ResetHourly, at, 100); err != nil {
		t.Fatalf("AddHierarchical alice: %v", err)
	}
	if _, err := c.AddHierarchical(ctx, "acme", "bob", quota.ResetHourly, at, 20); err != nil {
		t.Fatalf("AddHierarchical bob: %v", err)
	}
	// A different tenant lifts only the global rollup.
	if _, err := c.AddHierarchical(ctx, "globex", "carol", quota.ResetHourly, at, 7); err != nil {
		t.Fatalf("AddHierarchical carol: %v", err)
	}

	alice, err := c.UsageHierarchical(ctx, "acme", "alice", quota.ResetHourly, at)
	if err != nil {
		t.Fatalf("UsageHierarchical alice: %v", err)
	}
	if alice.User != 100 {
		t.Errorf("alice.User = %d, want 100", alice.User)
	}
	if alice.Tenant != 120 {
		t.Errorf("acme tenant rollup = %d, want 120 (alice+bob)", alice.Tenant)
	}
	if alice.Global != 127 {
		t.Errorf("global rollup = %d, want 127 (alice+bob+carol)", alice.Global)
	}

	bob, _ := c.UsageHierarchical(ctx, "acme", "bob", quota.ResetHourly, at)
	if bob.User != 20 {
		t.Errorf("bob.User = %d, want 20 (independent of alice)", bob.User)
	}
}

// TestSlidingAddHierarchicalScopesAreIndependent mirrors the fixed-window
// case for the §11.2 rolling reset period.
func TestSlidingAddHierarchicalScopesAreIndependent(t *testing.T) {
	t.Parallel()
	c := newCounter(t)
	ctx := context.Background()
	at := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	if _, err := c.SlidingAddHierarchical(ctx, "acme", "alice", time.Hour, quotastore.DefaultBucketResolution, at, 40); err != nil {
		t.Fatalf("SlidingAddHierarchical alice: %v", err)
	}
	if _, err := c.SlidingAddHierarchical(ctx, "acme", "bob", time.Hour, quotastore.DefaultBucketResolution, at, 10); err != nil {
		t.Fatalf("SlidingAddHierarchical bob: %v", err)
	}
	got, err := c.SlidingUsageHierarchical(ctx, "acme", "alice", time.Hour, quotastore.DefaultBucketResolution, at)
	if err != nil {
		t.Fatalf("SlidingUsageHierarchical: %v", err)
	}
	if got.User != 40 {
		t.Errorf("alice rolling user window = %d, want 40", got.User)
	}
	if got.Tenant != 50 || got.Global != 50 {
		t.Errorf("rolling tenant/global rollups = (%d,%d), want 50 each", got.Tenant, got.Global)
	}
}

// TestDeleteByTenantErasesTenantRollup confirms a §12.8 tenant erasure
// clears the per-tenant rollup window along with the per-user windows,
// while the global rollup (under a synthetic tenant slot) survives.
func TestDeleteByTenantErasesTenantRollup(t *testing.T) {
	t.Parallel()
	c := newCounter(t)
	ctx := context.Background()
	at := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	if _, err := c.AddHierarchical(ctx, "acme", "alice", quota.ResetHourly, at, 100); err != nil {
		t.Fatalf("AddHierarchical: %v", err)
	}
	if _, err := c.DeleteByTenant(ctx, "acme"); err != nil {
		t.Fatalf("DeleteByTenant: %v", err)
	}
	got, _ := c.UsageHierarchical(ctx, "acme", "alice", quota.ResetHourly, at)
	if got.User != 0 || got.Tenant != 0 {
		t.Errorf("after tenant erase user/tenant = (%d,%d), want 0", got.User, got.Tenant)
	}
	if got.Global != 100 {
		t.Errorf("global rollup = %d, want 100 (a single tenant's erasure must not zero the platform window)", got.Global)
	}
}
