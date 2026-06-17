// SPDX-License-Identifier: MIT

package memstore_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/agentpodstate"
	"github.com/lennylabs/lenny/pkg/agentpodstate/memstore"
)

// fixedClock returns a controllable clock for deterministic updated_at and
// MirrorLagSeconds. Advance moves it forward.
type fixedClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fixedClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fixedClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newStore(t *testing.T) (*memstore.Store, *fixedClock) {
	t.Helper()
	clk := &fixedClock{t: time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC)}
	return memstore.New(clk.now), clk
}

// TestIncrementSessionsServedFromNull pins the §4.7 ReportSessionScrub
// counter write: a NULL (never-written) sessions_served is treated as 0,
// so the first increment returns 1 and each subsequent one adds one.
// spec: §4.7 (ReportSessionScrub increments sessionsServed), §5.2.
func TestIncrementSessionsServedFromNull_spec_4_7(t *testing.T) {
	s, _ := newStore(t)
	s.Put(agentpodstate.PodState{PodID: "pod-a", PoolID: "pool-1", State: "claimed"})

	for want := 1; want <= 3; want++ {
		got, ok, err := s.IncrementSessionsServed(context.Background(), "pod-a")
		if err != nil || !ok {
			t.Fatalf("IncrementSessionsServed: got (%d, %v, %v)", got, ok, err)
		}
		if got != want {
			t.Fatalf("IncrementSessionsServed = %d, want %d", got, want)
		}
	}
}

// TestIncrementScrubFailureCountFromNull pins the §4.7 ReportPodScrub
// counter write on a failed whole-pod scrub.
// spec: §4.7 (ReportPodScrub increments scrubFailureCount), §5.2.
func TestIncrementScrubFailureCountFromNull_spec_4_7(t *testing.T) {
	s, _ := newStore(t)
	s.Put(agentpodstate.PodState{PodID: "pod-a", PoolID: "pool-1", State: "claimed"})

	got, ok, err := s.IncrementScrubFailureCount(context.Background(), "pod-a")
	if err != nil || !ok || got != 1 {
		t.Fatalf("first IncrementScrubFailureCount = (%d, %v, %v), want (1, true, nil)", got, ok, err)
	}
	got, _, _ = s.IncrementScrubFailureCount(context.Background(), "pod-a")
	if got != 2 {
		t.Fatalf("second IncrementScrubFailureCount = %d, want 2", got)
	}
}

// TestCountersAreIndependent confirms incrementing one recycle counter
// leaves the other untouched, so the recycle disposition evaluates each
// against its own bound.
// spec: §5.2 (recycle.maxSessionsPerPod / recycle.maxScrubFailures).
func TestCountersAreIndependent_spec_5_2(t *testing.T) {
	s, _ := newStore(t)
	s.Put(agentpodstate.PodState{PodID: "pod-a", PoolID: "pool-1", State: "claimed"})

	_, _, _ = s.IncrementSessionsServed(context.Background(), "pod-a")
	_, _, _ = s.IncrementSessionsServed(context.Background(), "pod-a")
	_, _, _ = s.IncrementScrubFailureCount(context.Background(), "pod-a")

	rc, ok, err := s.RecycleCounters(context.Background(), "pod-a")
	if err != nil || !ok {
		t.Fatalf("RecycleCounters: (%+v, %v, %v)", rc, ok, err)
	}
	if rc.SessionsServed != 2 || rc.ScrubFailureCount != 1 {
		t.Fatalf("RecycleCounters = %+v, want {SessionsServed:2 ScrubFailureCount:1}", rc)
	}
}

// TestRecycleCountersNullReadsAsZero pins the §12.6 NULL-as-0 read: a pod
// whose counters were never written reads back zeroes, so the disposition
// sees a fresh pod rather than a missing value.
// spec: §12.6 (agent_pod_state columns nullable), §5.2.
func TestRecycleCountersNullReadsAsZero_spec_12_6(t *testing.T) {
	s, _ := newStore(t)
	s.Put(agentpodstate.PodState{PodID: "pod-a", PoolID: "pool-1", State: "idle"})

	rc, ok, err := s.RecycleCounters(context.Background(), "pod-a")
	if err != nil || !ok {
		t.Fatalf("RecycleCounters: (%+v, %v, %v)", rc, ok, err)
	}
	if rc.SessionsServed != 0 || rc.ScrubFailureCount != 0 {
		t.Fatalf("RecycleCounters = %+v, want zeroes", rc)
	}
}

