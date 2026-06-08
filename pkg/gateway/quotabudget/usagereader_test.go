// SPDX-License-Identifier: MIT

package quotabudget

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/quota"
)

// spec: §12.4 line 268 — the adapter reports the in-memory tenant usage on
// the tenant scope only; the per-user and platform-global scopes report
// zero so QuotaEvaluator gates solely on the tenant budget in this mode.
func TestUsageReader_TenantScopeOnly_spec_12_4_268(t *testing.T) {
	clk := &clock{t: time.Date(2026, 6, 8, 14, 0, 0, 0, time.UTC)}
	adder := newFakeAdder()
	tr := newTracker(t, 1000, 1, adder, clk)
	ctx := context.Background()

	r := NewUsageReader(tr)
	if _, err := r.UsageHierarchical(ctx, tenant, "alice", quota.ResetHourly, clk.now()); err != nil {
		t.Fatalf("UsageHierarchical (draw): %v", err)
	}
	tr.Add(ctx, tenant, quota.ResetHourly, clk.now(), 100)

	scoped, err := r.UsageHierarchical(ctx, tenant, "alice", quota.ResetHourly, clk.now())
	if err != nil {
		t.Fatalf("UsageHierarchical: %v", err)
	}
	if scoped.Tenant != 100 {
		t.Fatalf("scoped.Tenant = %d, want 100", scoped.Tenant)
	}
	if scoped.User != 0 || scoped.Global != 0 {
		t.Fatalf("scoped.User=%d scoped.Global=%d, want both 0 (not tracked in this mode)", scoped.User, scoped.Global)
	}
}

func TestUsageReader_SlidingReturnsZero(t *testing.T) {
	clk := &clock{t: time.Date(2026, 6, 8, 14, 0, 0, 0, time.UTC)}
	tr := newTracker(t, 1000, 1, newFakeAdder(), clk)
	r := NewUsageReader(tr)
	scoped, err := r.SlidingUsageHierarchical(context.Background(), tenant, "alice", time.Hour, time.Minute, clk.now())
	if err != nil {
		t.Fatalf("SlidingUsageHierarchical: %v", err)
	}
	if scoped.User != 0 || scoped.Tenant != 0 || scoped.Global != 0 {
		t.Fatalf("rolling-period read = %+v, want all-zero", scoped)
	}
}
