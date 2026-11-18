// SPDX-License-Identifier: MIT

package llmproxy_test

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/lennylabs/lenny/pkg/gateway/meteringproxy/llmproxy"
)

// spec: §16.1 (Metrics), §4.9 (Credential Leasing Service), §11.2 (Budgets and Quotas)
func TestPromMetricsRegisterAndEmitOnTheGivenRegistry(t *testing.T) {
	reg := prometheus.NewRegistry()
	m, err := llmproxy.NewPromMetrics(reg)
	if err != nil {
		t.Fatalf("NewPromMetrics: %v", err)
	}
	m.IncLLMProxyConnections()
	m.DecLLMProxyConnections()
	m.ObserveLLMTranslation("claude-prod", "anthropic_direct", "anthropic", "request", 0.01)
	m.IncLLMTranslationError("claude-prod", "anthropic_direct", "upstream_5xx")
	m.IncCredentialRotation("rate_limited")
	m.IncCredentialFallbackExhausted("claude-prod", "anthropic_direct", "rate_limited")
	m.IncSessionBudgetExceeded("acme")

	want := `
# HELP lenny_gateway_llm_proxy_active_connections LLM Proxy active connections (§16.1).
# TYPE lenny_gateway_llm_proxy_active_connections gauge
lenny_gateway_llm_proxy_active_connections 0
# HELP lenny_gateway_llm_translation_errors_total LLM translator failures by error type (§16.1).
# TYPE lenny_gateway_llm_translation_errors_total counter
lenny_gateway_llm_translation_errors_total{error_type="upstream_5xx",pool="claude-prod",provider="anthropic_direct"} 1
# HELP lenny_credential_rotation_total Credential rotations by error type (§16.1).
# TYPE lenny_credential_rotation_total counter
lenny_credential_rotation_total{error_type="rate_limited"} 1
# HELP lenny_gateway_credential_fallback_exhausted_total Credential fallback-chain exhaustions by pool, provider, and error type (§4.9).
# TYPE lenny_gateway_credential_fallback_exhausted_total counter
lenny_gateway_credential_fallback_exhausted_total{error_type="rate_limited",pool="claude-prod",provider="anthropic_direct"} 1
# HELP lenny_gateway_session_budget_exceeded_total §11.2 sessions terminated mid-session for token-budget exhaustion, by tenant.
# TYPE lenny_gateway_session_budget_exceeded_total counter
lenny_gateway_session_budget_exceeded_total{tenant_id="acme"} 1
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(want),
		"lenny_gateway_llm_proxy_active_connections",
		"lenny_gateway_llm_translation_errors_total",
		"lenny_credential_rotation_total",
		"lenny_gateway_credential_fallback_exhausted_total",
		"lenny_gateway_session_budget_exceeded_total",
	); err != nil {
		t.Fatal(err)
	}
	if n := testutil.CollectAndCount(reg, "lenny_gateway_llm_translation_duration_seconds"); n != 1 {
		t.Fatalf("translation duration series = %d, want 1", n)
	}
}

func TestNewPromMetricsRefusesARegistryThatHoldsTheNames(t *testing.T) {
	reg := prometheus.NewRegistry()
	if _, err := llmproxy.NewPromMetrics(reg); err != nil {
		t.Fatalf("first NewPromMetrics: %v", err)
	}
	if _, err := llmproxy.NewPromMetrics(reg); err == nil {
		t.Fatal("second NewPromMetrics on the same registry returned no error")
	}
}

func TestNilPromMetricsIgnoresEveryCall(t *testing.T) {
	var m *llmproxy.PromMetrics
	m.IncLLMProxyConnections()
	m.DecLLMProxyConnections()
	m.ObserveLLMTranslation("p", "anthropic_direct", "anthropic", "request", 0.01)
	m.IncLLMTranslationError("p", "anthropic_direct", "upstream_5xx")
	m.IncCredentialRotation("rate_limited")
	m.IncCredentialFallbackExhausted("p", "anthropic_direct", "rate_limited")
	m.IncSessionBudgetExceeded("acme")
}
