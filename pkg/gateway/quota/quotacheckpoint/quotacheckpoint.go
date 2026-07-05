// SPDX-License-Identifier: MIT

// Package quotacheckpoint is the §11.2 durability layer for the Redis
// token-usage counters (pkg/gateway/quotastore). The fast path lives in
// Redis under the §12.4 key t:{tenant_id}:quota:tokens:{user_id}:{window};
// those counters are volatile, so this package adds the two obligations
// §11.2 attaches to them:
//
//   - the periodic Postgres checkpoint (§11.2 line 44): every
//     quotaSyncIntervalSeconds the gateway persists each active
//     (tenant, user) window total and the per-tenant rollup total to the
//     token_usage_checkpoint table, plus a final checkpoint on session
//     completion ("final reconciliation"), and
//   - the two-source reconstruction on Redis recovery (§11.2 line 48): the
//     gateway restores each checkpointed counter to
//     MAX(redis_current, postgres_checkpoint) so a stale checkpoint can
//     never lower a counter below its actual accumulated usage and
//     silently un-enforce a budget breach.
//
// The same reconstruction primitive backs the §24.6 operator-driven
// `POST /v1/admin/quota/reconcile` ("re-aggregate in-flight session usage
// from Postgres into Redis after a Redis recovery"): Service.Reconcile
// reads the durable checkpoint and applies the MAX rule for one tenant or
// every tenant. The Reconciler ties the periodic checkpoint and the
// recovery reconstruction together on the checkpoint cadence, mirroring
// delegationbudget.Reconciler.
//
// Only the fixed-interval reset periods (hourly, daily, monthly) are
// checkpointed. A rolling-window counter is a sliding sum across many
// sub-minute buckets with no single restorable window total; its buckets
// carry their own 2×window TTL and rebuild from live traffic as usage
// resumes, so the durable checkpoint deliberately skips it (WindowLabel
// returns an error for the rolling period and the checkpoint/reconcile
// paths drop the subject).
//
// spec: §11.2 lines 42-48; §12.4; §24.6 line 99.
package quotacheckpoint

import (
	"context"
	"errors"
	"time"

	"github.com/lennylabs/lenny/pkg/quota"
)

// Scope discriminates a checkpoint row: a per-user window or the
// per-tenant rollup window the §11.2 hierarchy keeps alongside it. The
// §11.2 line 48 MAX rule restores "each active session and tenant scope";
// the platform-wide global rollup is not checkpointed (it lives under a
// synthetic tenant slot with no tenants(id) row).
const (
	ScopeUser   = "user"
	ScopeTenant = "tenant"
)

// Reconcile outcome labels for lenny_quota_checkpoint_reconcile_total.
const (
	// OutcomeRestored — the MAX rule was applied to a still-current window.
	OutcomeRestored = "restored"
	// OutcomeSkipped — the checkpoint's window has already rolled over, so
	// restoring it would revive a stale bucket; it is dropped.
	OutcomeSkipped = "skipped"
)

// ErrTenantNotFound — a per-tenant reconcile named a tenant the registry
// does not hold. Service.Reconcile returns it so the admin adapter can map
// it to 404. spec: §24.6 line 99.
var ErrTenantNotFound = errors.New("quotacheckpoint: tenant not found")

// Row is one token_usage_checkpoint row: the recorded token total for a
// single §11.2 window at checkpoint time. CheckpointAt is populated on
// read from the server-side clock_timestamp().
//
// spec: §11.2 line 44.
type Row struct {
	TenantID     string
	Scope        string
	SubjectID    string
	Period       string
	WindowLabel  string
	TokenTotal   int64
	CheckpointAt time.Time
}

// Store persists and reads the token_usage_checkpoint rows. The Postgres
// implementation lives in the pgstore subpackage.
type Store interface {
	// Write upserts each row, replacing token_total and checkpoint_at on
	// conflict. Rows are grouped by tenant so each tenant's writes run
	// under its own RLS transaction.
	Write(ctx context.Context, rows []Row) error
	// ListActive returns every checkpoint row across all tenants for the
	// platform-wide recovery reconstruction.
	ListActive(ctx context.Context) ([]Row, error)
	// ListByTenant returns every checkpoint row for tenantID, for a
	// per-tenant reconcile.
	ListByTenant(ctx context.Context, tenantID string) ([]Row, error)
	// DeleteByUser removes the per-user checkpoint rows for (tenantID,
	// userID) and returns the count deleted — the §12.1 mandatory erasure
	// primitive. A subject with no rows is a no-op returning (0, nil).
	DeleteByUser(ctx context.Context, tenantID, userID string) (int, error)
	// DeleteByTenant removes every checkpoint row for tenantID — the §12.8
	// Phase-4 tenant-deletion erasure adapter.
	DeleteByTenant(ctx context.Context, tenantID string) (int, error)
}

