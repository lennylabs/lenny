// SPDX-License-Identifier: MIT

package deadlock_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	session "github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/deadlock"
	"github.com/lennylabs/lenny/pkg/gateway/session/inputwait"
)

type fakeMetrics struct {
	detected    int
	resolutions []string
	durations   []float64
}

func (f *fakeMetrics) IncDelegationDeadlockDetected(string) { f.detected++ }
func (f *fakeMetrics) ObserveDelegationDeadlockResolution(resolution string, seconds float64) {
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
	if to := mgr.Observe(deadlockedSnap(), t0); len(to) != 0 {
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
// deepest blocked tasks, records a timeout resolution with the elapsed
// duration, and drops the active entry.
func TestManagerTimeoutFiresDeepestTasks_spec_8_8_981(t *testing.T) {
	m := &fakeMetrics{}
	mgr := deadlock.NewManager(120*time.Second, m)
	mgr.Observe(deadlockedSnap(), base)
	to := mgr.Observe(deadlockedSnap(), base.Add(120*time.Second))
	if len(to) != 1 {
		t.Fatalf("Observe at timeout returned %d actions, want 1", len(to))
	}
	if to[0].Root != "P" || !reflect.DeepEqual(to[0].DeepestTasks, []string{"C"}) {
		t.Errorf("timeout action = %+v, want root=P deepest=[C]", to[0])
	}
	if to[0].TenantID != "acme" {
		t.Errorf("timeout tenant = %q, want acme", to[0].TenantID)
	}
	if !reflect.DeepEqual(m.resolutions, []string{"timeout"}) {
		t.Errorf("resolutions = %v, want [timeout]", m.resolutions)
	}
	if len(m.durations) != 1 || m.durations[0] != 120 {
		t.Errorf("durations = %v, want [120]", m.durations)
	}
	if _, ok := mgr.Event("P"); ok {
		t.Errorf("Event(P) should be cleared after timeout")
	}
}

// TestManagerResolvedWhenNoLongerDetected_spec_8_8_997 covers the root
// breaking the deadlock before willTimeoutAt: the next sweep no longer
// detects it, so the manager records a `resolved` resolution and emits no
// timeout.
func TestManagerResolvedWhenNoLongerDetected_spec_8_8_997(t *testing.T) {
	m := &fakeMetrics{}
	mgr := deadlock.NewManager(120*time.Second, m)
	mgr.Observe(deadlockedSnap(), base)
	// The root answered the child's request_input — C is no longer blocked.
	resolved := snap(
		node("P", session.StateRunning, []string{"C"}),
		node("C", session.StateRunning, nil),
	)
	to := mgr.Observe(resolved, base.Add(40*time.Second))
	if len(to) != 0 {
		t.Fatalf("Observe after resolution returned timeouts %+v, want none", to)
	}
	if !reflect.DeepEqual(m.resolutions, []string{"resolved"}) {
		t.Errorf("resolutions = %v, want [resolved]", m.resolutions)
	}
	if len(m.durations) != 1 || m.durations[0] != 40 {
		t.Errorf("durations = %v, want [40]", m.durations)
	}
	if _, ok := mgr.Event("P"); ok {
		t.Errorf("Event(P) should be cleared after resolution")
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

	lookup := func(_ context.Context, _ /*tenant*/, sessionID string) (session.State, bool) {
		// Both P and C are live and running; C's input_required signal is
		// carried by the request_input registry, not the coarse row state.
		if sessionID == "P" || sessionID == "C" {
			return session.StateRunning, true
		}
		return "", false
	}
	var failed []string
	fail := func(_ context.Context, tenant, sessionID string) {
		if tenant != "acme" {
			t.Errorf("fail tenant = %q, want acme", tenant)
		}
		failed = append(failed, sessionID)
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
}