// TestRecycleCounterMissingPod fails closed: an increment or read against a
// pod with no mirror row reports not-found and writes nothing, so the
// gateway does not silently fabricate a counter for an unknown pod.
// spec: §4.7 (counter writes target the pod's agent_pod_state row).
func TestRecycleCounterMissingPod_spec_4_7(t *testing.T) {
	s, _ := newStore(t)

	if got, ok, err := s.IncrementSessionsServed(context.Background(), "ghost"); ok || err != nil || got != 0 {
		t.Fatalf("IncrementSessionsServed(ghost) = (%d, %v, %v), want (0, false, nil)", got, ok, err)
	}
	if got, ok, err := s.IncrementScrubFailureCount(context.Background(), "ghost"); ok || err != nil || got != 0 {
		t.Fatalf("IncrementScrubFailureCount(ghost) = (%d, %v, %v), want (0, false, nil)", got, ok, err)
	}
	if rc, ok, err := s.RecycleCounters(context.Background(), "ghost"); ok || err != nil || rc != (agentpodstate.RecycleCounters{}) {
		t.Fatalf("RecycleCounters(ghost) = (%+v, %v, %v), want (zero, false, nil)", rc, ok, err)
	}
}

// TestRecycleCounterEmptyPodID short-circuits an empty pod id without a
// fabricated write, matching the Postgres backend's NOT NULL primary key.
// spec: §12.6 (pod_id PRIMARY KEY).
func TestRecycleCounterEmptyPodID_spec_12_6(t *testing.T) {
	s, _ := newStore(t)
	if _, ok, err := s.IncrementSessionsServed(context.Background(), ""); ok || err != nil {
		t.Fatalf("IncrementSessionsServed(\"\") = (_, %v, %v), want (false, nil)", ok, err)
	}
	if _, ok, err := s.RecycleCounters(context.Background(), ""); ok || err != nil {
		t.Fatalf("RecycleCounters(\"\") = (_, %v, %v), want (false, nil)", ok, err)
	}
}

// TestCounterWriteAdvancesMirrorClock confirms a counter write stamps
// updated_at from the injected clock, so the §10.1 mirror-staleness gauge
// reflects gateway counter writes and not just WarmPoolController mirror
// passes.
// spec: §10.1 (mirror lag gauge), §12.6 (updated_at).
func TestCounterWriteAdvancesMirrorClock_spec_10_1(t *testing.T) {
	s, clk := newStore(t)
	s.Put(agentpodstate.PodState{PodID: "pod-a", PoolID: "pool-1", State: "claimed"})

	clk.advance(30 * time.Second)
	if lag, err := s.MirrorLagSeconds(context.Background(), "pool-1"); err != nil || lag != 30 {
		t.Fatalf("MirrorLagSeconds before write = (%v, %v), want (30, nil)", lag, err)
	}

	// The counter write re-stamps updated_at to the current clock, resetting lag.
	if _, _, err := s.IncrementSessionsServed(context.Background(), "pod-a"); err != nil {
		t.Fatalf("IncrementSessionsServed: %v", err)
	}
	if lag, err := s.MirrorLagSeconds(context.Background(), "pool-1"); err != nil || lag != 0 {
		t.Fatalf("MirrorLagSeconds after write = (%v, %v), want (0, nil)", lag, err)
	}
}

// TestConcurrentIncrementsNoLostUpdate exercises the store under -race:
// N goroutines each increment sessions_served once, and the final value is
// exactly N with no lost update.
// spec: §4.7 (concurrent ReportSessionScrub increments are atomic).
func TestConcurrentIncrementsNoLostUpdate_spec_4_7(t *testing.T) {
	s, _ := newStore(t)
	s.Put(agentpodstate.PodState{PodID: "pod-a", PoolID: "pool-1", State: "claimed"})

	const n = 64
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			if _, _, err := s.IncrementSessionsServed(context.Background(), "pod-a"); err != nil {
				t.Errorf("IncrementSessionsServed: %v", err)
			}
		}()
	}
	wg.Wait()

	rc, _, err := s.RecycleCounters(context.Background(), "pod-a")
	if err != nil {
		t.Fatalf("RecycleCounters: %v", err)
	}
	if rc.SessionsServed != n {
		t.Fatalf("SessionsServed = %d, want %d", rc.SessionsServed, n)
	}
}