// Subject is one (tenant, user) pair with at least one active session.
type Subject struct {
	TenantID string
	UserID   string
}

// SubjectLister enumerates the (tenant, user) pairs with active sessions
// whose windows the periodic checkpoint persists.
type SubjectLister interface {
	ListActiveSubjects(ctx context.Context) ([]Subject, error)
}

// PeriodResolver resolves a tenant's configured §11.2 reset period so the
// checkpoint reads and writes the same window the QuotaEvaluator enforces.
type PeriodResolver interface {
	ResolvePeriod(ctx context.Context, tenantID string) (quota.ResetPeriod, error)
}

// WindowReader reads the current §11.2 window totals. quotastore.Counter
// satisfies it (via the CounterAdapter for the label helper).
type WindowReader interface {
	// Usage returns the per-user window total of period containing at.
	Usage(ctx context.Context, tenantID, userID string, period quota.ResetPeriod, at time.Time) (int64, error)
	// TenantRollupUsage returns the per-tenant rollup window total.
	TenantRollupUsage(ctx context.Context, tenantID string, period quota.ResetPeriod, at time.Time) (int64, error)
}

// CounterRestorer applies the §11.2 line 48 MAX rule to a window,
// returning the resulting total. quotastore.Counter satisfies it.
type CounterRestorer interface {
	RestoreUserWindow(ctx context.Context, tenantID, userID string, period quota.ResetPeriod, at time.Time, value int64) (int64, error)
	RestoreTenantRollupWindow(ctx context.Context, tenantID string, period quota.ResetPeriod, at time.Time, value int64) (int64, error)
}

// TenantExister reports whether a tenant id is known to the registry, for
// the per-tenant reconcile's 404 gate.
type TenantExister interface {
	TenantExists(ctx context.Context, tenantID string) (bool, error)
}

// MetricEmitter records each reconcile event by outcome.
type MetricEmitter interface {
	IncQuotaCheckpointReconcile(outcome string)
}

// PodUsageReader is the §11.2 line 46 crash-recovery MAX-rule source: the
// pod-reported cumulative token total each bound direct-mode session's
// runtime adapter retains and re-reports on reconnection to a new gateway
// replica. §11.2 line 46 reconstructs a direct-mode quota counter after a
// gateway crash as MAX(postgres_checkpoint, pod-reported cumulative total);
// this seam supplies that third source so the recovery reconcile writes
// MAX(redis_current, postgres_checkpoint, in_memory_failopen,
// pod_reported_cumulative). A gateway-side implementation pulls each bound
// direct-mode session's cumulative total over the §4.7 ReportUsage RPC
// (cumulative=true) and aggregates the per-window totals; a proxy-mode,
// missing, errored, or timed-out session contributes nothing (it never
// fabricates a total, so the seam is fail-closed to the other MAX sources).
// The read shape mirrors quotafailopen.Accumulator's source-(2) seam so the
// fold sites are symmetric.
//
// A nil reader on Service.PodUsage preserves the exact prior behaviour
// (guarded like FailOpen==nil), so a gateway without the pod registry, the
// adapter-client accessor, or the lease store degrades to the
// MAX(redis, postgres, failopen) rule.
//
// spec: §11.2 line 46 (crash-recovery MAX rule; pod-reported cumulative
// total); §4.7 (ReportUsage cumulative read).
type PodUsageReader interface {
	// UserWindow returns the pod-reported cumulative token total for the
	// per-user window of period containing at, or 0 when no bound direct-mode
	// session contributes a total for that window.
	UserWindow(ctx context.Context, tenantID, userID string, period quota.ResetPeriod, at time.Time) int64
	// TenantRollup returns the pod-reported cumulative token total for the
	// per-tenant rollup window of period containing at, or 0 when no bound
	// direct-mode session contributes a total for that window.
	TenantRollup(ctx context.Context, tenantID string, period quota.ResetPeriod, at time.Time) int64
	// Snapshot returns every (tenant, user, period)-window the reader can
	// attribute a pod-reported cumulative total to as of now, so the recovery
	// reconcile can also restore a window that opened during the outage with
	// no checkpoint row. An empty UserID addresses the per-tenant rollup
	// window.
	Snapshot(ctx context.Context, now time.Time) []PodUsageSample
}

// PodUsageSample is one (tenant, user, period)-window pod-reported
// cumulative total the recovery reconcile can restore. An empty UserID
// addresses the per-tenant rollup window. spec: §11.2 line 46.
type PodUsageSample struct {
	TenantID string
	UserID   string
	Period   quota.ResetPeriod
	Tokens   int64
}
