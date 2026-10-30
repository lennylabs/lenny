// SPDX-License-Identifier: MIT

package deadlock_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	session "github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/deadlock"
	"github.com/lennylabs/lenny/pkg/gateway/session/inputwait"
)

type fakeMetrics struct {
	detected      int
	detectedPools []string
	resolutions   []string
	resolvedPools []string
	durations     []float64
}

func (f *fakeMetrics) IncDelegationDeadlockDetected(pool string) {
	f.detected++
	f.detectedPools = append(f.detectedPools, pool)
}

func (f *fakeMetrics) ObserveDelegationDeadlockResolution(pool, resolution string, seconds float64) {
	f.resolvedPools = append(f.resolvedPools, pool)
	f.resolutions = append(f.resolutions, resolution)
	f.durations = append(f.durations, seconds)
}

// deadlockedSnap is the canonical P→C(input_required) deadlock.
func deadlockedSnap() deadlock.Snapshot {
	return snap(
		node("P", session.StateRunning, []string{"C"}),
		node("C", session.StateRunning, nil, "r1"),
	)
}

// TestManagerDetectsAndCachesEvent_spec_8_8_985 covers the first
// detection: the metric fires once, the willTimeoutAt is detectedAt +
// maxWait, and the cached event is served to the root.
func TestManagerDetectsAndCachesEvent_spec_8_8_985(t *testing.T) {
	m := &fakeMetrics{}
	mgr := deadlock.NewManager(120*time.Second, m)
	t0 := base
	if to, _ := mgr.Observe(deadlockedSnap(), t0); len(to) != 0 {
		t.Fatalf("first Observe returned timeouts %+v, want none", to)
	}
	if m.detected != 1 {
		t.Errorf("detected metric = %d, want 1", m.detected)
	}
	ev, ok := mgr.Event("P")
	if !ok {
		t.Fatalf("Event(P) not found after detection")
	}
	if ev.Type != deadlock.EventType || ev.DeadlockedSubtreeRoot != "P" {
		t.Errorf("event = %+v, want type=%s root=P", ev, deadlock.EventType)
	}
	if !ev.DetectedAt.Equal(t0) {
		t.Errorf("DetectedAt = %v, want %v", ev.DetectedAt, t0)
	}
	if want := t0.Add(120 * time.Second); !ev.WillTimeoutAt.Equal(want) {
		t.Errorf("WillTimeoutAt = %v, want %v", ev.WillTimeoutAt, want)
	}
	if _, ok := mgr.Event("C"); ok {
		t.Errorf("Event(C) should be absent — only the root carries the event")
	}
}

// TestManagerRepeatedObserveDoesNotRefireDetection_spec_8_8_981 covers a
// still-deadlocked subtree across sweeps before the timeout: the
// detection metric fires only once and detectedAt stays fixed.
func TestManagerRepeatedObserveDoesNotRefireDetection_spec_8_8_981(t *testing.T) {
	m := &fakeMetrics{}
	mgr := deadlock.NewManager(120*time.Second, m)
	mgr.Observe(deadlockedSnap(), base)
	mgr.Observe(deadlockedSnap(), base.Add(30*time.Second))
	if m.detected != 1 {
		t.Errorf("detected metric = %d after two sweeps, want 1", m.detected)
	}
	ev, _ := mgr.Event("P")
	if !ev.DetectedAt.Equal(base) {
		t.Errorf("DetectedAt drifted to %v, want %v", ev.DetectedAt, base)
	}
}

