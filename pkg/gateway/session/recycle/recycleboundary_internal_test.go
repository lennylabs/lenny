// SPDX-License-Identifier: MIT

package recycle

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podclaim"
	claimstate "github.com/lennylabs/lenny/pkg/sandboxclaim/state"
)

// fakeBoundaryState scripts the claim-binding and pod-readiness seams the
// RecycleBoundaryCoordinator reads, and records the reserve/retire patches it
// issues, so the timer and poll mechanics are exercised without a cluster.
type fakeBoundaryState struct {
	mu sync.Mutex

	phase         claimstate.State
	rewarmStarted bool
	claimExists   bool
	podReadyVal   bool
	podGone       bool

	reserveCalls int
	retireCalls  int
	retiredFail  bool
	holds        []string
}

func (f *fakeBoundaryState) binding(context.Context, string) (claimstate.State, bool, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.phase, f.rewarmStarted, f.claimExists, nil
}

func (f *fakeBoundaryState) ready(context.Context, string) (bool, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.podReadyVal, f.podGone, nil
}

func (f *fakeBoundaryState) reserve(context.Context, string) (podclaim.ReservedHold, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reserveCalls++
	f.phase = claimstate.Reserved
	return podclaim.ReservedHold{UID: "uid-1", ResourceVersion: "rv-1", HoldExpiresAt: time.Now().Add(time.Second)}, nil
}

func (f *fakeBoundaryState) retire(_ context.Context, _ string, failed bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.retireCalls++
	f.retiredFail = failed
	if failed {
		f.phase = claimstate.Failed
	} else {
		f.phase = claimstate.Released
	}
	return nil
}

func (f *fakeBoundaryState) Hold(podID string, _ podclaim.ReservedHold) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.holds = append(f.holds, podID)
}

func (f *fakeBoundaryState) snapshot() fakeBoundaryState {
	f.mu.Lock()
	defer f.mu.Unlock()
	return fakeBoundaryState{
		phase:        f.phase,
		reserveCalls: f.reserveCalls,
		retireCalls:  f.retireCalls,
		retiredFail:  f.retiredFail,
		holds:        append([]string(nil), f.holds...),
	}
}

// newTestBoundary constructs a RecycleBoundaryCoordinator with the fake seams
// wired in, an injectable missing-report timer, a fixed cleanup timeout, and a
// short re-warm poll. fixedTimeout is the value the cleanup-timeout resolver
// returns so the missing-report delay is deterministic.
func newTestBoundary(t *testing.T, st *fakeBoundaryState, fixedTimeout time.Duration) (*RecycleBoundaryCoordinator, *fakeTimer) {
	t.Helper()
	var lastTimer *fakeTimer
	c := &RecycleBoundaryCoordinator{
		reserve:        st.reserve,
		retire:         st.retire,
		binding:        st.binding,
		podReady:       st.ready,
		cleanupTimeout: func(context.Context, string) time.Duration { return fixedTimeout },
		holds:          st,
		now:            time.Now,
		rewarmBudget:   2 * time.Second,
		pollEvery:      5 * time.Millisecond,
		grace:          MissingReportGracePeriod,
		log:            testLogger(),
		afterFunc: func(d time.Duration, fn func()) timerHandle {
			lastTimer = &fakeTimer{delay: d, fn: fn}
			return lastTimer
		},
		timers:    make(map[string]timerHandle),
		rewarming: make(map[string]context.CancelFunc),
	}
	return c, func() *fakeTimer { return lastTimer }()
}

// TestMissingReportTimeoutRetiresFailed verifies the §3.4 gateway-side
// missing-report timeout retires the pod (fail-closed `failed`) when no
// ReportPodScrub arrives: the claim is still `recycling` with no rewarmStartedAt
// when the timer fires.
//
// spec: §3.4 (missing-report timeout retires the pod), §4.7.
func TestMissingReportTimeoutRetiresFailed(t *testing.T) {
	st := &fakeBoundaryState{phase: claimstate.Recycling, claimExists: true}
	c, _ := newTestBoundary(t, st, 60*time.Second)
	c.OnRecycling("pod-1")

	timer := c.timers["pod-1"].(*fakeTimer)
	wantDelay := 60*time.Second + MissingReportGracePeriod
	if timer.delay != wantDelay {
		t.Fatalf("missing-report delay = %s, want %s (cleanupTimeout + grace)", timer.delay, wantDelay)
	}
	timer.fn() // fire the timer

	got := st.snapshot()
	if got.retireCalls != 1 || !got.retiredFail {
		t.Fatalf("retire calls = %d failed = %v, want 1 failed (fail-closed missing-report retire)", got.retireCalls, got.retiredFail)
	}
	if _, armed := c.timers["pod-1"]; armed {
		t.Error("timer entry not cleared after firing")
	}
}

