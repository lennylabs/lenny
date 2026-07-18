// SPDX-License-Identifier: MIT

package storagequota

import (
	"context"
	"time"
)

// DefaultRecoveryProbeInterval is the cadence at which RecoveryReconciler
// re-probes Redis reachability. It matches dualstore.DefaultProbeInterval
// so a recovery edge is observed within one interval of Redis returning.
const DefaultRecoveryProbeInterval = 2 * time.Second

// ReachabilityProbe reports whether the Redis counter is reachable.
// cmd/lenny-gateway wires redisconn.PingWithTimeout; a test drives the
// state machine deterministically.
type ReachabilityProbe func(ctx context.Context) bool

// TenantLister enumerates the tenant ids whose counters are rehydrated on
// recovery. tenantstore.Store satisfies the ListTenants shape the gateway
// already uses for the startup rehydration.
type TenantLister func(ctx context.Context) ([]string, error)

// RecoveryReconciler implements the §12.4 line 210 "On Redis recovery,
// the rehydrated counter is written back to Redis and normal fast-path
// enforcement resumes" obligation for the storage-quota counter.
//
// It probes Redis reachability on a fixed interval and, on a
// down-to-up edge (Redis was unreachable on the prior tick and is
// reachable now), reconstructs every tenant's storage_bytes_used counter
// from the authoritative Postgres byte total (live artifact_size_bytes plus
// outstanding checkpoint reservations, through the reservation-aware SizeOf
// seam) and Sets it back into Redis via Rehydrate. Without this, a Redis
// restart mid-run
// (which empties the counter) would leave the fast path enforcing against
// a stale-zero value until the counter naturally re-grew, silently
// un-enforcing the quota — the same failure the startup rehydration
// prevents for a process restart.
//
// The reconciler complements Failover: Failover serves correct
// Postgres-derived values during the outage window, and the reconciler
// restores the Redis fast path the moment Redis returns.
//
// spec: §12.4 line 210; §12.5 line 309 ("rehydrate counters on Redis
// restart").
type RecoveryReconciler struct {
	// Probe reports whether Redis is reachable this tick. Required; a nil
	// probe makes Run a no-op (nothing can detect a recovery edge).
	Probe ReachabilityProbe
	// Primary is the Redis-backed counter whose keys are rewritten on
	// recovery. Required.
	Primary Counter
	// Tenants enumerates the tenants to rehydrate. Required.
	Tenants TenantLister
	// SizeOf reads each tenant's authoritative durable byte total from
	// Postgres. In production it is the reservation-aware seam
	// ReservationAwareLiveBytes composes (live artifact_store bytes plus
	// outstanding checkpoint reservations), so the recovery-edge rebuild
	// re-adds every unreleased reservation the guarded relative Adjust will
	// later release. Required.
	SizeOf LiveBytesSource
	// Interval is the probe cadence. Zero selects
	// DefaultRecoveryProbeInterval.
	Interval time.Duration
	// Now is the injectable clock. Nil selects time.Now.
	Now func() time.Time
	// Logf, when set, receives a one-line diagnostic on each recovery
	// rehydration.
	Logf func(format string, args ...any)
	// OnRehydrate, when set, is invoked after each recovery rehydration
	// with the tenant count rewritten — a place to advance a metric.
	OnRehydrate func(tenantCount int)

	// startReachable seeds the first observed reachability so a replica
	// that starts while Redis is reachable does not treat the first tick
	// as a recovery edge. Defaults to true.
	startReachable *bool
}

// Run drives the probe loop until ctx is cancelled. It blocks; callers
// start it in a goroutine. A reconciler missing any required field is a
// no-op so a misconfigured wiring degrades to the prior behavior rather
// than panicking.
func (r *RecoveryReconciler) Run(ctx context.Context) {
	if r.Probe == nil || r.Primary == nil || r.Tenants == nil || r.SizeOf == nil {
		return
	}
	interval := r.Interval
	if interval <= 0 {
		interval = DefaultRecoveryProbeInterval
	}
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
// rehydrates. Exposed (unexported) so the test suite can step the loop
// deterministically without a real ticker.
func (r *RecoveryReconciler) tick(ctx context.Context, wasReachable bool) bool {
	now := r.Probe(ctx)
	if now && !wasReachable {
		r.rehydrate(ctx)
	}
	return now
}

func (r *RecoveryReconciler) rehydrate(ctx context.Context) {
	ids, err := r.Tenants(ctx)
	if err != nil {
		r.logf("storagequota: recovery rehydration: list tenants: %v", err)
		return
	}
	if err := Rehydrate(ctx, r.Primary, ids, r.SizeOf); err != nil {
		r.logf("storagequota: recovery rehydration: %v", err)
		// A per-tenant fault does not abort the sweep (Rehydrate joins
		// and continues); still record the attempt so the counter is not
		// silently left stale.
	}
	if r.OnRehydrate != nil {
		r.OnRehydrate(len(ids))
	}
	r.logf("storagequota: Redis recovered; rehydrated storage-quota counters from artifact_store for %d tenant(s)", len(ids))
}

func (r *RecoveryReconciler) logf(format string, args ...any) {
	if r.Logf == nil {
		return
	}
	r.Logf(format, args...)
}
