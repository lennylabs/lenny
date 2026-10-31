// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// waitForWaiterOrDone polls until n goroutines hold or wait on sessionID's
// lock, or done is closed. It returns false when done closed first, which
// is how a hook that never waits for the lock shows up.
func waitForWaiterOrDone(t *testing.T, l *sessionLocks, sessionID string, n int, done <-chan struct{}) bool {
	t.Helper()
	deadline := time.Now().Add(funnelWait)
	for {
		if l.refCount(sessionID) == n {
			return true
		}
		select {
		case <-done:
			return false
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("lock refs for %s = %d, want %d", sessionID, l.refCount(sessionID), n)
		}
		time.Sleep(time.Millisecond)
	}
}

// awaitDone fails the test when done is not closed within funnelWait.
func awaitDone(t *testing.T, done <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(funnelWait):
		t.Fatalf("%s did not return", what)
	}
}

// runHookWriter runs a hook-path terminal writer for id on its own
// goroutine: the cancelled write over a non-terminal row, then the
// post-terminal hook on ctx, then end. It closes the returned channel when
// the writer returns.
func runHookWriter(ctx context.Context, f *funnelFixture, id string, end func()) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer end()
		from, updated, err := f.srv.writeTerminal(ctx, "acme", id, session.StateCancelled, nonTerminalGuard)
		if err != nil {
			return
		}
		f.srv.AfterTerminalWrite(ctx, from, updated, nil)
	}()
	return done
}

// recoveryEdges are the failure funnel's non-terminal edges, each with the
// retry count that selects it for a retryable report.
var recoveryEdges = []struct {
	to      session.State
	retries int64
}{
	{session.StateResumePending, 0},
	{session.StateAwaitingClientAction, 2},
}

// TestHookReleaseWaitsForTheFailureFunnel_spec_5_2 drives a failure funnel
// that commits a recovery edge while a terminal writer races it, over the
// edge, the terminal claim opened before or after the funnel's edge write,
// and the pool kind. The terminal write lands after the edge commits and its
// hook reaches the release step while the funnel still holds the session's
// slot-accounting lock. The binding is released once, by the funnel, with
// the failed disposition; on a concurrent pool the drain the funnel's
// accounting requests is stamped before that release; the hook's executor
// release runs after the funnel unlocks and misses.
//
// spec: §5.2 (whole-pod replacement trigger), §7.3 (Resume flow after pod
// failure), §7.2 (Interactive Session Model)
func TestHookReleaseWaitsForTheFailureFunnel_spec_5_2(t *testing.T) {
	for _, edge := range recoveryEdges {
		for _, claimFirst := range []bool{true, false} {
			for _, slot := range []bool{false, true} {
				name := fmt.Sprintf("%s/claim_first_%v/concurrent_%v", edge.to, claimFirst, slot)
				t.Run(name, func(t *testing.T) {
					hookWaitsForFunnel(t, edge.to, edge.retries, claimFirst, slot)
				})
			}
		}
	}
}

func hookWaitsForFunnel(t *testing.T, edge session.State, retries int64, claimFirst, slot bool) {
	ctx := context.Background()
	f := newFunnelFixture(t, funnelOpts{})
	snap, _ := f.seed(t, "s", session.StateRunning, retries, slot)
	if slot {
		// One earlier failure puts pod-a one short of its threshold of 2,
		// so this report's accounting requests the drain.
		f.srv.slotHealth.RecordFailure("pod-a")
	}
	var end func()
	if claimFirst {
		end = f.srv.BeginTerminal("s")
	}
	var writer <-chan struct{}
	var waited bool
	var once sync.Once
	f.store.setAfter(func(r sessionstore.Session) {
		if r.State != edge {
			return
		}
		once.Do(func() {
			if !claimFirst {
				end = f.srv.BeginTerminal("s")
			}
			writer = runHookWriter(ctx, f, "s", end)
			waited = waitForWaiterOrDone(t, &f.srv.slotAccountLocks, "s", 2, writer)
		})
	})

	disp, err := f.report(ctx, snap, "runtime_crash")
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if writer == nil {
		t.Fatalf("the funnel's edge write never committed; To = %q", disp.To)
	}
	awaitDone(t, writer, "the terminal writer")

	if disp.To != edge {
		t.Fatalf("To = %q, want %q", disp.To, edge)
	}
	if !waited {
		t.Error("the hook's release step did not wait for the funnel's slot-accounting lock")
	}
	if got := f.state(t, "s"); got.State != session.StateCancelled {
		t.Errorf("row state = %q, want cancelled", got.State)
	}
	if n, i := f.log.count("release:"), f.log.index("release:failed"); n != 1 || i < 0 {
		t.Fatalf("events = %v, want one release:failed by the funnel", f.log.list())
	}
	if slot {
		if drain, release := f.log.index("drain:pod-a"), f.log.index("release:"); drain < 0 || drain > release {
			t.Errorf("events = %v, want drain:pod-a before the release", f.log.list())
		}
	}
	calls := f.exec.callList()
	if len(calls) != 2 || calls[0] != executor.DispositionFailed || calls[1] != executor.DispositionCancelled {
		t.Errorf("executor calls = %v, want [failed cancelled]: the funnel's release, then the hook's miss", calls)
	}
	if n := f.srv.slotAccountLocks.refCount("s"); n != 0 {
		t.Errorf("lock refs = %d after both paths returned, want 0", n)
	}
}

