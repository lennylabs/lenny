// SPDX-License-Identifier: MIT

package slothealth

import (
	"sync"
	"testing"
	"time"
)

// fakeClock is a deterministic, monotonically-advancing clock for the
// rolling-window tests.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// spec: §5.2 ceil(maxConcurrent/2) — the unhealthy-slot threshold.
func TestUnhealthyThreshold_spec_5_2(t *testing.T) {
	cases := map[int32]int{1: 1, 2: 1, 3: 2, 4: 2, 5: 3, 8: 4, 0: 1, -3: 1}
	for mc, want := range cases {
		if got := UnhealthyThreshold(mc); got != want {
			t.Errorf("UnhealthyThreshold(%d) = %d, want %d", mc, got, want)
		}
	}
}

// spec: §5.2 whole-pod replacement — failed slots reaching the threshold
// within the window mark the pod unhealthy.
func TestFailuresReachThreshold_spec_5_2(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	tr := New(WithClock(clk.now))

	// maxConcurrent=4 → threshold ceil(4/2)=2.
	tr.RecordFailure("pod-a")
	if tr.Unhealthy("pod-a", 4) {
		t.Fatal("one failure must not trip the maxConcurrent=4 threshold (needs 2)")
	}
	tr.RecordFailure("pod-a")
	if !tr.Unhealthy("pod-a", 4) {
		t.Fatal("two failures must trip the maxConcurrent=4 threshold")
	}
}

// spec: §6.2 "`leaked` slot semantics" — the persistent leaked count is
// combined with the windowed failed count; leaks alone reaching the
// threshold trip it.
func TestLeaksCountTowardThreshold_spec_6_2(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	tr := New(WithClock(clk.now))

	tr.RecordLeak("pod-b")
	tr.RecordLeak("pod-b")
	if !tr.Unhealthy("pod-b", 3) { // threshold ceil(3/2)=2
		t.Fatal("two leaks must trip the threshold independent of any failures")
	}
	f, l := tr.Counts("pod-b")
	if f != 0 || l != 2 {
		t.Fatalf("Counts = (failed=%d, leaked=%d), want (0, 2)", f, l)
	}
}

// spec: §6.2 "`leaked` slot semantics" — windowed failed_slots plus
// persistent leaked_slots combine toward the threshold.
func TestFailAndLeakCombine_spec_6_2(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	tr := New(WithClock(clk.now))

	tr.RecordFailure("pod-c")
	if tr.Unhealthy("pod-c", 4) { // threshold 2
		t.Fatal("one failure alone must not trip")
	}
	tr.RecordLeak("pod-c")
	if !tr.Unhealthy("pod-c", 4) {
		t.Fatal("one failure plus one leak must trip the maxConcurrent=4 threshold")
	}
}

// Regression for F-5.2.31 / SPEC-D (§6.2 "leaked slot semantics"): a leaked
// slot is counted persistently, so permanent leaks arriving more than one
// window apart still accumulate toward ceil(maxConcurrent/2) rather than
// aging each leak out of the count and never reaching the threshold. Under
// the pre-fix rolling-window model each leak was a timestamped event pruned
// by the 5-minute window, so this pod would never be unhealthy; that is the
// availability gap this asserts is closed.
// spec: §6.2 "`leaked` slot semantics" (leaked slots counted persistently),
// §5.2 whole-pod replacement trigger.
func TestLeaksCountPersistentlyAcrossWindows_spec_6_2(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	tr := New(WithClock(clk.now), WithWindow(5*time.Minute))

	// maxConcurrent=3 → threshold ceil(3/2)=2.
	tr.RecordLeak("pod-leak")
	// Advance well past the rolling window before the second permanent leak.
	clk.advance(10 * time.Minute)
	if tr.Unhealthy("pod-leak", 3) {
		t.Fatal("one persistent leak alone must not trip the maxConcurrent=3 threshold (needs 2)")
	}
	tr.RecordLeak("pod-leak")
	if !tr.Unhealthy("pod-leak", 3) {
		t.Fatal("two permanent leaks 10m apart must both count and trip the threshold; a leak must not age out of the persistent count")
	}
	if f, l := tr.Counts("pod-leak"); f != 0 || l != 2 {
		t.Fatalf("Counts = (failed=%d, leaked=%d), want (0, 2): the leaked count is persistent, not windowed", f, l)
	}
}

