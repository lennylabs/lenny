// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/quota/quotacheckpoint"
	"github.com/lennylabs/lenny/pkg/gateway/quota/quotastore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/quota"
)

// directUsageRecoveryReader is the gateway-side §11.2 line 46 crash-recovery
// MAX-rule source: it supplies the pod-reported cumulative token total each
// bound direct-mode session's runtime adapter retains and re-reports on
// reconnection. On a Redis-recovery edge (or an operator-driven reconcile) a
// reconnected gateway replica folds this source into the counter
// reconstruction so a direct-mode session whose Redis usage was lost is
// restored to MAX(redis_current, postgres_checkpoint, in_memory_failopen,
// pod_reported_cumulative) rather than silently under-counting.
//
// The reader enumerates the replica's bound direct-delivery sessions
// (podRegistry.Snapshot), re-reads each binding through podRegistry.Get at
// pull time so a session torn down since the snapshot is skipped, resolves
// each session's credential lease and (tenant, user) subject and reset-period
// window, pulls each session's cumulative total over the §4.7 ReportUsage RPC
// (cumulative=true), and aggregates the per-window totals. A proxy-mode lease
// (ErrUsageReportProxyMode), a missing lease, a missing session row, a
// transport error, or a timeout contributes nothing to that window: the
// reader is fail-closed to the other MAX sources and never fabricates a
// total.
//
// The §4.7 cumulative read advances the adapter's last-read watermark to the
// returned cumulative total, so the aggregation MUST run at most once per
// reconcile pass: a second pull in the same pass would return zero (the
// watermark already advanced) and the fold would under-count. quotacheckpoint
// calls UserWindow, TenantRollup, and Snapshot several times within one
// reconcile, all with the same `now`, so the reader caches the aggregation
// keyed on that `now` and reuses it across the fold sites.
//
// spec: §11.2 line 46 (crash-recovery MAX rule; pod-reported cumulative
// total), §4.7 (ReportUsage cumulative read). F-15.3.7.
type directUsageRecoveryReader struct {
	registry    podBindingRegistry
	leases      directUsageLeaseLookup
	subjects    directUsageSubjectResolver
	periods     quotacheckpoint.PeriodResolver
	pullerFor   func(*podsession.BindResult) directUsagePuller
	pullTimeout time.Duration
	log         *slog.Logger

	mu       sync.Mutex
	cachedAt time.Time
	cache    map[podUsageWindowKey]int64
}

// podBindingRegistry enumerates the live pod bindings the replica holds and
// re-resolves a single binding by session id. *podsession.Registry satisfies
// it via Snapshot and Get; it is defined at the consumer so a test can
// substitute a fixed binding set. The recovery reader enumerates candidate
// sessions with Snapshot, then re-reads each binding with Get at pull time so
// a session torn down between the snapshot and the pull is skipped and the
// caller-owned, teardown-closed adapter is never dialed — the same
// registry-keyed teardown guard the steady-state directUsageLoop uses
// (direct_usage.go pullSession).
type podBindingRegistry interface {
	Snapshot() []*podsession.BindResult
	Get(sessionID string) (*podsession.BindResult, bool)
}

// directUsageSubjectResolver resolves a bound session's (tenant, user)
// subject so a pulled cumulative total lands in the same per-user window the
// quota counter enforces. The SessionStore satisfies it; a session with no
// row, or an empty user id, contributes only to the per-tenant rollup. It is
// defined at the consumer so the reader does not depend on the store type.
type directUsageSubjectResolver interface {
	// ResolveUser returns the user id for (tenantID, sessionID), or "" when
	// the session has no row or no user (the per-tenant rollup still counts).
	ResolveUser(ctx context.Context, tenantID, sessionID string) string
}

// podUsageWindowKey identifies one aggregated (scope, tenant, subject,
// period) window. An empty subject with the tenant scope addresses the
// per-tenant rollup.
type podUsageWindowKey struct {
	scope     string
	tenantID  string
	subjectID string
	period    quota.ResetPeriod
}

