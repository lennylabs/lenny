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