// TestFunnelReleaseStandsWhenTheTerminalWriteLoses_spec_7_3 races a terminal
// writer whose guard admits only running, as the idle expiry's does, against
// a funnel that already committed its recovery edge. The terminal write is
// refused, no hook runs, and the funnel's failed-disposition release is the
// binding's one release.
//
// spec: §7.3 (Resume flow after pod failure), §7.2 (Interactive Session
// Model), §5.2 (whole-pod replacement trigger)
func TestFunnelReleaseStandsWhenTheTerminalWriteLoses_spec_7_3(t *testing.T) {
	runningOnly := func(r *sessionstore.Session) error {
		if r.State != session.StateRunning {
			return sessionstore.ErrSuperseded
		}
		return nil
	}
	for _, edge := range recoveryEdges {
		for _, slot := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/concurrent_%v", edge.to, slot), func(t *testing.T) {
				ctx := context.Background()
				f := newFunnelFixture(t, funnelOpts{})
				snap, _ := f.seed(t, "s", session.StateRunning, edge.retries, slot)
				var writeErr error
				var once sync.Once
				f.store.setAfter(func(r sessionstore.Session) {
					if r.State != edge.to {
						return
					}
					once.Do(func() {
						end := f.srv.BeginTerminal("s")
						defer end()
						_, _, writeErr = f.srv.writeTerminal(ctx, "acme", "s", session.StateExpired, runningOnly)
					})
				})
				if _, err := f.report(ctx, snap, "runtime_crash"); err != nil {
					t.Fatalf("report: %v", err)
				}
				if writeErr == nil {
					t.Fatal("the terminal write applied over the funnel's edge, want it refused")
				}
				if got := f.state(t, "s"); got.State != edge.to {
					t.Errorf("row state = %q, want %q", got.State, edge.to)
				}
				if calls := f.exec.callList(); len(calls) != 1 || calls[0] != executor.DispositionFailed {
					t.Errorf("executor calls = %v, want [failed]", calls)
				}
				if _, ok := f.registry.Get("s"); ok {
					t.Error("the binding is still registered after the funnel's release")
				}
			})
		}
	}
}

