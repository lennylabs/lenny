// SPDX-License-Identifier: MIT

package checkpointer

import (
	"context"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/checkpoint"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
)

// FreshnessGauge is the surface the freshness reaper writes to. The
// gateway's gatewaymetrics.Metrics implements it via
// SetCheckpointStaleSessions.
// spec: §4.4 line 256 — "the `lenny_checkpoint_stale_sessions` gauge
// (per pool/level, reported by the gateway) counts the number of
// active sessions whose last checkpoint age exceeds the interval".
type FreshnessGauge interface {
	SetCheckpointStaleSessions(pool, level string, count int)
}

// PoolLevelResolver resolves a session's (pool, level) labels for the
// `lenny_checkpoint_stale_sessions` gauge. Callers wire this against
// their pool / runtime registry; when no resolver is supplied the
// reaper falls back to the session's recorded pool_ref and
// `unknown` for level.
type PoolLevelResolver func(s *sessionstore.Session) (pool string, level checkpoint.Level)

// TenantLister enumerates the tenants the freshness reaper sweeps. The
// gateway wires its tenants store here; tests use a thin in-memory
// stub. The method name matches the watchdog / retention-gc lister
// surface in `cmd/lenny-gateway/main.go` so the existing
// `tenantsLister` adapter satisfies it without a wrapper.
type TenantLister interface {
	ListTenants(ctx context.Context) ([]string, error)
}

// FreshnessReaper periodically scans every active session and bumps
// the `lenny_checkpoint_stale_sessions` gauge with the count of
// sessions whose `last_successful_checkpoint_at` is older than the
// configured periodic-checkpoint interval. It is the production
// caller of `pkg/checkpoint.FreshnessCheck`.
//
// The reaper is independent of the checkpoint-write path: it observes
// sessions rather than mutating them, and a stuck reaper does not
// stall periodic checkpoints. The Sweep method is exported so tests
// can drive it directly without a wall-clock dependency.
// spec: §4.4 line 256.
type FreshnessReaper struct {
	// Tenants enumerates the tenant ids to scan. The reaper sweeps
	// every tenant's active sessions because the freshness gauge is
	// platform-wide labelled (per pool/level, not per tenant).
	Tenants TenantLister
	// Sessions is the session store the reaper reads
	// `last_successful_checkpoint_at` from.
	Sessions sessionstore.Store
	// Gauge receives the per-(pool, level) stale count. Required.
	Gauge FreshnessGauge
	// Interval is the freshness threshold the reaper compares
	// `now - last_successful_checkpoint_at` against. Should match the
	// gateway's `periodicCheckpointIntervalSeconds` so the gauge
	// honors the spec's "active session ... has a successful
	// checkpoint within the last interval" definition.
	Interval time.Duration
	// SweepInterval is the cadence Run ticks on. Zero selects
	// DefaultFreshnessSweepInterval (60 s) per the §4.4 line 256
	// "the gateway enforces this by scheduling periodic checkpoints
	// for active sessions" and §16.5 CheckpointStale alert's "for >
	// 60 s" trigger.
	SweepInterval time.Duration
	// Now returns the reaper's wall clock. Nil selects time.Now.
	Now func() time.Time
	// ResolveLabels maps a session to the gauge's (pool, level)
	// labels. Nil falls back to the session's pool_ref and the
	// `unknown` level.
	ResolveLabels PoolLevelResolver
	// OnError, when set, receives a per-sweep failure (a tenant list
	// or a session list that returned an error). A sweep continues
	// past a failed tenant regardless.
	OnError func(tenantID string, err error)
}

// DefaultFreshnessSweepInterval is the default cadence the reaper
// runs at. 60 s mirrors the §16.5 CheckpointStale alert's `for: 60s`
// hold time so the gauge changes within one alert-evaluation cycle.
// spec: §4.4 line 256 / §16.5 CheckpointStale alert.
const DefaultFreshnessSweepInterval = 60 * time.Second

// Run drives the periodic freshness loop until ctx is cancelled. Each
// tick performs one Sweep across every tenant.
func (r *FreshnessReaper) Run(ctx context.Context) {
	tick := r.sweepInterval()
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	// Take an immediate sweep so the gauge has a value the moment the
	// gateway starts serving; otherwise the first observation lags by
	// one full SweepInterval.
	r.Sweep(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.Sweep(ctx)
		}
	}
}

// Sweep scans every active session across every tenant and updates
// the gauge once per observed (pool, level) bucket. Pools or levels
// that no longer carry stale sessions are zeroed so a previously
// stale label combination does not pin at its peak count.
func (r *FreshnessReaper) Sweep(ctx context.Context) {
	if r == nil || r.Tenants == nil || r.Sessions == nil || r.Gauge == nil {
		return
	}
	now := r.now()
	resolve := r.ResolveLabels
	if resolve == nil {
		resolve = DefaultResolveLabels
	}
	// Maintain the set of observed labels across every tenant so the
	// final Set call covers all observed (pool, level) pairs in this
	// sweep cycle.
	counts := map[labelKey]int{}
	tenantIDs, err := r.Tenants.ListTenants(ctx)
	if err != nil {
		r.reportError("", err)
		return
	}
	for _, tenantID := range tenantIDs {
		sessions, err := r.Sessions.List(ctx, tenantID, sessionstore.ListFilter{})
		if err != nil {
			r.reportError(tenantID, err)
			continue
		}
		for i := range sessions {
			s := &sessions[i]
			if !isActiveForFreshness(s.State) {
				continue
			}
			pool, level := resolve(s)
			key := labelKey{pool: pool, level: string(level)}
			if err := checkpoint.FreshnessCheck(now, s.LastSuccessfulCheckpointAt, r.Interval); err == nil {
				// Fresh session — still record the label so we can
				// zero a previously-stale gauge cell.
				if _, ok := counts[key]; !ok {
					counts[key] = 0
				}
				continue
			}
			// Stale: bump the per-(pool, level) count.
			counts[key]++
		}
	}
	for k, v := range counts {
		r.Gauge.SetCheckpointStaleSessions(k.pool, k.level, v)
	}
}

// DefaultResolveLabels falls back to the session's recorded pool_ref
// and the `unknown` level. The gateway typically supplies a real
// resolver wired against its runtime / pool registry.
func DefaultResolveLabels(s *sessionstore.Session) (string, checkpoint.Level) {
	pool := s.PoolRef
	if pool == "" {
		pool = "unknown"
	}
	return pool, checkpoint.Level("unknown")
}

// labelKey is the gauge label tuple (pool, level).
type labelKey struct {
	pool  string
	level string
}

func (r *FreshnessReaper) sweepInterval() time.Duration {
	if r.SweepInterval > 0 {
		return r.SweepInterval
	}
	return DefaultFreshnessSweepInterval
}

func (r *FreshnessReaper) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now().UTC()
}

func (r *FreshnessReaper) reportError(tenantID string, err error) {
	if r.OnError != nil {
		r.OnError(tenantID, err)
	}
}

// isActiveForFreshness reports whether the §4.4 freshness SLO applies
// to a session in the given state. The SLO covers active sessions
// only: terminal sessions cannot be checkpointed and their lack of
// `last_successful_checkpoint_at` should not skew the gauge.
// spec: §4.4 line 256 — "Every active session MUST have a successful
// checkpoint recorded within the last periodicCheckpointIntervalSeconds".
func isActiveForFreshness(state session.State) bool {
	switch state {
	case session.StateRunning, session.StateStarting,
		session.StateAwaitingClientAction, session.StateResumePending,
		session.StateSuspended:
		return true
	default:
		return false
	}
}
