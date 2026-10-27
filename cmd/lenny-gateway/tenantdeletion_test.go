// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	apisession "github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/controller/tenantdeletion"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	sessionmem "github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// spec: §12.8 — the gateway-hosted §12.8
// tenant-deletion reconciler drives a tenant out of `disabling`/`deleting`
// through the erasure phases to the `deleted` tombstone, and pauses at
// Phase 3.5 while an active legal hold is in force. F-12.8.1, F-24.10.3.

// countingEraser is a single-store TenantEraser stub.
type countingEraser struct{ calls int }

func (e *countingEraser) erase(context.Context, string) (int, error) {
	e.calls++
	return 1, nil
}

// phaseRecorder is a Phase 2 SessionTerminator and a Phase 3
// CredentialRevoker that appends each call to one ordered log, so a test
// can assert the drain ran before any revocation. The drain fails while
// drainErr is set.
type phaseRecorder struct {
	mu       sync.Mutex
	log      []string
	drainErr error
}

func (p *phaseRecorder) TerminateTenantSessions(_ context.Context, tenantID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.log = append(p.log, "drain:"+tenantID)
	return p.drainErr
}

func (p *phaseRecorder) RevokeTenantCredentials(_ context.Context, tenantID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.log = append(p.log, "revoke:"+tenantID)
	return nil
}

func (p *phaseRecorder) setDrainErr(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.drainErr = err
}

func (p *phaseRecorder) calls() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.log...)
}

func fixedClock() func() time.Time {
	return func() time.Time { return time.Date(2026, 6, 4, 0, 0, 0, 0, time.UTC) }
}

// newDeletionRunner builds a gateway runner over in-memory stores with a
// recording Phase 2 Terminator. It returns the runner, the tenant store,
// the session store, the audit appender, and the eraser so a test can
// drive and assert.
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
		Terminator: &phaseRecorder{},
		Revoker:    tenantCredentialRevoker{tenants: tenants, sessions: sessions, leases: &recordingLeaseRevoker{}, clock: clock},
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

// After a Phase 2 drain that does not verify, the runner leaves the
// tenant row disabling. A job store rebuilt after a restart resumes the
// tenant at Phase 1 from that row and re-runs the drain before any
// Phase 3 call. Before the fix the drain error failed the job.
//
// spec: §12.8 (Tenant deletion lifecycle), §12.8 (Lifecycle fence and drain)
func TestTenantDeletionRunnerFailedDrainStaysDisablingAndRedrainsAfterRestart_spec_12_8(t *testing.T) {
	runner, tenants, _, _, _ := newDeletionRunner(t)
	rec := &phaseRecorder{drainErr: errors.New("session drain incomplete: 0 failed, 1 remaining")}
	runner.reconciler.Terminator = rec
	runner.reconciler.Revoker = rec
	ctx := context.Background()
	if err := tenants.Create(ctx, tenantstore.Tenant{ID: "acme", State: tenantstore.TenantStateDisabling}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	drive(ctx, runner, 5)

	job, err := runner.reconciler.Jobs.Get(ctx, "acme")
	if err != nil {
		t.Fatalf("Get job: %v", err)
	}
	if job.Phase != tenantdeletion.PhaseTerminateSessions {
		t.Fatalf("job phase = %q (%s), want terminate_sessions", job.Phase, job.Failure)
	}
	row, _ := tenants.Get(ctx, "acme")
	if row.State != tenantstore.TenantStateDisabling {
		t.Fatalf("tenant state after a failed drain = %q, want disabling", row.State)
	}

	// A restart loses the in-memory job store; the drain now succeeds.
	runner.reconciler.Jobs = tenantdeletion.NewMemory()
	rec.setDrainErr(nil)
	before := len(rec.calls())
	drive(ctx, runner, 3)
	after := rec.calls()[before:]
	if len(after) < 2 || after[0] != "drain:acme" {
		t.Fatalf("calls after restart = %v, want the drain first", after)
	}
	for _, c := range after {
		if strings.HasPrefix(c, "revoke:") {
			break
		}
		if c != "drain:acme" {
			t.Fatalf("unexpected call %q before the revocation in %v", c, after)
		}
	}
	if !containsCall(after, "revoke:acme") {
		t.Errorf("calls after restart = %v, want Phase 3 after the verified drain", after)
	}
	for _, c := range rec.calls()[:before] {
		if strings.HasPrefix(c, "revoke:") {
			t.Fatalf("Phase 3 ran while the drain was failing: %v", rec.calls())
		}
	}
}

func containsCall(calls []string, want string) bool {
	for _, c := range calls {
		if c == want {
			return true
		}
	}
	return false
}

// sleepyDrainTerminator is a DrainTerminator whose forced transition
// sleeps for delay. It sleeps rather than waiting on its context,
// because the drain detaches each transition from the caller's deadline.
type sleepyDrainTerminator struct {
	mu    sync.Mutex
	delay time.Duration
	ids   []string
}

func (s *sleepyDrainTerminator) ForceTerminate(_ context.Context, _, id string) (sessionstore.Session, apisession.State, bool, error) {
	time.Sleep(s.delay)
	s.mu.Lock()
	s.ids = append(s.ids, id)
	s.mu.Unlock()
	return sessionstore.Session{ID: id}, apisession.StateRunning, true, nil
}

func (s *sleepyDrainTerminator) terminated() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.ids...)
}

