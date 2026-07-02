// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"time"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/billing/usagestore"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/llmproxy"
	"github.com/lennylabs/lenny/pkg/gateway/policy/policy"
	"github.com/lennylabs/lenny/pkg/gateway/quota/quotabudget"
	"github.com/lennylabs/lenny/pkg/gateway/quota/quotafailopen"
	"github.com/lennylabs/lenny/pkg/gateway/quota/quotastore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionbudget"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionidle"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionusage"
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

	// sessionUsage is the §8.8 per-session token accumulator. Each
	// proxy-extracted (input, output) pair is folded into the originating
	// session's running totals so the §8.8 TaskResult.usage / treeUsage
	// rollups can read a settled task's token consumption. A nil store
	// disables per-session metering (the rollups surface zero tokens).
	// spec: §8.8 lines 897-917.
	sessionUsage sessionusage.Store

	// quota is the §11.2 Redis-backed hierarchical token counter. A nil
	// counter disables quota recording (the metering write still runs).
	quota *quotastore.Counter
	// budgetTracker is the §12.4 line 268 in-memory per-replica budget
	// tracker. When set (the in_memory_reconciled enforcement mode) the
	// recorder folds the proxy-extracted tenant token count into the
	// budget slice instead of the Redis counter. A nil tracker selects the
	// default Redis-counter path.
	budgetTracker *quotabudget.Tracker
	// failopenAccum is the §12.4 / §11.2 line 48 MAX-rule source (2): a
	// cumulative in-memory per-(tenant, user) token counter the recorder
	// folds every proxy-extracted token delta into (in the Redis-counter
	// mode) so the Redis-recovery reconcile can restore usage that a Redis
	// write dropped while the shared counter was unreachable. Nil disables
	// the accumulation (no fail-open recovery source). F-12.4.20.
	failopenAccum *quotafailopen.Accumulator
	// limits resolves the tenant's §11.2 reset period and rolling-window
	// length so the recorder writes the same window QuotaEvaluator reads.
	limits policy.TenantLimitLookup
	// budget is the §11.2 mid-session token-budget enforcer. It tracks
	// each session's cumulative proxy-recorded tokens against the
	// session's §8.2 token budget and terminates an over-budget session.
	// A nil enforcer disables mid-session enforcement (no budget cap).
	budget *sessionbudget.Enforcer
	// activity stamps §6.2 line 277 proxy-mode activity: each proxied LLM
	// response resets the session's idle clock so a session whose agent is
	// mid-generation is not reaped by the §11.3 idle watchdog. A nil
	// stamper disables the reset. F-11.3.7.
	activity *sessionidle.Stamper
	// now returns the current time; nil selects time.Now. Overridden in
	// tests so the quota window key is deterministic.
	now func() time.Time
}

func newProxyUsageRecorder(usage usagestore.Store, sessions sessionstore.Store, sessUsage sessionusage.Store, quotaCounter *quotastore.Counter, limits policy.TenantLimitLookup, budget *sessionbudget.Enforcer) *proxyUsageRecorder {
	if usage == nil {
		return nil
	}
	return &proxyUsageRecorder{
		usage:        usage,
		sessions:     sessions,
		sessionUsage: sessUsage,
		quota:        quotaCounter,
		limits:       limits,
		budget:       budget,
	}
}

// setBudgetTracker selects the §12.4 line 268 in_memory_reconciled quota
// path: recordQuota folds the proxy-extracted tenant token count into the
// per-replica budget slice instead of the Redis counter. Nil-safe so the
// caller can pass a nil tracker (Redis mode) or invoke it on a nil
// recorder (no usagestore wired).
func (r *proxyUsageRecorder) setBudgetTracker(tracker *quotabudget.Tracker) {
	if r == nil {
		return
	}
	r.budgetTracker = tracker
}

// setFailOpenAccumulator wires the §12.4 source (2) in-memory fail-open
// token accumulator. Nil-safe on both the recorder and the accumulator.
// F-12.4.20.
func (r *proxyUsageRecorder) setFailOpenAccumulator(a *quotafailopen.Accumulator) {
	if r == nil {
		return
	}
	r.failopenAccum = a
}

// setActivityStamper wires the §6.2 idle-timer activity stamper so each
// proxied LLM response resets the session's idle clock. Nil-safe on both
// the recorder and the stamper. F-11.3.7.
func (r *proxyUsageRecorder) setActivityStamper(s *sessionidle.Stamper) {
	if r == nil {
		return
	}
	r.activity = s
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
	// spec: §6.2 lines 277 — proxy-mode activity. Each proxied response is
	// direct evidence of active agent work; reset the idle clock so a
	// long-running LLM call is not mistaken for idle. The stamper coalesces
	// to ≤1/s per session. F-11.3.7.
	if r.activity != nil && lease.SessionID != "" {
		r.activity.Stamp(lease.TenantID, lease.SessionID)
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
			// spec: §14 line 106 — denormalize the session's labels onto the
			// proxy-extracted usage record so a label-scoped usage report
			// captures the session's token consumption, not only its
			// session.created count. F-14.1.13.
			rec.Labels = sess.Labels
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

	// spec: §8.8 lines 897-917 — fold the proxy-extracted counts into the
	// originating session's per-session totals so the §8.8 TaskResult
	// usage / treeUsage rollups can read them at settle time. Best-effort
	// for the same reason as the metering write.
	if r.sessionUsage != nil && lease.SessionID != "" {
		ctx, cancel := context.WithTimeout(context.Background(), proxyUsageRecordTimeout)
		_ = r.sessionUsage.Add(ctx, lease.TenantID, lease.SessionID, int64(u.InputTokens), int64(u.OutputTokens))
		cancel()
	}

	tokens := int64(u.InputTokens) + int64(u.OutputTokens)
	r.recordQuota(lease.TenantID, userID, tokens)

	// spec: §11.2 line 44 — enforce the per-session token budget against
	// the cumulative proxy-recorded usage and terminate immediately when
	// it is exhausted. Runs after the metering / quota writes so the
	// authoritative record lands before the session is torn down.
	if r.budget != nil {
		// The request context threading (r.Context() -> reqCtx, the derived
		// proxyExtensionWaitTimeout waitCtx) and the granted-delta budget
		// input are wired by the proxy record-boundary in a later build step
		// (proposal 0023 S3/S4). Until then the enforcer's nil seam denies
		// and terminates immediately on exhaustion, so background contexts
		// preserve the current §11.2 line 44 behavior.
		r.budget.Record(context.Background(), context.Background(), lease.TenantID, lease.SessionID, tokenBudget, tokens)
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
	if r.limits == nil || tokens <= 0 {
		return
	}
	if r.quota == nil && r.budgetTracker == nil {
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
	// spec: §12.4 line 268 — in the in_memory_reconciled mode the
	// per-tenant token count decrements the per-replica budget slice; the
	// Redis hierarchical counter is not the enforcement source.
	if r.budgetTracker != nil {
		r.budgetTracker.Add(ctx, tenantID, period, now, tokens)
		return
	}
	// spec: §12.4 source (2); §11.2 line 48 — fold the proxy-extracted
	// tokens into the cumulative in-memory accumulator before the Redis
	// write below. When the write is dropped during a fail-open window the
	// accumulator still carries the usage, so the Redis-recovery reconcile
	// restores it via the MAX rule. The accumulator self-skips the rolling
	// period (no single restorable window). F-12.4.20.
	r.failopenAccum.Record(tenantID, userID, period, now, tokens)
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
