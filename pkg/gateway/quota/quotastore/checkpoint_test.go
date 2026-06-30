// SPDX-License-Identifier: MIT

package quotastore_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/quota/quotastore"
	"github.com/lennylabs/lenny/pkg/quota"
)

// spec: §12.4 — WindowLabel returns a stable label for the fixed-interval
// periods and an error for the rolling period (no single bucket).
func TestWindowLabel_spec_12_4(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 6, 2, 14, 30, 0, 0, time.UTC)
	for _, tc := range []struct {
		period quota.ResetPeriod
		want   string
	}{
		{quota.ResetHourly, "hourly-2026060214"},
		{quota.ResetDaily, "daily-20260602"},
		{quota.ResetMonthly, "monthly-202606"},
	} {
		got, err := quotastore.WindowLabel(tc.period, at)
		if err != nil {
			t.Fatalf("WindowLabel(%s) err = %v", tc.period, err)
		}
		if got != tc.want {
			t.Errorf("WindowLabel(%s) = %q, want %q", tc.period, got, tc.want)
		}
	}
	if _, err := quotastore.WindowLabel(quota.ResetRolling, at); err == nil {
		t.Error("WindowLabel(rolling) err = nil, want error")
	}
}

// spec: §11.2 line 44 — TenantRollupUsage reads the per-tenant rollup
// window AddHierarchical advances, so the checkpoint persists the
// tenant-scope counter the reconstruction restores.
func TestTenantRollupUsage_spec_11_2(t *testing.T) {
	t.Parallel()
	c := newCounter(t)
	ctx := context.Background()
	at := time.Now().UTC()
	if _, err := c.AddHierarchical(ctx, "acme", "alice@acme.com", quota.ResetHourly, at, 700); err != nil {
		t.Fatalf("AddHierarchical: %v", err)
	}
	got, err := c.TenantRollupUsage(ctx, "acme", quota.ResetHourly, at)
	if err != nil {
		t.Fatalf("TenantRollupUsage: %v", err)
	}
	if got != 700 {
		t.Errorf("TenantRollupUsage = %d, want 700", got)
	}
}

// spec: §11.2 line 48 — RestoreUserWindow raises a counter to the
// checkpoint when the checkpoint exceeds the live value (Redis came back
// empty) and leaves a higher live value intact.
func TestRestoreUserWindowMaxRule_spec_11_2_line48(t *testing.T) {
	t.Parallel()
	c := newCounter(t)
	ctx := context.Background()
	at := time.Now().UTC()

	// Redis empty: restore lifts the counter to the checkpoint.
	written, err := c.RestoreUserWindow(ctx, "acme", "alice@acme.com", quota.ResetHourly, at, 500)
	if err != nil {
		t.Fatalf("RestoreUserWindow(empty): %v", err)
	}
	if written != 500 {
		t.Errorf("restore into empty = %d, want 500", written)
	}
	if got, _ := c.Usage(ctx, "acme", "alice@acme.com", quota.ResetHourly, at); got != 500 {
		t.Errorf("Usage after restore = %d, want 500", got)
	}

	// Live value already higher: a stale checkpoint must not lower it.
	if _, err := c.Add(ctx, "acme", "alice@acme.com", quota.ResetHourly, at, 300); err != nil {
		t.Fatalf("Add: %v", err) // 500 + 300 = 800
	}
	written, err = c.RestoreUserWindow(ctx, "acme", "alice@acme.com", quota.ResetHourly, at, 600)
	if err != nil {
		t.Fatalf("RestoreUserWindow(stale): %v", err)
	}
	if written != 800 {
		t.Errorf("restore with lower checkpoint = %d, want 800 (live preserved)", written)
	}
}

// spec: §11.2 line 48 — RestoreTenantRollupWindow applies the MAX rule to
// the tenant-scope rollup the §11.2 line 48 sentence names.
func TestRestoreTenantRollupWindowMaxRule_spec_11_2_line48(t *testing.T) {
	t.Parallel()
	c := newCounter(t)
	ctx := context.Background()
	at := time.Now().UTC()
	written, err := c.RestoreTenantRollupWindow(ctx, "acme", quota.ResetHourly, at, 1200)
	if err != nil {
		t.Fatalf("RestoreTenantRollupWindow: %v", err)
	}
	if written != 1200 {
		t.Errorf("restore = %d, want 1200", written)
	}
	if got, _ := c.TenantRollupUsage(ctx, "acme", quota.ResetHourly, at); got != 1200 {
		t.Errorf("TenantRollupUsage after restore = %d, want 1200", got)
	}
}

// spec: §11.2 line 48 — a negative checkpoint value is rejected; the
// rolling period has no restorable fixed window.
func TestRestoreWindowRejectsBadInput_spec_11_2(t *testing.T) {
	t.Parallel()
	c := newCounter(t)
	ctx := context.Background()
	at := time.Now().UTC()
	if _, err := c.RestoreUserWindow(ctx, "acme", "alice@acme.com", quota.ResetHourly, at, -1); err == nil {
		t.Error("RestoreUserWindow(negative) err = nil, want error")
	}
	if _, err := c.RestoreUserWindow(ctx, "acme", "alice@acme.com", quota.ResetRolling, at, 10); err == nil {
		t.Error("RestoreUserWindow(rolling) err = nil, want error")
	}
}
