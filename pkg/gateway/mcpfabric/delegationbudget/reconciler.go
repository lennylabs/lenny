// SPDX-License-Identifier: MIT

package delegationbudget

import (
	"context"
	"time"
)

// DefaultInterval is the checkpoint cadence used when Interval is unset.
// It matches the §11.2 line 44 quotaSyncIntervalSeconds default (30s);
// production threads the configured value.
const DefaultInterval = 30 * time.Second

// Reconciler drives the §11.2 periodic checkpoint and the §11.2 line 48
// recovery reconstruction for delegation tree budget counters. It probes
// Redis reachability on the checkpoint cadence: while Redis is reachable
// it snapshots every active tree's counters into Postgres, and on a
// down-to-up edge it first reconstructs every checkpointed tree's
// counters via the two-source MAX rule before the next checkpoint.
//
// The struct mirrors storagequota.RecoveryReconciler: any missing
// required seam makes Run a no-op so a partial wiring degrades to the
// prior behavior rather than panicking.
//
// spec: §11.2 lines 44, 48; §12.4 line 218.
type Reconciler struct {
	// Probe reports whether the Redis delegation-counter backend is
	// reachable this tick. Required.
	Probe func(ctx context.Context) bool
	// Counters reads and restores the Redis tree-wide counters. Required.
	Counters CounterStore
	// Trees enumerates the active trees to checkpoint. Required.
	Trees TreeLister
	// Store persists and reads the Postgres checkpoint rows. Required.
	Store Store
	// Live derives each tree's live-side reconstruction estimate.
	// Required for reconstruction; if nil, a recovery edge restores from
	// the Postgres checkpoint alone (live estimate treated as zero).
	Live LiveEnumerator
	// Marker moves an irrecoverable tree root to awaiting_client_action.
	// Required for the irrecoverable branch; if nil, an irrecoverable
	// tree is still counted but left untouched.
	Marker SessionMarker
	// Metrics records reconstruction outcomes. Optional.
	Metrics MetricEmitter
	// Interval is the checkpoint cadence and the base unit for the
	// 2 x Interval staleness threshold. Zero selects DefaultInterval.
	Interval time.Duration
	// NodeMemoryBytes is the §11.2 line 48 per-node footprint estimate.
	// Zero selects DefaultNodeMemoryFootprintBytes.
	NodeMemoryBytes int64
	// Now is the injectable clock for the checkpoint-age computation.
	// Nil selects time.Now.
	Now func() time.Time
	// Logf, when set, receives a one-line diagnostic per sweep.
	Logf func(format string, args ...any)

	// startReachable seeds the first observed reachability so a replica
	// that starts while Redis is reachable does not treat the first tick
	// as a recovery edge. Defaults to true.
	startReachable *bool
}

// Run drives the probe/checkpoint loop until ctx is cancelled. It blocks;
// callers start it in a goroutine. A Reconciler missing any required
// seam is a no-op.
func (r *Reconciler) Run(ctx context.Context) {
	if r.Probe == nil || r.Counters == nil || r.Trees == nil || r.Store == nil {
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
		r.Reconcile(ctx)
	}
	if now {
		r.Checkpoint(ctx)
	}
	return now
}

// Checkpoint snapshots every active tree's Redis counters into Postgres.
// A tree whose counters are all zero (no delegation admitted) is skipped
// so the table tracks only trees with real budget state. A per-tree
// snapshot error skips that tree rather than aborting the sweep.
//
// spec: §11.2 line 44 (durable checkpoint).
func (r *Reconciler) Checkpoint(ctx context.Context) {
	refs, err := r.Trees.ListActiveTrees(ctx)
	if err != nil {
		r.logf("delegationbudget: checkpoint: list active trees: %v", err)
		return
	}
	rows := make([]Checkpoint, 0, len(refs))
	for _, ref := range refs {
		c, err := r.Counters.Snapshot(ctx, ref.RootSessionID)
		if err != nil {
			r.logf("delegationbudget: checkpoint: snapshot tree %q: %v", ref.RootSessionID, err)
			continue
		}
		if c.TreeSize == 0 && c.Tokens == 0 && c.TreeMemory == 0 {
			continue
		}
		rows = append(rows, Checkpoint{
			TenantID:            ref.TenantID,
			RootSessionID:       ref.RootSessionID,
			TreeSize:            c.TreeSize,
			TokenBudgetConsumed: c.Tokens,
			TreeMemoryBytes:     c.TreeMemory,
		})
	}
	if len(rows) == 0 {
		return
	}
	if err := r.Store.Write(ctx, rows); err != nil {
		r.logf("delegationbudget: checkpoint: write %d row(s): %v", len(rows), err)
		return
	}
	r.logf("delegationbudget: checkpointed %d delegation tree(s)", len(rows))
}

