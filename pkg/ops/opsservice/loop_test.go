// SPDX-License-Identifier: MIT

package opsservice

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// manualTicker replaces the package tickerFor so a loop's ticks are
// deterministic rather than wall-clock driven. Each loop the runner
// starts gets its own channel; fire broadcasts a tick to every loop,
// so a runner with several loops advances them all in lockstep. It
// returns a restore function.
func manualTicker(t *testing.T) (fire func(), restore func()) {
	t.Helper()
	var mu sync.Mutex
	var channels []chan time.Time
	prev := tickerFor
	tickerFor = func(time.Duration) (<-chan time.Time, func()) {
		ch := make(chan time.Time, 64)
		mu.Lock()
		channels = append(channels, ch)
		mu.Unlock()
		return ch, func() {}
	}
	fire = func() {
		mu.Lock()
		defer mu.Unlock()
		for _, ch := range channels {
			ch <- time.Now()
		}
	}
	return fire, func() { tickerFor = prev }
}

// waitFor polls cond until it holds or the deadline elapses, so a test
// does not depend on a fixed sleep for a goroutine to make progress.
func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", msg)
}

func TestLoopRunsImmediatelyAndOnTick(t *testing.T) {
	fire, restore := manualTicker(t)
	defer restore()

	var ticks atomic.Int32
	r := NewLoopRunner(Loop{
		Name:       "counter",
		Interval:   time.Hour,
		LeaderOnly: true,
		Tick: func(context.Context) error {
			ticks.Add(1)
			return nil
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.StartLeaderLoops(ctx)

	// The loop runs Tick once immediately on start.
	waitFor(t, func() bool { return ticks.Load() == 1 }, "the immediate first tick")

	fire()
	waitFor(t, func() bool { return ticks.Load() == 2 }, "a tick after firing the ticker")

	r.StopLeaderLoops()
}

func TestStopLeaderLoopsHaltsTicks(t *testing.T) {
	fire, restore := manualTicker(t)
	defer restore()

	var ticks atomic.Int32
	r := NewLoopRunner(Loop{
		Name: "counter", Interval: time.Hour, LeaderOnly: true,
		Tick: func(context.Context) error { ticks.Add(1); return nil },
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r.StartLeaderLoops(ctx)
	waitFor(t, func() bool { return ticks.Load() == 1 }, "the first tick")
	r.StopLeaderLoops()

	if r.Running() {
		t.Error("Running() = true after StopLeaderLoops")
	}
	// After Stop the loop goroutine has returned; further ticks are not
	// observed. The ticker channel is buffered, so firing it is safe.
	after := ticks.Load()
	fire()
	time.Sleep(20 * time.Millisecond)
	if got := ticks.Load(); got != after {
		t.Errorf("tick count moved from %d to %d after StopLeaderLoops", after, got)
	}
}

func TestReplicaLoopsRunWithoutLeadership(t *testing.T) {
	_, restore := manualTicker(t)
	defer restore()

	var leaderTicks, replicaTicks atomic.Int32
	r := NewLoopRunner(
		Loop{
			Name: "leader-only", Interval: time.Hour, LeaderOnly: true,
			Tick: func(context.Context) error { leaderTicks.Add(1); return nil },
		},
		Loop{
			Name: "every-replica", Interval: time.Hour, LeaderOnly: false,
			Tick: func(context.Context) error { replicaTicks.Add(1); return nil },
		},
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// A follower starts only the replica loops, never the leader loops.
	r.StartReplicaLoops(ctx)
	waitFor(t, func() bool { return replicaTicks.Load() == 1 }, "the replica loop's first tick")
	if got := leaderTicks.Load(); got != 0 {
		t.Errorf("leader-only loop ticked %d times on a follower, want 0", got)
	}
}

func TestLoopSurvivesTickErrorAndPanic(t *testing.T) {
	fire, restore := manualTicker(t)
	defer restore()

	var ticks atomic.Int32
	r := NewLoopRunner(Loop{
		Name: "flaky", Interval: time.Hour, LeaderOnly: true,
		Tick: func(context.Context) error {
			n := ticks.Add(1)
			switch n {
			case 1:
				panic("boom")
			case 2:
				return context.DeadlineExceeded
			}
			return nil
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.StartLeaderLoops(ctx)

	// Tick 1 panics; the loop must keep running.
	waitFor(t, func() bool { return ticks.Load() == 1 }, "the panicking first tick")
	fire() // tick 2 returns an error
	waitFor(t, func() bool { return ticks.Load() == 2 }, "the erroring second tick")
	fire() // tick 3 succeeds — proves the loop survived both
	waitFor(t, func() bool { return ticks.Load() == 3 }, "a successful tick after panic and error")

	r.StopLeaderLoops()
}

func TestLoopNamesReflectDeclaredLoops(t *testing.T) {
	r := NewLoopRunner(
		Loop{Name: "cron-evaluator"},
		Loop{Name: "webhook-delivery"},
		Loop{Name: "self-monitor"},
	)
	got := r.Loops()
	want := []string{"cron-evaluator", "webhook-delivery", "self-monitor"}
	if len(got) != len(want) {
		t.Fatalf("Loops() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Loops()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
