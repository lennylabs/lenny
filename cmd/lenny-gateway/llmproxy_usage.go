// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"sync"
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
	// anomaly observes each direct-mode ReportUsage delta for the §11.2
	// direct-mode integrity control (the S5 detector that emits
	// lenny_gateway_token_usage_anomaly_total). It fires only on the
	// direct-mode path (RecordDirectUsage); the proxy hot path has
	// authoritative §4.9 counts and no under-reporting anomaly to detect. A
	// nil observer disables anomaly detection. spec: §11.2 (direct-mode
	// under-reporting integrity control). F-11.2.20.
	anomaly directUsageObserver
	// now returns the current time; nil selects time.Now. Overridden in
	// tests so the quota window key is deterministic.
	now func() time.Time
	// proxyExtensionWaitTimeout bounds the in-path §8.6 extension wait the
	// recorder derives (context.WithTimeout(reqCtx, proxyExtensionWaitTimeout))
	// before calling the enforcer's Record, so a slow elicitation-mode
	// extension falls through to a recover-on-next-request BUDGET_EXHAUSTED
	// rather than pinning the request. It is the operator-tunable gateway
	// deadline (§8.6 does not fix it); a non-positive value selects
	// defaultProxyExtensionWaitTimeout. spec: §8.6 line 629; proposal 0023.
	proxyExtensionWaitTimeout time.Duration

	// grantMu guards grantedDelta.
	grantMu sync.Mutex
	// grantedDelta accumulates, per session, the §8.6 granted token deltas the
	// recorder observes as the SessionReclaimer (RaiseBudget). Enforcer.Record
	// overwrites c.budget from the budget argument on every call, so a raise
	// applied through RaiseBudget alone is clobbered the next time RecordUsage
	// settles a request unless the budget the recorder passes to Record already
	// carries the accumulated delta. RecordUsage therefore computes the effective
	// budget as base MaxTokenBudget plus grantedDelta[sessionID] so the raise
	// survives the next Record (proposal 0023 S3/S4 raise-survival invariant). A
	// session is cleared from the map when the recorder observes its termination
	// or when the enforcer forgets it. spec: §8.6 line 629; proposal 0023.
	grantedDelta map[string]int64
}

// defaultProxyExtensionWaitTimeout is the recorder's fallback in-path §8.6
// extension wait when the operator has not tuned one. It matches the
// llmproxy handler default so the two paths agree. spec: §8.6 line 629.
const defaultProxyExtensionWaitTimeout = 5 * time.Second

// directUsageObserver is the consumer-side seam the recorder feeds each
// direct-mode ReportUsage delta into. The S5 anomaly detector implements
// it and evaluates the per-session zero-token-delta window and the
// implausibly-small ratio, emitting lenny_gateway_token_usage_anomaly_total
// on a §11.2 direct-mode under-reporting anomaly. The interface is defined
// at the consumer so the recorder does not depend on the detector's
// package; the detector lands in a later build step and is registered
// through setAnomalyObserver. spec: §11.2 (direct-mode integrity control).
type directUsageObserver interface {
	// Observe records one direct-mode ReportUsage delta for a session. The
	// tokens are the incremental (input, output) counts the gateway pulled
	// since the previous poll; a zero-token delta is the primary
	// under-reporting signal.
	Observe(tenantID, sessionID string, input, output int64)
}

func newProxyUsageRecorder(usage usagestore.Store, sessions sessionstore.Store, sessUsage sessionusage.Store, quotaCounter *quotastore.Counter, limits policy.TenantLimitLookup, budget *sessionbudget.Enforcer) *proxyUsageRecorder {
	if usage == nil {
		return nil
	}
	return &proxyUsageRecorder{
		usage:                     usage,
		sessions:                  sessions,
		sessionUsage:              sessUsage,
		quota:                     quotaCounter,
		limits:                    limits,
		budget:                    budget,
		proxyExtensionWaitTimeout: defaultProxyExtensionWaitTimeout,
		grantedDelta:              make(map[string]int64),
	}
}

// RaiseBudget records a §8.6 granted token delta for sessionID and raises
// the enforcer's per-session budget. It is the recorder's SessionReclaimer
// implementation, wired as the leasecontrol episode fan-out's reclaimer and
// the in-path Granted applier (proposal 0023 S3/S4). The two responsibilities
// are inseparable: the enforcer raises c.budget so the pre-flight gate admits
// the session's next request, but Enforcer.Record overwrites c.budget from the
// budget argument on the next settlement, so the recorder must also accumulate
// the delta and add it to the base MaxTokenBudget it passes to Record. Applying
// both here keeps the two in lockstep for the in-path and deferred grant paths
// alike. A non-positive delta records nothing and still clears the deny flag
// through the enforcer. spec: §8.6 line 629, line 719; proposal 0023.
func (r *proxyUsageRecorder) RaiseBudget(sessionID string, delta int64) {
	if r == nil || sessionID == "" {
		return
	}
	if delta > 0 {
		r.grantMu.Lock()
		r.grantedDelta[sessionID] += delta
		r.grantMu.Unlock()
	}
	if r.budget != nil {
		r.budget.RaiseBudget(sessionID, delta)
	}
}

