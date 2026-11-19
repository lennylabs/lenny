// SPDX-License-Identifier: MIT

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/replica/gatewaymetrics"
)

// TestMeteringMetricsAppearOnTheGatewayScrape checks that the metering
// proxy's metric set, which the metering proxy owns, is exposed on the
// gateway's /metrics as the gateway metrics exposed it before.
//
// spec: §16.1 (Metrics)
func TestMeteringMetricsAppearOnTheGatewayScrape(t *testing.T) {
	gw, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("gatewaymetrics.New: %v", err)
	}
	m, err := registerMeteringMetrics(gw)
	if err != nil {
		t.Fatalf("registerMeteringMetrics: %v", err)
	}
	m.ObserveLLMTranslation("claude-prod", "anthropic_direct", "anthropic", "request", 0.01)
	m.IncLLMTranslationError("claude-prod", "anthropic_direct", "upstream_5xx")
	m.IncCredentialRotation("rate_limited")
	m.IncCredentialFallbackExhausted("claude-prod", "anthropic_direct", "rate_limited")
	m.IncSessionBudgetExceeded("acme")

	rr := httptest.NewRecorder()
	gw.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("/metrics status = %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"lenny_gateway_llm_proxy_active_connections 0",
		"lenny_gateway_llm_translation_duration_seconds_count",
		"lenny_gateway_llm_translation_errors_total",
		"lenny_credential_rotation_total",
		"lenny_gateway_credential_fallback_exhausted_total",
		"lenny_gateway_session_budget_exceeded_total",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics output missing %q", want)
		}
	}
	if _, err := registerMeteringMetrics(gw); err == nil {
		t.Error("a second registration on the same gateway registry succeeded")
	}
}