// TestManagerTimeoutFiresDeepestTasks_spec_8_8_981 covers the
// DEADLOCK_TIMEOUT edge: after willTimeoutAt the manager returns the
// deepest blocked tasks with the first-detection time and drops the active
// entry. Observe alone records no resolution: the `timeout` resolution
// follows an applied fail write, which only the detector sweep performs.
//
// spec: §8.8 (Subtree deadlock detection), §16.1
func TestManagerTimeoutFiresDeepestTasks_spec_8_8_981(t *testing.T) {
	m := &fakeMetrics{}
	mgr := deadlock.NewManager(120*time.Second, m)
	mgr.Observe(deadlockedSnap(), base)
	to, _ := mgr.Observe(deadlockedSnap(), base.Add(120*time.Second))
	if len(to) != 1 {
		t.Fatalf("Observe at timeout returned %d actions, want 1", len(to))
	}
	if to[0].Root != "P" || !reflect.DeepEqual(to[0].DeepestTasks, []string{"C"}) {
		t.Errorf("timeout action = %+v, want root=P deepest=[C]", to[0])
	}
	if to[0].TenantID != "acme" {
		t.Errorf("timeout tenant = %q, want acme", to[0].TenantID)
	}
	if !to[0].DetectedAt.Equal(base) {
		t.Errorf("timeout DetectedAt = %v, want %v", to[0].DetectedAt, base)
	}
	if len(m.resolutions) != 0 {
		t.Errorf("Observe alone recorded resolutions %v, want none", m.resolutions)
	}
	if _, ok := mgr.Event("P"); ok {
		t.Errorf("Event(P) should be cleared after timeout")
	}
}

// TestManagerClearanceReturnedWhenNoLongerDetected_spec_16_1 covers the
// root breaking the deadlock before willTimeoutAt: the next sweep no longer
// detects it, so Observe returns a Clearance carrying the root's pool, the
// first-detection time, and each member's answered count at the last
// detecting sweep, records no resolution itself (§16.1 lists no `resolved`
// value), and emits no timeout.
//
// spec: §16.1 (Metrics), §8.8 (Subtree deadlock detection)
func TestManagerClearanceReturnedWhenNoLongerDetected_spec_16_1(t *testing.T) {
	m := &fakeMetrics{}
	mgr := deadlock.NewManager(120*time.Second, m)
	p := node("P", session.StateRunning, []string{"C"})
	p.Pool = "pool-a"
	c := node("C", session.StateRunning, nil, "r1")
	c.Answered = 2
	mgr.Observe(snap(p, c), base)
	if !reflect.DeepEqual(m.detectedPools, []string{"pool-a"}) {
		t.Errorf("detection pools = %v, want [pool-a]", m.detectedPools)
	}
	// The root answered the child's request_input — C is no longer blocked.
	c.PendingInputs, c.Answered = nil, 3
	to, cleared := mgr.Observe(snap(p, c), base.Add(40*time.Second))
	if len(to) != 0 {
		t.Fatalf("Observe after resolution returned timeouts %+v, want none", to)
	}
	want := []deadlock.Clearance{{
		Root: "P", TenantID: "acme", Pool: "pool-a", DetectedAt: base,
		Answered: map[string]int{"P": 0, "C": 2},
	}}
	if !reflect.DeepEqual(cleared, want) {
		t.Errorf("cleared = %+v, want %+v", cleared, want)
	}
	if len(m.resolutions) != 0 {
		t.Errorf("Observe alone recorded resolutions %v, want none", m.resolutions)
	}
	if _, ok := mgr.Event("P"); ok {
		t.Errorf("Event(P) should be cleared after resolution")
	}
}

// TestManagerTimeoutCarriesRootPool_spec_16_1 pins that a timed-out action
// carries the deadlocked root's pool, the label the detector records the
// `timeout` resolution under.
//
// spec: §16.1 (Metrics), §8.8 (Subtree deadlock detection)
func TestManagerTimeoutCarriesRootPool_spec_16_1(t *testing.T) {
	mgr := deadlock.NewManager(120*time.Second, &fakeMetrics{})
	p := node("P", session.StateRunning, []string{"C"})
	p.Pool = "pool-root"
	c := node("C", session.StateRunning, nil, "r1")
	c.Pool = "pool-child"
	mgr.Observe(snap(p, c), base)
	to, _ := mgr.Observe(snap(p, c), base.Add(120*time.Second))
	if len(to) != 1 || to[0].Pool != "pool-root" {
		t.Fatalf("timeout actions = %+v, want one carrying pool-root", to)
	}
}

