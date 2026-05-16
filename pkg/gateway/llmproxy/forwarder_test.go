// SPDX-License-Identifier: MIT

package llmproxy_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/llmproxy"
)

// spec: §4.9 — the LLM Proxy upstream forwarder, gating each upstream
// call through the circuit breaker.

func upstreamRequest(url string) *llmproxy.UpstreamRequest {
	return &llmproxy.UpstreamRequest{
		URL:  url,
		Body: []byte(`{"model":"claude-3-5-sonnet"}`),
		Header: map[string]string{
			"x-api-key":         "sk-ant-secret",
			"anthropic-version": "2023-06-01",
		},
	}
}

func TestForwardReturnsUpstreamResponseAndForwardsHeaders(t *testing.T) {
	var gotKey, gotVersion string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"msg_1"}`))
	}))
	defer srv.Close()

	f := &llmproxy.Forwarder{Breaker: &llmproxy.CircuitBreaker{}}
	resp, err := f.Forward(context.Background(), upstreamRequest(srv.URL))
	if err != nil {
		t.Fatalf("Forward: %v", err)
	}
	if resp.StatusCode != http.StatusOK || string(resp.Body) != `{"id":"msg_1"}` {
		t.Errorf("response = %d %q, want 200 with the upstream body", resp.StatusCode, resp.Body)
	}
	if gotKey != "sk-ant-secret" || gotVersion != "2023-06-01" {
		t.Errorf("upstream saw x-api-key=%q anthropic-version=%q, want the forwarded headers", gotKey, gotVersion)
	}
}

func TestForwardTripsBreakerOnUpstream5xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	breaker := &llmproxy.CircuitBreaker{FailureThreshold: 1}
	f := &llmproxy.Forwarder{Breaker: breaker}
	resp, err := f.Forward(context.Background(), upstreamRequest(srv.URL))
	if err != nil {
		t.Fatalf("Forward returned an error for a completed 5xx exchange: %v", err)
	}
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
	if breaker.State() != llmproxy.CircuitOpen {
		t.Errorf("breaker state = %q, want open after a 5xx", breaker.State())
	}
}

func TestForwardKeepsBreakerClosedOnUpstream4xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	breaker := &llmproxy.CircuitBreaker{FailureThreshold: 1}
	f := &llmproxy.Forwarder{Breaker: breaker}
	resp, err := f.Forward(context.Background(), upstreamRequest(srv.URL))
	if err != nil {
		t.Fatalf("Forward: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	if breaker.State() != llmproxy.CircuitClosed {
		t.Errorf("breaker state = %q, want closed — a 4xx does not feed the breaker", breaker.State())
	}
}

func TestForwardRejectsWhenBreakerOpen(t *testing.T) {
	var dialed bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dialed = true
	}))
	defer srv.Close()

	// Trip the breaker before the call.
	breaker := &llmproxy.CircuitBreaker{FailureThreshold: 1, Cooldown: time.Hour}
	breaker.Allow()
	breaker.RecordFailure()

	f := &llmproxy.Forwarder{Breaker: breaker}
	_, err := f.Forward(context.Background(), upstreamRequest(srv.URL))
	if !errors.Is(err, llmproxy.ErrCircuitOpen) {
		t.Fatalf("error = %v, want ErrCircuitOpen", err)
	}
	if dialed {
		t.Error("forwarder dialed the upstream though the breaker was open")
	}
}

func TestForwardClassifiesTransportFailureAsUpstream5xx(t *testing.T) {
	breaker := &llmproxy.CircuitBreaker{FailureThreshold: 1}
	f := &llmproxy.Forwarder{Breaker: breaker}
	// Port 1 refuses connections: a pre-first-byte transport failure.
	_, err := f.Forward(context.Background(), upstreamRequest("http://127.0.0.1:1"))
	wantErrorType(t, err, llmproxy.ErrUpstream5xx)
	if breaker.State() != llmproxy.CircuitOpen {
		t.Errorf("breaker state = %q, want open after a transport failure", breaker.State())
	}
}

func TestForwardClassifiesTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	f := &llmproxy.Forwarder{Breaker: &llmproxy.CircuitBreaker{}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := f.Forward(ctx, upstreamRequest(srv.URL))
	wantErrorType(t, err, llmproxy.ErrTimeout)
}
