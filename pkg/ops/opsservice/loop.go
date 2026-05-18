// SPDX-License-Identifier: MIT

package opsservice

import (
	"context"
	"log"
	"sync"
	"time"
)

// Loop is one §25.4 leader-gated background loop: a named unit of work
// the leader replica runs on a fixed interval. The cron evaluator, the
// webhook delivery worker, the scheduled-backup runner, the
// reconciliation goroutines, and the self-monitor are each a Loop.
type Loop struct {
	// Name identifies the loop in logs and the Loops() introspection.
	Name string
	// Interval is the period between ticks.
	Interval time.Duration
	// Tick performs one iteration. It is invoked once immediately when
	// the loop starts and then once per Interval. A returned error is
	// logged; the loop continues on the next tick.
	Tick func(ctx context.Context) error
	// LeaderOnly marks a loop that runs only while this replica holds the
	// §25.4 leader-election lease. The singleton behaviors (cron
	// evaluator, webhook delivery, scheduled backups, the reconciliation
	// goroutines) set it. A loop that every replica runs — the
	// self-monitor — leaves it false.
	LeaderOnly bool
}

// tickerFor returns the channel that fires the loop's ticks. It is a
// package variable so tests can substitute a deterministic clock; the
// production value uses a real time.Ticker.
var tickerFor = func(d time.Duration) (<-chan time.Time, func()) {
	t := time.NewTicker(d)
	return t.C, t.Stop
}

// LoopRunner owns the §25.4 background loops. The leader-only loops are
// started by the leader-election OnStartedLeading callback and stopped
// by OnStoppedLeading; loops that every replica runs are started once
// at process start. Start and Stop are idempotent and safe to call
// from the election callbacks, which fire on the leader-election
// goroutine.
type LoopRunner struct {
	mu      sync.Mutex
	loops   []Loop
	cancel  context.CancelFunc
	running bool
	// leaderWG tracks the leader-gated loop goroutines; StopLeaderLoops
	// waits on it alone so a replica that loses leadership does not
	// block on the per-replica loops, which keep running. replicaWG
	// tracks the per-replica loop goroutines.
	leaderWG  sync.WaitGroup
	replicaWG sync.WaitGroup
}

// NewLoopRunner returns a runner over the given loops.
func NewLoopRunner(loops ...Loop) *LoopRunner {
	return &LoopRunner{loops: loops}
}

// Loops returns the names of every loop the runner owns, in declared
// order. The HTTP surface uses it for operator introspection.
func (r *LoopRunner) Loops() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	names := make([]string, len(r.loops))
	for i, l := range r.loops {
		names[i] = l.Name
	}
	return names
}

// Running reports whether the leader-gated loops are currently active.
func (r *LoopRunner) Running() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running
}

// StartLeaderLoops starts every LeaderOnly loop. The parent context
// bounds the loops; losing leadership (StopLeaderLoops) or process
// shutdown (cancelling parent) stops them. A second call while the
// loops are already running is a no-op.
func (r *LoopRunner) StartLeaderLoops(parent context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	r.cancel = cancel
	r.running = true
	for _, l := range r.loops {
		if l.LeaderOnly {
			r.leaderWG.Add(1)
			go r.run(ctx, l, &r.leaderWG)
		}
	}
}

// StopLeaderLoops cancels the leader-gated loops and blocks until every
// loop goroutine has returned, so a replica that loses leadership has
// fully relinquished the singleton behaviors before another replica
// picks them up. A call while the loops are not running is a no-op.
func (r *LoopRunner) StopLeaderLoops() {
	r.mu.Lock()
	if !r.running {
		r.mu.Unlock()
		return
	}
	cancel := r.cancel
	r.running = false
	r.cancel = nil
	r.mu.Unlock()

	cancel()
	// Wait only on the leader-gated loops: the per-replica loops keep
	// running regardless of leadership, so waiting on them here would
	// deadlock a replica that loses the lease while still serving.
	r.leaderWG.Wait()
}

// StartReplicaLoops starts every loop that is not LeaderOnly — the
// loops every replica runs regardless of leadership, the self-monitor
// among them. They run until parent is cancelled.
func (r *LoopRunner) StartReplicaLoops(parent context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, l := range r.loops {
		if !l.LeaderOnly {
			r.replicaWG.Add(1)
			go r.run(parent, l, &r.replicaWG)
		}
	}
}

// Wait blocks until every loop goroutine the runner started — both the
// leader-gated and the per-replica loops — has returned. It is used at
// shutdown after the parent context is cancelled.
func (r *LoopRunner) Wait() {
	r.leaderWG.Wait()
	r.replicaWG.Wait()
}

// run drives one loop: an immediate first tick, then one tick per
// interval until ctx is cancelled. A tick error is logged and the loop
// continues — a transient dependency outage must not kill the loop.
func (r *LoopRunner) run(ctx context.Context, l Loop, wg *sync.WaitGroup) {
	defer wg.Done()
	if l.Interval <= 0 || l.Tick == nil {
		return
	}
	r.tick(ctx, l)
	c, stop := tickerFor(l.Interval)
	defer stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-c:
			r.tick(ctx, l)
		}
	}
}

// tick runs one loop iteration, recovering from a panic so a bug in
// one loop does not crash the process or take down the other loops.
func (r *LoopRunner) tick(ctx context.Context, l Loop) {
	defer func() {
		if p := recover(); p != nil {
			log.Printf("lenny-ops: loop %q panicked: %v", l.Name, p)
		}
	}()
	if ctx.Err() != nil {
		return
	}
	if err := l.Tick(ctx); err != nil {
		log.Printf("lenny-ops: loop %q tick: %v", l.Name, err)
	}
}
