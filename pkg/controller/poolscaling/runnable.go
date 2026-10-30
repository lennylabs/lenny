// SPDX-License-Identifier: MIT

package poolscaling

import (
	"context"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

// defaultSyncInterval is the §4.6.2 pool-config reconcile cadence. It
// is well inside the 60s window after which the PoolConfigDrift alert
// fires, so a healthy controller never trips that alert.
const defaultSyncInterval = 30 * time.Second

// Runnable adapts the PoolScalingController to a controller-runtime
// manager.Runnable. The pool definitions are the admin API's Postgres
// state, which Kubernetes cannot watch, so the controller re-syncs on
// a timer rather than reconciling on CRD events.
type Runnable struct {
	// Reconciler is the controller whose Sync runs each tick.
	Reconciler *Reconciler
	// Interval is the sync cadence. A non-positive value selects
	// defaultSyncInterval.
	Interval time.Duration
}

var (
	_ manager.Runnable               = (*Runnable)(nil)
	_ manager.LeaderElectionRunnable = (*Runnable)(nil)
)

// Start runs the sync loop until ctx is cancelled. A Sync error is
// logged and the loop continues so the next tick retries. Start syncs
// once immediately so a freshly-elected leader does not wait a full
// interval before reconciling the pool CRDs.
func (rn *Runnable) Start(ctx context.Context) error {
	logger := log.FromContext(ctx).WithName("poolscaling")
	interval := rn.Interval
	if interval <= 0 {
		interval = defaultSyncInterval
	}

	if err := rn.Reconciler.Sync(ctx); err != nil {
		logger.Error(err, "initial pool-config sync failed")
	}

	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			if err := rn.Reconciler.Sync(ctx); err != nil {
				logger.Error(err, "pool-config sync failed")
			}
		}
	}
}

// NeedLeaderElection reports that only the elected leader runs the
// sync loop, so replicas never race on the derived-CRD writes.
func (rn *Runnable) NeedLeaderElection() bool { return true }
