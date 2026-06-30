// SPDX-License-Identifier: MIT

package erasurejob_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/erasurejob"
)

// recordingLock is a §12.8 line 856 advisory-lock seam that records the
// order in which the protected critical sections run, so a test can assert
// the lock actually serializes a rotation against a pseudonymize.
type recordingLock struct {
	mu     sync.Mutex
	events []string
}

func (l *recordingLock) WithSaltLock(ctx context.Context, tenantID string, fn func(context.Context) error) error {
	l.mu.Lock()
	l.events = append(l.events, "enter:"+tenantID)
	l.mu.Unlock()
	err := fn(ctx)
	l.mu.Lock()
	l.events = append(l.events, "exit:"+tenantID)
	l.mu.Unlock()
	return err
}

// spec: §12.8 lines 856-857 — RotateErasureSalt generates and stores a
// fresh per-tenant salt, replacing any prior one. F-12.8.5.
func TestRotateErasureSaltStoresFreshSalt_spec_12_8_857(t *testing.T) {
	ctx := context.Background()
	tenants := tenantstore.NewMemory()
	seedTenant(t, tenants, tenantstore.Tenant{ID: "acme", ErasureSalt: []byte("old-salt-old-salt-old-salt-old01")})

	eraser := erasurejob.NewBillingEraser(billingstore.NewMemory(), tenants)
	if err := eraser.RotateErasureSalt(ctx, "acme"); err != nil {
		t.Fatalf("RotateErasureSalt: %v", err)
	}
	tn, _ := tenants.Get(ctx, "acme")
	if len(tn.ErasureSalt) != 32 {
		t.Fatalf("rotated salt is %d bytes, want a fresh 256-bit salt", len(tn.ErasureSalt))
	}
	if string(tn.ErasureSalt) == "old-salt-old-salt-old-salt-old01" {
		t.Error("§12.8 line 857: the old salt must be replaced, not retained")
	}
}

// spec: §12.8 line 855 — a tenant exempt from billing erasure has no salt,
// so rotation returns ErrBillingErasureExempt. F-12.8.5.
func TestRotateErasureSaltExemptTenant_spec_12_8_855(t *testing.T) {
	ctx := context.Background()
	tenants := tenantstore.NewMemory()
	seedTenant(t, tenants, tenantstore.Tenant{ID: "acme", BillingErasurePolicy: tenantstore.BillingErasureExempt})

	eraser := erasurejob.NewBillingEraser(billingstore.NewMemory(), tenants)
	if err := eraser.RotateErasureSalt(ctx, "acme"); !errors.Is(err, erasurejob.ErrBillingErasureExempt) {
		t.Fatalf("RotateErasureSalt on exempt tenant = %v, want ErrBillingErasureExempt", err)
	}
}

func TestRotateErasureSaltUnknownTenant(t *testing.T) {
	eraser := erasurejob.NewBillingEraser(billingstore.NewMemory(), tenantstore.NewMemory())
	if err := eraser.RotateErasureSalt(context.Background(), "ghost"); err == nil {
		t.Error("RotateErasureSalt must error for a tenant not in the registry")
	}
}

// spec: §12.8 line 856 — both the pseudonymize and the rotation run under
// the advisory lock when one is wired, so the migration and erasure never
// race. F-12.8.5.
func TestSaltRotationLockWrapsBothPaths_spec_12_8_856(t *testing.T) {
	ctx := context.Background()
	tenants := tenantstore.NewMemory()
	seedTenant(t, tenants, tenantstore.Tenant{ID: "acme"})
	billing := billingstore.NewMemory()
	seedBilling(t, billing, "acme", "alice@acme", 1)

	lock := &recordingLock{}
	eraser := erasurejob.NewBillingEraser(billing, tenants).WithRotationLock(lock)

	if _, err := eraser.Pseudonymize(ctx, "acme", "alice@acme"); err != nil {
		t.Fatalf("Pseudonymize: %v", err)
	}
	if err := eraser.RotateErasureSalt(ctx, "acme"); err != nil {
		t.Fatalf("RotateErasureSalt: %v", err)
	}
	// Each critical section enters and exits the lock exactly once.
	want := []string{"enter:acme", "exit:acme", "enter:acme", "exit:acme"}
	if len(lock.events) != len(want) {
		t.Fatalf("lock events = %v, want %v", lock.events, want)
	}
	for i := range want {
		if lock.events[i] != want[i] {
			t.Fatalf("lock event %d = %q, want %q", i, lock.events[i], want[i])
		}
	}
}

// spec: §12.8 line 856 — a nil lock leaves both paths unsynchronized,
// which is the single-process in-memory default. F-12.8.5.
func TestSaltRotationNilLockIsNoOp_spec_12_8_856(t *testing.T) {
	ctx := context.Background()
	tenants := tenantstore.NewMemory()
	seedTenant(t, tenants, tenantstore.Tenant{ID: "acme"})

	eraser := erasurejob.NewBillingEraser(billingstore.NewMemory(), tenants) // no lock
	if err := eraser.RotateErasureSalt(ctx, "acme"); err != nil {
		t.Fatalf("RotateErasureSalt with nil lock: %v", err)
	}
}

// spec: §12.8 line 853 — the caller's source salt slice is not corrupted
// by the in-memory zeroing of the eraser's working copy; the persisted
// pseudonyms remain derivable from the original salt. F-12.8.5.
func TestPseudonymizeDoesNotZeroCallerSalt_spec_12_8_853(t *testing.T) {
	ctx := context.Background()
	knownSalt := []byte("0123456789abcdef0123456789abcdef")
	saltCopy := append([]byte(nil), knownSalt...)
	tenants := tenantstore.NewMemory()
	seedTenant(t, tenants, tenantstore.Tenant{ID: "acme", ErasureSalt: knownSalt})
	billing := billingstore.NewMemory()
	seedBilling(t, billing, "acme", "alice@acme", 1)

	eraser := erasurejob.NewBillingEraser(billing, tenants)
	if _, err := eraser.Pseudonymize(ctx, "acme", "alice@acme"); err != nil {
		t.Fatalf("Pseudonymize: %v", err)
	}
	// The §12.8 line 853 in-memory zeroing operates on the eraser's working
	// copy (the tenant store clones ErasureSalt on Get), so the original
	// salt is intact and the persisted pseudonyms remain derivable from it.
	want := billingstore.Pseudonymize("alice@acme", saltCopy)
	events, _ := billing.Since(ctx, "acme", 0, 0)
	if events[0].UserID != want {
		t.Errorf("pseudonym = %q, want %q (derived from the original salt)", events[0].UserID, want)
	}
}
