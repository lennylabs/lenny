// SPDX-License-Identifier: MIT

package storagequota_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/storagequota"
)

// spec: §11.2 per-tenant storage quota.

func TestReserveAdmitsWithinQuota(t *testing.T) {
	c := storagequota.NewMemory()
	ctx := context.Background()

	prior, err := c.Reserve(ctx, "acme", 100, 1000)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if prior != 0 {
		t.Errorf("priorUsed on first reserve: got %d, want 0", prior)
	}
	used, _ := c.Used(ctx, "acme")
	if used != 100 {
		t.Errorf("Used after reserve: got %d, want 100", used)
	}
}

func TestReserveAccumulates(t *testing.T) {
	c := storagequota.NewMemory()
	ctx := context.Background()
	if _, err := c.Reserve(ctx, "acme", 300, 1000); err != nil {
		t.Fatalf("first Reserve: %v", err)
	}
	prior, err := c.Reserve(ctx, "acme", 200, 1000)
	if err != nil {
		t.Fatalf("second Reserve: %v", err)
	}
	if prior != 300 {
		t.Errorf("priorUsed on second reserve: got %d, want 300", prior)
	}
	used, _ := c.Used(ctx, "acme")
	if used != 500 {
		t.Errorf("Used: got %d, want 500", used)
	}
}

func TestReserveRejectsOverQuota(t *testing.T) {
	c := storagequota.NewMemory()
	ctx := context.Background()
	if _, err := c.Reserve(ctx, "acme", 500, 1000); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	prior, err := c.Reserve(ctx, "acme", 600, 1000) // 500 + 600 > 1000
	if !errors.Is(err, storagequota.ErrQuotaExceeded) {
		t.Fatalf("over-quota reserve: got err %v, want ErrQuotaExceeded", err)
	}
	if prior != 500 {
		t.Errorf("priorUsed on rejection: got %d, want 500", prior)
	}
	used, _ := c.Used(ctx, "acme")
	if used != 500 {
		t.Errorf("a rejected reserve must not change the counter: got %d, want 500", used)
	}
}

func TestReserveAdmitsExactlyAtQuota(t *testing.T) {
	c := storagequota.NewMemory()
	if _, err := c.Reserve(context.Background(), "acme", 1000, 1000); err != nil {
		t.Errorf("a reserve that exactly fills the quota must be admitted: %v", err)
	}
}

func TestAdjustReleasesAndClampsAtZero(t *testing.T) {
	c := storagequota.NewMemory()
	ctx := context.Background()
	if _, err := c.Reserve(ctx, "acme", 800, 1000); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	// Reconcile a smaller-than-declared upload: release 300.
	if err := c.Adjust(ctx, "acme", -300); err != nil {
		t.Fatalf("Adjust: %v", err)
	}
	if used, _ := c.Used(ctx, "acme"); used != 500 {
		t.Errorf("Used after release: got %d, want 500", used)
	}
	// An over-large release clamps at zero rather than going negative.
	if err := c.Adjust(ctx, "acme", -9999); err != nil {
		t.Fatalf("Adjust: %v", err)
	}
	if used, _ := c.Used(ctx, "acme"); used != 0 {
		t.Errorf("Used after clamping release: got %d, want 0", used)
	}
}

func TestUsedIsPerTenant(t *testing.T) {
	c := storagequota.NewMemory()
	ctx := context.Background()
	if _, err := c.Reserve(ctx, "acme", 400, 1000); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if used, _ := c.Used(ctx, "globex"); used != 0 {
		t.Errorf("an untouched tenant must report 0 used, got %d", used)
	}
}

// spec: §11 line 37 — Set overwrites the counter with an absolute value
// (the rehydration write) and clamps a negative value to zero.
func TestSetOverwritesAndClamps(t *testing.T) {
	c := storagequota.NewMemory()
	ctx := context.Background()
	if _, err := c.Reserve(ctx, "acme", 400, 10000); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if err := c.Set(ctx, "acme", 900); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if used, _ := c.Used(ctx, "acme"); used != 900 {
		t.Errorf("Used after Set: got %d, want 900", used)
	}
	if err := c.Set(ctx, "acme", -5); err != nil {
		t.Fatalf("Set negative: %v", err)
	}
	if used, _ := c.Used(ctx, "acme"); used != 0 {
		t.Errorf("Set negative must clamp to 0, got %d", used)
	}
}

// spec: §11 line 37 — Rehydrate reconstructs each tenant's counter from
// the authoritative live-byte sum after a Redis restart.
func TestRehydrateReconstructsPerTenantCounters(t *testing.T) {
	c := storagequota.NewMemory()
	ctx := context.Background()
	sums := map[string]int64{"acme": 1234, "globex": 56}
	sizeOf := func(_ context.Context, tenantID string) (int64, error) {
		return sums[tenantID], nil
	}
	if err := storagequota.Rehydrate(ctx, c, []string{"acme", "globex", ""}, sizeOf); err != nil {
		t.Fatalf("Rehydrate: %v", err)
	}
	if used, _ := c.Used(ctx, "acme"); used != 1234 {
		t.Errorf("acme rehydrated = %d, want 1234", used)
	}
	if used, _ := c.Used(ctx, "globex"); used != 56 {
		t.Errorf("globex rehydrated = %d, want 56", used)
	}
}

// spec: §11 line 37 — a per-tenant read fault is collected and the
// sweep continues so one tenant cannot abort the rest.
func TestRehydrateCollectsErrorsAndContinues(t *testing.T) {
	c := storagequota.NewMemory()
	ctx := context.Background()
	boom := errors.New("boom")
	sizeOf := func(_ context.Context, tenantID string) (int64, error) {
		if tenantID == "globex" {
			return 0, boom
		}
		return 100, nil
	}
	err := storagequota.Rehydrate(ctx, c, []string{"acme", "globex"}, sizeOf)
	if err == nil {
		t.Fatal("Rehydrate should report the globex fault")
	}
	if used, _ := c.Used(ctx, "acme"); used != 100 {
		t.Errorf("acme must still rehydrate despite globex fault, got %d", used)
	}
}
