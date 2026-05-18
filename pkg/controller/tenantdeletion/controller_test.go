// SPDX-License-Identifier: MIT

package tenantdeletion_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/controller/tenantdeletion"
	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/tenantkms"
)

// spec: §12.8 ("Tenant deletion lifecycle", "Idempotency and
// resumption") / §12.9 — the tenant-deletion controller drives a
// tenant marked for deletion through the six-phase erasure flow,
// resumes from the persisted phase after an interruption, and runs the
// §12.8 Phase 4a per-tenant KMS-key destruction for T4 tenants.

// fakeEraser records its DeleteByTenant calls and returns a fixed
// per-store tally; an err field forces a Phase 4 failure.
type fakeEraser struct {
	calls  int
	counts map[string]int
	err    error
}

func (f *fakeEraser) DeleteByTenant(context.Context, string) (map[string]int, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.counts, nil
}

// fakeAction is a one-method seam stub shared by the soft-disable,
// session-terminate, credential-revoke, and CRD-clean phases. It
// records call count and can force an error.
type fakeAction struct {
	calls int
	err   error
}

func (f *fakeAction) do() error {
	f.calls++
	return f.err
}

func (f *fakeAction) SoftDisableTenant(context.Context, string) error       { return f.do() }
func (f *fakeAction) TerminateTenantSessions(context.Context, string) error { return f.do() }
func (f *fakeAction) RevokeTenantCredentials(context.Context, string) error { return f.do() }
func (f *fakeAction) CleanTenantCRDs(context.Context, string) error         { return f.do() }

// fakeReceipts records the §12.8 Phase 6 receipt the controller writes.
type fakeReceipts struct {
	written []tenantdeletion.Receipt
	err     error
}

func (f *fakeReceipts) WriteReceipt(_ context.Context, r tenantdeletion.Receipt) error {
	if f.err != nil {
		return f.err
	}
	f.written = append(f.written, r)
	return nil
}

// newReconciler builds a Reconciler with the given seams and a fresh
// in-memory job store and tenantkms Lifecycle.
func newReconciler(t *testing.T, eraser tenantdeletion.TenantEraser, receipts tenantdeletion.ReceiptSink, action *fakeAction) (*tenantdeletion.Reconciler, *tenantdeletion.Memory) {
	t.Helper()
	seed := bytes.Repeat([]byte{0x5a}, kms.DEKSize)
	local, err := kms.NewLocal(seed)
	if err != nil {
		t.Fatalf("kms.NewLocal: %v", err)
	}
	jobs := tenantdeletion.NewMemory()
	r := &tenantdeletion.Reconciler{
		Jobs:       jobs,
		KMS:        tenantkms.New(tenantkms.NewLocalManager(local)),
		Disabler:   action,
		Terminator: action,
		Revoker:    action,
		Eraser:     eraser,
		Cleaner:    action,
		Receipts:   receipts,
	}
	return r, jobs
}

// runToCompletion advances the named tenant's job until it reaches a
// terminal phase or the pass budget is exhausted.
func runToCompletion(t *testing.T, r *tenantdeletion.Reconciler, tenantID string) tenantdeletion.Job {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < 20; i++ {
		if err := r.ReconcileTenant(ctx, tenantID); err != nil {
			t.Fatalf("ReconcileTenant pass %d: %v", i, err)
		}
		j, err := r.Jobs.Get(ctx, tenantID)
		if err != nil {
			t.Fatalf("Get job: %v", err)
		}
		if j.Phase.Terminal() {
			return j
		}
	}
	t.Fatalf("tenant %q did not reach a terminal phase within the pass budget", tenantID)
	return tenantdeletion.Job{}
}

