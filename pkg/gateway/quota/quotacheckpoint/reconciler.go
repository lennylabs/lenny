// SPDX-License-Identifier: MIT

package quotacheckpoint

import (
	"context"
	"time"
)

// DefaultInterval is the checkpoint cadence used when Interval is unset.
// It matches the §11.2 line 44 quotaSyncIntervalSeconds default (30s);
// production threads the configured value.
const DefaultInterval = 30 * time.Second

// Reconciler drives the §11.2 periodic checkpoint and the §11.2 line 48
// recovery reconstruction for the token-usage counters. It probes Redis
// reachability on the checkpoint cadence: while Redis is reachable it
// checkpoints every active window into Postgres, and on a down-to-up edge
// it first reconstructs every checkpointed counter via the MAX rule before
// the next checkpoint. It mirrors delegationbudget.Reconciler.
//
// spec: §11.2 lines 44, 48.
type Reconciler struct {
	// Probe reports whether the Redis quota backend is reachable this tick.
	// Required.
	Probe func(ctx context.Context) bool
	// Service holds the checkpoint and reconcile seams. Required.
	Service *Service
	// Interval is the checkpoint cadence. Zero selects DefaultInterval.
	Interval time.Duration

	// startReachable seeds the first observed reachability so a replica
	// that starts while Redis is reachable does not treat the first tick as
	// a recovery edge. Defaults to true.
	startReachable *bool
}

// Run drives the probe/checkpoint loop until ctx is cancelled. It blocks;
// callers start it in a goroutine. A Reconciler missing any required seam
// is a no-op.
func (r *Reconciler) Run(ctx context.Context) {
	if r.Probe == nil || r.Service == nil {
		return
	}
	interval := r.interval()
	reachable := true
	if r.startReachable != nil {
		reachable = *r.startReachable
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			reachable = r.tick(ctx, reachable)
		}
	}
}

// tick runs one probe round, returning the reachability observed this
// round so the caller can track the edge. On a down-to-up transition it
// reconstructs before checkpointing; while reachable it checkpoints.
// Unexported so the test suite can step the loop deterministically.
func (r *Reconciler) tick(ctx context.Context, wasReachable bool) bool {
	now := r.Probe(ctx)
	if now && !wasReachable {
		_, _ = r.Service.Reconcile(ctx, ReconcileScope{AllTenants: true})
	}
	if now {
		r.Service.Checkpoint(ctx)
	}
	return now
}

func (r *Reconciler) interval() time.Duration {
	if r.Interval <= 0 {
		return DefaultInterval
	}
	return r.Interval
}
