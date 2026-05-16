// SPDX-License-Identifier: MIT

package llmproxy_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credleasestore"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy"
)

// spec: §4.9 — the LLM reverse proxy HTTP handler: lease-token
// resolution, the per-request lease checks, translation, credential
// injection, breaker-gated forwarding, and response translation.

const messagesBody = `{"model":"claude-3-5-sonnet","messages":[{"role":"user","content":"hi"}]}`

// fakeResolver is a CredentialResolver returning a fixed upstream key.
type fakeResolver struct {
	key string
	ok  bool
}

func (f fakeResolver) UpstreamCredential(credential.Lease) (string, bool) {
	return f.key, f.ok
}

// fakeDenyList is a DenyList returning a fixed verdict.
type fakeDenyList struct{ revoked bool }

func (f fakeDenyList) Revoked(credential.CredentialKey) bool { return f.revoked }

// handlerLease returns a valid pool-backed proxy lease for the handler
// tests, holding the given lease token.
func handlerLease(token string) credential.Lease {
	return credential.Lease{
		LeaseID:      "cl_h1",
		SessionID:    "s_h1",
		Provider:     credential.ProviderAnthropicDirect,
		Source:       credential.SourcePool,
		PoolID:       "claude-prod",
		CredentialID: "key-1",
		DeliveryMode: credential.DeliveryProxy,
		ExpiresAt:    time.Now().Add(time.Hour),
		Proxy: &credential.ProxyConfig{
			ProxyURL:     "https://gateway-internal:8443/llm-proxy",
			ProxyDialect: "anthropic",
			LeaseToken:   token,
		},
	}
}

// proxyHarness bundles a handler with its in-memory lease store and the
// upstream key the fake Anthropic server should observe.
type proxyHarness struct {
	handler  *llmproxy.Handler
	leases   *credleasestore.Store
	gotKey   *string
	upstream *httptest.Server
}

// newProxyHarness builds a handler wired to a fake upstream Anthropic
// server. The server records the x-api-key it receives and replies 200
// with a usage-bearing body.
func newProxyHarness(t *testing.T) *proxyHarness {
	t.Helper()
	gotKey := new(string)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*gotKey = r.Header.Get("x-api-key")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"msg_1","usage":{"input_tokens":5,"output_tokens":7}}`))
	}))
	t.Cleanup(upstream.Close)

	leases := credleasestore.New()
	h := &llmproxy.Handler{
		Leases:      leases,
		Translator:  &llmproxy.AnthropicDirectTranslator{BaseURL: upstream.URL, DefaultAnthropicVersion: "2023-06-01"},
		Forwarder:   &llmproxy.Forwarder{Breaker: &llmproxy.CircuitBreaker{}},
		Credentials: fakeResolver{key: "sk-ant-real-upstream-key", ok: true},
	}
	return &proxyHarness{handler: h, leases: leases, gotKey: gotKey, upstream: upstream}
}

// post issues a proxy request carrying the lease token in x-api-key.
func post(h *llmproxy.Handler, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	if token != "" {
		req.Header.Set("x-api-key", token)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// errorCode decodes the proxy error envelope and returns its code.
func errorCode(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error envelope: %v; body=%s", err, rr.Body.String())
	}
	return env.Error.Code
}

func TestHandlerProxiesAndInjectsTheRealCredential(t *testing.T) {
	h := newProxyHarness(t)
	if err := h.leases.Put(handlerLease("lt-abc")); err != nil {
		t.Fatalf("seed lease: %v", err)
	}

	rr := post(h.handler, "lt-abc", messagesBody)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"id":"msg_1"`) {
		t.Errorf("body = %q, want the upstream response passed through", rr.Body.String())
	}
	// The upstream must see the real key, never the agent pod's lease
	// token — the §4.9 guarantee that the real key never leaves the
	// gateway is meaningless if the proxy forwards the lease token.
	if *h.gotKey != "sk-ant-real-upstream-key" {
		t.Errorf("upstream x-api-key = %q, want the injected real credential", *h.gotKey)
	}
	if *h.gotKey == "lt-abc" {
		t.Error("the proxy forwarded the agent pod's lease token upstream")
	}
}

func TestHandlerRejectsMissingLeaseToken(t *testing.T) {
	h := newProxyHarness(t)
	rr := post(h.handler, "", messagesBody)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
	if code := errorCode(t, rr); code != "LEASE_TOKEN_MISSING" {
		t.Errorf("error code = %q, want LEASE_TOKEN_MISSING", code)
	}
}

