// SPDX-License-Identifier: MIT

package tenantdeletion_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/controller/tenantdeletion"
	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/tenantkms"
)

// spec: §12.8 line 872, lines 878-889 — the §12.8 Phase 3.5 legal-hold
// segregation step. The standard path pauses the deletion while any
// active tenant-scoped hold is in force; an unblock resumes it. The
// override / escrow path (POST /v1/admin/tenants/{id}/force-delete) is
// tracked separately (F-12.8.2).

// fakeHolds returns a programmable set of active tenant holds and counts
// the calls so a test can release a hold mid-lifecycle.
type fakeHolds struct {
	calls int
	holds []tenantdeletion.HeldResource
	err   error
}

func (f *fakeHolds) ActiveTenantHolds(context.Context, string) ([]tenantdeletion.HeldResource, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.holds, nil
}

// fakeBlockedSink records the §12.8 admin.tenant.deletion_blocked
// emissions so a test can assert it fires once per block transition.
type fakeBlockedSink struct {
	calls int
	last  []tenantdeletion.HeldResource
}

func (f *fakeBlockedSink) DeletionBlocked(_ context.Context, _ string, holds []tenantdeletion.HeldResource) {
	f.calls++
	f.last = holds
}

// newReconcilerWithHolds builds a Reconciler with a wired legal-hold
// enumerator and blocked sink over a fresh in-memory job store.
func newReconcilerWithHolds(t *testing.T, holds *fakeHolds, blocked *fakeBlockedSink) (*tenantdeletion.Reconciler, *tenantdeletion.Memory) {
	t.Helper()
	seed := bytes.Repeat([]byte{0x5a}, kms.DEKSize)
	local, err := kms.NewLocal(seed)
	if err != nil {
		t.Fatalf("kms.NewLocal: %v", err)
	}
	jobs := tenantdeletion.NewMemory()
	action := &fakeAction{}
	r := &tenantdeletion.Reconciler{
		Jobs:       jobs,
		KMS:        tenantkms.New(tenantkms.NewLocalManager(local)),
		Disabler:   action,
		Terminator: action,
		Revoker:    action,
		Eraser:     &fakeEraser{counts: map[string]int{}},
		Cleaner:    action,
		Receipts:   &fakeReceipts{},
		LegalHolds: holds,
		Blocked:    blocked,
	}
	return r, jobs
}