// TerminateSession terminates sessionID for budget exhaustion (fail closed)
// and drops its accumulated grant delta, the recorder's SessionReclaimer path
// for a session whose deferred extension outcome is terminal. Clearing the
// delta keeps the map from retaining a raised budget for a session that is
// being torn down. spec: §8.6 line 629, line 719; proposal 0023.
func (r *proxyUsageRecorder) TerminateSession(sessionID string) {
	if r == nil || sessionID == "" {
		return
	}
	r.grantMu.Lock()
	delete(r.grantedDelta, sessionID)
	r.grantMu.Unlock()
	if r.budget != nil {
		r.budget.TerminateSession(sessionID)
	}
}

// grantedDeltaFor returns the accumulated §8.6 granted token delta for
// sessionID, which RecordUsage adds to the base MaxTokenBudget so the
// enforcer's next Record refreshes c.budget with the raised value rather than
// the stale pre-extension budget. A session with no recorded grant returns 0.
func (r *proxyUsageRecorder) grantedDeltaFor(sessionID string) int64 {
	r.grantMu.Lock()
	defer r.grantMu.Unlock()
	return r.grantedDelta[sessionID]
}

// forget drops sessionID's accumulated grant delta. The gateway calls it from
// the same terminal-side-effects pipeline that forgets the enforcer counter,
// so the recorder's per-session delta map does not grow without bound as
// sessions settle. Nil-safe on the recorder. spec: §8.6 line 629; proposal
// 0023.
func (r *proxyUsageRecorder) forget(sessionID string) {
	if r == nil || sessionID == "" {
		return
	}
	r.grantMu.Lock()
	delete(r.grantedDelta, sessionID)
	r.grantMu.Unlock()
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

// setAnomalyObserver wires the §11.2 direct-mode integrity detector (the S5
// observer that emits lenny_gateway_token_usage_anomaly_total). The recorder
// feeds it every direct-mode ReportUsage delta from RecordDirectUsage. A nil
// observer disables anomaly detection; the proxy hot path never feeds it (the
// §4.9 counts are authoritative and carry no under-reporting anomaly).
// Nil-safe on the recorder. spec: §11.2 (direct-mode integrity control).
// F-11.2.20.
func (r *proxyUsageRecorder) setAnomalyObserver(o directUsageObserver) {
	if r == nil {
		return
	}
	r.anomaly = o
}

// setProxyExtensionWaitTimeout applies the operator-tunable in-path §8.6
// extension deadline (the --proxy-extension-wait-timeout flag). A
// non-positive value leaves the recorder's defaultProxyExtensionWaitTimeout
// in place, so a zeroed flag never collapses the wait window to nothing.
// Nil-safe on the recorder. spec: §8.6 line 629; proposal 0023.
func (r *proxyUsageRecorder) setProxyExtensionWaitTimeout(d time.Duration) {
	if r == nil || d <= 0 {
		return
	}
	r.proxyExtensionWaitTimeout = d
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
func (r *proxyUsageRecorder) RecordUsage(ctx context.Context, lease credential.Lease, u llmproxy.Usage) (exhausted bool, outcome llmproxy.Outcome) {
	if r == nil {
		return false, llmproxy.OutcomeGranted
	}
	if lease.DeliveryMode != credential.DeliveryProxy {
		// spec: §4.9 line 1468 — only proxy-mode counts are authoritative.
		return false, llmproxy.OutcomeGranted
	}
	if lease.TenantID == "" {
		// Without a tenant attribution the record is meaningless to the
		// §15.1 byTenant rollup and the §11.2 per-tenant window; drop
		// rather than emit an "unknown" tenant series.
		return false, llmproxy.OutcomeGranted
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
		lookupCtx, cancel := context.WithTimeout(ctx, proxyUsageLookupTimeout)
		if sess, err := r.sessions.Get(lookupCtx, lease.TenantID, lease.SessionID); err == nil {
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
	// Fan the extracted counts into the metering store, the §8.8 per-session
	// accumulator, and the §11.2 quota counter. This block is shared with the
	// direct-mode path (RecordDirectUsage), which reaches the same accounting
	// sinks and excludes only the mid-session enforcer below.
	tokens := r.recordAccounting(rec, lease.TenantID, lease.SessionID, userID, u.InputTokens, u.OutputTokens)

	// spec: §11.2 line 44 / §8.6 line 629 — enforce the per-session token
	// budget against the cumulative proxy-recorded usage. Record detects
	// exhaustion, dispatches the single §8.6 extension through the enforcer's
	// injected seam, applies the raise / deny / terminate side-effects, and
	// returns the resolved tri-state Outcome. The recorder maps that onto the
	// proxy-local Outcome so the proxy drives its write-path branch without a
	// second, independent extension dispatch (proposal 0023 S4). The enforcer
	// runs under the request context so its in-path extension wait honors a
	// client disconnect and the proxyExtensionWaitTimeout deadline (the
	// waitCtx the recorder derives). The metering write above already landed
	// the authoritative record before the session can be torn down.
	outcome = llmproxy.OutcomeGranted
	if r.budget != nil {
		// spec: §8.6 line 629 / proposal 0023 S3/S4 raise-survival — the base
		// MaxTokenBudget read from the session store never reflects a prior §8.6
		// grant (ApplyGrant writes only the leasecontrol view and the Redis tree
		// counter, never the session-store lease). Add the accumulated granted
		// delta so Enforcer.Record refreshes c.budget with the raised value
		// rather than the stale base, which would otherwise re-exhaust and
		// re-terminate a session that just had its budget raised.
		effectiveBudget := tokenBudget
		if lease.SessionID != "" {
			effectiveBudget += r.grantedDeltaFor(lease.SessionID)
		}
		waitCtx, cancel := context.WithTimeout(ctx, r.proxyExtensionWaitTimeout)
		var budgetOutcome sessionbudget.Outcome
		exhausted, budgetOutcome = r.budget.Record(ctx, waitCtx, lease.TenantID, lease.SessionID, effectiveBudget, tokens)
		cancel()
		outcome = proxyOutcome(budgetOutcome)
	}
	return exhausted, outcome
}

// recordAccounting fans one (input, output) token pair into the metering
// store, the §8.8 per-session accumulator, and the §11.2 quota counter, and
// returns the total tokens recorded. It is the shared accounting fan-out the
// proxy path (RecordUsage) and the direct-mode path (RecordDirectUsage) both
// run; only the mid-session enforcer and the idle-clock reset differ between
// the two, and each caller handles those. The metering and per-session writes
// are best-effort and MUST survive a client disconnect: they are the
// authoritative §15.1 billing and §8.8 rollup records, so they run on a fresh
// background-derived context with their own timeout rather than the request
// context. A cancelled request must not drop the usage the pod already
// consumed. spec: §15.1 (metering), §8.8 lines 897-917 (per-session rollup),
// §11.2 (quota counter).
func (r *proxyUsageRecorder) recordAccounting(rec usagestore.Record, tenantID, sessionID, userID string, input, output int) int64 {
	recordCtx, cancel := context.WithTimeout(context.Background(), proxyUsageRecordTimeout)
	_ = r.usage.Record(recordCtx, rec)
	cancel()

	// spec: §8.8 lines 897-917 — fold the counts into the originating
	// session's per-session totals so the §8.8 TaskResult usage / treeUsage
	// rollups can read them at settle time. Best-effort for the same reason
	// as the metering write.
	if r.sessionUsage != nil && sessionID != "" {
		addCtx, cancel := context.WithTimeout(context.Background(), proxyUsageRecordTimeout)
		_ = r.sessionUsage.Add(addCtx, tenantID, sessionID, int64(input), int64(output))
		cancel()
	}

	tokens := int64(input) + int64(output)
	r.recordQuota(tenantID, userID, tokens)
	return tokens
}

// RecordDirectUsage records one §11.2 direct-mode ReportUsage delta the
// gateway pulled from a pod's adapter. The gateway is the sole observer of a
// direct-mode session's provider token counts (the pod egresses to the
// provider directly and never reaches the §4.9 LLM proxy), so this path is
// the direct-mode counterpart of the proxy hot path RecordUsage.
//
// It reaches the same accounting sinks — the metering store, the §8.8
// per-session accumulator, the §11.2 quota counter, and the fail-open
// accumulator — plus the S5 anomaly detector, and it deliberately EXCLUDES the
// mid-session sessionbudget.Enforcer.Record breach path that RecordUsage runs.
// §11.2 line 44 and §8.3 line 631 forbid both an in-path termination and an
// in-path extension for a direct-mode session: a direct-mode session "is not
// terminated in-path on budget breach ... and it is not eligible for the
// in-path extension." The over-run bound is settlement-time reconciliation
// against the parent's delegation budget via budget_return.lua per §11.2 line
// 44 and §8.3 line 435; §11.2 line 42 requires only monitoring the
// under-reporting anomaly, which the S5 detector does.
//
// The §6.2 idle clock is stamped only when the pulled delta is non-zero.
// A gateway ReportUsage pull is a timer tick rather than direct evidence of
// agent work, so a zero-delta poll must not reset the idle clock, or a hung or
// wedged direct-mode pod that emits no tokens would never idle-terminate and
// its pod, lease, and session would leak. A non-zero delta is direct evidence
// the agent issued LLM work during the interval, so it resets the clock. This
// is the §6.2 line 253 direct-mode idle reconciliation under the gateway pull.
//
// A proxy-mode lease is dropped: proxy-extracted counts are already
// authoritative and recorded by RecordUsage on the §4.9 path, so recording a
// proxy lease here would double-count. A lease without a tenant attribution is
// dropped for the same reason RecordUsage drops it.
//
// spec: §11.2 lines 42-44 (direct-mode integrity control, no in-path
// termination or extension), §8.3 lines 435, 631, §6.2 line 253 (idle-clock
// reset on non-zero delta), §4.9 line 1468. F-15.3.7, F-11.2.20.
func (r *proxyUsageRecorder) RecordDirectUsage(ctx context.Context, lease credential.Lease, u llmproxy.Usage) {
	if r == nil {
		return
	}
	if lease.DeliveryMode != credential.DeliveryDirect {
		// spec: §4.9 line 1468 — a proxy (or unset) lease's counts are the
		// §4.9 proxy's authoritative record; recording them here would
		// double-count. Fail closed by dropping rather than accepting.
		return
	}
	if lease.TenantID == "" {
		// Without a tenant attribution the record is meaningless to the
		// §15.1 byTenant rollup and the §11.2 per-tenant window; drop rather
		// than emit an "unknown" tenant series (matching RecordUsage).
		return
	}

	// spec: §6.2 line 253 — reset the direct-mode idle clock only on a
	// non-zero pulled delta. A zero-delta poll is a bare timer tick, not
	// evidence of agent work, so a hung pod that emits no tokens still
	// idle-terminates. A non-zero delta is direct evidence of LLM work.
	if r.activity != nil && lease.SessionID != "" && (u.InputTokens > 0 || u.OutputTokens > 0) {
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
	if r.sessions != nil && lease.SessionID != "" {
		lookupCtx, cancel := context.WithTimeout(ctx, proxyUsageLookupTimeout)
		if sess, err := r.sessions.Get(lookupCtx, lease.TenantID, lease.SessionID); err == nil {
			rec.Runtime = sess.RuntimeRef
			// spec: §14 line 106 — denormalize the session's labels onto the
			// usage record so a label-scoped report captures direct-mode token
			// consumption, matching RecordUsage. F-14.1.13.
			rec.Labels = sess.Labels
			// spec: §11.2 — the per-user quota window keys on the
			// authenticated subject.
			userID = sess.UserID
		}
		cancel()
	}

	// Reach the same accounting sinks the proxy path writes, minus the
	// mid-session enforcer. The idle stamp above is the one sink that behaves
	// differently between the two paths (proxy stamps unconditionally; direct
	// mode gates on a non-zero delta).
	r.recordAccounting(rec, lease.TenantID, lease.SessionID, userID, u.InputTokens, u.OutputTokens)

	// spec: §11.2 line 42 — feed the S5 direct-mode integrity detector every
	// pulled delta so it can raise lenny_gateway_token_usage_anomaly_total on
	// a sustained zero-token or implausibly-small under-reporting pattern. The
	// enforcer breach path (RecordUsage line 347) is intentionally NOT run
	// here: §11.2 line 44 and §8.3 line 631 forbid an in-path termination and
	// an in-path extension for a direct-mode session. F-11.2.20.
	if r.anomaly != nil && lease.SessionID != "" {
		r.anomaly.Observe(lease.TenantID, lease.SessionID, int64(u.InputTokens), int64(u.OutputTokens))
	}
}

// proxyOutcome maps the §11.2 sessionbudget tri-state onto the proxy-local
// tri-state so the enforcer stays decoupled from the llmproxy package. The
// two enums are structurally identical; the mapping is explicit so a future
// divergence in either enum is a compile-time break here rather than a silent
// mis-branch on the proxy write path. spec: §8.6 line 629; proposal 0023 S6.
func proxyOutcome(o sessionbudget.Outcome) llmproxy.Outcome {
	switch o {
	case sessionbudget.Granted:
		return llmproxy.OutcomeGranted
	case sessionbudget.Pending:
		return llmproxy.OutcomePending
	default:
		// sessionbudget.Terminal and any unknown value fail closed to the
		// proxy's terminal branch (deny the request).
		return llmproxy.OutcomeTerminal
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
