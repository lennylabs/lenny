// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"net/http"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/credentials/credcache"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credfallback"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credleasestore"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/denylist"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/llmproxy"
	"github.com/lennylabs/lenny/pkg/gateway/metrics/gatewaymetrics"
	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor"
)

// llmFallbackWiring bundles the §4.9 Fallback Flow dependencies the LLM
// proxy handler drives on an upstream credential fault. A zero value (or
// nil controller) leaves the proxy on its pre-fallback behavior.
type llmFallbackWiring struct {
	controller *credfallback.Controller
	rotator    llmproxy.FallbackRotator
	audit      llmproxy.FallbackAuditSink
	metrics    llmproxy.FallbackMetrics
}

func newLLMProxyServer(addr string, translators llmproxy.TranslatorRegistry, leases credleasestore.LeaseStore, creds *credcache.Cache, denyList *denylist.DenyList, chain *interceptor.Chain, cache llmproxy.ProxyCache, gwMetrics *gatewaymetrics.Metrics, usage llmproxy.UsageRecorder, budgetGate llmproxy.BudgetGate, fallback llmFallbackWiring) *http.Server {
	if addr == "" {
		return nil
	}
	proxyMux := http.NewServeMux()
	proxyMux.Handle("POST /llm-proxy/v1/messages", &llmproxy.Handler{
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
		Metrics: gwMetrics,
		// spec: §4.9 — the credentialPolicy Fallback Flow.
		Fallback:        fallback.controller,
		FallbackRotator: fallback.rotator,
		FallbackAudit:   fallback.audit,
		FallbackMetrics: fallback.metrics,
	})
	return &http.Server{
		Addr:              addr,
		Handler:           proxyMux,
		ReadHeaderTimeout: 10 * time.Second,
	}
}

// llmTranslatorConfig carries the §4.9 per-provider translator config
// the gateway reads from flags. anthropic_direct and openai_direct
// register unconditionally with their defaults; the provider-config
// dependent translators register only when their required fields are
// set.
type llmTranslatorConfig struct {
	anthropicVersion string
	openaiBaseURL    string
	openaiOrg        string
	bedrockRegion    string
	vertexRegion     string
	vertexProject    string
	azureEndpoint    string
	azureAPIVersion  string
}

// buildLLMTranslatorRegistry assembles the §4.9 provider→translator
// registry the proxy dispatches on. spec: §4.9.
func buildLLMTranslatorRegistry(c llmTranslatorConfig) llmproxy.TranslatorRegistry {
	translators := []llmproxy.Translator{
		&llmproxy.AnthropicDirectTranslator{DefaultAnthropicVersion: c.anthropicVersion},
		&llmproxy.OpenAIDirectTranslator{BaseURL: c.openaiBaseURL, Organization: c.openaiOrg},
	}
	if c.bedrockRegion != "" {
		translators = append(translators, &llmproxy.AWSBedrockTranslator{Region: c.bedrockRegion})
	}
	if c.vertexRegion != "" && c.vertexProject != "" {
		translators = append(translators, &llmproxy.VertexAITranslator{Region: c.vertexRegion, Project: c.vertexProject})
	}
	if c.azureEndpoint != "" && c.azureAPIVersion != "" {
		translators = append(translators, &llmproxy.AzureOpenAITranslator{Endpoint: c.azureEndpoint, APIVersion: c.azureAPIVersion})
	}
	return llmproxy.NewTranslatorRegistry(translators...)
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