// TestHookReleaseIgnoresTheWritersContextDuringTheLockWait_spec_7_3 holds the
// session's slot-accounting lock, standing in for a failure funnel whose edge
// write was refused because the terminal write already applied, and cancels
// the writer's context while the hook waits. The hook's release still runs
// exactly once after the lock is released, because the wait runs on the
// server-lifetime context.
//
// spec: §7.3 (Resume flow after pod failure), §5.2 (whole-pod replacement
// trigger), §7.2 (Interactive Session Model)
func TestHookReleaseIgnoresTheWritersContextDuringTheLockWait_spec_7_3(t *testing.T) {
	f := newFunnelFixture(t, funnelOpts{})
	row, _ := f.seed(t, "s", session.StateCancelled, 0, true)
	unlock, err := f.srv.slotAccountLocks.Lock(context.Background(), "s")
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}
	defer unlock()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.srv.AfterTerminalWrite(ctx, session.StateRunning, row, nil)
	}()
	if !waitForWaiterOrDone(t, &f.srv.slotAccountLocks, "s", 2, done) {
		t.Fatal("the hook returned without waiting for the slot-accounting lock")
	}
	cancel()
	time.Sleep(20 * time.Millisecond)
	if calls := f.exec.callList(); len(calls) != 0 {
		t.Fatalf("executor calls = %v while the lock is held, want none", calls)
	}
	select {
	case <-done:
		t.Fatal("the hook returned on the writer's cancel, want it still waiting")
	default:
	}

	unlock()
	awaitDone(t, done, "the hook")
	if calls := f.exec.callList(); len(calls) != 1 || calls[0] != executor.DispositionCancelled {
		t.Errorf("executor calls = %v, want one cancelled release", calls)
	}
	if n := f.log.count("release:cancelled"); n != 1 {
		t.Errorf("events = %v, want one release:cancelled", f.log.list())
	}
}

// TestHookReleaseSkippedOnServerShutdown_spec_7_2 holds the session's
// slot-accounting lock and closes the server while the hook waits. The
// release step is skipped: a pre-running session makes no by-name reclaim
// (its slot member, claim, and leases stay), and a bound session makes no
// executor release (its binding stays registered for the terminal binding
// release).
//
// spec: §7.2 (Interactive Session Model), §5.2 (whole-pod replacement
// trigger), §7.3 (Resume flow after pod failure)
func TestHookReleaseSkippedOnServerShutdown_spec_7_2(t *testing.T) {
	t.Run("pre-running reclaim", func(t *testing.T) {
		p := newPreRunningFixture(t)
		p.reserve(t, "sess-1")
		row := sessionstore.Session{
			UserID: testOwner,
			ID:     "sess-1", TenantID: "acme", RuntimeRef: "echo", State: session.StateCancelled,
			PodAssignment: reclaimPod, ScrubPolicy: ScrubPolicyPerSlot,
		}
		if err := p.store.Create(context.Background(), row); err != nil {
			t.Fatalf("seed: %v", err)
		}
		hookAfterShutdown(t, p.funnelFixture, session.StateReady, row)
		if got := p.members(t); got != "[sess-1]" {
			t.Errorf("members = %s, want [sess-1] unchanged", got)
		}
		if !p.claimPresent(t) || p.writes.count("delete-claim") != 0 {
			t.Errorf("claim present = %v after %d deletes, want present with none", p.claimPresent(t), p.writes.count("delete-claim"))
		}
		if len(p.creds.released) != 0 {
			t.Errorf("ReleaseSession calls = %v, want none", p.creds.released)
		}
	})
	t.Run("executor release", func(t *testing.T) {
		f := newFunnelFixture(t, funnelOpts{})
		row, bind := f.seed(t, "s", session.StateCancelled, 0, true)
		hookAfterShutdown(t, f, session.StateRunning, row)
		if calls := f.exec.callList(); len(calls) != 0 {
			t.Errorf("executor calls = %v, want none", calls)
		}
		if got, ok := f.registry.Get("s"); !ok || got != bind {
			t.Error("the binding was removed, want it left for the terminal binding release")
		}
	})
}

// hookAfterShutdown holds row's slot-accounting lock, runs the hook, closes
// the server while the hook waits, and returns once the hook has returned.
func hookAfterShutdown(t *testing.T, f *funnelFixture, from session.State, row sessionstore.Session) {
	t.Helper()
	unlock, err := f.srv.slotAccountLocks.Lock(context.Background(), row.ID)
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}
	defer unlock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.srv.AfterTerminalWrite(context.Background(), from, row, nil)
	}()
	if !waitForWaiterOrDone(t, &f.srv.slotAccountLocks, row.ID, 2, done) {
		t.Fatal("the hook returned without waiting for the slot-accounting lock")
	}
	f.srv.Close()
	awaitDone(t, done, "the hook")
}

