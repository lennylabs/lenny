// SPDX-License-Identifier: MIT

package gatewaymetrics_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/metrics/gatewaymetrics"
)

func TestInterceptorMTLSHandshakeMetric_spec_10_3_332(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.ObserveInterceptorMTLSHandshake("success", 0.012)
	m.ObserveInterceptorMTLSHandshake("san_mismatch", 0.004)
	m.ObserveInterceptorMTLSHandshake("cert_missing", 0.001)

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("/metrics status %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`lenny_interceptor_mtls_handshake_duration_seconds_count{result="success"} 1`,
		`lenny_interceptor_mtls_handshake_duration_seconds_count{result="san_mismatch"} 1`,
		`lenny_interceptor_mtls_handshake_duration_seconds_count{result="cert_missing"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics missing %q\n---\n%s", want, body)
		}
	}
}

func TestInterceptorMTLSHandshakeMetricNilSafe(t *testing.T) {
	var m *gatewaymetrics.Metrics
	m.ObserveInterceptorMTLSHandshake("success", 1.0)
}

// TestRevocationPropagationMetricRegisteredOnGateway_SEC_TS_1 pins the
// SEC-TS-1 producer wiring on the gateway: the gateway process must
// register lenny_token_revocation_propagation_seconds and observe it keyed
// by the §16.7 propagation_mode (`postgres_only` for the §17.6 in-process
// admin-credential rotation), matching the C3/§7 design that names
// pkg/gateway/externalapi/admintoken as a producer site alongside the Token
// Service. Before the fix the gateway registered no such histogram, so the
// producer site the proposal lists was absent and this scrape would return
// no lenny_token_revocation_propagation_seconds series.
// spec: §16.1, §16.5, §16.7 — SEC-TS-1.
func TestRevocationPropagationMetricRegisteredOnGateway_SEC_TS_1(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.ObserveRevocationPropagation("postgres_only", 30*time.Millisecond)
	m.ObserveRevocationPropagation("postgres_only", 40*time.Millisecond)

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("/metrics status %d", rr.Code)
	}
	body := rr.Body.String()
	// Two observations on the postgres_only outcome; the count series must
	// exist and equal 2, proving the gateway is a registered producer.
	if want := `lenny_token_revocation_propagation_seconds_count{outcome="postgres_only"} 2`; !strings.Contains(body, want) {
		t.Errorf("/metrics missing %q — gateway is not a registered SEC-TS-1 producer\n---\n%s", want, body)
	}
}

// TestRevocationPropagationMetricNilSafe asserts the observe method is safe
// on a nil emitter, matching the other gateway observe methods so an
// unwired dev deployment does not panic. spec: §16.1 — SEC-TS-1.
func TestRevocationPropagationMetricNilSafe(t *testing.T) {
	var m *gatewaymetrics.Metrics
	m.ObserveRevocationPropagation("postgres_only", time.Second)
}

// TestDelegationBudgetKeysExpiredMetricExposes_spec_16_1 asserts that one
// BUDGET_KEYS_EXPIRED detection is exposed on the registry's handler under
// the root's pool and the tenant.
// spec: §16.1
func TestDelegationBudgetKeysExpiredMetricExposes_spec_16_1(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.IncDelegationBudgetKeysExpired("p", "acme")

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("/metrics status %d", rr.Code)
	}
	if want := `lenny_delegation_budget_keys_expired_total{pool="p",tenant_id="acme"} 1`; !strings.Contains(rr.Body.String(), want) {
		t.Errorf("/metrics missing %q\n---\n%s", want, rr.Body.String())
	}
}

// TestDelegationBudgetKeysExpiredMetricNilSafe asserts the increment is a
// no-op on a nil emitter. spec: §16.1
func TestDelegationBudgetKeysExpiredMetricNilSafe(t *testing.T) {
	var m *gatewaymetrics.Metrics
	m.IncDelegationBudgetKeysExpired("p", "acme")
}
