// SPDX-License-Identifier: MIT

package main

import (
	"net/http"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/billing/usagestore"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credfallback"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/llmproxy"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/proxycache"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/semanticcache"
	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/policy/policy"
	"github.com/lennylabs/lenny/pkg/gateway/quota/quotastore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionbudget"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionidle"
)

func (w *gatewayWiring) buildLLMProxy(
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
	llmProxyAddr := f.llmProxyAddr
	llmSemanticCache := f.llmSemanticCache
	anthropicVersion := f.anthropicVersion
	openaiBaseURL := f.openaiBaseURL
	openaiOrg := f.openaiOrg
	bedrockRegion := f.bedrockRegion
	vertexRegion := f.vertexRegion
	vertexProject := f.vertexProject
	azureEndpoint := f.azureEndpoint
	azureAPIVersion := f.azureAPIVersion
	credentialFallbackMaxRotations := f.credentialFallbackMaxRotations
	credentialFallbackCooldownSeconds := f.credentialFallbackCooldownSeconds

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
	llmTranslators := buildLLMTranslatorRegistry(llmTranslatorConfig{
		anthropicVersion: *anthropicVersion,
		openaiBaseURL:    *openaiBaseURL,
		openaiOrg:        *openaiOrg,
		bedrockRegion:    *bedrockRegion,
		vertexRegion:     *vertexRegion,
		vertexProject:    *vertexProject,
		azureEndpoint:    *azureEndpoint,
		azureAPIVersion:  *azureAPIVersion,
	})
	// §4.9 lines 1542-1556: the semantic cache is opt-in per pool. When
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
	// spec: §4.9 line 1468 — wire the §15.1 / §11.2 usage recorder so
	// proxy-extracted (authoritative) counts are persisted as the
	// quota-accounting record. Pod-reported counts are filtered at the
	// adapterclient ReportUsage boundary (see §11.2 usage path).
	llmProxyUsage := newProxyUsageRecorder(usage, w.sessions, w.sessionUsage, quotaCounter, tenantLimits, sessionBudgetEnforcer)
	// spec: §8.6 line 629 — apply the operator-tunable in-path extension
	// deadline (--proxy-extension-wait-timeout). §8.6 does not fix this
	// value; a zeroed flag leaves the recorder's 5s default in place. 0023.
	llmProxyUsage.setProxyExtensionWaitTimeout(*f.proxyExtensionWaitTimeout)
	// spec: §12.4 line 268 — in the in_memory_reconciled mode the
	// authoritative per-tenant token accounting feeds the per-replica
	// budget slice rather than the Redis counter; route the recorder's
	// quota write to the tracker.
	llmProxyUsage.setBudgetTracker(w.quotaBudgetTracker)
	// spec: §12.4 source (2) — fold each proxy-extracted token delta into the
	// in-memory fail-open accumulator so the Redis-recovery reconcile can
	// restore usage a Redis write dropped during an outage. F-12.4.20.
	llmProxyUsage.setFailOpenAccumulator(w.quotaFailOpenAccum)
	// spec: §6.2 line 277 — each proxied LLM response is direct evidence of
	// active agent work; reset the session's idle clock so a long-running
	// streaming generation is not reaped by the §11.3 idle watchdog. F-11.3.7.
	llmProxyUsage.setActivityStamper(activityStamper)
	// spec: §4.9 lines 1383-1411 — the credentialPolicy Fallback Flow.
	// The Controller holds each session's rotation budget and per-provider
	// fallback chain; the rotator mints a replacement from the chain's
	// next pool and pushes it via the §4.7 RotateCredentials RPC, and the
	// audit sink emits credential.fallback_exhausted on exhaustion.
	llmFallback := credfallback.NewController(*credentialFallbackMaxRotations,
		time.Duration(*credentialFallbackCooldownSeconds)*time.Second)
	llmFallbackDeps := llmFallbackWiring{
		controller: llmFallback,
		rotator:    proxyFallbackRotator{assign: w.credAssign, registry: w.podRegistry},
		audit:      proxyFallbackAudit{sink: auditSink},
		metrics:    w.gwMetrics,
	}
	return newLLMProxyServer(*llmProxyAddr, llmTranslators, w.llmLeases, w.credCache, w.credDeny, policyChain, llmCache, w.gwMetrics, llmProxyUsage, sessionBudgetEnforcer, llmFallbackDeps)
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
