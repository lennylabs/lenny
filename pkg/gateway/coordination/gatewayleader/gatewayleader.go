// SPDX-License-Identifier: MIT

// Package gatewayleader implements the §12.5 gateway-scoped leader
// election that lets exactly one gateway replica run the §12.5 GC
// orchestrator and the other gateway-singleton background sweeps
// (artifact GC, tombstone hard-prune, audit-retention pruner, EventBus
// retranscribe worker, legal-hold reconciler, and the §12.5 line 307 T4
// KMS probe).
//
// spec: §12.5 line 317 — "The job is owned by the gateway — it runs as a
// leader-elected goroutine inside the gateway process (not a separate
// CronJob). Only one gateway instance runs GC at a time via the existing
// leader-election lease." spec: §12.5 line 332 — "gateway-scoped
// singleton jobs use an equivalent `lenny-gateway-leader` Lease scoped to
// the gateway Deployment ... During the bounded failover window (near-zero
// on clean shutdown; up to 25s on crash ...)".
//
// The Lease-based LeaseElector mirrors the §25.4 lenny-ops elector and the
// §4.6.1 controller-runtime leader election the WarmPoolController uses,
// but with the gateway lease name and the §12.5 failover bound. The
// AlwaysLeader elector is the single-process / dev fallback used when the
// gateway is not running in-cluster: there is only one replica, so it is
// always the GC writer and every sweep runs.
package gatewayleader

import (
	"context"
	"sync/atomic"
	"time"

	coordinationv1client "k8s.io/client-go/kubernetes/typed/coordination/v1"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
)

// §12.5 line 332 leader-election lease parameters. The worst-case
// crash-failover window is LeaseDuration + RenewDeadline = 25s, matching
// the §4.6.1 controller failover bound the spec ties the GC failover bound
// to.
const (
	// LeaseName is the §12.5 line 332 leader-election Lease name. It is
	// scoped to the gateway Deployment's namespace.
	LeaseName = "lenny-gateway-leader"
	// LeaseDuration is the §12.5 line 332 lease validity window.
	LeaseDuration = 15 * time.Second
	// RenewDeadline is how long the leader keeps trying to renew before
	// giving up. LeaseDuration + RenewDeadline is the 25s crash-failover
	// bound the spec names.
	RenewDeadline = 10 * time.Second
	// RetryPeriod is the inter-attempt backoff for acquire/renew.
	RetryPeriod = 2 * time.Second
)

// Elector reports whether this replica currently holds the §12.5
// gateway-leader lease and drives the leader/follower callbacks. The
// production implementation wraps the Kubernetes Lease API; tests and the
// single-process dev gateway use AlwaysLeader (or a fake) so the
// leader-gated sweeps can run without a cluster.
type Elector interface {
	// Run blocks until ctx is cancelled, invoking onStarted with a
	// leader-scoped context when this replica acquires leadership and
	// onStopped when it loses it. The leader-scoped context is cancelled
	// before onStopped fires so leader-gated work tears down.
	Run(ctx context.Context, onStarted func(context.Context), onStopped func())
	// IsLeader reports whether this replica currently holds the lease.
	IsLeader() bool
}

// LeaseTimings carries optional overrides for the §12.5 line 332 lease
// durations. A zero field keeps the built-in default. The window must
// satisfy client-go's LeaseDuration > RenewDeadline > RetryPeriod
// invariant; withDefaults only substitutes built-ins for omitted fields.
type LeaseTimings struct {
	LeaseDuration time.Duration
	RenewDeadline time.Duration
	RetryPeriod   time.Duration
}

func (t LeaseTimings) withDefaults() LeaseTimings {
	if t.LeaseDuration <= 0 {
		t.LeaseDuration = LeaseDuration
	}
	if t.RenewDeadline <= 0 {
		t.RenewDeadline = RenewDeadline
	}
	if t.RetryPeriod <= 0 {
		t.RetryPeriod = RetryPeriod
	}
	return t
}

// LeaseElector is the §12.5 Kubernetes Lease-based Elector. It wraps
// client-go's leaderelection with the lenny-gateway-leader lease name and
// the §12.5 failover durations.
type LeaseElector struct {
	config   leaderelection.LeaderElectionConfig
	isLeader atomic.Bool
}