func TestPhase35BlocksOnActiveHold_spec_12_8_878(t *testing.T) {
	holds := &fakeHolds{holds: []tenantdeletion.HeldResource{
		{ResourceType: "session", ResourceID: "sess-1"},
		{ResourceType: "artifact", ResourceID: "art-9"},
	}}
	blocked := &fakeBlockedSink{}
	r, jobs := newReconcilerWithHolds(t, holds, blocked)
	ctx := context.Background()
	if err := r.Start(ctx, "acme", "T3"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Advance well past the budget; the job must wedge at Phase 3.5, not
	// run to completion and not fail.
	for i := 0; i < 12; i++ {
		if err := r.ReconcileTenant(ctx, "acme"); err != nil {
			t.Fatalf("ReconcileTenant pass %d: %v", i, err)
		}
	}
	j, _ := jobs.Get(ctx, "acme")
	if j.Phase != tenantdeletion.PhaseLegalHoldSegregation {
		t.Fatalf("phase = %q, want legal_hold_segregation (paused)", j.Phase)
	}
	if j.State != tenantdeletion.TenantDeleting {
		t.Errorf("state = %q, want deleting", j.State)
	}
	if j.BlockedReason == "" {
		t.Error("a blocked job must carry a BlockedReason")
	}
	if len(j.BlockedHolds) != 2 {
		t.Errorf("BlockedHolds = %v, want the 2 active holds", j.BlockedHolds)
	}
	// The deletion_blocked audit event fires once per block transition,
	// not on every re-evaluation pass.
	if blocked.calls != 1 {
		t.Errorf("DeletionBlocked emissions = %d, want exactly 1 (block transition only)", blocked.calls)
	}
	if len(blocked.last) != 2 {
		t.Errorf("DeletionBlocked holds = %v, want the 2 held resources", blocked.last)
	}
}

func TestPhase35ResumesWhenHoldReleased_spec_12_8_878(t *testing.T) {
	holds := &fakeHolds{holds: []tenantdeletion.HeldResource{
		{ResourceType: "session", ResourceID: "sess-1"},
	}}
	blocked := &fakeBlockedSink{}
	r, jobs := newReconcilerWithHolds(t, holds, blocked)
	ctx := context.Background()
	if err := r.Start(ctx, "acme", "T3"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Drive into the Phase 3.5 block.
	for i := 0; i < 6; i++ {
		_ = r.ReconcileTenant(ctx, "acme")
	}
	j, _ := jobs.Get(ctx, "acme")
	if j.Phase != tenantdeletion.PhaseLegalHoldSegregation || j.BlockedReason == "" {
		t.Fatalf("expected the job blocked at Phase 3.5, got phase=%q reason=%q", j.Phase, j.BlockedReason)
	}
	// The operator releases the hold; the next pass clears the block and
	// the lifecycle runs to completion.
	holds.holds = nil
	j = runToCompletion(t, r, "acme")
	if j.Phase != tenantdeletion.PhaseCompleted {
		t.Fatalf("final phase = %q (%s), want completed", j.Phase, j.Failure)
	}
	if j.State != tenantdeletion.TenantDeleted {
		t.Errorf("final state = %q, want deleted", j.State)
	}
	if j.BlockedReason != "" || len(j.BlockedHolds) != 0 {
		t.Errorf("a resumed job must clear its blocked markers, got reason=%q holds=%v", j.BlockedReason, j.BlockedHolds)
	}
	// Still exactly one block event across the whole lifecycle — the
	// release does not re-emit.
	if blocked.calls != 1 {
		t.Errorf("DeletionBlocked emissions = %d, want exactly 1 over the lifecycle", blocked.calls)
	}
}

func TestPhase35EnumeratorErrorFailsJob_spec_12_8_878(t *testing.T) {
	// A transient enumeration error is a phase failure (retryable), not a
	// silent advance — the fail-closed posture must never destroy data on
	// an unreadable ledger.
	holds := &fakeHolds{err: errors.New("audit ledger unreachable")}
	r, jobs := newReconcilerWithHolds(t, holds, &fakeBlockedSink{})
	ctx := context.Background()
	if err := r.Start(ctx, "acme", "T3"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	for i := 0; i < 8; i++ {
		_ = r.ReconcileTenant(ctx, "acme")
		j, _ := jobs.Get(ctx, "acme")
		if j.Phase == tenantdeletion.PhaseFailed {
			break
		}
	}
	j, _ := jobs.Get(ctx, "acme")
	if j.Phase != tenantdeletion.PhaseFailed {
		t.Fatalf("phase = %q, want failed", j.Phase)
	}
	if j.Failure == "" {
		t.Error("an enumeration failure must record the cause")
	}
}

func TestPhase35NilEnumeratorAdvances_spec_12_8_878(t *testing.T) {
	// A deployment with no legal-hold surface wired advances Phase 3.5
	// unconditionally (fail-open is the documented nil posture).
	r, _ := newReconciler(t, &fakeEraser{counts: map[string]int{}}, &fakeReceipts{}, &fakeAction{})
	ctx := context.Background()
	if err := r.Start(ctx, "acme", "T3"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	j := runToCompletion(t, r, "acme")
	if j.Phase != tenantdeletion.PhaseCompleted {
		t.Fatalf("final phase = %q (%s), want completed", j.Phase, j.Failure)
	}
}

func TestLifecycleCompletesWithOnlyRequiredSeams_spec_12_8_865(t *testing.T) {
	// The gateway-hosted posture wires only the required Eraser + Receipts
	// (+ KMS); Disabler, Terminator, Revoker, Cleaner, and LegalHolds are
	// nil. The lifecycle must still run to completion.
	seed := bytes.Repeat([]byte{0x5a}, kms.DEKSize)
	local, _ := kms.NewLocal(seed)
	eraser := &fakeEraser{counts: map[string]int{"SessionStore": 3}}
	receipts := &fakeReceipts{}
	r := &tenantdeletion.Reconciler{
		Jobs:     tenantdeletion.NewMemory(),
		KMS:      tenantkms.New(tenantkms.NewLocalManager(local)),
		Eraser:   eraser,
		Receipts: receipts,
	}
	ctx := context.Background()
	if err := r.Start(ctx, "acme", "T3"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	j := runToCompletion(t, r, "acme")
	if j.Phase != tenantdeletion.PhaseCompleted {
		t.Fatalf("final phase = %q (%s), want completed", j.Phase, j.Failure)
	}
	if eraser.calls != 1 {
		t.Errorf("DeleteByTenant calls = %d, want 1", eraser.calls)
	}
	if len(receipts.written) != 1 {
		t.Errorf("receipts written = %d, want 1", len(receipts.written))
	}
}

func TestNilKMST4TenantCompletesWithoutDestroy_spec_12_5_301(t *testing.T) {
	// A probe-only host (nil KMS) cannot run control-plane Phase 4a; a T4
	// tenant's deletion still completes and the receipt records that the
	// key was not destroyed by the controller (operator destroys it
	// out-of-band per §12.5 line 301).
	eraser := &fakeEraser{counts: map[string]int{}}
	receipts := &fakeReceipts{}
	r := &tenantdeletion.Reconciler{
		Jobs:     tenantdeletion.NewMemory(),
		Eraser:   eraser,
		Receipts: receipts,
	}
	ctx := context.Background()
	if err := r.Start(ctx, "acme", tenantkms.WorkspaceTierT4); err != nil {
		t.Fatalf("Start: %v", err)
	}
	j := runToCompletion(t, r, "acme")
	if j.Phase != tenantdeletion.PhaseCompleted {
		t.Fatalf("final phase = %q (%s), want completed", j.Phase, j.Failure)
	}
	if j.KMSKeyDestroyed {
		t.Error("a nil-KMS host must not claim the T4 key was destroyed")
	}
}
