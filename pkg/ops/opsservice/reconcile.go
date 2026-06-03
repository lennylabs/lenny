// SPDX-License-Identifier: MIT

package opsservice

import (
	"context"
	"time"
)

// Default intervals for the §25.4 leader-only reconciliation loops.
// §25.4 names the reconciliation goroutines but does not fix their
// periods, so these are conservative defaults; the cron evaluator,
// webhook worker, and self-monitor intervals come from the §25.4
// values that the spec does define.
const (
	// EscalationFlushInterval paces the §25.4 escalation-flush loop that
	// drains the in-memory escalation buffer to Postgres once a durable
	// store is available again.
	EscalationFlushInterval = 30 * time.Second
	// IdempotencyCleanupInterval paces the §25.4 idempotency-cleanup loop
	// that removes idempotency keys past their TTL.
	IdempotencyCleanupInterval = 5 * time.Minute
	// LockEpochReconcileInterval paces the §25.4 lock outage-epoch
	// reconciliation loop that resolves remediation locks orphaned by a
	// storage outage.
	LockEpochReconcileInterval = time.Minute
	// DriftSnapshotValidateInterval paces the §25.4 drift snapshot
	// validation loop that checks the desired-state snapshot is current.
	DriftSnapshotValidateInterval = 10 * time.Minute
	// BundleRulesReconcileInterval paces the §25.4 bundleRules reconciler
	// that keeps the rendered alerting rules in sync.
	BundleRulesReconcileInterval = 5 * time.Minute
	// OperationsObserveInterval paces the §25.2 operations-observe loop
	// that scans the Operations Inventory to maintain the
	// lenny_ops_operations_stalled gauge (the OperationStalled alert
	// backing) and to emit operation_progressed on step transitions and
	// percent-threshold crossings.
	OperationsObserveInterval = 30 * time.Second
)

// Reconciler is a tick function for one §25.4 leader-only
// reconciliation loop. A nil Reconciler disables its loop, which lets
// a deployment without (for example) a Postgres-backed escalation
// store skip the escalation-flush loop.
type Reconciler func(ctx context.Context) error

// Reconcilers groups the §25.4 leader-only reconciliation goroutines.
// Each field is the tick function for the named loop; a nil field
// means the loop is not run.
type Reconcilers struct {
	// EscalationFlush drains the in-memory escalation buffer to Postgres.
	EscalationFlush Reconciler
	// IdempotencyCleanup removes expired idempotency keys.
	IdempotencyCleanup Reconciler
	// LockEpochReconcile resolves remediation locks orphaned by an outage.
	LockEpochReconcile Reconciler
	// DriftSnapshotValidate checks the desired-state snapshot is current.
	DriftSnapshotValidate Reconciler
	// BundleRulesReconcile keeps the rendered alerting rules in sync.
	BundleRulesReconcile Reconciler
	// OperationsObserve scans the §25.2 Operations Inventory to maintain
	// the lenny_ops_operations_stalled gauge and emit operation_progressed
	// on step transitions and percent-threshold crossings.
	OperationsObserve Reconciler
}

// loops projects the configured reconcilers into leader-only Loops. A
// nil reconciler contributes no loop, so the returned slice contains
// only the reconciliation goroutines this deployment actually runs.
func (r Reconcilers) loops() []Loop {
	type spec struct {
		name     string
		interval time.Duration
		fn       Reconciler
	}
	specs := []spec{
		{"escalation-flush", EscalationFlushInterval, r.EscalationFlush},
		{"idempotency-cleanup", IdempotencyCleanupInterval, r.IdempotencyCleanup},
		{"lock-epoch-reconcile", LockEpochReconcileInterval, r.LockEpochReconcile},
		{"drift-snapshot-validate", DriftSnapshotValidateInterval, r.DriftSnapshotValidate},
		{"bundle-rules-reconcile", BundleRulesReconcileInterval, r.BundleRulesReconcile},
		{"operations-observe", OperationsObserveInterval, r.OperationsObserve},
	}
	var loops []Loop
	for _, s := range specs {
		if s.fn == nil {
			continue
		}
		loops = append(loops, Loop{
			Name:       s.name,
			Interval:   s.interval,
			Tick:       s.fn,
			LeaderOnly: true,
		})
	}
	return loops
}
