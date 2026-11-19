// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/lennylabs/lenny/pkg/billing/usagestore"
	"github.com/lennylabs/lenny/pkg/gateway/api/admin"
	"github.com/lennylabs/lenny/pkg/gateway/core/session/sessionidle"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/proxycache"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/semanticcache"
	"github.com/lennylabs/lenny/pkg/gateway/meteringproxy/llmproxy"
	"github.com/lennylabs/lenny/pkg/gateway/meteringproxy/metering"
	"github.com/lennylabs/lenny/pkg/gateway/meteringproxy/sessionbudget"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/pkg/gateway/session/tokenanomaly"
	"github.com/lennylabs/lenny/pkg/policy"
	"github.com/lennylabs/lenny/pkg/policy/interceptor"
	"github.com/lennylabs/lenny/pkg/quota/quotastore"
)

func (w *gatewayWiring) buildLLMProxy(
	proxyFlags *llmproxy.Flags,
	meteringFlags *metering.Flags,
	policyChain *interceptor.Chain,
	sessionBudgetEnforcer *sessionbudget.Enforcer,
	activityStamper *sessionidle.Stamper,
	auditSink admin.AuditSink,
	erasureSemanticCache *semanticcache.InMemory,
	usage usagestore.Store,
	quotaCounter *quotastore.Counter,
	tenantLimits *policy.TenantStoreLimits,
) *http.Server {
	f := w.f
	llmSemanticCache := f.llmSemanticCache

	// ----- §4.9 LLM reverse proxy -----
	// With --llm-proxy-addr the gateway serves the §4.9 LLM proxy for
	// proxy-mode agent pods on a listener separate from the REST API. It
	// resolves an inbound lease token against llmLeases — the same store
	// the credassign service records minted leases in — and the upstream
	// credential against credCache. The credential deny list credDeny is
	// built earlier and wrapped by the credential-lease revocation
	// propagator, so the admin router's §11.4 full_revoke fan-out and
	// the emergency-revocation path revoke a user's leases onto the same
	// list the proxy reads here.
	llmTranslators := proxyFlags.Translators()
	// §4.9: the semantic cache is opt-in per pool. When
	// --llm-semantic-cache is set the gateway provisions the in-process
	// backend and the proxy consults it for any pool whose cachePolicy is
	// enabled; left off, the proxy path is uncached regardless of pool
	// policy. Per-user scope (the §4.9 default) keys on the session's
	// owning user, resolved from the session store.
	var llmCache llmproxy.ProxyCache
	if *llmSemanticCache {
		// Reuse the same in-memory store the §12.8 erasure orchestrator
		// wired above (erasureSemanticCache), so a DeleteByUser purges the
		// exact cache this proxy path populates. F-12.2.16.
		llmCache = proxycache.New(w.credentialPools, erasureSemanticCache, sessionUserLookup{w.sessions})
	}
	// spec: §4.9 — wire the §15.1 / §11.2 usage recorder so
	// proxy-extracted (authoritative) counts are persisted as the
	// quota-accounting record. Pod-reported counts are filtered at the
	// adapterclient ReportUsage boundary (see §11.2 usage path).
	llmProxyUsage := metering.NewRecorder(usage, w.sessions, w.sessionUsage, quotaCounter, tenantLimits, sessionBudgetEnforcer)
	// §8.6 / proposal 0023 S3/S4: record the recorder for the
	// direct-mode usage loop, and attach it to the metering proxy's budgets
	// so the leasecontrol episode fan-out reaches it: it accumulates each
	// granted token delta as it raises the enforcer budget, so RecordUsage
	// adds that delta to the base MaxTokenBudget and the raise survives the
	// next Enforcer.Record.
	w.proxyUsageRec = llmProxyUsage
	budgets := w.meteringBudgets
	budgets.SetRecorder(llmProxyUsage)
	// spec: §8.6 — apply the operator-tunable in-path extension
	// deadline (--proxy-extension-wait-timeout). §8.6 does not fix this
	// value; a zeroed flag leaves the recorder's 5s default in place. 0023.
	llmProxyUsage.SetExtensionWaitTimeout(*meteringFlags.ExtensionWaitTimeout)
	// spec: §12.4 — in the in_memory_reconciled mode the
	// authoritative per-tenant token accounting feeds the per-replica
	// budget slice rather than the Redis counter; route the recorder's
	// quota write to the tracker.
	llmProxyUsage.SetBudgetTracker(w.quotaBudgetTracker)
	// spec: §12.4 source (2) — fold each proxy-extracted token delta into the
	// in-memory fail-open accumulator so the Redis-recovery reconcile can
	// restore usage a Redis write dropped during an outage. F-12.4.20.
	llmProxyUsage.SetFailOpenAccumulator(w.quotaFailOpenAccum)
	// spec: §6.2 — each proxied LLM response is direct evidence of
	// active agent work; reset the session's idle clock so a long-running
	// streaming generation is not reaped by the §11.3 idle watchdog. F-11.3.7.
	llmProxyUsage.SetActivity(proxyActivityStamper(activityStamper))
	// spec: §11.2 (direct-mode usage integrity) — wire the direct-mode
	// under-reporting anomaly detector. It observes every direct-mode
	// ReportUsage delta the recorder fans in and raises
	// lenny_gateway_token_usage_anomaly_total (zero_delta / implausibly_small)
	// with a per-session structured log. The label set is a compile-time
	// constant, so New fails only on a coding-time §16.1.1 violation
	// (a forbidden label); that is a programming error, so it panics rather
	// than shipping a mislabeled counter. The proxy hot path never feeds it.
	// F-11.2.20.
	tokenAnomaly, err := tokenanomaly.New(w.gwMetrics.Registerer(), tokenanomaly.Config{
		ZeroTokenWindow:       *f.tokenAnomalyZeroWindow,
		ImplausiblySmallRatio: *f.tokenAnomalyImplausibleRatio,
	}, slog.Default())
	if err != nil {
		panic(fmt.Sprintf("wire token-usage anomaly detector: %v", err))
	}
	llmProxyUsage.SetAnomalyObserver(tokenAnomaly)
	budgets.SetAnomalyTracker(tokenAnomaly)
	// spec: §4.9 — the credentialPolicy Fallback Flow.
	// The Controller holds each session's rotation budget and per-provider
	// fallback chain; the rotator mints a replacement from the chain's
	// next pool and pushes it via the §4.7 RotateCredentials RPC, and the
	// audit sink emits credential.fallback_exhausted on exhaustion.
	llmFallbackDeps := llmFallbackWiring{
		controller: proxyFlags.FallbackController(),
		rotator:    proxyFallbackRotator{assign: w.credAssign, registry: w.podRegistry},
		audit:      proxyFallbackAudit{sink: auditSink},
		metrics:    w.meteringMetrics,
	}
	return newLLMProxyServer(*proxyFlags.Addr, llmTranslators, w.llmLeases, w.credCache, w.credDeny, policyChain, llmCache, w.meteringMetrics, llmProxyUsage, sessionBudgetEnforcer, llmFallbackDeps, proxyActivityStamper(activityStamper))
}

