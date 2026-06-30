// SPDX-License-Identifier: MIT

package llmproxy_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credleasestore"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/llmproxy"
)

// spec: §16.1 lines 97, 99, 100 — the proxy handler moves the
// active-connections gauge for the request lifetime, observes the
// translator-leg duration on each successful translation, and counts a
// translator failure by its §4.9 error_type.

type translationObservation struct {
	pool, provider, dialect, direction string
}

type translationErrorCall struct {
	pool, provider, errorType string
}

// spyProxyMetrics records every llmproxy.Metrics call for assertions.
type spyProxyMetrics struct {
	connInc, connDec int
	observations     []translationObservation
	errors           []translationErrorCall
}

func (s *spyProxyMetrics) IncLLMProxyConnections() { s.connInc++ }
func (s *spyProxyMetrics) DecLLMProxyConnections() { s.connDec++ }

func (s *spyProxyMetrics) ObserveLLMTranslation(pool, provider, proxyDialect, direction string, _ float64) {
	s.observations = append(s.observations, translationObservation{pool, provider, proxyDialect, direction})
}

func (s *spyProxyMetrics) IncLLMTranslationError(pool, provider, errorType string) {
	s.errors = append(s.errors, translationErrorCall{pool, provider, errorType})
}

func TestHandlerBalancesActiveConnectionGauge_spec_16_1(t *testing.T) {
	h := newProxyHarness(t)
	m := &spyProxyMetrics{}
	h.handler.Metrics = m
	if err := h.leases.Put(handlerLease("lt-conn")); err != nil {
		t.Fatalf("seed lease: %v", err)
	}

	rr := post(h.handler, "lt-conn", messagesBody)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rr.Code, rr.Body.String())
	}
	// The gauge is incremented on entry and decremented on exit so it
	// reflects in-flight requests; the two must balance.
	if m.connInc != 1 || m.connDec != 1 {
		t.Errorf("conn inc/dec = %d/%d, want 1/1", m.connInc, m.connDec)
	}
}

func TestHandlerObservesBothTranslationLegs_spec_16_1(t *testing.T) {
	h := newProxyHarness(t)
	m := &spyProxyMetrics{}
	h.handler.Metrics = m
	if err := h.leases.Put(handlerLease("lt-tr")); err != nil {
		t.Fatalf("seed lease: %v", err)
	}

	if rr := post(h.handler, "lt-tr", messagesBody); rr.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rr.Code, rr.Body.String())
	}

	var sawRequest, sawResponse bool
	for _, o := range m.observations {
		if o.pool != "claude-prod" || o.provider != string(credential.ProviderAnthropicDirect) || o.dialect != "anthropic" {
			t.Errorf("observation labels = %+v, want claude-prod/%s/anthropic", o, credential.ProviderAnthropicDirect)
		}
		switch o.direction {
		case "request":
			sawRequest = true
		case "response":
			sawResponse = true
		default:
			t.Errorf("unexpected direction %q", o.direction)
		}
	}
	if !sawRequest || !sawResponse {
		t.Errorf("translation legs observed: request=%v response=%v, want both", sawRequest, sawResponse)
	}
}

func TestHandlerCountsTranslationErrorByType_spec_16_1(t *testing.T) {
	// An upstream 5xx surfaces as the ErrUpstream5xx translator error,
	// routed through writeTranslationError, which counts the failure.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(upstream.Close)

	leases := credleasestore.New()
	if err := leases.Put(handlerLease("lt-5xx")); err != nil {
		t.Fatalf("seed lease: %v", err)
	}
	m := &spyProxyMetrics{}
	h := &llmproxy.Handler{
		Leases:      leases,
		Translator:  &llmproxy.AnthropicDirectTranslator{BaseURL: upstream.URL, DefaultAnthropicVersion: "2023-06-01"},
		Forwarder:   &llmproxy.Forwarder{Breaker: &llmproxy.CircuitBreaker{}},
		Credentials: fakeResolver{key: "sk-ant-real", ok: true},
		Metrics:     m,
	}

	rr := post(h, "lt-5xx", messagesBody)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
	if len(m.errors) != 1 {
		t.Fatalf("translation errors = %d, want 1", len(m.errors))
	}
	got := m.errors[0]
	if got.pool != "claude-prod" || got.provider != string(credential.ProviderAnthropicDirect) || got.errorType != string(llmproxy.ErrUpstream5xx) {
		t.Errorf("error call = %+v, want claude-prod/%s/%s", got, credential.ProviderAnthropicDirect, llmproxy.ErrUpstream5xx)
	}
	// The connection gauge still balances on the error path.
	if m.connInc != 1 || m.connDec != 1 {
		t.Errorf("conn inc/dec = %d/%d, want 1/1", m.connInc, m.connDec)
	}
}

func TestHandlerNilMetricsIsNoOp_spec_16_1(t *testing.T) {
	h := newProxyHarness(t)
	h.handler.Metrics = nil
	if err := h.leases.Put(handlerLease("lt-nil")); err != nil {
		t.Fatalf("seed lease: %v", err)
	}
	if rr := post(h.handler, "lt-nil", messagesBody); rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with nil Metrics", rr.Code)
	}
}