// TestDetectorRunOnceFailsDeepestTask_spec_8_8_981 covers the end-to-end
// detector sweep over the live await tracker + request_input registry: a
// deadlock detected at t0 and unresolved at t0+maxWait drives the deepest
// blocked task to a deadlock_timeout failure.
func TestDetectorRunOnceFailsDeepestTask_spec_8_8_981(t *testing.T) {
	tracker := deadlock.NewAwaitTracker()
	reg := inputwait.NewRegistry()
	reg.SetClock(func() time.Time { return base })

	// P (tenant acme) awaits C; C is blocked on request_input r1.
	endP := tracker.Begin("acme", "P", []string{"C"})
	defer endP()
	if _, err := reg.Register("C", "r1", nil); err != nil {
		t.Fatalf("Register: %v", err)
	}

	lookup := func(_ context.Context, _ /*tenant*/, sessionID string) (deadlock.NodeInfo, bool) {
		// Both P and C are live and running; C's input_required signal is
		// carried by the request_input registry, not the coarse row state.
		if sessionID == "P" || sessionID == "C" {
			return deadlock.NodeInfo{State: session.StateRunning, Pool: "pool-" + sessionID}, true
		}
		return deadlock.NodeInfo{}, false
	}
	var failed []string
	fail := func(_ context.Context, tenant, sessionID string) bool {
		if tenant != "acme" {
			t.Errorf("fail tenant = %q, want acme", tenant)
		}
		failed = append(failed, sessionID)
		return true
	}
	m := &fakeMetrics{}
	det := deadlock.NewDetector(deadlock.NewManager(120*time.Second, m), tracker, reg, lookup, fail)

	det.RunOnce(context.Background(), base)
	if len(failed) != 0 {
		t.Fatalf("RunOnce at detection failed %v, want none yet", failed)
	}
	ev, ok := det.Manager().Event("P")
	if !ok || ev.DeadlockedSubtreeRoot != "P" {
		t.Fatalf("Event(P) = %+v, ok=%v after detection", ev, ok)
	}
	if len(ev.BlockedRequests) != 1 || ev.BlockedRequests[0].BlockedSince != base {
		t.Errorf("blockedRequests = %+v, want one carrying blockedSince=%v", ev.BlockedRequests, base)
	}

	det.RunOnce(context.Background(), base.Add(120*time.Second))
	if !reflect.DeepEqual(failed, []string{"C"}) {
		t.Errorf("failed tasks = %v, want [C]", failed)
	}
	if !reflect.DeepEqual(m.resolutions, []string{"timeout"}) || len(m.durations) != 1 || m.durations[0] != 120 {
		t.Errorf("resolutions = %v durations = %v, want [timeout] [120]", m.resolutions, m.durations)
	}
	if !reflect.DeepEqual(m.detectedPools, []string{"pool-P"}) || !reflect.DeepEqual(m.resolvedPools, []string{"pool-P"}) {
		t.Errorf("pools detected %v resolved %v, want the root's pool-P on both", m.detectedPools, m.resolvedPools)
	}
}

// twoDeepestDetector builds a detector over a live tracker and registry in
// which P awaits C1 and C2, each blocked on its own request_input, so a
// timed-out action carries both as deepest tasks. fail is the detector's
// FailFunc.
func twoDeepestDetector(t *testing.T, m *fakeMetrics, fail deadlock.FailFunc) *deadlock.Detector {
	t.Helper()
	tracker := deadlock.NewAwaitTracker()
	reg := inputwait.NewRegistry()
	reg.SetClock(func() time.Time { return base })
	t.Cleanup(tracker.Begin("acme", "P", []string{"C1", "C2"}))
	for _, id := range []string{"C1", "C2"} {
		if _, err := reg.Register(id, "r-"+id, nil); err != nil {
			t.Fatalf("Register %s: %v", id, err)
		}
	}
	lookup := func(context.Context, string, string) (deadlock.NodeInfo, bool) {
		return deadlock.NodeInfo{State: session.StateRunning}, true
	}
	return deadlock.NewDetector(deadlock.NewManager(120*time.Second, m), tracker, reg, lookup, fail)
}