func TestHandlerRejectsUnknownLeaseToken(t *testing.T) {
	h := newProxyHarness(t)
	rr := post(h.handler, "lt-unknown", messagesBody)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
	if code := errorCode(t, rr); code != "LEASE_TOKEN_INVALID" {
		t.Errorf("error code = %q, want LEASE_TOKEN_INVALID", code)
	}
}

func TestHandlerRejectsExpiredLease(t *testing.T) {
	h := newProxyHarness(t)
	lease := handlerLease("lt-exp")
	lease.ExpiresAt = time.Now().Add(-time.Minute)
	if err := h.leases.Put(lease); err != nil {
		t.Fatalf("seed lease: %v", err)
	}
	rr := post(h.handler, "lt-exp", messagesBody)
	if rr.Code != http.StatusForbidden || errorCode(t, rr) != "LEASE_EXPIRED" {
		t.Errorf("status %d code %q, want 403 LEASE_EXPIRED", rr.Code, errorCode(t, rr))
	}
}

func TestHandlerRejectsRevokedCredential(t *testing.T) {
	h := newProxyHarness(t)
	h.handler.DenyList = fakeDenyList{revoked: true}
	if err := h.leases.Put(handlerLease("lt-rev")); err != nil {
		t.Fatalf("seed lease: %v", err)
	}
	rr := post(h.handler, "lt-rev", messagesBody)
	if rr.Code != http.StatusForbidden || errorCode(t, rr) != "CREDENTIAL_REVOKED" {
		t.Errorf("status %d code %q, want 403 CREDENTIAL_REVOKED", rr.Code, errorCode(t, rr))
	}
}

func TestHandlerRejectsSpiffeMismatch(t *testing.T) {
	h := newProxyHarness(t)
	lease := handlerLease("lt-spiffe")
	// The lease is SPIFFE-bound; a plain (non-mTLS) test request carries
	// no peer SPIFFE identity, so the bound lease cannot be matched.
	lease.SpiffeURI = "spiffe://lenny.test/agent/claude-prod/pod-1"
	if err := h.leases.Put(lease); err != nil {
		t.Fatalf("seed lease: %v", err)
	}
	rr := post(h.handler, "lt-spiffe", messagesBody)
	if rr.Code != http.StatusForbidden || errorCode(t, rr) != "LEASE_SPIFFE_MISMATCH" {
		t.Errorf("status %d code %q, want 403 LEASE_SPIFFE_MISMATCH", rr.Code, errorCode(t, rr))
	}
}

func TestHandlerRejectsWhenUpstreamCredentialUnavailable(t *testing.T) {
	h := newProxyHarness(t)
	h.handler.Credentials = fakeResolver{ok: false}
	if err := h.leases.Put(handlerLease("lt-nokey")); err != nil {
		t.Fatalf("seed lease: %v", err)
	}
	rr := post(h.handler, "lt-nokey", messagesBody)
	if rr.Code != http.StatusBadGateway || errorCode(t, rr) != "UPSTREAM_CREDENTIAL_UNAVAILABLE" {
		t.Errorf("status %d code %q, want 502 UPSTREAM_CREDENTIAL_UNAVAILABLE", rr.Code, errorCode(t, rr))
	}
}

func TestHandlerRejectsMalformedRequestBody(t *testing.T) {
	h := newProxyHarness(t)
	if err := h.leases.Put(handlerLease("lt-bad")); err != nil {
		t.Fatalf("seed lease: %v", err)
	}
	rr := post(h.handler, "lt-bad", `{not json`)
	if rr.Code != http.StatusBadRequest || errorCode(t, rr) != "PROVIDER_REQUEST_INVALID" {
		t.Errorf("status %d code %q, want 400 PROVIDER_REQUEST_INVALID", rr.Code, errorCode(t, rr))
	}
}

func TestHandlerMapsUpstream5xxToProviderUnavailable(t *testing.T) {
	gotKey := new(string)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*gotKey = r.Header.Get("x-api-key")
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer upstream.Close()

	leases := credleasestore.New()
	if err := leases.Put(handlerLease("lt-5xx")); err != nil {
		t.Fatalf("seed lease: %v", err)
	}
	h := &llmproxy.Handler{
		Leases:      leases,
		Translator:  &llmproxy.AnthropicDirectTranslator{BaseURL: upstream.URL, DefaultAnthropicVersion: "2023-06-01"},
		Forwarder:   &llmproxy.Forwarder{Breaker: &llmproxy.CircuitBreaker{}},
		Credentials: fakeResolver{key: "sk-ant-real", ok: true},
	}
	rr := post(h, "lt-5xx", messagesBody)
	if rr.Code != http.StatusServiceUnavailable || errorCode(t, rr) != "PROVIDER_UNAVAILABLE" {
		t.Errorf("status %d code %q, want 503 PROVIDER_UNAVAILABLE", rr.Code, errorCode(t, rr))
	}
}