// seedReleaseFixture seeds a cancelled session on a concurrent pool with a
// reserved slot and a binding over a real pod executor, and returns the
// fixture, the row with terminatedAt stamped, and the adapter that records
// Shutdown requests.
func seedReleaseFixture(t *testing.T, terminatedAt time.Time) (*preRunningFixture, sessionstore.Session, *reclaimAdapter) {
	t.Helper()
	p := newPreRunningFixture(t)
	p.srv.executor = executor.NewPodExecutor(p.registry, p.srv.podBinder)
	p.reserve(t, "sess-1")
	row := sessionstore.Session{
		UserID: testOwner,
		ID:     "sess-1", TenantID: "acme", RuntimeRef: "echo", State: session.StateCancelled,
		PodAssignment: reclaimPod, ScrubPolicy: ScrubPolicyPerSlot,
	}
	if err := p.store.Create(context.Background(), row); err != nil {
		t.Fatalf("seed: %v", err)
	}
	row = p.state(t, "sess-1")
	row.TerminatedAt = terminatedAt
	a := &reclaimAdapter{}
	p.registry.Put(&podsession.BindResult{
		SessionID: "sess-1", TenantID: "acme", SandboxName: reclaimPod, SlotID: "sess-1",
		Adapter: dialReclaimAdapter(t, a), CoordinationGeneration: row.CoordinationGeneration,
	})
	return p, row, a
}

// TestHookReleaseOnCancelledWriterContextReleasesTheSlot_spec_7_1 runs the
// hook with a writer context that ended before the hook, over a real pod
// executor and slot set on a concurrent pool. The hook runs on the
// gateway-owned terminal context, so the writer's cancellation does not cut
// the adapter Shutdown: the Shutdown is sent, no leaked member is left, and
// the binding is released. Against a hook on the writer's context, the
// Shutdown is cut and the slot stays counted through its leaked member.
//
// spec: §7.1 (Terminal pipeline context), §5.2 (Slot assignment atomicity),
// §7.2 (Interactive Session Model)
func TestHookReleaseOnCancelledWriterContextReleasesTheSlot_spec_7_1(t *testing.T) {
	p, row, a := seedReleaseFixture(t, time.Time{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p.srv.AfterTerminalWrite(ctx, session.StateRunning, row, nil)

	if got := a.sent(); got == "[]" {
		t.Errorf("Shutdown requests = %s, want the release's Shutdown on the detached terminal context", got)
	}
	if got := p.members(t); strings.Contains(got, "leaked:") {
		t.Errorf("members = %s, want no leaked member after a release that was not cut", got)
	}
	if _, ok := p.registry.Get("sess-1"); ok {
		t.Error("the binding is still registered after the release")
	}
}

// TestHookReleasePastTheBudgetLeaksTheSlot_spec_5_2 runs the hook for a
// session whose terminal transition is older than the terminal pipeline
// budget, over a real pod executor and slot set on a concurrent pool. The
// terminal context is already past its deadline, so the adapter Shutdown is
// cut, the keyed release removes the session's member and adds its leaked
// member on the detached context, and no claim is written: the slot stays
// counted and the claim stays.
//
// spec: §5.2 (Slot assignment atomicity), §6.2 (leaked slot semantics),
// §7.1 (Terminal pipeline context), §7.2 (Interactive Session Model)
func TestHookReleasePastTheBudgetLeaksTheSlot_spec_5_2(t *testing.T) {
	p, row, a := seedReleaseFixture(t, time.Now().Add(-(DefaultWorkspaceSealMaxDuration + DefaultStreamFailureReportTimeout + time.Minute)))
	p.srv.AfterTerminalWrite(context.Background(), session.StateRunning, row, nil)

	if got := p.members(t); got != "[leaked:sess-1]" {
		t.Errorf("members = %s, want [leaked:sess-1] (the leaked slot stays counted)", got)
	}
	if !p.claimPresent(t) {
		t.Error("claim deleted, want it kept while the leaked slot is counted")
	}
	if n := p.writes.count("delete-claim") + p.writes.count("patch-claim-status"); n != 0 {
		t.Errorf("claim writes = %d, want none", n)
	}
	if got := a.sent(); got != "[]" {
		t.Errorf("Shutdown requests = %s, want none on an expired terminal context", got)
	}
	if _, ok := p.registry.Get("sess-1"); ok {
		t.Error("the binding is still registered after the release")
	}
}