// buildStores constructs the §4.2/§4.4/§4.5 persistence layer and the
// §4.3/§10.2/§10.3 credential, signing, and verification surfaces: the
// Postgres or in-memory (Source-Mode SQLite) stores, the §12.3 store
// router, the §11.2.1 two-tier billing failover pipeline, the §7.1
// uploadToken key ring, the §4 KMS provider, the token service, the
// bearer verifier, the session executor, and the §4.9 credential-
// assignment service. It records every store and client the later build
// steps wire onto the accumulator. The §17.4 SQLite flush-loop cancel
// and the §4.3 token-service connection close are deferred by the
// composition root (runGateway) so they run at process shutdown rather
// than when this build step returns.
//
// spec: §4.1 gateway subsystem seams; §4.2 / §4.4 / §4.5 stores.
// buildAdminRouter is an extracted §4.1 composition-root build step (R1).
// spec: §4.1 gateway subsystem seams.
// buildAdminRouter constructs the §15.1 admin REST router and its
// per-domain WithX wiring (tenants, runtimes, pools, connectors, billing
// corrections, CA rotation, runtime upgrade, user revocation, and the
// rest), records it on w.adminRouter, and returns the sibling-block locals
// the §8.6 control server and the §4.9 LLM proxy still consume:
// connectorAuthorizer/connectorInvoker (the §9.1 connector tool bridge),
// the §10.5 runtime-upgrade durable store the /internal endpoint reads,
// and the §12.8 erasure semantic cache the proxy purges on DeleteByUser.
//
// spec: §4.1 gateway subsystem seams; §15.1 admin API.

// proxyActivityStamper returns the stamper as the session core's
// GWSEAM-StampActivity for the LLM proxy, or an untyped nil for a nil
// stamper.
// spec: §6.2 (maxClientIdleSeconds clock behavior across states)
func proxyActivityStamper(s *sessionidle.Stamper) seam.Activity {
	if s == nil {
		return nil
	}
	return s
}