func TestStartCreatesJobAtPhaseOne(t *testing.T) {
	r, jobs := newReconciler(t, &fakeEraser{}, &fakeReceipts{}, &fakeAction{})
	if err := r.Start(context.Background(), "acme", "T3"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	j, err := jobs.Get(context.Background(), "acme")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if j.Phase != tenantdeletion.PhaseSoftDisable {
		t.Errorf("phase = %q, want soft_disable", j.Phase)
	}
	if j.State != tenantdeletion.TenantDisabling {
		t.Errorf("state = %q, want disabling", j.State)
	}
}

func TestStartRejectsDuplicate(t *testing.T) {
	r, _ := newReconciler(t, &fakeEraser{}, &fakeReceipts{}, &fakeAction{})
	ctx := context.Background()
	if err := r.Start(ctx, "acme", "T3"); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	if err := r.Start(ctx, "acme", "T3"); !errors.Is(err, tenantdeletion.ErrAlreadyExists) {
		t.Errorf("duplicate Start error = %v, want ErrAlreadyExists", err)
	}
}

func TestLifecycleRunsAllPhasesAndProducesReceipt(t *testing.T) {
	eraser := &fakeEraser{counts: map[string]int{"SessionStore": 7, "TokenStore": 2}}
	receipts := &fakeReceipts{}
	action := &fakeAction{}
	r, _ := newReconciler(t, eraser, receipts, action)

	if err := r.Start(context.Background(), "acme", "T3"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	j := runToCompletion(t, r, "acme")

	if j.Phase != tenantdeletion.PhaseCompleted {
		t.Fatalf("final phase = %q (%s), want completed", j.Phase, j.Failure)
	}
	if j.State != tenantdeletion.TenantDeleted {
		t.Errorf("final state = %q, want deleted", j.State)
	}
	// §12.8 Phase 4 ran DeleteByTenant exactly once and the tally is
	// carried into the receipt.
	if eraser.calls != 1 {
		t.Errorf("DeleteByTenant calls = %d, want 1", eraser.calls)
	}
	if len(receipts.written) != 1 {
		t.Fatalf("receipts written = %d, want 1", len(receipts.written))
	}
	rcpt := receipts.written[0]
	if rcpt.DeletedCounts["SessionStore"] != 7 || rcpt.DeletedCounts["TokenStore"] != 2 {
		t.Errorf("receipt DeletedCounts = %v, want the Phase 4 tally", rcpt.DeletedCounts)
	}
	// The §12.8 Phase 6 receipt records every phase's completion time.
	for _, p := range []tenantdeletion.Phase{
		tenantdeletion.PhaseSoftDisable, tenantdeletion.PhaseTerminateSessions,
		tenantdeletion.PhaseRevokeCredentials, tenantdeletion.PhaseDeleteData,
		tenantdeletion.PhaseDestroyKMSKey, tenantdeletion.PhaseCleanCRDs,
	} {
		if rcpt.PhaseTimestamps[p].IsZero() {
			t.Errorf("receipt missing completion timestamp for phase %q", p)
		}
	}
}

func TestT4TenantDestroysKMSKey(t *testing.T) {
	// §12.8 Phase 4a / §12.9: a T4 tenant's per-tenant KMS key is
	// destroyed during deletion.
	seed := bytes.Repeat([]byte{0x5a}, kms.DEKSize)
	local, _ := kms.NewLocal(seed)
	mgr := tenantkms.NewLocalManager(local)
	kmsLifecycle := tenantkms.New(mgr)

	// Provision the T4 tenant key up front, as the tenant-create hook
	// would have.
	ctx := context.Background()
	if _, err := kmsLifecycle.EnsureForTenant(ctx, "acme", tenantkms.WorkspaceTierT4); err != nil {
		t.Fatalf("provision T4 key: %v", err)
	}

	receipts := &fakeReceipts{}
	action := &fakeAction{}
	r := &tenantdeletion.Reconciler{
		Jobs:       tenantdeletion.NewMemory(),
		KMS:        kmsLifecycle,
		Disabler:   action,
		Terminator: action,
		Revoker:    action,
		Eraser:     &fakeEraser{counts: map[string]int{}},
		Cleaner:    action,
		Receipts:   receipts,
	}
	if err := r.Start(ctx, "acme", tenantkms.WorkspaceTierT4); err != nil {
		t.Fatalf("Start: %v", err)
	}
	j := runToCompletion(t, r, "acme")
	if j.Phase != tenantdeletion.PhaseCompleted {
		t.Fatalf("final phase = %q (%s)", j.Phase, j.Failure)
	}
	if !j.KMSKeyDestroyed {
		t.Error("a T4 tenant's deletion job must record KMSKeyDestroyed = true")
	}
	// The key is in fact destroyed in KMS.
	info, err := mgr.KeyInfoFor(ctx, tenantkms.AliasFor("acme"))
	if err != nil {
		t.Fatalf("KeyInfoFor: %v", err)
	}
	if info.State != tenantkms.KeyStateDestroyed {
		t.Errorf("tenant key state = %q, want destroyed", info.State)
	}
	if !receipts.written[0].KMSKeyDestroyed {
		t.Error("the §12.8 Phase 6 receipt must record the cryptographic erasure")
	}
}

func TestNonT4TenantSkipsKMSDestruction(t *testing.T) {
	// §12.9: only T4 tenants have a per-tenant KMS key. A T3 tenant's
	// Phase 4a is a no-op.
	r, _ := newReconciler(t, &fakeEraser{counts: map[string]int{}}, &fakeReceipts{}, &fakeAction{})
	if err := r.Start(context.Background(), "acme", "T3"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	j := runToCompletion(t, r, "acme")
	if j.Phase != tenantdeletion.PhaseCompleted {
		t.Fatalf("final phase = %q (%s)", j.Phase, j.Failure)
	}
	if j.KMSKeyDestroyed {
		t.Error("a non-T4 tenant has no per-tenant key; KMSKeyDestroyed must be false")
	}
}

func TestAdvancesOnePhasePerPass(t *testing.T) {
	// §12.8: the controller advances one phase per reconcile pass.
	r, _ := newReconciler(t, &fakeEraser{counts: map[string]int{}}, &fakeReceipts{}, &fakeAction{})
	ctx := context.Background()
	if err := r.Start(ctx, "acme", "T3"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := r.ReconcileTenant(ctx, "acme"); err != nil {
		t.Fatalf("ReconcileTenant: %v", err)
	}
	j, _ := r.Jobs.Get(ctx, "acme")
	// After one pass the soft-disable phase is done; the next phase is
	// terminate_sessions.
	if j.Phase != tenantdeletion.PhaseTerminateSessions {
		t.Errorf("phase after one pass = %q, want terminate_sessions", j.Phase)
	}
}

func TestResumesFromPersistedPhaseAfterInterruption(t *testing.T) {
	// §12.8 "Idempotency and resumption": the controller persists the
	// phase; a restart resumes from it. Simulate a crash by building a
	// new Reconciler over the same job store and confirming it
	// continues, not restarts.
	eraser := &fakeEraser{counts: map[string]int{"SessionStore": 1}}
	seed := bytes.Repeat([]byte{0x5a}, kms.DEKSize)
	local, _ := kms.NewLocal(seed)
	jobs := tenantdeletion.NewMemory()

	action1 := &fakeAction{}
	r1 := &tenantdeletion.Reconciler{
		Jobs: jobs, KMS: tenantkms.New(tenantkms.NewLocalManager(local)),
		Disabler: action1, Terminator: action1, Revoker: action1,
		Eraser: eraser, Cleaner: action1, Receipts: &fakeReceipts{},
	}
	ctx := context.Background()
	if err := r1.Start(ctx, "acme", "T3"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Advance two phases, then "crash".
	_ = r1.ReconcileTenant(ctx, "acme")
	_ = r1.ReconcileTenant(ctx, "acme")
	mid, _ := jobs.Get(ctx, "acme")
	if mid.Phase != tenantdeletion.PhaseRevokeCredentials {
		t.Fatalf("phase after two passes = %q, want revoke_credentials", mid.Phase)
	}

	// A fresh Reconciler over the same store resumes from the
	// persisted phase.
	action2 := &fakeAction{}
	receipts2 := &fakeReceipts{}
	r2 := &tenantdeletion.Reconciler{
		Jobs: jobs, KMS: tenantkms.New(tenantkms.NewLocalManager(local)),
		Disabler: action2, Terminator: action2, Revoker: action2,
		Eraser: eraser, Cleaner: action2, Receipts: receipts2,
	}
	j := runToCompletion(t, r2, "acme")
	if j.Phase != tenantdeletion.PhaseCompleted {
		t.Fatalf("final phase = %q (%s)", j.Phase, j.Failure)
	}
	// The resumed controller did NOT re-run Phase 1 (soft-disable) or
	// Phase 2 (terminate) — those completed before the crash.
	if action2.calls != 2 {
		// action2 backs revoke (Phase 3) and clean (Phase 5) only;
		// soft-disable and terminate ran on action1.
		t.Errorf("resumed controller ran %d shared-seam actions, want 2 (revoke + clean only)", action2.calls)
	}
}

func TestPhaseFailureMarksJobFailed(t *testing.T) {
	// §12.8: a phase error marks the job failed with the cause; the
	// controller does not advance past the failed phase.
	eraser := &fakeEraser{err: errors.New("postgres unreachable")}
	r, _ := newReconciler(t, eraser, &fakeReceipts{}, &fakeAction{})
	ctx := context.Background()
	if err := r.Start(ctx, "acme", "T3"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Advance to Phase 4 (delete_data), which fails.
	var lastErr error
	for i := 0; i < 10; i++ {
		lastErr = r.ReconcileTenant(ctx, "acme")
		j, _ := r.Jobs.Get(ctx, "acme")
		if j.Phase == tenantdeletion.PhaseFailed {
			break
		}
	}
	j, _ := r.Jobs.Get(ctx, "acme")
	if j.Phase != tenantdeletion.PhaseFailed {
		t.Fatalf("phase = %q, want failed", j.Phase)
	}
	if lastErr == nil {
		t.Error("ReconcileTenant should surface the phase failure")
	}
	if j.Failure == "" {
		t.Error("a failed job must record the failure cause")
	}
}

func TestRetryResumesFailedJob(t *testing.T) {
	// §12.8: a failed §12.8 deletion job can be retried from the
	// recorded phase.
	eraser := &fakeEraser{err: errors.New("postgres unreachable")}
	r, _ := newReconciler(t, eraser, &fakeReceipts{}, &fakeAction{})
	ctx := context.Background()
	if err := r.Start(ctx, "acme", "T3"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	for i := 0; i < 10; i++ {
		_ = r.ReconcileTenant(ctx, "acme")
		j, _ := r.Jobs.Get(ctx, "acme")
		if j.Phase == tenantdeletion.PhaseFailed {
			break
		}
	}
	// The store recovers; clear the error and retry.
	eraser.err = nil
	if err := r.Retry(ctx, "acme"); err != nil {
		t.Fatalf("Retry: %v", err)
	}
	j, _ := r.Jobs.Get(ctx, "acme")
	// Retry resumes from the first incomplete phase — delete_data,
	// which is where the failure happened.
	if j.Phase != tenantdeletion.PhaseDeleteData {
		t.Errorf("phase after Retry = %q, want delete_data", j.Phase)
	}
	final := runToCompletion(t, r, "acme")
	if final.Phase != tenantdeletion.PhaseCompleted {
		t.Errorf("retried job did not complete: phase = %q (%s)", final.Phase, final.Failure)
	}
}

func TestTerminalJobIsNotReRun(t *testing.T) {
	// A completed job is left untouched — a crash-recovery re-run is a
	// no-op.
	eraser := &fakeEraser{counts: map[string]int{}}
	r, _ := newReconciler(t, eraser, &fakeReceipts{}, &fakeAction{})
	ctx := context.Background()
	if err := r.Start(ctx, "acme", "T3"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	runToCompletion(t, r, "acme")
	before := eraser.calls
	if err := r.ReconcileTenant(ctx, "acme"); err != nil {
		t.Fatalf("ReconcileTenant on a completed job: %v", err)
	}
	if eraser.calls != before {
		t.Error("re-running a completed job must not re-invoke any phase action")
	}
}

func TestReconcileAllAdvancesEveryActiveJob(t *testing.T) {
	r, _ := newReconciler(t, &fakeEraser{counts: map[string]int{}}, &fakeReceipts{}, &fakeAction{})
	ctx := context.Background()
	for _, id := range []string{"acme", "globex", "initech"} {
		if err := r.Start(ctx, id, "T3"); err != nil {
			t.Fatalf("Start %q: %v", id, err)
		}
	}
	if err := r.ReconcileAll(ctx); err != nil {
		t.Fatalf("ReconcileAll: %v", err)
	}
	for _, id := range []string{"acme", "globex", "initech"} {
		j, _ := r.Jobs.Get(ctx, id)
		if j.Phase != tenantdeletion.PhaseTerminateSessions {
			t.Errorf("tenant %q phase after one ReconcileAll = %q, want terminate_sessions", id, j.Phase)
		}
	}
}

func TestNextPhaseWalksTheSequence(t *testing.T) {
	got, ok := tenantdeletion.NextPhase(tenantdeletion.PhaseSoftDisable)
	if !ok || got != tenantdeletion.PhaseTerminateSessions {
		t.Errorf("NextPhase(soft_disable) = %q,%v want terminate_sessions,true", got, ok)
	}
	got, ok = tenantdeletion.NextPhase(tenantdeletion.PhaseProduceReceipt)
	if !ok || got != tenantdeletion.PhaseCompleted {
		t.Errorf("NextPhase(produce_receipt) = %q,%v want completed,true", got, ok)
	}
	got, ok = tenantdeletion.NextPhase(tenantdeletion.PhaseCompleted)
	if ok {
		t.Errorf("NextPhase(completed) ok = true, want false (terminal)")
	}
}

func TestStartRejectsEmptyTenantID(t *testing.T) {
	r, _ := newReconciler(t, &fakeEraser{}, &fakeReceipts{}, &fakeAction{})
	if err := r.Start(context.Background(), "", "T3"); !errors.Is(err, tenantdeletion.ErrMissingTenantID) {
		t.Errorf("Start(\"\") error = %v, want ErrMissingTenantID", err)
	}
}

func TestReconcileSetsSLAClockOnStart(t *testing.T) {
	// The §12.8 deletion SLA clock starts at Phase 1. StartedAt must
	// be set on the job.
	fixed := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	r, jobs := newReconciler(t, &fakeEraser{}, &fakeReceipts{}, &fakeAction{})
	r.Clock = func() time.Time { return fixed }
	if err := r.Start(context.Background(), "acme", "T3"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	j, _ := jobs.Get(context.Background(), "acme")
	if !j.StartedAt.Equal(fixed) {
		t.Errorf("StartedAt = %v, want %v", j.StartedAt, fixed)
	}
}
