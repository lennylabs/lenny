// SPDX-License-Identifier: MIT

package tenantdeletion_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/controller/tenantdeletion"
)

// spec: §12.8 lines 880-889 — the Phase 3.5 force-delete override path:
// once an operator force-deletes a held tenant, Phase 3.5 segregates the
// held evidence into the region-scoped escrow and the deletion proceeds
// instead of blocking. F-12.8.2, F-24.10.2, F-24.10.5.

// fakeEscrow records EscrowHolds calls and returns a programmable
// outcome or an unresolvable-region error.
type fakeEscrowMigrator struct {
	calls   int
	lastReq tenantdeletion.EscrowRequest
	outcome tenantdeletion.EscrowOutcome
	err     error
}

func (f *fakeEscrowMigrator) EscrowHolds(_ context.Context, req tenantdeletion.EscrowRequest) (tenantdeletion.EscrowOutcome, error) {
	f.calls++
	f.lastReq = req
	if f.err != nil {
		return tenantdeletion.EscrowOutcome{}, f.err
	}
	return f.outcome, nil
}

// fakeOverrideSink records the §12.8 gdpr.legal_hold_overridden_tenant
// emissions.
type fakeOverrideSink struct {
	calls int
	last  tenantdeletion.OverrideAppliedEvent
}

func (f *fakeOverrideSink) OverrideApplied(_ context.Context, ev tenantdeletion.OverrideAppliedEvent) {
	f.calls++
	f.last = ev
}

func reconcilerWithEscrow(t *testing.T, holds *fakeHolds, esc tenantdeletion.EscrowMigrator, sink tenantdeletion.OverrideSink) (*tenantdeletion.Reconciler, *tenantdeletion.Memory) {
	t.Helper()
	jobs := tenantdeletion.NewMemory()
	action := &fakeAction{}
	return &tenantdeletion.Reconciler{
		Jobs:       jobs,
		Disabler:   action,
		Terminator: action,
		Revoker:    action,
		Eraser:     &fakeEraser{counts: map[string]int{}},
		Cleaner:    action,
		Receipts:   &fakeReceipts{},
		LegalHolds: holds,
		Blocked:    &fakeBlockedSink{},
		Escrow:     esc,
		Override:   sink,
		Clock:      func() time.Time { return time.Unix(1700000000, 0).UTC() },
	}, jobs
}

// stampOverride simulates the admin force-delete endpoint authorizing the
// override on the reconstructed job.
func stampOverride(t *testing.T, jobs *tenantdeletion.Memory, tenantID, by, justification string) {
	t.Helper()
	if _, err := jobs.Update(context.Background(), tenantID, func(j *tenantdeletion.Job) error {
		j.OverrideHoldAck = true
		j.OverrideBy = by
		j.OverrideJustification = justification
		j.OverrideAt = time.Unix(1699999999, 0).UTC()
		return nil
	}); err != nil {
		t.Fatalf("stamp override: %v", err)
	}
}