// Reconcile runs the §11.2 line 48 two-source reconstruction for every
// checkpointed tree on a Redis-recovery edge. For each tree it loads the
// Postgres checkpoint, derives the live estimate from the SessionStore,
// and either restores the Redis counters to max(checkpoint, live) per
// axis or — when the checkpoint is older than 2 x Interval AND the live
// state cannot be enumerated — moves the root to awaiting_client_action
// with reason BUDGET_STATE_UNRECOVERABLE. Each tree emits one
// reconstruction outcome metric.
//
// spec: §11.2 line 48; §12.4 line 218.
func (r *Reconciler) Reconcile(ctx context.Context) {
	rows, err := r.Store.ListActive(ctx)
	if err != nil {
		r.logf("delegationbudget: reconcile: list checkpoints: %v", err)
		return
	}
	now := r.now()
	staleThreshold := 2 * r.interval()
	var success, irrecoverable int
	for _, row := range rows {
		var live LiveTree
		var liveErr error
		if r.Live != nil {
			live, liveErr = r.Live.LiveTree(ctx, row.TenantID, row.RootSessionID)
		}
		canEnumerate := r.Live != nil && liveErr == nil && live.RootExists

		// §11.2 line 48 irrecoverability: stale checkpoint AND no live
		// enumeration possible.
		checkpointAge := now.Sub(row.CheckpointAt)
		if !canEnumerate && checkpointAge > staleThreshold {
			if r.Marker != nil {
				if err := r.Marker.MarkBudgetUnrecoverable(ctx, row.TenantID, row.RootSessionID, BudgetStateUnrecoverableReason); err != nil {
					r.logf("delegationbudget: reconcile: mark tree %q unrecoverable: %v", row.RootSessionID, err)
				}
			}
			irrecoverable++
			r.inc(OutcomeIrrecoverable)
			continue
		}

		// Recoverable: restore counters to max(checkpoint, live) per axis.
		// When live cannot be enumerated but the checkpoint is fresh, the
		// live estimate is zero so the checkpoint values are restored
		// as-is.
		liveMemory := live.NodeCount * r.nodeMemoryBytes()
		restored := TreeCounters{
			TreeSize:   maxInt64(row.TreeSize, live.NodeCount),
			Tokens:     maxInt64(row.TokenBudgetConsumed, live.TokenAllocations),
			TreeMemory: maxInt64(row.TreeMemoryBytes, liveMemory),
		}
		if err := r.Counters.Restore(ctx, row.RootSessionID, restored); err != nil {
			r.logf("delegationbudget: reconcile: restore tree %q: %v", row.RootSessionID, err)
			continue
		}
		success++
		r.inc(OutcomeSuccess)
	}
	r.logf("delegationbudget: Redis recovered; reconstructed %d tree(s) (success=%d, irrecoverable=%d)",
		len(rows), success, irrecoverable)
}

func (r *Reconciler) interval() time.Duration {
	if r.Interval <= 0 {
		return DefaultInterval
	}
	return r.Interval
}

func (r *Reconciler) nodeMemoryBytes() int64 {
	if r.NodeMemoryBytes <= 0 {
		return DefaultNodeMemoryFootprintBytes
	}
	return r.NodeMemoryBytes
}

func (r *Reconciler) now() time.Time {
	if r.Now == nil {
		return time.Now()
	}
	return r.Now()
}

func (r *Reconciler) inc(outcome string) {
	if r.Metrics != nil {
		r.Metrics.IncDelegationBudgetReconstruction(outcome)
	}
}

func (r *Reconciler) logf(format string, args ...any) {
	if r.Logf == nil {
		return
	}
	r.Logf(format, args...)
}