// newDirectUsageRecoveryReader builds the reader from the gateway seams. It
// returns nil when any seam the pull needs is absent (no pod registry, no
// lease store, no subject resolver, no period resolver, or no puller), so
// buildQuotaCheckpoint can assign the result unconditionally and a minimal
// gateway degrades to the MAX(redis, postgres, failopen) rule with a nil
// PodUsage seam.
func newDirectUsageRecoveryReader(
	registry podBindingRegistry,
	leases directUsageLeaseLookup,
	subjects directUsageSubjectResolver,
	periods quotacheckpoint.PeriodResolver,
	pullTimeout time.Duration,
	log *slog.Logger,
) *directUsageRecoveryReader {
	if registry == nil || leases == nil || subjects == nil || periods == nil {
		return nil
	}
	if log == nil {
		log = slog.Default()
	}
	if pullTimeout <= 0 {
		pullTimeout = 5 * time.Second
	}
	return &directUsageRecoveryReader{
		registry:    registry,
		leases:      leases,
		subjects:    subjects,
		periods:     periods,
		pullerFor:   adapterPuller,
		pullTimeout: pullTimeout,
		log:         log,
	}
}

// UserWindow implements quotacheckpoint.PodUsageReader.
func (r *directUsageRecoveryReader) UserWindow(ctx context.Context, tenantID, userID string, period quota.ResetPeriod, at time.Time) int64 {
	agg := r.aggregate(ctx, at)
	return agg[podUsageWindowKey{scope: quotacheckpoint.ScopeUser, tenantID: tenantID, subjectID: userID, period: period}]
}

// TenantRollup implements quotacheckpoint.PodUsageReader.
func (r *directUsageRecoveryReader) TenantRollup(ctx context.Context, tenantID string, period quota.ResetPeriod, at time.Time) int64 {
	agg := r.aggregate(ctx, at)
	return agg[podUsageWindowKey{scope: quotacheckpoint.ScopeTenant, tenantID: tenantID, subjectID: "", period: period}]
}

// Snapshot implements quotacheckpoint.PodUsageReader.
func (r *directUsageRecoveryReader) Snapshot(ctx context.Context, now time.Time) []quotacheckpoint.PodUsageSample {
	agg := r.aggregate(ctx, now)
	out := make([]quotacheckpoint.PodUsageSample, 0, len(agg))
	for k, tokens := range agg {
		if tokens <= 0 {
			continue
		}
		out = append(out, quotacheckpoint.PodUsageSample{
			TenantID: k.tenantID,
			UserID:   k.subjectID,
			Period:   k.period,
			Tokens:   tokens,
		})
	}
	return out
}

// aggregate returns the per-window pod-reported cumulative totals for the
// reconcile pass at `at`, pulling every bound direct-mode session once and
// caching the result. quotacheckpoint runs one reconcile with a single fixed
// `at`, so a cache keyed on `at` guarantees each session's watermark-advancing
// cumulative read runs at most once per pass across the UserWindow,
// TenantRollup, and Snapshot fold sites. A later pass with a different `at`
// re-pulls.
func (r *directUsageRecoveryReader) aggregate(ctx context.Context, at time.Time) map[podUsageWindowKey]int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cache != nil && r.cachedAt.Equal(at) {
		return r.cache
	}
	agg := r.pull(ctx, at)
	r.cache = agg
	r.cachedAt = at
	return agg
}

// pull enumerates the bound direct-mode sessions and pulls each session's
// cumulative total, folding it into both the per-user window and the
// per-tenant rollup for the window containing `at`. A rolling-period tenant
// (no single restorable window) is skipped, matching the checkpoint path.
// Snapshot supplies only the candidate session ids; pullSession re-reads each
// binding through registry.Get at pull time so a session torn down since the
// snapshot is skipped.
func (r *directUsageRecoveryReader) pull(ctx context.Context, at time.Time) map[podUsageWindowKey]int64 {
	bindings := r.registry.Snapshot()
	agg := make(map[podUsageWindowKey]int64)
	if len(bindings) == 0 {
		return agg
	}
	sessionIDs := make([]string, 0, len(bindings))
	for _, b := range bindings {
		sessionIDs = append(sessionIDs, b.SessionID)
	}
	leasesByID := make(map[string]credential.Lease, len(sessionIDs))
	for _, lease := range r.leases.LeasesBySession(sessionIDs) {
		leasesByID[lease.SessionID] = lease
	}
	for _, sessionID := range sessionIDs {
		lease, ok := leasesByID[sessionID]
		if !ok || lease.DeliveryMode != credential.DeliveryDirect {
			// No lease resolved, or a proxy-mode session: proxy-extracted counts
			// are already authoritative and recorded on the §4.9 path, so pulling
			// here would double-count. Skip. spec: §4.9 line 1468.
			continue
		}
		r.pullSession(ctx, sessionID, lease, at, agg)
	}
	return agg
}