// A Phase 2 drain that exceeds --lifecycle-tick-budget stops between
// sessions and counts as a drain failure: its tenant stays at Phase 2 in
// disabling, while a tenant with no sessions advances in the same tick.
//
// spec: §12.8 (Tenant deletion lifecycle), §12.8 (Lifecycle fence and drain)
func TestTenantSessionTerminatorBudgetExpiryKeepsPhase2_spec_12_8(t *testing.T) {
	runner, tenants, sessions, _, _ := newDeletionRunner(t)
	slow := &sleepyDrainTerminator{delay: 200 * time.Millisecond}
	runner.reconciler.Terminator = tenantSessionTerminator{
		sessions: sessions, term: slow, budget: 20 * time.Millisecond, cap: time.Minute, clock: time.Now,
	}
	ctx := context.Background()
	for _, id := range []string{"acme", "globex"} {
		if err := tenants.Create(ctx, tenantstore.Tenant{ID: id, State: tenantstore.TenantStateDisabling}); err != nil {
			t.Fatalf("Create tenant %s: %v", id, err)
		}
	}
	for _, id := range []string{"sess-1", "sess-2"} {
		if err := sessions.Create(ctx, sessionstore.Session{
			ID: id, TenantID: "acme", UserID: "alice", State: apisession.StateRunning,
		}); err != nil {
			t.Fatalf("Create session %s: %v", id, err)
		}
	}
	// Tick 1 runs Phase 1; tick 2 runs the Phase 2 drains.
	drive(ctx, runner, 2)

	acme, _ := runner.reconciler.Jobs.Get(ctx, "acme")
	if acme.Phase != tenantdeletion.PhaseTerminateSessions || acme.State != tenantdeletion.TenantDisabling {
		t.Fatalf("acme job = %q/%q (%s), want terminate_sessions/disabling", acme.Phase, acme.State, acme.Failure)
	}
	if !strings.Contains(acme.BlockedReason, context.DeadlineExceeded.Error()) {
		t.Errorf("acme BlockedReason = %q, want the budget expiry", acme.BlockedReason)
	}
	if got := slow.terminated(); len(got) != 1 {
		t.Errorf("forced transitions = %v, want exactly 1 before the budget stopped the drain", got)
	}
	globex, _ := runner.reconciler.Jobs.Get(ctx, "globex")
	if globex.Phase != tenantdeletion.PhaseRevokeCredentials {
		t.Errorf("globex phase = %q (%s), want revoke_credentials", globex.Phase, globex.Failure)
	}
	row, _ := tenants.Get(ctx, "acme")
	if row.State != tenantstore.TenantStateDisabling {
		t.Errorf("acme tenant state = %q, want disabling", row.State)
	}
}

