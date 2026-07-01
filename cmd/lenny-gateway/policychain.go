// SPDX-License-Identifier: MIT

package main

import (
	"log"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/audit/auditscope"
	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/policy/policy"
	"github.com/lennylabs/lenny/pkg/gateway/quota/quotabudget"
	quotacheckpointpg "github.com/lennylabs/lenny/pkg/gateway/quota/quotacheckpoint/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/quota/quotafailopen"
	"github.com/lennylabs/lenny/pkg/gateway/quota/quotastore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/failopen"
	"github.com/lennylabs/lenny/pkg/quota"
	"github.com/lennylabs/lenny/pkg/storerouter"
)

// buildPolicyChain is the §4.1 composition-root build step (R1) for the §4.8
// gateway policy engine. It constructs the interceptor chain, registers the
// built-in evaluators (§4.8 AuthEvaluator at PreAuth, DelegationPolicyEvaluator
// at PreDelegation, RetryPolicyEvaluator at PostRoute, and the §11.2
// QuotaEvaluator at PostAuth when a token counter is wired), resolves the
// §12.4 quota enforcement mode, and constructs the §11.2 quota counter,
// tenant-limits resolver, §12.4 in-memory budget tracker and fail-open
// accumulator, and the §12.4 replica-count source. It records the chain, the
// §8.3 maxInputSize resolver holder, the policy audit sink, and the quota
// surfaces on the accumulator so the session server, the MCP fabric, the
// admin router, the HTTP surface, and the LLM proxy read them back.
//
// spec: §4.1 gateway subsystem seams; §4.8 policy engine; §11.2 / §12.4 quota.
func (w *gatewayWiring) buildPolicyChain(
	auditAppender policy.AuditAppender,
	auditValidator *auditscope.Validator,
) {
	f := w.f
	interceptorFailOpenMax := f.interceptorFailOpenMax
	delegationMaxInputSize := f.delegationMaxInputSize
	retryMaxRetries := f.retryMaxRetries
	quotaEnforcementMode := f.quotaEnforcementMode
	quotaSyncIntervalSeconds := f.quotaSyncIntervalSeconds
	globalTokenQuota := f.globalTokenQuota
	userTokenQuota := f.userTokenQuota
	quotaRollingWindowSeconds := f.quotaRollingWindowSeconds

	// ----- §4.8 policy interceptor chain -----
	// The PostAuth chain runs the built-in §4.8 QuotaEvaluator (priority
	// 200) on the session-creation path. QuotaEvaluator enforces the
	// §11.2 hierarchical token budget against the Redis token-usage
	// counter, so it is registered only when --redis-url is set; without
	// Redis there is no §11.2 token counter and the chain stays empty.
	policyChain := interceptor.NewChain()
	// §4.8 line 1030: arm cumulative fail-open escalation on every chain.
	// A fail-open interceptor that crosses the ceiling within the rolling
	// 5-minute window is auto-promoted to fail-closed; the transition
	// writes interceptor.fail_open_escalated to the tenant's audit chain.
	policyChain.SetFailOpenEscalation(*interceptorFailOpenMax, 0,
		policy.NewFailOpenObserver(auditAppender, nil), nil)
	// §4.8 line 972: AuthEvaluator (priority 100) is the sole built-in at
	// PreAuth. It runs in the auth middleware after the principal is
	// resolved as the fail-closed identity gate every later phase relies
	// on. "Always active" — registered unconditionally.
	if err := policyChain.Register(interceptor.PhasePreAuth, policy.NewAuthEvaluator()); err != nil {
		log.Fatalf("lenny-gateway: register AuthEvaluator: %v", err)
	}
	// §4.8 line 974: DelegationPolicyEvaluator (priority 250) fires at
	// PreDelegation only. It enforces the §8.3 contentPolicy.maxInputSize
	// cap on TaskSpec.input (INPUT_TOO_LARGE); the §8.3 depth/fan-out/
	// cycle/tag enforcement stays canonical in delegation.Service. The
	// resolver reads the effective DelegationPolicy's per-policy
	// maxInputSize (filled in once delegationSvc exists, below); a
	// runtime that names no policy falls back to the operator-tunable
	// default cap (--delegation-max-input-size). F-13.5.1 / F-8.2.9.
	maxInputResolver := &maxInputSizeResolverHolder{}
	if err := policyChain.Register(interceptor.PhasePreDelegation,
		policy.NewDelegationPolicyEvaluator(maxInputResolver, *delegationMaxInputSize)); err != nil {
		log.Fatalf("lenny-gateway: register DelegationPolicyEvaluator: %v", err)
	}
	// §4.8 line 977: RetryPolicyEvaluator (priority 600) fires at PostRoute
	// only. It enforces the §7.3 automatic-retry budget — a session whose
	// retryCount has reached the effective retryPolicy.maxRetries is
	// rejected at routing rather than re-claiming a warm pod for an attempt
	// past its budget. The §7.3 resume-window timer stays canonical in the
	// watchdog. Backed by the session store; "always active" — registered
	// unconditionally.
	if err := policyChain.Register(interceptor.PhasePostRoute,
		policy.NewRetryPolicyEvaluator(sessionRetryLookup{sessions: w.sessions}, nil, *retryMaxRetries)); err != nil {
		log.Fatalf("lenny-gateway: register RetryPolicyEvaluator: %v", err)
	}
	log.Printf("lenny-gateway: §4.8 AuthEvaluator (PreAuth), DelegationPolicyEvaluator (PreDelegation, maxInputSize=%d), and RetryPolicyEvaluator (PostRoute, maxRetries=%d) registered", *delegationMaxInputSize, *retryMaxRetries)
	var policyAuditSink *policy.AuditSink
	// quotaCounter / tenantLimits are hoisted out of the redis-only block
	// so the §4.9 proxy usage recorder (built later) can advance the same
	// §11.2 hierarchical token counter QuotaEvaluator reads. quotaCounter
	// stays nil when --redis-url is unset (Redis mode) or always (in-memory
	// mode); tenantLimits is resolved whenever either enforcement mode is
	// active. quotaBudgetTracker is non-nil only in the §12.4 line 268
	// in_memory_reconciled mode; the recorder feeds it and the periodic
	// reconcile loop is started under watchdogCtx below.
	var quotaCounter *quotastore.Counter
	var tenantLimits *policy.TenantStoreLimits
	var quotaBudgetTracker *quotabudget.Tracker
	// §12.4 source (2) / §11.2 line 48 in-memory fail-open token accumulator.
	// Shared by the proxy usage recorder (which folds each proxy-extracted
	// token delta into it) and the quotacheckpoint reconcile (which restores
	// dropped usage from it on a Redis-recovery edge). Only meaningful in the
	// Redis-counter mode; constructed below when quotaCounter is wired.
	// F-12.4.20.
	var quotaFailOpenAccum *quotafailopen.Accumulator

	// §12.4 line 224 cached_replica_count, shared by the fail-open ceiling
	// (buildFailOpenController) and the §12.4 line 268 in-memory budget
	// slice divisor; the Endpoints poller below updates it in a Kubernetes
	// deployment and a cold start reads as 1.
	failOpenReplicas := failopen.NewReplicaCount()

	// spec: §12.4 line 268 — resolve the quota enforcement mode. The
	// in-memory reconciled mode draws a per-replica budget slice from
	// Postgres rather than reading the Redis counters; it requires Postgres.
	quotaMode, qemErr := quota.ParseEnforcementMode(*quotaEnforcementMode)
	if qemErr != nil {
		log.Fatalf("lenny-gateway: %v", qemErr)
	}
	quotaSyncSeconds := quota.ClampSyncIntervalSeconds(*quotaSyncIntervalSeconds)
	if quotaSyncSeconds != *quotaSyncIntervalSeconds && *quotaSyncIntervalSeconds > 0 {
		// spec: §11.2 line 44 — clamp the operator-supplied cadence up to
		// the 10s floor so a misconfiguration cannot drive a busy-loop.
		log.Printf("lenny-gateway: §11.2 line 44 quotaSyncIntervalSeconds=%d below the %ds floor; clamping to the minimum",
			*quotaSyncIntervalSeconds, quota.MinSyncIntervalSeconds)
	}

	switch {
	case quotaMode == quota.EnforcementModeInMemoryReconciled:
		// spec: §12.4 line 268 — the per-replica in-memory budget slice is
		// drawn from and reconciled to the token_usage_checkpoint table, so
		// Postgres is mandatory. Fail closed rather than silently falling
		// back to an unmetered admission path.
		if w.pgPool == nil {
			log.Fatalf("lenny-gateway: --quota-enforcement-mode=in_memory_reconciled requires Postgres (--postgres-dsn): the per-replica budget slice is drawn from and reconciled to the token_usage_checkpoint table")
		}
		tenantLimits = policy.NewTenantStoreLimits(w.tenants, policy.TenantStoreLimitsOptions{
			GlobalTokenQuotaPerWindow: *globalTokenQuota,
			UserTokenQuotaPerWindow:   *userTokenQuota,
			RollingWindow:             time.Duration(*quotaRollingWindowSeconds) * time.Second,
		})
		quotaBudgetTracker = quotabudget.New(quotabudget.Options{
			Limits:            tenantLimits,
			Adder:             quotacheckpointpg.New(w.pgPool),
			Replicas:          failOpenReplicas,
			ReconcileInterval: time.Duration(quotaSyncSeconds) * time.Second,
		})
		quotaEval := policy.NewQuotaEvaluator(tenantLimits, quotabudget.NewUsageReader(quotaBudgetTracker), nil)
		if err := policyChain.Register(interceptor.PhasePostAuth, quotaEval); err != nil {
			log.Fatalf("lenny-gateway: register QuotaEvaluator (in_memory_reconciled): %v", err)
		}
		// spec: §11.7 line 428 — route policy-rejection audit rows through
		// the write-time tenant-scope validator alongside the admin sink.
		policyAuditSink = policy.NewAuditSink(auditValidator, nil)
		log.Printf("lenny-gateway: §12.4 quotaEnforcementMode=in_memory_reconciled — QuotaEvaluator enforcing per-tenant token budgets from per-replica Postgres budget slices (reconcile cadence %ds); the Redis quota counters are not consulted", quotaSyncSeconds)
	case w.redisClient != nil:
		quotaCounter = quotastore.New(w.concernRedis.For(storerouter.RedisConcernQuota))
		// §12.4 source (2): the Redis-counter mode keeps the in-memory
		// fail-open accumulator so a Redis outage's dropped token writes are
		// recoverable via the MAX-rule reconcile. F-12.4.20.
		quotaFailOpenAccum = quotafailopen.New()
		tenantLimits = policy.NewTenantStoreLimits(w.tenants, policy.TenantStoreLimitsOptions{
			GlobalTokenQuotaPerWindow: *globalTokenQuota,
			UserTokenQuotaPerWindow:   *userTokenQuota,
			RollingWindow:             time.Duration(*quotaRollingWindowSeconds) * time.Second,
		})
		quotaEval := policy.NewQuotaEvaluator(tenantLimits, quotaCounter, nil)
		if err := policyChain.Register(interceptor.PhasePostAuth, quotaEval); err != nil {
			log.Fatalf("lenny-gateway: register QuotaEvaluator: %v", err)
		}
		// spec: §11.7 line 428 — route policy-rejection audit rows through
		// the write-time tenant-scope validator alongside the admin sink.
		policyAuditSink = policy.NewAuditSink(auditValidator, nil)
		log.Printf("lenny-gateway: §4.8 QuotaEvaluator enforcing §11.2 token budgets on the PostAuth chain (quota checkpoint cadence %ds)", quotaSyncSeconds)
	}

	w.policyChain = policyChain
	w.maxInputResolver = maxInputResolver
	w.policyAuditSink = policyAuditSink
	w.quotaCounter = quotaCounter
	w.tenantLimits = tenantLimits
	w.quotaBudgetTracker = quotaBudgetTracker
	w.quotaFailOpenAccum = quotaFailOpenAccum
	w.failOpenReplicas = failOpenReplicas
}
