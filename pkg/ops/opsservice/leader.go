// SPDX-License-Identifier: MIT

package opsservice

import (
	"context"
	"sync/atomic"
	"time"

	coordinationv1client "k8s.io/client-go/kubernetes/typed/coordination/v1"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
)

// §25.4 leader-election lease parameters. lenny-ops uses the
// Kubernetes Lease API for leader election; the lease is named
// lenny-ops-leader in the release namespace. The worst-case
// crash-failover window is leaseDuration + renewDeadline = 25s.
const (
	// LeaseName is the §25.4 leader-election Lease name. The Helm value
	// ops.leaderElection.leaseName resolves to this.
	LeaseName = "lenny-ops-leader"
	// LeaseDuration is the §25.4 ops.leaderElection.leaseDurationSeconds.
	LeaseDuration = 15 * time.Second
	// RenewDeadline is the §25.4 ops.leaderElection.renewDeadlineSeconds.
	RenewDeadline = 10 * time.Second
	// RetryPeriod is the §25.4 ops.leaderElection.retryPeriodSeconds.
	RetryPeriod = 2 * time.Second
)

// Elector reports whether this replica currently holds the §25.4
// leader-election lease and drives the leader/follower callbacks. The
// production implementation wraps the Kubernetes Lease API; tests
// supply a fake so the leader-gated loops can be exercised without a
// cluster.
type Elector interface {
	// Run blocks until ctx is cancelled, invoking onStarted when this
	// replica acquires leadership and onStopped when it loses it.
	Run(ctx context.Context, onStarted func(context.Context), onStopped func())
	// IsLeader reports whether this replica currently holds the lease.
	IsLeader() bool
}

// LeaseElector is the §25.4 Kubernetes Lease-based Elector. It wraps
// client-go's leaderelection with the §25.4 lease name and durations,
// mirroring the controller-runtime leader election the WarmPoolController
// uses (§4.6.1) but for a plain HTTP service rather than a manager.
type LeaseElector struct {
	config   leaderelection.LeaderElectionConfig
	isLeader atomic.Bool
}

// NewLeaseElector builds the §25.4 Lease-based Elector. namespace is
// the release namespace that holds the lenny-ops-leader Lease, and
// identity is the unique holder identity (the pod name). The
// coordination and core clients come from the in-cluster client-go
// clientset.
func NewLeaseElector(
	namespace, identity string,
	coreClient corev1client.CoreV1Interface,
	coordinationClient coordinationv1client.CoordinationV1Interface,
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
	e := &LeaseElector{}
	e.config = leaderelection.LeaderElectionConfig{
		Lock:          lock,
		LeaseDuration: LeaseDuration,
		RenewDeadline: RenewDeadline,
		RetryPeriod:   RetryPeriod,
		// Release the lease on a clean shutdown so the surviving replica
		// becomes leader without waiting out the full lease duration.
		ReleaseOnCancel: true,
		Name:            LeaseName,
	}
	return e, nil
}

// Run blocks until ctx is cancelled, driving the §25.4 leader-election
// loop. onStarted runs the leader-only background loops; onStopped
// tears them down when leadership is lost. client-go re-runs the
// election after a lost lease, so Run loops until ctx ends.
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

// IsLeader reports whether this replica currently holds the lease. The
// HTTP surface uses it so an operator querying any replica can see
// which one is the leader.
func (e *LeaseElector) IsLeader() bool {
	return e.isLeader.Load()
}