func TestHandlerRejectsWhenCircuitOpen(t *testing.T) {
	h := newProxyHarness(t)
	// Trip the forwarder's breaker before the request.
	breaker := &llmproxy.CircuitBreaker{FailureThreshold: 1, Cooldown: time.Hour}
	breaker.Allow()
	breaker.RecordFailure()
	h.handler.Forwarder = &llmproxy.Forwarder{Breaker: breaker}
	if err := h.leases.Put(handlerLease("lt-open")); err != nil {
		t.Fatalf("seed lease: %v", err)
	}
	rr := post(h.handler, "lt-open", messagesBody)
	if rr.Code != http.StatusServiceUnavailable || errorCode(t, rr) != "PROVIDER_UNAVAILABLE" {
		t.Errorf("status %d code %q, want 503 PROVIDER_UNAVAILABLE", rr.Code, errorCode(t, rr))
	}
}

func TestHandlerRejectsNonPost(t *testing.T) {
	h := newProxyHarness(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/messages", nil)
	rr := httptest.NewRecorder()
	h.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rr.Code)
	}
}

// fakeUsage is a UsageRecorder capturing the last recorded usage.
type fakeUsage struct {
	usage llmproxy.Usage
	calls int
}

func (f *fakeUsage) RecordUsage(_ credential.Lease, u llmproxy.Usage) {
	f.usage = u
	f.calls++
}

func TestHandlerRecordsAuthoritativeUsage(t *testing.T) {
	h := newProxyHarness(t)
	rec := &fakeUsage{}
	h.handler.Usage = rec
	if err := h.leases.Put(handlerLease("lt-usage")); err != nil {
		t.Fatalf("seed lease: %v", err)
	}
	if rr := post(h.handler, "lt-usage", messagesBody); rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	// The fake upstream replies with usage 5 in / 7 out.
	if rec.calls != 1 || rec.usage.InputTokens != 5 || rec.usage.OutputTokens != 7 {
		t.Errorf("recorded usage = %+v calls=%d, want 5/7 recorded once", rec.usage, rec.calls)
	}
}

func TestHandlerStreamsSSEResponse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(anthropicSSEStream))
	}))
	defer upstream.Close()

	leases := credleasestore.New()
	if err := leases.Put(handlerLease("lt-stream")); err != nil {
		t.Fatalf("seed lease: %v", err)
	}
	rec := &fakeUsage{}
	h := &llmproxy.Handler{
		Leases:      leases,
		Translator:  &llmproxy.AnthropicDirectTranslator{BaseURL: upstream.URL, DefaultAnthropicVersion: "2023-06-01"},
		Forwarder:   &llmproxy.Forwarder{Breaker: &llmproxy.CircuitBreaker{}},
		Credentials: fakeResolver{key: "sk-ant-real", ok: true},
		Usage:       rec,
	}

	rr := post(h, "lt-stream", `{"model":"claude-3-5-sonnet","messages":[{"role":"user","content":"hi"}],"stream":true}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	if rr.Body.String() != anthropicSSEStream {
		t.Errorf("relayed body is not the upstream SSE stream:\n got %q", rr.Body.String())
	}
	if rec.calls != 1 || rec.usage.InputTokens != 12 || rec.usage.OutputTokens != 34 {
		t.Errorf("streamed usage = %+v calls=%d, want 12/34 recorded once", rec.usage, rec.calls)
	}
}

func TestHandlerStreamingRejectsExpiredLeaseBeforeUpstream(t *testing.T) {
	h := newProxyHarness(t)
	lease := handlerLease("lt-stream-exp")
	lease.ExpiresAt = time.Now().Add(-time.Minute)
	if err := h.leases.Put(lease); err != nil {
		t.Fatalf("seed lease: %v", err)
	}
	// A streaming request on an expired lease is rejected with a normal
	// JSON error, not a half-opened event stream.
	rr := post(h.handler, "lt-stream-exp",
		`{"model":"claude-3-5-sonnet","messages":[{"role":"user","content":"hi"}],"stream":true}`)
	if rr.Code != http.StatusForbidden || errorCode(t, rr) != "LEASE_EXPIRED" {
		t.Errorf("status %d code %q, want 403 LEASE_EXPIRED", rr.Code, errorCode(t, rr))
	}
}
