// SPDX-License-Identifier: MIT

package tenantdeletion

import (
	"context"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

// defaultSyncInterval is the §12.8 tenant-deletion reconcile cadence.
// The §12.8 deletion SLA is measured in hours (72h for T3, 4h for T4),
// so a 30s pass is well inside the window — a multi-phase deletion
// advances one phase per pass and completes in a handful of passes.
const defaultSyncInterval = 30 * time.Second

// Runnable adapts the tenant-deletion Reconciler to a
// controller-runtime manager.Runnable. The tenant registry is the
// admin API's Postgres state, which Kubernetes cannot watch, so the
// controller re-syncs on a timer rather than reconciling on CRD events
// — the same pattern the §4.6.2 PoolScalingController uses.
type Runnable struct {
	// Reconciler is the §12.8 controller whose ReconcileAll runs each
	// tick.
	Reconciler *Reconciler
	// Interval is the sync cadence. A non-positive value selects
	// defaultSyncInterval.
	Interval time.Duration
}

var (
	_ manager.Runnable               = (*Runnable)(nil)
	_ manager.LeaderElectionRunnable = (*Runnable)(nil)
)

// Start runs the §12.8 reconcile loop until ctx is cancelled. A
// ReconcileAll error is logged and the loop continues so the next tick
// retries — a per-tenant phase failure is already recorded on that
// tenant's job and is retried from the recorded phase. Start
// reconciles once immediately so a freshly-elected leader does not
// wait a full interval before advancing in-progress deletions.
func (rn *Runnable) Start(ctx context.Context) error {
	logger := log.FromContext(ctx).WithName("tenantdeletion")
	interval := rn.Interval
	if interval <= 0 {
		interval = defaultSyncInterval
	}

	if err := rn.Reconciler.ReconcileAll(ctx); err != nil {
		logger.Error(err, "initial tenant-deletion reconcile failed")
	}

	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			if err := rn.Reconciler.ReconcileAll(ctx); err != nil {
				logger.Error(err, "tenant-deletion reconcile failed")
			}
		}
	}
}

// NeedLeaderElection reports that only the elected leader runs the
// §12.8 reconcile loop, so replicas never race on the destructive
// phase actions.
func (rn *Runnable) NeedLeaderElection() bool { return true }
