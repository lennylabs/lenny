// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"testing"
	"time"

	apisession "github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/controller/tenantdeletion"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	sessionmem "github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
)

// spec: §12.8 line 865, lines 872-889 — the gateway-hosted §12.8
// tenant-deletion reconciler drives a tenant out of `disabling`/`deleting`
// through the erasure phases to the `deleted` tombstone, and pauses at
// Phase 3.5 while an active legal hold is in force. F-12.8.1, F-24.10.3.

// countingEraser is a single-store TenantEraser stub.
type countingEraser struct{ calls int }

func (e *countingEraser) erase(context.Context, string) (int, error) {
	e.calls++
	return 1, nil
}

func fixedClock() func() time.Time {
	return func() time.Time { return time.Date(2026, 6, 4, 0, 0, 0, 0, time.UTC) }
}

// newDeletionRunner builds a gateway runner over in-memory stores. It
// returns the runner, the tenant store, the session store, the audit
// appender, and the eraser so a test can drive and assert.
func newDeletionRunner(t *testing.T) (*tenantDeletionRunner, *tenantstore.Memory, *sessionmem.Store, *fakeAppender, *countingEraser) {
	t.Helper()
	tenants := tenantstore.NewMemory()
	sessions := sessionmem.New()
	appender := &fakeAppender{}
	eraser := &countingEraser{}
	clock := fixedClock()
	reconciler := &tenantdeletion.Reconciler{
		Jobs:       tenantdeletion.NewMemory(),
		Eraser:     &tenantEraser{erasers: []namedTenantEraser{{"sessions", eraser.erase}}},
		Disabler:   tenantStateDisabler{tenants: tenants, clock: clock},
		Receipts:   auditReceiptSink{appender: appender, clock: clock},
		Blocked:    tenantDeletionBlockedSink{appender: appender, clock: clock},
		LegalHolds: tenantHoldEnumerator{sessions: sessions},
		Clock:      clock,
	}
	runner := &tenantDeletionRunner{reconciler: reconciler, tenants: tenants, clock: clock, interval: time.Second}
	return runner, tenants, sessions, appender, eraser
}

// drive runs reconcileOnce n times.
func drive(ctx context.Context, runner *tenantDeletionRunner, n int) {
	for i := 0; i < n; i++ {
		runner.reconcileOnce(ctx)
	}
}

func TestTenantDeletionRunnerCompletesLifecycle_spec_12_8_865(t *testing.T) {
	runner, tenants, _, appender, eraser := newDeletionRunner(t)
	ctx := context.Background()
	if err := tenants.Create(ctx, tenantstore.Tenant{ID: "acme", State: tenantstore.TenantStateDisabling}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Enough passes to advance through every phase plus the tombstone.
	drive(ctx, runner, 12)

	row, err := tenants.Get(ctx, "acme")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if row.State != tenantstore.TenantStateDeleted {
		t.Fatalf("final tenant state = %q, want deleted (tombstoned)", row.State)
	}
	if row.DeletedAt.IsZero() {
		t.Error("a completed lifecycle must tombstone the row (DeletedAt set)")
	}
	if eraser.calls == 0 {
		t.Error("Phase 4 DeleteByTenant must have run at least once")
	}
	// The §12.8 Phase 6 erasure receipt is written as a gdpr.* audit event.
	var sawReceipt bool
	for _, c := range appender.snapshot() {
		if c.eventType == "gdpr.tenant_erased" && c.tenant == "acme" {
			sawReceipt = true
		}
	}
	if !sawReceipt {
		t.Error("the lifecycle must write a gdpr.tenant_erased receipt")
	}
}

func TestTenantDeletionRunnerBlocksThenResumesOnHold_spec_12_8_878(t *testing.T) {
	runner, tenants, sessions, appender, _ := newDeletionRunner(t)
	ctx := context.Background()
	if err := tenants.Create(ctx, tenantstore.Tenant{ID: "acme", State: tenantstore.TenantStateDisabling}); err != nil {
		t.Fatalf("Create tenant: %v", err)
	}
	// A held session blocks Phase 3.5.
	if err := sessions.Create(ctx, sessionstore.Session{
		ID: "sess-1", TenantID: "acme", UserID: "alice",
		State: apisession.StateRunning, LegalHold: true,
	}); err != nil {
		t.Fatalf("Create session: %v", err)
	}
	drive(ctx, runner, 12)

	row, _ := tenants.Get(ctx, "acme")
	if row.State == tenantstore.TenantStateDeleted {
		t.Fatal("a tenant with an active legal hold must NOT be tombstoned")
	}
	if row.State != tenantstore.TenantStateDeleting {
		t.Errorf("blocked tenant state = %q, want deleting (paused at Phase 3.5)", row.State)
	}
	// The deletion_blocked event fired exactly once across the passes.
	var blocked int
	for _, c := range appender.snapshot() {
		if c.eventType == "admin.tenant.deletion_blocked" {
			blocked++
		}
	}
	if blocked != 1 {
		t.Errorf("deletion_blocked emissions = %d, want exactly 1", blocked)
	}

	// Release the hold; the lifecycle resumes to the tombstone.
	if _, err := sessions.Update(ctx, "acme", "sess-1", func(s *sessionstore.Session) error {
		s.LegalHold = false
		return nil
	}); err != nil {
		t.Fatalf("release hold: %v", err)
	}
	drive(ctx, runner, 12)
	row, _ = tenants.Get(ctx, "acme")
	if row.State != tenantstore.TenantStateDeleted {
		t.Errorf("after releasing the hold, state = %q, want deleted", row.State)
	}
}

func TestTenantDeletionRunnerIgnoresActiveTenant_spec_12_8_865(t *testing.T) {
	runner, tenants, _, _, eraser := newDeletionRunner(t)
	ctx := context.Background()
	if err := tenants.Create(ctx, tenantstore.Tenant{ID: "acme", State: tenantstore.TenantStateActive}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	drive(ctx, runner, 5)
	row, _ := tenants.Get(ctx, "acme")
	if row.State != tenantstore.TenantStateActive {
		t.Errorf("an active tenant must be untouched, got state %q", row.State)
	}
	if eraser.calls != 0 {
		t.Errorf("an active tenant must not be erased, eraser calls = %d", eraser.calls)
	}
}
