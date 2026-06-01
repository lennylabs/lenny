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