// TestMissingReportTimeoutCancelledByScrubReport verifies a ReportPodScrub
// arriving before the timeout cancels the timer so the pod is never retired by
// the missing-report path.
//
// spec: §3.4 (ReportPodScrub cancels the missing-report timer).
func TestMissingReportTimeoutCancelledByScrubReport(t *testing.T) {
	st := &fakeBoundaryState{phase: claimstate.Reserved, claimExists: true}
	c, _ := newTestBoundary(t, st, 60*time.Second)
	c.OnRecycling("pod-1")
	timer := c.timers["pod-1"].(*fakeTimer)

	c.OnScrubReported("pod-1", false) // non-preConnect reserve already happened

	if !timer.stopped {
		t.Error("missing-report timer not stopped on scrub report")
	}
	if _, armed := c.timers["pod-1"]; armed {
		t.Error("timer entry not removed on scrub report")
	}
	// Firing the (stale) timer must not retire: the entry is gone.
	timer.fn()
	if got := st.snapshot(); got.retireCalls != 0 {
		t.Errorf("retire calls = %d after cancellation, want 0", got.retireCalls)
	}
}

// TestMissingReportTimeoutNoOpWhenAlreadyReserved verifies the timer callback
// does not retire a claim that advanced to `reserved` (a slow ReportPodScrub
// landed after the timer fired but before the callback read the claim): the
// report was not missing.
//
// spec: §3.4 (the timeout retires only when no report arrived).
func TestMissingReportTimeoutNoOpWhenAlreadyReserved(t *testing.T) {
	st := &fakeBoundaryState{phase: claimstate.Reserved, claimExists: true}
	c, _ := newTestBoundary(t, st, 60*time.Second)
	c.OnRecycling("pod-1")
	c.timers["pod-1"].(*fakeTimer).fn()

	if got := st.snapshot(); got.retireCalls != 0 {
		t.Errorf("retire calls = %d for a reserved claim, want 0", got.retireCalls)
	}
}

// TestMissingReportTimeoutNoOpWhenRewarmStarted verifies the timer callback
// does not retire a recycling claim that carries rewarmStartedAt: a preConnect
// ReportPodScrub arrived and the re-warm began, so the report was not missing.
//
// spec: §3.4 (rewarmStartedAt means the report arrived).
func TestMissingReportTimeoutNoOpWhenRewarmStarted(t *testing.T) {
	st := &fakeBoundaryState{phase: claimstate.Recycling, rewarmStarted: true, claimExists: true}
	c, _ := newTestBoundary(t, st, 60*time.Second)
	c.OnRecycling("pod-1")
	c.timers["pod-1"].(*fakeTimer).fn()

	if got := st.snapshot(); got.retireCalls != 0 {
		t.Errorf("retire calls = %d for a re-warming claim, want 0", got.retireCalls)
	}
}

// TestMissingReportTimeoutNoOpWhenClaimGone verifies a vanished claim (a
// concurrent orphan-GC reclaim or hold-expiry DELETE) is a no-op: there is
// nothing to retire.
//
// spec: §3.4 / §4.6.1 (concurrent reclaim).
func TestMissingReportTimeoutNoOpWhenClaimGone(t *testing.T) {
	st := &fakeBoundaryState{phase: "", claimExists: false}
	c, _ := newTestBoundary(t, st, 60*time.Second)
	c.OnRecycling("pod-1")
	c.timers["pod-1"].(*fakeTimer).fn()

	if got := st.snapshot(); got.retireCalls != 0 {
		t.Errorf("retire calls = %d for a gone claim, want 0", got.retireCalls)
	}
}

// TestOnRecyclingReArmCancelsPriorTimer verifies a re-arm (a duplicate recycle
// patch on a re-bound pod) cancels the prior timer so a single timer fires per
// recycle episode.
//
// spec: §3.4 (OnRecycling idempotent on the pod key).
func TestOnRecyclingReArmCancelsPriorTimer(t *testing.T) {
	st := &fakeBoundaryState{phase: claimstate.Recycling, claimExists: true}
	c, _ := newTestBoundary(t, st, 60*time.Second)
	c.OnRecycling("pod-1")
	first := c.timers["pod-1"].(*fakeTimer)
	c.OnRecycling("pod-1")
	if !first.stopped {
		t.Error("prior timer not stopped on re-arm")
	}
}

