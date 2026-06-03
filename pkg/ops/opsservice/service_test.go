// SPDX-License-Identifier: MIT

package opsservice

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeElector is a test Elector whose leadership the test controls. It
// invokes the leader-acquired callback when Lead is called and the
// leader-lost callback when Demote is called, so the leader-election
// gating can be exercised without a Kubernetes cluster.
type fakeElector struct {
	mu       sync.Mutex
	leader   atomic.Bool
	started  func(context.Context)
	stopped  func()
	ctx      context.Context
	cancelLE context.CancelFunc
	ready    chan struct{}
}

func newFakeElector() *fakeElector {
	return &fakeElector{ready: make(chan struct{})}
}

func (f *fakeElector) Run(ctx context.Context, onStarted func(context.Context), onStopped func()) {
	f.mu.Lock()
	f.started = onStarted
	f.stopped = onStopped
	f.ctx = ctx
	close(f.ready)
	f.mu.Unlock()
	<-ctx.Done()
}

// Lead simulates this replica winning the lenny-ops-leader Lease.
func (f *fakeElector) Lead() {
	<-f.ready
	f.mu.Lock()
	leaderCtx, cancel := context.WithCancel(f.ctx)
	f.cancelLE = cancel
	started := f.started
	f.mu.Unlock()
	f.leader.Store(true)
	started(leaderCtx)
}

// Demote simulates this replica losing the lease.
func (f *fakeElector) Demote() {
	f.mu.Lock()
	cancel := f.cancelLE
	stopped := f.stopped
	f.mu.Unlock()
	f.leader.Store(false)
	if cancel != nil {
		cancel()
	}
	stopped()
}

func (f *fakeElector) IsLeader() bool { return f.leader.Load() }

// TestOnlyLeaderRunsLeaderLoops is the §25.4 leader-gating contract: a
// replica runs the singleton background loops only while it holds the
// lease. Before acquiring leadership the leader-only loops are idle;
// they start on acquisition and stop on loss.
func TestOnlyLeaderRunsLeaderLoops(t *testing.T) {
	fire, restore := manualTicker(t)
	defer restore()

	var leaderTicks atomic.Int32
	elector := newFakeElector()
	svc, err := New(Config{
		ReplicaID:          "ops-0",
		Elector:            elector,
		SelfHealthInterval: time.Hour,
		Reconcilers: Reconcilers{
			// EscalationFlush is one of the §25.4 leader-only
			// reconciliation goroutines; counting its ticks proves the
			// leader gating.
			EscalationFlush: func(context.Context) error {
				leaderTicks.Add(1)
				return nil
			},
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { svc.Run(ctx); close(done) }()

	// Phase 1: before leadership, the leader-only loop must be idle.
	waitFor(t, func() bool { return svc.LoopNames() != nil }, "the service to start")
	time.Sleep(20 * time.Millisecond)
	if got := leaderTicks.Load(); got != 0 {
		t.Fatalf("leader loop ticked %d times before leadership, want 0", got)
	}
	if svc.LeaderLoopsRunning() {
		t.Fatal("LeaderLoopsRunning() = true before leadership")
	}

	// Phase 2: acquire leadership — the leader-only loop starts.
	elector.Lead()
	waitFor(t, func() bool { return svc.IsLeader() && svc.LeaderLoopsRunning() }, "leadership")
	waitFor(t, func() bool { return leaderTicks.Load() >= 1 }, "the leader loop's first tick")
	fire()
	waitFor(t, func() bool { return leaderTicks.Load() >= 2 }, "a leader-loop tick after firing")

	// Phase 3: lose leadership — the leader-only loop stops.
	elector.Demote()
	waitFor(t, func() bool { return !svc.LeaderLoopsRunning() }, "leader loops to stop")
	if svc.IsLeader() {
		t.Error("IsLeader() = true after Demote")
	}
	ticksAfterDemote := leaderTicks.Load()
	fire()
	time.Sleep(20 * time.Millisecond)
	if got := leaderTicks.Load(); got != ticksAfterDemote {
		t.Errorf("leader loop ticked after losing leadership: %d -> %d", ticksAfterDemote, got)
	}

	cancel()
	<-done
}

// TestServiceRejectsBadCronExpression confirms New surfaces a malformed
// §25.4 cron expression as an error rather than constructing a service
// whose cron evaluator silently never fires.
func TestServiceRejectsBadCronExpression(t *testing.T) {
	_, err := New(Config{
		ReplicaID: "ops-0",
		Elector:   newFakeElector(),
		CronJobs: []ScheduledJob{
			{Name: "bad", Expression: "not a cron", Run: func(context.Context) error { return nil }},
		},
	})
	if err == nil {
		t.Fatal("New accepted a malformed cron expression, want an error")
	}
}

// TestServiceRunsSelfMonitorOnEveryReplica confirms the §25.4
// self-monitor runs without leadership: a follower still evaluates its
// own self-health.
func TestServiceRunsSelfMonitorOnEveryReplica(t *testing.T) {
	fire, restore := manualTicker(t)
	defer restore()

	var evaluated atomic.Int32
	svc, err := New(Config{
		ReplicaID:          "ops-0",
		Elector:            newFakeElector(),
		SelfHealthInterval: time.Hour,
		SelfHealthChecks: map[string]SelfCheck{
			"probe": func() CheckResult {
				evaluated.Add(1)
				return CheckResult{Name: "probe", Status: StatusHealthy}
			},
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { svc.Run(ctx); close(done) }()

	// The self-monitor evaluates once immediately, with no leadership.
	waitFor(t, func() bool { return evaluated.Load() >= 1 }, "the self-monitor's first evaluation")
	fire()
	waitFor(t, func() bool { return evaluated.Load() >= 2 }, "a self-monitor evaluation after a tick")

	cancel()
	<-done
}

// spec: §16.8 line 704 / §25.4 line 2507 — the self-monitor invokes
// OnSelfHealthSample with the full report on every evaluation (not only on
// a status transition), so the §16.9 /metrics publisher refreshes the
// lenny_ops_self_health_status{check} gauge each tick.
func TestServicePublishesSelfHealthSampleEveryTick_spec_16_8(t *testing.T) {
	fire, restore := manualTicker(t)
	defer restore()

	var samples atomic.Int32
	var lastReport atomic.Pointer[SelfHealthReport]
	svc, err := New(Config{
		ReplicaID:          "ops-0",
		Elector:            newFakeElector(),
		SelfHealthInterval: time.Hour,
		SelfHealthChecks: map[string]SelfCheck{
			"postgres_pool": func() CheckResult {
				return CheckResult{Name: "postgres_pool", Status: StatusDegraded}
			},
		},
		OnSelfHealthSample: func(r SelfHealthReport) {
			samples.Add(1)
			cp := r
			lastReport.Store(&cp)
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { svc.Run(ctx); close(done) }()

	// A healthy->degraded sample is published on the very first evaluation,
	// without any status transition having to fire.
	waitFor(t, func() bool { return samples.Load() >= 1 }, "the first self-health sample")
	fire()
	waitFor(t, func() bool { return samples.Load() >= 2 }, "a self-health sample after a tick")

	if r := lastReport.Load(); r == nil || len(r.Checks) != 1 || r.Checks[0].Name != "postgres_pool" || r.Checks[0].Status != StatusDegraded {
		t.Fatalf("sampled report = %+v, want one degraded postgres_pool check", lastReport.Load())
	}

	cancel()
	<-done
}
