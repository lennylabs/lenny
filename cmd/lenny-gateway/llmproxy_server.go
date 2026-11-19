// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"net/http"

	"github.com/lennylabs/lenny/pkg/credential/credcache"
	"github.com/lennylabs/lenny/pkg/credential/denylist"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credfallback"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credleasestore"
	"github.com/lennylabs/lenny/pkg/gateway/meteringproxy/llmproxy"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/pkg/policy/interceptor"
)

// llmFallbackWiring bundles the §4.9 Fallback Flow dependencies the LLM
// proxy handler drives on an upstream credential fault. A zero value (or
// nil controller) leaves the proxy on its pre-fallback behavior.
type llmFallbackWiring struct {
	controller *credfallback.Controller
	rotator    seam.CredentialRotations
	audit      llmproxy.FallbackAuditSink
	metrics    llmproxy.FallbackMetrics
}

// newLLMProxyServer builds the §4.9 LLM proxy handler over the composition
// root's stores and the dependencies buildLLMProxy wires, and returns the
// proxy's server on addr, or nil when addr is empty.
func newLLMProxyServer(addr string, translators llmproxy.TranslatorRegistry, leases credleasestore.LeaseStore, creds *credcache.Cache, denyList *denylist.DenyList, chain *interceptor.Chain, cache llmproxy.ProxyCache, metrics *llmproxy.PromMetrics, usage llmproxy.UsageRecorder, budgetGate llmproxy.BudgetGate, fallback llmFallbackWiring, activity seam.Activity) *http.Server {
	if addr == "" {
		return nil
	}
	return llmproxy.NewServer(addr, &llmproxy.Handler{
		Leases:       leases,
		Translators:  translators,
		Forwarder:    &llmproxy.Forwarder{Breaker: &llmproxy.CircuitBreaker{}},
		Credentials:  creds,
		DenyList:     denyList,
		Interceptors: chain,
		Cache:        cache,
		// spec: §4.9 — proxy-extracted counts feed the §15.1 /
		// §11.2 usage record. A nil Usage discards the counts.
		Usage: usage,
		// spec: §11.2 / §8.10 — reject a proxied request
		// for a session that has already exhausted its token budget.
		BudgetGate: budgetGate,
		// §16.1: active connections, translation
		// duration, and translation errors on the gateway registry.
		Metrics: metrics,
		// spec: §4.9 — the credentialPolicy Fallback Flow.
		Fallback:        fallback.controller,
		FallbackRotator: fallback.rotator,
		FallbackAudit:   fallback.audit,
		FallbackMetrics: fallback.metrics,
		// spec: §6.2 (maxClientIdleSeconds clock behavior across states) —
		// each relayed stream line of a proxy-mode session stamps its idle
		// clock.
		Activity: activity,
	})
}

func (l sessionUserLookup) UserID(ctx context.Context, tenantID, sessionID string) (string, bool) {
	sess, err := l.sessions.Get(ctx, tenantID, sessionID)
	if err != nil || sess.UserID == "" {
		return "", false
	}
	return sess.UserID, true
}

// buildLLMProxy builds the §4.9 LLM reverse-proxy HTTP server for
// proxy-mode agent pods: the translator registry, the opt-in semantic
// cache, the §15.1/§11.2 usage recorder, and the §4.9 credential
// fallback controller. It returns nil when --llm-proxy-addr is empty,
// which disables the proxy listener. The dependencies it does not own
// (the credential and policy surfaces, the session-budget gate, and the
// idle-activity stamper) are passed in; the stores and metrics it shares
// with the rest of the gateway are read from the accumulator.
//
// spec: §4.9 — LLM Proxy subsystem (a named §4.1 extraction target).
