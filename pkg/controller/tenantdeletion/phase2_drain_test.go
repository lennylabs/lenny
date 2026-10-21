// SPDX-License-Identifier: MIT

package tenantdeletion_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/controller/tenantdeletion"
)

// scriptedTerminator is a Phase 2 SessionTerminator whose drain fails
// while err is set. It records every call so a test can count re-runs.
type scriptedTerminator struct {
	calls int
	err   error
}

func (f *scriptedTerminator) TerminateTenantSessions(context.Context, string) error {
	f.calls++
	return f.err
}

// newPhase2Reconciler builds a Reconciler with the given Terminator and
// a logger writing into the returned buffer.
func newPhase2Reconciler(term tenantdeletion.SessionTerminator) (*tenantdeletion.Reconciler, *tenantdeletion.Memory, *bytes.Buffer) {
	jobs := tenantdeletion.NewMemory()
	var logs bytes.Buffer
	r := &tenantdeletion.Reconciler{
		Jobs:       jobs,
		Terminator: term,
		Eraser:     &fakeEraser{counts: map[string]int{}},
		Receipts:   &fakeReceipts{},
		Logger:     slog.New(slog.NewTextHandler(&logs, nil)),
	}
	return r, jobs, &logs
}

// reconcileTo advances acme by n passes, failing the test on any
// returned error.
func reconcileTo(t *testing.T, r *tenantdeletion.Reconciler, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := r.ReconcileTenant(context.Background(), "acme"); err != nil {
			t.Fatalf("ReconcileTenant pass %d: %v", i, err)
		}
	}
}

// A drain that does not verify keeps the job at Phase 2 in state
// disabling with the drain error in BlockedReason, rather than failing
// it, and the next pass re-runs the drain and advances once it succeeds.
// Before the fix the drain error failed the job, which is terminal.
//
// spec: §12.8 (Tenant deletion lifecycle), §12.8 (Lifecycle fence and drain)
func TestPhase2DrainFailurePausesInPhaseAndRetries_spec_12_8(t *testing.T) {
	term := &scriptedTerminator{err: errors.New("session drain incomplete: 1 failed, 0 remaining")}
	r, jobs, logs := newPhase2Reconciler(term)
	ctx := context.Background()
	if err := r.Start(ctx, "acme", "T3"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Pass 1 runs Phase 1; passes 2 to 4 run the failing drain.
	reconcileTo(t, r, 4)

	j, _ := jobs.Get(ctx, "acme")
	if j.Phase != tenantdeletion.PhaseTerminateSessions {
		t.Fatalf("phase = %q (%s), want terminate_sessions", j.Phase, j.Failure)
	}
	// State is the discriminating assertion: the gateway runner copies it
	// onto the tenant row, and a deleting row resumes past the drain.
	if j.State != tenantdeletion.TenantDisabling {
		t.Errorf("state = %q, want disabling", j.State)
	}
	if !strings.Contains(j.BlockedReason, "session drain incomplete") {
		t.Errorf("BlockedReason = %q, want the drain error", j.BlockedReason)
	}
	if j.Failure != "" || !j.CompletedAt.IsZero() {
		t.Errorf("a paused job must not carry a failure, got Failure=%q CompletedAt=%v", j.Failure, j.CompletedAt)
	}
	if term.calls != 3 {
		t.Errorf("drain calls = %d, want 3 (one per pass at Phase 2)", term.calls)
	}
	if out := logs.String(); !strings.Contains(out, "level=WARN") || !strings.Contains(out, "tenant_id=acme") ||
		!strings.Contains(out, "session drain incomplete") {
		t.Errorf("pause log = %q, want a WARN naming the tenant and the drain error", out)
	}

	// The drain succeeds: the next pass advances and clears the reason.
	term.err = nil
	reconcileTo(t, r, 1)
	j, _ = jobs.Get(ctx, "acme")
	if j.Phase != tenantdeletion.PhaseRevokeCredentials {
		t.Fatalf("phase after a verified drain = %q, want revoke_credentials", j.Phase)
	}
	if j.BlockedReason != "" {
		t.Errorf("BlockedReason after advance = %q, want empty", j.BlockedReason)
	}
	if _, ok := j.PhaseLog[tenantdeletion.PhaseTerminateSessions]; !ok {
		t.Error("the verified drain must record Phase 2 in PhaseLog")
	}
	j = runToCompletion(t, r, "acme")
	if j.Phase != tenantdeletion.PhaseCompleted {
		t.Fatalf("final phase = %q (%s), want completed", j.Phase, j.Failure)
	}
}

// A paused tenant does not stop ReconcileAll from advancing the others.
//
// spec: §12.8 (Tenant deletion lifecycle), §12.8 (Lifecycle fence and drain)
func TestPhase2DrainPauseDoesNotBlockOtherTenants_spec_12_8(t *testing.T) {
	term := &tenantScopedTerminator{failing: map[string]bool{"acme": true}}
	r, jobs, _ := newPhase2Reconciler(term)
	ctx := context.Background()
	for _, id := range []string{"acme", "globex"} {
		if err := r.Start(ctx, id, "T3"); err != nil {
			t.Fatalf("Start %s: %v", id, err)
		}
	}
	for i := 0; i < 3; i++ {
		if err := r.ReconcileAll(ctx); err != nil {
			t.Fatalf("ReconcileAll pass %d: %v", i, err)
		}
	}
	acme, _ := jobs.Get(ctx, "acme")
	globex, _ := jobs.Get(ctx, "globex")
	if acme.Phase != tenantdeletion.PhaseTerminateSessions || acme.State != tenantdeletion.TenantDisabling {
		t.Errorf("acme = %q/%q, want terminate_sessions/disabling", acme.Phase, acme.State)
	}
	if globex.Phase != tenantdeletion.PhaseLegalHoldSegregation {
		t.Errorf("globex phase = %q, want legal_hold_segregation (two phases past Phase 2)", globex.Phase)
	}
}

// tenantScopedTerminator fails the drain for the tenants in failing.
type tenantScopedTerminator struct{ failing map[string]bool }

func (f *tenantScopedTerminator) TerminateTenantSessions(_ context.Context, tenantID string) error {
	if f.failing[tenantID] {
		return errors.New("drain list: primary unreachable")
	}
	return nil
}

// A reconciler with no Phase 2 Terminator is misconfigured: the job
// fails at Phase 2 rather than advancing to the erasers with the
// tenant's sessions still live. Before the fix a nil Terminator advanced.
//
// spec: §12.8 (Tenant deletion lifecycle), §12.8 (Lifecycle fence and drain)
func TestPhase2NilTerminatorFailsJob_spec_12_8(t *testing.T) {
	r, jobs, _ := newPhase2Reconciler(nil)
	ctx := context.Background()
	if err := r.Start(ctx, "acme", "T3"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	reconcileTo(t, r, 1)
	err := r.ReconcileTenant(ctx, "acme")
	if err == nil || !strings.Contains(err.Error(), "SessionTerminator") {
		t.Fatalf("ReconcileTenant at Phase 2 = %v, want the missing-terminator error", err)
	}
	j, _ := jobs.Get(ctx, "acme")
	if j.Phase != tenantdeletion.PhaseFailed {
		t.Fatalf("phase = %q, want failed", j.Phase)
	}
	if !strings.Contains(j.Failure, string(tenantdeletion.PhaseTerminateSessions)) {
		t.Errorf("Failure = %q, want it to name the Phase 2 phase", j.Failure)
	}
	if _, ok := j.PhaseLog[tenantdeletion.PhaseTerminateSessions]; ok {
		t.Error("a failed Phase 2 must not be recorded as completed")
	}
}