// TestPreConnectRewarmCompletionReservesOnReady verifies the §3.4 preConnect
// re-warm completion path: after OnScrubReported(preConnect=true) the
// coordinator polls the pod readiness and, once Ready with the claim still
// recycling and carrying rewarmStartedAt, patches the claim recycling →
// reserved and registers the hold. This is the producer of the recycling →
// reserved patch that was previously absent on preConnect pools.
//
// spec: §3.4 (re-warm completion drives recycling → reserved), §3.2 (hold
// registration after the reserved patch), §6.2 / §6.14 (recycling → reserved
// binding edge).
func TestPreConnectRewarmCompletionReservesOnReady(t *testing.T) {
	st := &fakeBoundaryState{
		phase:         claimstate.Recycling,
		rewarmStarted: true,
		claimExists:   true,
		podReadyVal:   true,
	}
	c, _ := newTestBoundary(t, st, 60*time.Second)
	c.OnRecycling("pod-1")
	c.OnScrubReported("pod-1", true)

	if !waitFor(t, func() bool { return st.snapshot().reserveCalls == 1 }) {
		t.Fatalf("preConnect re-warm completion did not reserve the claim (reserveCalls = %d)", st.snapshot().reserveCalls)
	}
	got := st.snapshot()
	if got.phase != claimstate.Reserved {
		t.Errorf("claim phase = %q after re-warm completion, want reserved", got.phase)
	}
	if len(got.holds) != 1 || got.holds[0] != "pod-1" {
		t.Errorf("registered holds = %v, want [pod-1]", got.holds)
	}
}

// TestPreConnectRewarmPollWaitsForReady verifies the re-warm poll does not
// reserve while the pod is still NotReady (the SDK re-warm in progress), and
// reserves once it becomes Ready.
//
// spec: §3.4 (reserve only when the SDK re-warm makes the pod Ready).
func TestPreConnectRewarmPollWaitsForReady(t *testing.T) {
	st := &fakeBoundaryState{
		phase:         claimstate.Recycling,
		rewarmStarted: true,
		claimExists:   true,
		podReadyVal:   false, // re-warm not complete yet
	}
	c, _ := newTestBoundary(t, st, 60*time.Second)
	c.OnScrubReported("pod-1", true)

	// A few poll intervals pass with the pod NotReady: no reserve.
	time.Sleep(30 * time.Millisecond)
	if got := st.snapshot(); got.reserveCalls != 0 {
		t.Fatalf("reserve calls = %d while pod NotReady, want 0", got.reserveCalls)
	}
	// The SDK re-warm completes; the next poll reserves.
	st.mu.Lock()
	st.podReadyVal = true
	st.mu.Unlock()
	if !waitFor(t, func() bool { return st.snapshot().reserveCalls == 1 }) {
		t.Fatalf("reserve not issued after pod became Ready (reserveCalls = %d)", st.snapshot().reserveCalls)
	}
}

// TestPreConnectRewarmPollStopsWhenClaimLeavesRecycling verifies the poll
// stops without reserving when the claim leaves `recycling` (a concurrent
// rebind, reserve by a peer, or retire): the coordinator must not double-reserve.
//
// spec: §3.4 (stop when the claim advanced).
func TestPreConnectRewarmPollStopsWhenClaimLeavesRecycling(t *testing.T) {
	st := &fakeBoundaryState{
		phase:         claimstate.Bound, // already advanced (a rebind)
		rewarmStarted: true,
		claimExists:   true,
		podReadyVal:   true,
	}
	c, _ := newTestBoundary(t, st, 60*time.Second)
	c.OnScrubReported("pod-1", true)

	time.Sleep(30 * time.Millisecond)
	if got := st.snapshot(); got.reserveCalls != 0 {
		t.Errorf("reserve calls = %d for a claim that left recycling, want 0", got.reserveCalls)
	}
}

// TestNonPreConnectScrubReportDoesNotPoll verifies a non-preConnect
// ReportPodScrub (where the disposition driver reserved synchronously) only
// cancels the missing-report timer and does not start a re-warm poll.
//
// spec: §3.4 (no re-warm leg on a non-preConnect pool).
func TestNonPreConnectScrubReportDoesNotPoll(t *testing.T) {
	st := &fakeBoundaryState{phase: claimstate.Reserved, claimExists: true, podReadyVal: true}
	c, _ := newTestBoundary(t, st, 60*time.Second)
	c.OnScrubReported("pod-1", false)

	time.Sleep(30 * time.Millisecond)
	if got := st.snapshot(); got.reserveCalls != 0 {
		t.Errorf("non-preConnect scrub report issued %d reserve calls, want 0 (driver reserved synchronously)", got.reserveCalls)
	}
}

// waitFor polls cond up to a short deadline, returning true once it holds. It
// keeps the async re-warm-poll tests from depending on exact timing.
func waitFor(t *testing.T, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return cond()
}