// NewLeaseElector builds the §12.5 Lease-based Elector. namespace is the
// gateway Deployment's namespace that holds the lenny-gateway-leader
// Lease, and identity is the unique holder identity (the gateway pod
// name). timings carries optional duration overrides (zero fields take the
// built-in defaults).
func NewLeaseElector(
	namespace, identity string,
	coreClient corev1client.CoreV1Interface,
	coordinationClient coordinationv1client.CoordinationV1Interface,
	timings LeaseTimings,
) (*LeaseElector, error) {
	lock, err := resourcelock.New(
		resourcelock.LeasesResourceLock,
		namespace,
		LeaseName,
		coreClient,
		coordinationClient,
		resourcelock.ResourceLockConfig{Identity: identity},
	)
	if err != nil {
		return nil, err
	}
	t := timings.withDefaults()
	e := &LeaseElector{}
	e.config = leaderelection.LeaderElectionConfig{
		Lock:          lock,
		LeaseDuration: t.LeaseDuration,
		RenewDeadline: t.RenewDeadline,
		RetryPeriod:   t.RetryPeriod,
		// Release the lease on a clean shutdown so the surviving replica
		// becomes the GC writer without waiting out the full lease
		// duration (the "near-zero on clean shutdown" half of §12.5 line
		// 332).
		ReleaseOnCancel: true,
		Name:            LeaseName,
	}
	return e, nil
}

// Run blocks until ctx is cancelled, driving the §12.5 leader-election
// loop. client-go re-runs the election after a lost lease, so Run loops
// until ctx ends. onStarted receives a leader-scoped context that
// client-go cancels when leadership is lost.
func (e *LeaseElector) Run(ctx context.Context, onStarted func(context.Context), onStopped func()) {
	e.config.Callbacks = leaderelection.LeaderCallbacks{
		OnStartedLeading: func(leaderCtx context.Context) {
			e.isLeader.Store(true)
			onStarted(leaderCtx)
		},
		OnStoppedLeading: func() {
			e.isLeader.Store(false)
			onStopped()
		},
	}
	for ctx.Err() == nil {
		leaderelection.RunOrDie(ctx, e.config)
	}
}

// IsLeader reports whether this replica currently holds the lease.
func (e *LeaseElector) IsLeader() bool {
	return e.isLeader.Load()
}

// AlwaysLeader is the single-process / dev-mode Elector. The gateway is
// the only replica, so it is always the GC writer and every gateway
// singleton sweep runs. It is the fallback when the gateway is not running
// in-cluster (no Kubernetes Lease API available), preserving the
// pre-leader-election behavior where the sweeps always ran.
type AlwaysLeader struct{}

// Run invokes onStarted immediately with ctx and blocks until ctx is
// cancelled, then invokes onStopped. The context handed to onStarted is
// ctx itself, so leader-gated work runs for the whole process lifetime.
func (AlwaysLeader) Run(ctx context.Context, onStarted func(context.Context), onStopped func()) {
	onStarted(ctx)
	<-ctx.Done()
	onStopped()
}

// IsLeader always reports true.
func (AlwaysLeader) IsLeader() bool { return true }

// Job is one §12.5 gateway-singleton background sweep registered on a
// Gate. run blocks driving the sweep's own loop until its context is
// cancelled (leadership lost, or process shutdown).
type Job struct {
	Name string
	Run  func(context.Context)
}

// Gate runs a set of named §12.5 gateway-singleton sweeps under an
// Elector. Each registered job runs only while this replica holds the
// lease: on acquire every job is launched with a leader-scoped context; on
// loss the context is cancelled so each job's loop exits, and the jobs are
// re-launched if leadership is re-acquired.
type Gate struct {
	elector Elector
	jobs    []Job
}

// NewGate returns a Gate driven by elector.
func NewGate(elector Elector) *Gate { return &Gate{elector: elector} }

// Add registers a leader-gated sweep. It returns the Gate for chaining and
// is not safe for concurrent use; register every job before calling Run.
func (g *Gate) Add(name string, run func(context.Context)) *Gate {
	if run == nil {
		return g
	}
	g.jobs = append(g.jobs, Job{Name: name, Run: run})
	return g
}

// Len reports the number of registered jobs.
func (g *Gate) Len() int { return len(g.jobs) }

// Run drives the Elector until ctx is cancelled, launching every
// registered job when leadership is acquired and tearing them down when it
// is lost. Each job runs in its own goroutine bound to the leader-scoped
// context; client-go cancels that context on leadership loss so the jobs'
// own ctx.Done() select arms fire. It blocks until ctx ends.
func (g *Gate) Run(ctx context.Context) {
	g.elector.Run(ctx, func(leaderCtx context.Context) {
		for i := range g.jobs {
			job := g.jobs[i]
			go job.Run(leaderCtx)
		}
	}, func() {})
}
