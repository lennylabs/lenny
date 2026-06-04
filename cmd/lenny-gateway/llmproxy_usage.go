// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"time"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy"
	"github.com/lennylabs/lenny/pkg/gateway/policy"
	"github.com/lennylabs/lenny/pkg/gateway/quotastore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionbudget"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/usagestore"
	"github.com/lennylabs/lenny/pkg/quota"
)

// proxyUsageRecorder bridges the §4.9 LLM proxy's authoritative
// proxy-extracted token counts into the gateway's §15.1 usage store
// (metering rollup) and the §11.2 quota counter (admission
// enforcement). spec: spec/04_system-components.md line 1468 —
// proxy-extracted counts are the authoritative record for quota
// accounting; pod-reported counts are ignored for sessions in proxy
// mode.
//
// The recorder is wired only when the gateway has a usagestore. A nil
// recorder, or a record call for a non-proxy-mode lease, leaves both
// stores untouched. The quota write fires only when a quota counter and
// tenant-limit lookup are also wired (i.e. --redis-url is set).
type proxyUsageRecorder struct {
	usage    usagestore.Store
	sessions sessionstore.Store

	// quota is the §11.2 Redis-backed hierarchical token counter. A nil
	// counter disables quota recording (the metering write still runs).
	quota *quotastore.Counter
	// limits resolves the tenant's §11.2 reset period and rolling-window
	// length so the recorder writes the same window QuotaEvaluator reads.
	limits policy.TenantLimitLookup
	// budget is the §11.2 mid-session token-budget enforcer. It tracks
	// each session's cumulative proxy-recorded tokens against the
	// session's §8.2 token budget and terminates an over-budget session.
	// A nil enforcer disables mid-session enforcement (no budget cap).
	budget *sessionbudget.Enforcer
	// now returns the current time; nil selects time.Now. Overridden in
	// tests so the quota window key is deterministic.
	now func() time.Time
}

func newProxyUsageRecorder(usage usagestore.Store, sessions sessionstore.Store, quotaCounter *quotastore.Counter, limits policy.TenantLimitLookup, budget *sessionbudget.Enforcer) *proxyUsageRecorder {
	if usage == nil {
		return nil
	}
	return &proxyUsageRecorder{
		usage:    usage,
		sessions: sessions,
		quota:    quotaCounter,
		limits:   limits,
		budget:   budget,
	}
}

// RecordUsage implements llmproxy.UsageRecorder. It records the
// proxy-extracted token usage against the lease's owning tenant. A
// direct-mode lease never reaches the proxy hot path; this recorder
// defends against future direct-mode regressions by ignoring the call
// (the spec rule at line 1468 fires only in proxy mode).
//
// The runtime label and the per-user quota attribution are populated
// from a best-effort session lookup. A lookup miss records the tokens
// against the tenant alone, leaving Runtime empty and the per-user
// quota window keyed on the empty user id (the tenant and global rollups
// are unaffected).
func (r *proxyUsageRecorder) RecordUsage(lease credential.Lease, u llmproxy.Usage) {
	if r == nil {
		return
	}
	if lease.DeliveryMode != credential.DeliveryProxy {
		// spec: §4.9 line 1468 — only proxy-mode counts are authoritative.
		return
	}
	if lease.TenantID == "" {
		// Without a tenant attribution the record is meaningless to the
		// §15.1 byTenant rollup and the §11.2 per-tenant window; drop
		// rather than emit an "unknown" tenant series.
		return
	}
	rec := usagestore.Record{
		TenantID: lease.TenantID,
		Tokens: usagestore.Tokens{
			Input:  int64(u.InputTokens),
			Output: int64(u.OutputTokens),
		},
	}
	userID := ""
	var tokenBudget int64
	if r.sessions != nil && lease.SessionID != "" {
		ctx, cancel := context.WithTimeout(context.Background(), proxyUsageLookupTimeout)
		if sess, err := r.sessions.Get(ctx, lease.TenantID, lease.SessionID); err == nil {
			rec.Runtime = sess.RuntimeRef
			// spec: §11.2 — the per-user quota window keys on the
			// authenticated subject, the same id QuotaEvaluator reads from
			// the request metadata at admission.
			userID = sess.UserID
			// spec: §8.2 lines 38-48 — the session's effective LLM token
			// cap is the delegation lease's per-subtree maxTokenBudget.
			// Zero (no lease, or no budget set) leaves the session
			// unbounded so the §11.2 mid-session enforcer is a no-op for it.
			if sess.DelegationLease != nil {
				tokenBudget = sess.DelegationLease.MaxTokenBudget
			}
		}
		cancel()
	}
	// Record is best-effort: the metering store is already a degradable
	// dependency in this gateway (memory fallback when Postgres is off),
	// so a transient failure must not fail the proxied LLM call.
	ctx, cancel := context.WithTimeout(context.Background(), proxyUsageRecordTimeout)
	_ = r.usage.Record(ctx, rec)
	cancel()

	tokens := int64(u.InputTokens) + int64(u.OutputTokens)
	r.recordQuota(lease.TenantID, userID, tokens)

	// spec: §11.2 line 44 — enforce the per-session token budget against
	// the cumulative proxy-recorded usage and terminate immediately when
	// it is exhausted. Runs after the metering / quota writes so the
	// authoritative record lands before the session is torn down.
	if r.budget != nil {
		r.budget.Record(lease.TenantID, lease.SessionID, tokenBudget, tokens)
	}
}

// recordQuota advances the §11.2 hierarchical token counter (per-user,
// per-tenant, and global windows) by tokens for the resolved reset
// period, so QuotaEvaluator reads a non-zero window at the next
// admission. It is a no-op when no quota counter or limit lookup is
// wired, when tokens is non-positive, or when the tenant's limits cannot
// be resolved. The write is best-effort: a transient Redis fault must
// not fail the proxied call. spec: §11.2 ("writes them to Redis
// immediately").
func (r *proxyUsageRecorder) recordQuota(tenantID, userID string, tokens int64) {
	if r.quota == nil || r.limits == nil || tokens <= 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), proxyUsageRecordTimeout)
	defer cancel()
	lim, err := r.limits.LookupLimits(ctx, tenantID)
	if err != nil {
		// A tenant whose limits cannot be resolved (unknown, soft-deleted,
		// or a transient store fault) has its usage dropped rather than
		// recorded against a fabricated period; admission already fails
		// closed for such a tenant.
		return
	}
	now := time.Now().UTC()
	if r.now != nil {
		now = r.now()
	}
	period := lim.Period
	if period == "" {
		period = quota.ResetHourly
	}
	if period == quota.ResetRolling {
		window := lim.RollingWindow
		if window <= 0 {
			window = policy.DefaultRollingWindow
		}
		_, _ = r.quota.SlidingAddHierarchical(ctx, tenantID, userID, window, quotastore.DefaultBucketResolution, now, tokens)
		return
	}
	_, _ = r.quota.AddHierarchical(ctx, tenantID, userID, period, now, tokens)
}

// proxyUsageLookupTimeout bounds the per-request session lookup that
// the recorder uses to attach a runtime label and the per-user quota
// attribution. The proxy call returns to the pod before this fires; the
// recorder runs synchronously on the response path so a stuck store
// cannot pin the pod's request thread.
const proxyUsageLookupTimeout = 250 * time.Millisecond

// proxyUsageRecordTimeout bounds the usagestore Record and the §11.2
// quota counter writes.
const proxyUsageRecordTimeout = 250 * time.Millisecond
