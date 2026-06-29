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
	// EscalationEmissionRetryInterval paces the §25.4 escalation
	// emission-retry loop that re-attempts the escalation_created publish
	// for any record whose emitted flag is still false, until a destination
	// recovers. §25.4 fixes this period at 30s.
	EscalationEmissionRetryInterval = 30 * time.Second
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
	// ClockSkewSampleInterval paces the §25.4 Postgres-Redis clock-skew
	// sampler that reads both dependency clocks and publishes the absolute
	// skew on lenny_ops_clock_skew_seconds (the OpsClockSkewExceeded alert
	// backing). §25.4 does not fix the period; 30s is well below the rate
	// at which a >10s NTP breach would need to surface.
	ClockSkewSampleInterval = 30 * time.Second
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
	// EscalationEmissionRetry re-attempts the §25.4 escalation_created
	// publish for any escalation whose emitted flag is still false, so a
	// record created during a dual Redis-plus-gateway-buffer outage is
	// emitted once a destination recovers.
	EscalationEmissionRetry Reconciler
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
	// ClockSkewSample reads the Postgres and Redis dependency clocks and
	// publishes the absolute skew on lenny_ops_clock_skew_seconds, the
	// producer the §25.4 OpsClockSkewExceeded alert needs so the gauge is
	// not permanently 0.
	ClockSkewSample Reconciler
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
		{"escalation-emission-retry", EscalationEmissionRetryInterval, r.EscalationEmissionRetry},
		{"idempotency-cleanup", IdempotencyCleanupInterval, r.IdempotencyCleanup},
		{"lock-epoch-reconcile", LockEpochReconcileInterval, r.LockEpochReconcile},
		{"drift-snapshot-validate", DriftSnapshotValidateInterval, r.DriftSnapshotValidate},
		{"bundle-rules-reconcile", BundleRulesReconcileInterval, r.BundleRulesReconcile},
		{"operations-observe", OperationsObserveInterval, r.OperationsObserve},
		{"clock-skew-sample", ClockSkewSampleInterval, r.ClockSkewSample},
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