// Regression for SPEC-D: a windowed failure ages out of the fail/leak count
// while a persistent leak on the same pod does not. A leak recorded at t0
// stays counted; a failure recorded at t0 is gone after the window, so a
// pod carrying one persistent leak and one long-expired failure counts one,
// not two. This pins the split lifetime: failures roll, leaks persist.
// spec: §6.2 "`leaked` slot semantics" (rolling-window failed_slots plus
// persistent leaked_slots).
func TestFailureAgesOutLeakPersists_spec_6_2(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	tr := New(WithClock(clk.now), WithWindow(5*time.Minute))

	tr.RecordLeak("pod-mix")
	tr.RecordFailure("pod-mix")
	if f, l := tr.Counts("pod-mix"); f != 1 || l != 1 {
		t.Fatalf("precondition Counts = (failed=%d, leaked=%d), want (1, 1)", f, l)
	}
	// Advance past the window: the failure expires, the leak persists.
	clk.advance(6 * time.Minute)
	if f, l := tr.Counts("pod-mix"); f != 0 || l != 1 {
		t.Fatalf("after window Counts = (failed=%d, leaked=%d), want (0, 1): the failure must age out, the leak must persist", f, l)
	}
	// maxConcurrent=2 → threshold 1: the surviving persistent leak alone trips.
	if !tr.Unhealthy("pod-mix", 2) {
		t.Fatal("the persistent leak alone must keep the pod unhealthy after the failure ages out")
	}
}

// Forget releases the persistent leak count at pod termination so a
// replacement pod reusing the name starts clean. spec: §6.2 "`leaked` slot
// semantics" (released at pod termination).
func TestForgetClearsPersistentLeaks_spec_6_2(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	tr := New(WithClock(clk.now))
	tr.RecordLeak("pod-fl")
	tr.RecordLeak("pod-fl")
	if !tr.Unhealthy("pod-fl", 3) { // threshold 2
		t.Fatal("precondition: two persistent leaks trip maxConcurrent=3")
	}
	tr.Forget("pod-fl")
	if tr.Unhealthy("pod-fl", 3) {
		t.Fatal("Forget must release the persistent leak count")
	}
	if f, l := tr.Counts("pod-fl"); f != 0 || l != 0 {
		t.Fatalf("after Forget Counts = (%d, %d), want (0, 0)", f, l)
	}
}

// spec: §5.2 — the window is rolling: events older than the window are
// dropped and no longer count toward the threshold.
func TestRollingWindowExpiry_spec_5_2(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	tr := New(WithClock(clk.now), WithWindow(5*time.Minute))

	tr.RecordFailure("pod-d")
	clk.advance(4 * time.Minute)
	tr.RecordFailure("pod-d")
	if !tr.Unhealthy("pod-d", 3) { // both within 5m, threshold 2
		t.Fatal("two failures 4m apart are both in-window and must trip")
	}
	// Advance past the first event's expiry (total 6m from the first).
	clk.advance(90 * time.Second)
	if tr.Unhealthy("pod-d", 3) {
		t.Fatal("after the first event expires only one in-window failure remains; must not trip")
	}
	f, _ := tr.Counts("pod-d")
	if f != 1 {
		t.Fatalf("expected one in-window failure after expiry, got %d", f)
	}
}

// Forget clears a pod's history so a replacement (or recovered) pod starts
// clean. spec: §5.2 whole-pod replacement — the drained pod is replaced.
func TestForget(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	tr := New(WithClock(clk.now))
	tr.RecordFailure("pod-e")
	tr.RecordFailure("pod-e")
	if !tr.Unhealthy("pod-e", 2) {
		t.Fatal("precondition: two failures trip maxConcurrent=2")
	}
	tr.Forget("pod-e")
	if tr.Unhealthy("pod-e", 2) {
		t.Fatal("Forget must clear the pod's history")
	}
	if f, l := tr.Counts("pod-e"); f != 0 || l != 0 {
		t.Fatalf("after Forget Counts = (%d, %d), want (0, 0)", f, l)
	}
}

// Distinct pods are tracked independently.
func TestPerPodIsolation(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	tr := New(WithClock(clk.now))
	tr.RecordFailure("pod-f")
	tr.RecordFailure("pod-f")
	if tr.Unhealthy("pod-g", 2) {
		t.Fatal("pod-g has no events and must not be unhealthy")
	}
	if !tr.Unhealthy("pod-f", 2) {
		t.Fatal("pod-f must be unhealthy independent of pod-g")
	}
}

// The tracker is safe under concurrent recorders (race detector).
func TestConcurrentRecording(t *testing.T) {
	tr := New()
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tr.RecordFailure("pod-h")
			tr.RecordLeak("pod-h")
			_ = tr.Unhealthy("pod-h", 4)
			tr.Counts("pod-h")
		}()
	}
	wg.Wait()
	if f, l := tr.Counts("pod-h"); f != 32 || l != 32 {
		t.Fatalf("Counts = (failed=%d, leaked=%d), want (32, 32)", f, l)
	}
}