// pullSession pulls one direct-mode session's cumulative total and folds it
// into the aggregation. A resolve error, a proxy-mode misroute, a missing
// session, a transport error, or a timeout contributes nothing (fail-closed
// to the other MAX sources). It re-reads the binding through registry.Get at
// pull time — not the transient Snapshot BindResult — so a session torn down
// between the snapshot and the pull is skipped and the caller-owned,
// teardown-closed adapter is never dialed, mirroring the steady-state
// directUsageLoop.pullSession teardown guard (direct_usage.go).
func (r *directUsageRecoveryReader) pullSession(ctx context.Context, sessionID string, lease credential.Lease, at time.Time, agg map[podUsageWindowKey]int64) {
	period, err := r.periods.ResolvePeriod(ctx, lease.TenantID)
	if err != nil {
		return
	}
	if _, err := quotastore.WindowLabel(period, at); err != nil {
		// Rolling period: no single restorable window, so a pod-reported
		// cumulative total has nowhere to land. The checkpoint path skips it too.
		return
	}
	b, ok := r.registry.Get(sessionID)
	if !ok {
		// The session unbound between the snapshot and this pull; re-reading
		// through the registry (rather than the snapshot's BindResult) is what
		// stops the reader from dialing a torn-down session's closed adapter.
		return
	}
	puller := r.pullerFor(b)
	if puller == nil {
		// The binding carries no adapter; nothing to pull.
		return
	}
	pullCtx, cancel := context.WithTimeout(ctx, r.pullTimeout)
	defer cancel()
	// spec: §4.7 — the crash-recovery read pulls the cumulative total
	// (cumulative=true), distinct from the steady-state incremental poll.
	report, err := puller.ReportUsageForLease(pullCtx, sessionID, lease.DeliveryMode, true)
	if err != nil {
		r.log.Debug("direct-mode crash-recovery cumulative pull failed",
			slog.String("session_id", sessionID),
			slog.String("tenant_id", lease.TenantID),
			slog.Any("error", err))
		return
	}
	tokens := report.InputTokens + report.OutputTokens
	if tokens <= 0 {
		return
	}
	userID := r.subjects.ResolveUser(ctx, lease.TenantID, sessionID)
	if userID != "" {
		agg[podUsageWindowKey{scope: quotacheckpoint.ScopeUser, tenantID: lease.TenantID, subjectID: userID, period: period}] += tokens
	}
	agg[podUsageWindowKey{scope: quotacheckpoint.ScopeTenant, tenantID: lease.TenantID, subjectID: "", period: period}] += tokens
}

var _ quotacheckpoint.PodUsageReader = (*directUsageRecoveryReader)(nil)

// sessionStoreSubjectResolver resolves a session's user id from the
// SessionStore for the crash-recovery per-user window attribution. It reuses
// the same Get(tenantID, sessionID) lookup the proxyUsageRecorder uses to
// resolve a proxied session's user, so the recovery fold lands the
// pod-reported total in the same per-user window steady-state usage records.
type sessionStoreSubjectResolver struct {
	sessions sessionstore.Store
}

// ResolveUser implements directUsageSubjectResolver. A missing row or a
// lookup error yields "" (the pod-reported total still counts against the
// per-tenant rollup); the reader never fabricates a subject.
func (r sessionStoreSubjectResolver) ResolveUser(ctx context.Context, tenantID, sessionID string) string {
	if r.sessions == nil {
		return ""
	}
	sess, err := r.sessions.Get(ctx, tenantID, sessionID)
	if err != nil {
		return ""
	}
	return sess.UserID
}
