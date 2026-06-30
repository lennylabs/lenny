// SPDX-License-Identifier: MIT

package gatewayleader

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// spec: §12.5 line 332 — the AlwaysLeader elector is the single-process
// dev fallback: it is always the GC writer so every gateway-singleton
// sweep runs for the whole process lifetime and stops on shutdown.
func TestAlwaysLeader_RunsUntilContextCancel(t *testing.T) {
	var started, stopped atomic.Bool
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		AlwaysLeader{}.Run(ctx,
			func(context.Context) { started.Store(true) },
			func() { stopped.Store(true) })
		close(done)
	}()
	// onStarted fires promptly; Run blocks until ctx is cancelled.
	waitFor(t, func() bool { return started.Load() })
	if stopped.Load() {
		t.Fatal("AlwaysLeader invoked onStopped before context cancel")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("AlwaysLeader.Run did not return after context cancel")
	}
	if !stopped.Load() {
		t.Fatal("AlwaysLeader did not invoke onStopped after context cancel")
	}
	if !(AlwaysLeader{}).IsLeader() {
		t.Fatal("AlwaysLeader.IsLeader must always be true")
	}
}

// spec: §12.5 line 317 — when leadership is held, every registered
// gateway-singleton sweep runs.
func TestGate_RunsAllJobsWhileLeader(t *testing.T) {
	var jobAStarted, jobBStarted atomic.Bool
	g := NewGate(AlwaysLeader{}).
		Add("a", func(ctx context.Context) { jobAStarted.Store(true); <-ctx.Done() }).
		Add("b", func(ctx context.Context) { jobBStarted.Store(true); <-ctx.Done() })
	if g.Len() != 2 {
		t.Fatalf("Len = %d, want 2", g.Len())
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go g.Run(ctx)
	waitFor(t, func() bool { return jobAStarted.Load() && jobBStarted.Load() })
}

// A nil job func is ignored rather than launched (defensive: the gateway
// registers sweeps conditionally and a missing dependency yields a nil
// closure at some call sites).
func TestGate_AddNilJobIgnored(t *testing.T) {
	g := NewGate(AlwaysLeader{}).Add("nil", nil)
	if g.Len() != 0 {
		t.Fatalf("Len = %d, want 0 (nil job ignored)", g.Len())
	}
}

// spec: §12.5 line 332 — on leadership loss the leader-scoped context is
// cancelled so every sweep's loop exits; on re-acquire the sweeps are
// re-launched. The fakeElector drives the leader/follower transitions a
// real Lease would produce during failover.
func TestGate_StopsJobsOnLeadershipLossAndRestartsOnReacquire(t *testing.T) {
	var launches atomic.Int32
	var liveCtxMu sync.Mutex
	var liveCtxDone <-chan struct{}

	g := NewGate(nil) // elector set below
	g.Add("sweep", func(ctx context.Context) {
		launches.Add(1)
		liveCtxMu.Lock()
		liveCtxDone = ctx.Done()
		liveCtxMu.Unlock()
		<-ctx.Done()
	})

	fe := &fakeElector{ready: make(chan struct{})}
	g.elector = fe

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go g.Run(ctx)

	// First acquisition launches the sweep.
	fe.acquire()
	waitFor(t, func() bool { return launches.Load() == 1 })

	// Leadership loss cancels the leader-scoped context the sweep selects on.
	fe.lose()
	waitFor(t, func() bool {
		liveCtxMu.Lock()
		d := liveCtxDone
		liveCtxMu.Unlock()
		select {
		case <-d:
			return true
		default:
			return false
		}
	})

	// Re-acquisition re-launches the sweep.
	fe.acquire()
	waitFor(t, func() bool { return launches.Load() == 2 })
}

// spec: §12.5 line 332 — the lease name and failover durations are the
// gateway-scoped constants; the failover bound is LeaseDuration +
// RenewDeadline = 25s.
func TestLeaseConstants(t *testing.T) {
	if LeaseName != "lenny-gateway-leader" {
		t.Fatalf("LeaseName = %q, want lenny-gateway-leader", LeaseName)
	}
	if LeaseDuration+RenewDeadline != 25*time.Second {
		t.Fatalf("failover bound = %s, want 25s", LeaseDuration+RenewDeadline)
	}
	if !(LeaseDuration > RenewDeadline && RenewDeadline > RetryPeriod) {
		t.Fatal("client-go invariant LeaseDuration > RenewDeadline > RetryPeriod violated")
	}
}

func TestLeaseTimings_WithDefaults(t *testing.T) {
	got := LeaseTimings{}.withDefaults()
	if got.LeaseDuration != LeaseDuration || got.RenewDeadline != RenewDeadline || got.RetryPeriod != RetryPeriod {
		t.Fatalf("zero timings = %+v, want built-in defaults", got)
	}
	override := LeaseTimings{LeaseDuration: 30 * time.Second}.withDefaults()
	if override.LeaseDuration != 30*time.Second {
		t.Fatalf("override LeaseDuration dropped: %s", override.LeaseDuration)
	}
	if override.RenewDeadline != RenewDeadline || override.RetryPeriod != RetryPeriod {
		t.Fatal("withDefaults must keep built-ins for omitted fields")
	}
}

// fakeElector drives acquire/lose transitions for the Gate test without a
// Kubernetes cluster, mirroring the OnStartedLeading(leaderCtx) /
// OnStoppedLeading callback contract of client-go's leaderelection.
type fakeElector struct {
	mu        sync.Mutex
	onStarted func(context.Context)
	ready     chan struct{}
	cancel    context.CancelFunc
	leader    atomic.Bool
}

func (f *fakeElector) Run(ctx context.Context, onStarted func(context.Context), _ func()) {
	f.mu.Lock()
	f.onStarted = onStarted
	f.mu.Unlock()
	close(f.ready) // Run registered onStarted; acquire may proceed
	<-ctx.Done()
}

func (f *fakeElector) acquire() {
	<-f.ready // wait until Run has registered onStarted
	f.mu.Lock()
	defer f.mu.Unlock()
	leaderCtx, cancel := context.WithCancel(context.Background())
	f.cancel = cancel
	f.leader.Store(true)
	if f.onStarted != nil {
		f.onStarted(leaderCtx)
	}
}

func (f *fakeElector) lose() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cancel != nil {
		f.cancel()
	}
	f.leader.Store(false)
}

func (f *fakeElector) IsLeader() bool { return f.leader.Load() }

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("condition not met within 2s")
}