// TestDetectorTimeoutRecordedOnlyAfterAppliedFail_spec_8_8 pins the §16.1
// `timeout` resolution to an applied DEADLOCK_TIMEOUT write. An action whose
// deepest tasks all apply records `timeout` once, measured from the first
// detection. An action with two deepest tasks of which one applies still
// records `timeout` exactly once. An action whose writes are all refused or
// fail records no resolution of any label, and a nil FailFunc records
// nothing. Every deepest task is attempted in each case. Against a detector
// that recorded `timeout` in Observe, the refused case records a
// resolution and fails.
//
// spec: §8.8 (Subtree deadlock detection), §16.1
func TestDetectorTimeoutRecordedOnlyAfterAppliedFail_spec_8_8(t *testing.T) {
	cases := []struct {
		name            string
		applies         map[string]bool
		nilFail         bool
		wantResolutions []string
		wantAttempts    []string
	}{
		{name: "every fail applies", applies: map[string]bool{"C1": true, "C2": true}, wantResolutions: []string{"timeout"}, wantAttempts: []string{"C1", "C2"}},
		{name: "one of two applies", applies: map[string]bool{"C2": true}, wantResolutions: []string{"timeout"}, wantAttempts: []string{"C1", "C2"}},
		{name: "every fail refused", applies: map[string]bool{}, wantAttempts: []string{"C1", "C2"}},
		{name: "nil fail func", nilFail: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &fakeMetrics{}
			var attempts []string
			var fail deadlock.FailFunc
			if !tc.nilFail {
				fail = func(_ context.Context, _, sessionID string) bool {
					attempts = append(attempts, sessionID)
					return tc.applies[sessionID]
				}
			}
			det := twoDeepestDetector(t, m, fail)
			det.RunOnce(context.Background(), base)
			det.RunOnce(context.Background(), base.Add(150*time.Second))
			if !reflect.DeepEqual(attempts, tc.wantAttempts) {
				t.Errorf("fail attempts = %v, want %v", attempts, tc.wantAttempts)
			}
			if !reflect.DeepEqual(m.resolutions, tc.wantResolutions) {
				t.Fatalf("resolutions = %v, want %v", m.resolutions, tc.wantResolutions)
			}
			if len(tc.wantResolutions) == 1 && m.durations[0] != 150 {
				t.Errorf("timeout duration = %v, want 150 (now minus DetectedAt)", m.durations[0])
			}
		})
	}
}

// TestDetectorRefusedFailRedetectsWithFreshDetectedAt_spec_8_8 covers a
// timed-out action whose fail returned false, as a store error does: the
// entry leaves the active set with no resolution recorded, and the next
// sweep over the still-deadlocked subtree registers it again with a fresh
// detectedAt and willTimeoutAt and fires the detection metric again.
//
// spec: §8.8 (Subtree deadlock detection), §16.1
func TestDetectorRefusedFailRedetectsWithFreshDetectedAt_spec_8_8(t *testing.T) {
	m := &fakeMetrics{}
	det := twoDeepestDetector(t, m, func(context.Context, string, string) bool { return false })
	det.RunOnce(context.Background(), base)
	timedOut := base.Add(120 * time.Second)
	det.RunOnce(context.Background(), timedOut)
	if _, ok := det.Manager().Event("P"); ok {
		t.Fatal("Event(P) still active after the timed-out sweep, want the entry deleted")
	}
	if len(m.resolutions) != 0 {
		t.Fatalf("resolutions = %v after a refused fail, want none", m.resolutions)
	}
	redetect := timedOut.Add(10 * time.Second)
	det.RunOnce(context.Background(), redetect)
	ev, ok := det.Manager().Event("P")
	if !ok {
		t.Fatal("Event(P) absent on the next sweep, want the subtree re-detected")
	}
	if !ev.DetectedAt.Equal(redetect) || !ev.WillTimeoutAt.Equal(redetect.Add(120*time.Second)) {
		t.Errorf("re-detected window = %v..%v, want %v..%v", ev.DetectedAt, ev.WillTimeoutAt, redetect, redetect.Add(120*time.Second))
	}
	if m.detected != 2 {
		t.Errorf("detected metric = %d, want 2 (first detection and re-detection)", m.detected)
	}
}