// A drain that verifies returns nil, and a drain over a tenant whose
// listing fails returns the wrapped listing error.
//
// spec: §12.8 (Lifecycle fence and drain)
func TestTenantSessionTerminatorReturnsDrainOutcome_spec_12_8(t *testing.T) {
	sessions := sessionmem.New()
	ctx := context.Background()
	if err := sessions.Create(ctx, sessionstore.Session{ID: "sess-1", TenantID: "acme", UserID: "alice", State: apisession.StateCompleted}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	ok := tenantSessionTerminator{sessions: sessions, term: &sleepyDrainTerminator{}, budget: time.Second, cap: time.Minute, clock: time.Now}
	if err := ok.TerminateTenantSessions(ctx, "acme"); err != nil {
		t.Fatalf("drain over terminal sessions = %v, want nil", err)
	}
	failing := tenantSessionTerminator{sessions: failingLister{}, term: &sleepyDrainTerminator{}, budget: time.Second}
	if err := failing.TerminateTenantSessions(ctx, "acme"); err == nil || !strings.Contains(err.Error(), "primary unreachable") {
		t.Fatalf("drain over a failing listing = %v, want the listing error", err)
	}
}

// failingLister is a DrainLister whose listing always fails.
type failingLister struct{}

func (failingLister) List(context.Context, string, sessionstore.ListFilter) ([]sessionstore.Session, error) {
	return nil, errors.New("primary unreachable")
}

var _ admin.DrainLister = failingLister{}

// A non-positive --lifecycle-tick-budget is refused at startup.
//
// spec: §12.8 (Tenant deletion lifecycle)
func TestValidateLifecycleTickBudget_spec_12_8(t *testing.T) {
	for _, d := range []time.Duration{0, -time.Second} {
		if err := validateLifecycleTickBudget(d); err == nil {
			t.Errorf("validateLifecycleTickBudget(%v) = nil, want an error", d)
		}
	}
	if err := validateLifecycleTickBudget(time.Minute); err != nil {
		t.Errorf("validateLifecycleTickBudget(1m) = %v, want nil", err)
	}
}

// TestTenantDeletionPhase2WaitsForThePostTerminalRelease_spec_12_8 pins the
// §12.8 tenant-deletion hold on the §7.1 post-terminal window: a pass whose
// drain verifies but whose tenant still has a session in its window stays at
// Phase 2 in disabling with BlockedReason set, and the next pass after the
// release is recorded advances to Phase 3. On the pre-fix terminator the
// first pass advances at once. With no cap the terminator returns an error
// wrapping ErrReleaseCapUnset.
//
// spec: §12.8 (Compliance Interfaces), §7.1 (Post-terminal window)
func TestTenantDeletionPhase2WaitsForThePostTerminalRelease_spec_12_8(t *testing.T) {
	runner, tenants, sessions, _, _ := newDeletionRunner(t)
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	runner.reconciler.Terminator = tenantSessionTerminator{
		sessions: sessions, term: &sleepyDrainTerminator{}, budget: time.Second,
		cap: 10 * time.Minute, clock: func() time.Time { return now },
	}
	ctx := context.Background()
	if err := tenants.Create(ctx, tenantstore.Tenant{ID: "acme", State: tenantstore.TenantStateDisabling}); err != nil {
		t.Fatalf("Create tenant: %v", err)
	}
	if err := sessions.Create(ctx, sessionstore.Session{
		ID: "sess-sealing", TenantID: "acme", UserID: "alice", State: apisession.StateCompleted,
		PodAssignment: "pod-a", TerminatedAt: now.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("Create session: %v", err)
	}
	drive(ctx, runner, 2)
	job, _ := runner.reconciler.Jobs.Get(ctx, "acme")
	if job.Phase != tenantdeletion.PhaseTerminateSessions || job.State != tenantdeletion.TenantDisabling {
		t.Fatalf("job = %q/%q (%s), want terminate_sessions/disabling", job.Phase, job.State, job.Failure)
	}
	if !strings.Contains(job.BlockedReason, "post-terminal window") {
		t.Errorf("BlockedReason = %q, want the post-terminal wait", job.BlockedReason)
	}
	if err := sessionstore.RecordPodRelease(ctx, sessions, "acme", "sess-sealing", now); err != nil {
		t.Fatalf("record release: %v", err)
	}
	drive(ctx, runner, 1)
	job, _ = runner.reconciler.Jobs.Get(ctx, "acme")
	if job.Phase != tenantdeletion.PhaseRevokeCredentials {
		t.Errorf("phase after the release = %q (%s), want revoke_credentials", job.Phase, job.Failure)
	}

	unwired := tenantSessionTerminator{sessions: sessions, term: &sleepyDrainTerminator{}, budget: time.Second, clock: time.Now}
	if err := unwired.TerminateTenantSessions(ctx, "acme"); !errors.Is(err, admin.ErrReleaseCapUnset) {
		t.Errorf("terminator with no cap = %v, want ErrReleaseCapUnset", err)
	}
}