func TestPhase35OverrideEscrowsAndProceeds_spec_12_8_880(t *testing.T) {
	holds := &fakeHolds{holds: []tenantdeletion.HeldResource{
		{ResourceType: "artifact", ResourceID: "art-1"},
	}}
	esc := &fakeEscrowMigrator{outcome: tenantdeletion.EscrowOutcome{
		ResolvedRegion:   "eu",
		EscrowKEKID:      "platform:legal_hold_escrow:eu",
		EscrowObjectKeys: []string{"legal-hold-escrow/acme/artifact/art-1"},
	}}
	sink := &fakeOverrideSink{}
	r, jobs := reconcilerWithEscrow(t, holds, esc, sink)
	ctx := context.Background()
	if err := r.Start(ctx, "acme", "T3"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Advance to Phase 3.5 first (so the job exists), then authorize the
	// override the way the admin endpoint would, and run to completion.
	stampOverride(t, jobs, "acme", "admin@acme", "business wind-down deadline")
	job := runToCompletion(t, r, "acme")

	if job.Phase != tenantdeletion.PhaseCompleted {
		t.Fatalf("phase = %q, want completed (override escrows and proceeds)", job.Phase)
	}
	if esc.calls == 0 {
		t.Fatal("EscrowHolds was never called on the override path")
	}
	if len(esc.lastReq.Holds) != 1 {
		t.Fatalf("escrow request holds = %v", esc.lastReq.Holds)
	}
	if job.EscrowRegion != "eu" || job.EscrowKEKID != "platform:legal_hold_escrow:eu" {
		t.Fatalf("escrow outcome not recorded on job: region=%q kek=%q", job.EscrowRegion, job.EscrowKEKID)
	}
	if len(job.OverriddenHolds) != 1 {
		t.Fatalf("overridden holds = %v", job.OverriddenHolds)
	}
	// §12.8: the gdpr.legal_hold_overridden_tenant event fires with the
	// override identity, justification, holds, and escrow keys.
	if sink.calls == 0 {
		t.Fatal("OverrideApplied never emitted")
	}
	if sink.last.OverrideBy != "admin@acme" || sink.last.Justification != "business wind-down deadline" {
		t.Fatalf("override event identity = %+v", sink.last)
	}
	if len(sink.last.EscrowObjectKeys) != 1 {
		t.Fatalf("override event escrow keys = %v", sink.last.EscrowObjectKeys)
	}
}

func TestPhase35OverrideUnresolvableRegionPauses_spec_12_8_883(t *testing.T) {
	holds := &fakeHolds{holds: []tenantdeletion.HeldResource{
		{ResourceType: "artifact", ResourceID: "art-1"},
	}}
	esc := &fakeEscrowMigrator{err: tenantdeletion.ErrEscrowRegionUnresolvable}
	sink := &fakeOverrideSink{}
	r, jobs := reconcilerWithEscrow(t, holds, esc, sink)
	ctx := context.Background()
	if err := r.Start(ctx, "acme", "T3"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	stampOverride(t, jobs, "acme", "admin@acme", "wind-down")
	for i := 0; i < 12; i++ {
		if err := r.ReconcileTenant(ctx, "acme"); err != nil {
			t.Fatalf("ReconcileTenant pass %d: %v", i, err)
		}
	}
	j, _ := jobs.Get(ctx, "acme")
	// §12.8 line 883: an unresolvable escrow region pauses at Phase 3.5
	// (not failed), pending operator remediation. The override is NOT
	// emitted (no escrow happened).
	if j.Phase != tenantdeletion.PhaseLegalHoldSegregation {
		t.Fatalf("phase = %q, want legal_hold_segregation (paused)", j.Phase)
	}
	if j.Phase == tenantdeletion.PhaseFailed {
		t.Fatal("unresolvable region must pause, not fail")
	}
	if j.BlockedReason == "" {
		t.Error("paused job must carry a BlockedReason")
	}
	if sink.calls != 0 {
		t.Errorf("OverrideApplied must not fire when escrow could not complete; got %d", sink.calls)
	}
}

func TestPhase35OverrideWithoutMigratorBlocks_spec_12_8_880(t *testing.T) {
	// A deployment with no escrow migrator wired cannot honor the override;
	// Phase 3.5 falls back to the fail-closed standard-path block rather
	// than destroying held evidence.
	holds := &fakeHolds{holds: []tenantdeletion.HeldResource{
		{ResourceType: "session", ResourceID: "sess-1"},
	}}
	sink := &fakeOverrideSink{}
	r, jobs := reconcilerWithEscrow(t, holds, nil /* no escrow */, sink)
	ctx := context.Background()
	if err := r.Start(ctx, "acme", "T3"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	stampOverride(t, jobs, "acme", "admin@acme", "wind-down")
	for i := 0; i < 10; i++ {
		_ = r.ReconcileTenant(ctx, "acme")
	}
	j, _ := jobs.Get(ctx, "acme")
	if j.Phase != tenantdeletion.PhaseLegalHoldSegregation {
		t.Fatalf("phase = %q, want legal_hold_segregation (blocked, no escrow)", j.Phase)
	}
	if sink.calls != 0 {
		t.Errorf("OverrideApplied fired without an escrow migrator; got %d", sink.calls)
	}
}

// A force-delete override on an unheld tenant is a normal deletion: the
// override fields are inert when Phase 3.5 finds no holds.
func TestPhase35OverrideUnheldTenantProceeds_spec_12_8_880(t *testing.T) {
	holds := &fakeHolds{} // no holds
	esc := &fakeEscrowMigrator{}
	sink := &fakeOverrideSink{}
	r, jobs := reconcilerWithEscrow(t, holds, esc, sink)
	ctx := context.Background()
	if err := r.Start(ctx, "acme", "T3"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	stampOverride(t, jobs, "acme", "admin@acme", "wind-down")
	job := runToCompletion(t, r, "acme")
	if job.Phase != tenantdeletion.PhaseCompleted {
		t.Fatalf("phase = %q, want completed", job.Phase)
	}
	if esc.calls != 0 {
		t.Errorf("EscrowHolds called on an unheld tenant; got %d", esc.calls)
	}
	if sink.calls != 0 {
		t.Errorf("OverrideApplied fired on an unheld tenant; got %d", sink.calls)
	}
}
