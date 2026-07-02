// SPDX-License-Identifier: MIT

package llmproxy_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credleasestore"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/llmproxy"
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
		IssuedAt:     time.Now(),
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
	handler   *llmproxy.Handler
	leases    *credleasestore.Store
	gotKey    *string
	upstream  *httptest.Server
	upstreamN *int32
}

// newProxyHarness builds a handler wired to a fake upstream Anthropic
// server. The server records the x-api-key it receives, counts each
// forwarded request, and replies 200 with a usage-bearing body.
func newProxyHarness(t *testing.T) *proxyHarness {
	t.Helper()
	gotKey := new(string)
	var upstreamN int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&upstreamN, 1)
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
	return &proxyHarness{handler: h, leases: leases, gotKey: gotKey, upstream: upstream, upstreamN: &upstreamN}
}

// forwards returns the number of upstream requests the fake Anthropic
// server has served, so a test can assert exactly one Forward (no second
// upstream call on the §8.6 transparent path).
func (h *proxyHarness) forwards() int32 { return atomic.LoadInt32(h.upstreamN) }

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

// denyGate is a BudgetGate that denies a fixed session id.
type denyGate struct{ deny string }

func (g denyGate) Allow(sessionID string) bool { return sessionID != g.deny }

// spec: §8.10 line 1108 / §11.2 line 44 — a request for a session whose
// token budget is exhausted is rejected with BUDGET_EXHAUSTED before any
// upstream call (the §11.2 mid-session enforcement pre-flight gate).
func TestHandlerRejectsBudgetExhaustedSession_spec_8_10(t *testing.T) {
	h := newProxyHarness(t)
	if err := h.leases.Put(handlerLease("lt-budget")); err != nil {
		t.Fatalf("seed lease: %v", err)
	}
	// handlerLease binds SessionID "s_h1"; the gate denies it.
	h.handler.BudgetGate = denyGate{deny: "s_h1"}

	rr := post(h.handler, "lt-budget", messagesBody)
	if rr.Code != http.StatusForbidden || errorCode(t, rr) != "BUDGET_EXHAUSTED" {
		t.Fatalf("status %d code %q, want 403 BUDGET_EXHAUSTED", rr.Code, errorCode(t, rr))
	}
	// The gate fires before any upstream call: the fake Anthropic server
	// must never see the real key for this request.
	if *h.gotKey != "" {
		t.Errorf("upstream was called despite the budget gate: x-api-key=%q", *h.gotKey)
	}
}

// A gate that allows the session does not interfere with a normal proxied
// request.
func TestHandlerAllowsUnderBudgetSession_spec_11_2(t *testing.T) {
	h := newProxyHarness(t)
	if err := h.leases.Put(handlerLease("lt-ok")); err != nil {
		t.Fatalf("seed lease: %v", err)
	}
	h.handler.BudgetGate = denyGate{deny: "some-other-session"}
	rr := post(h.handler, "lt-ok", messagesBody)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
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

// fakeUsage is a UsageRecorder capturing the last recorded usage. It stands
// in for the cmd/lenny-gateway proxyUsageRecorder: it detects exhaustion and
// surfaces the §8.6 extension Outcome the real record path resolves inside the
// enforcer, so the handler drives its write-path branch off the returned
// Outcome without any second extension dispatch (proposal 0023 S4).
//
// exhaustOnCall is the 1-based call index on which RecordUsage reports
// exhausted (0 never exhausts). outcomes supplies the per-exhaustion Outcome
// the record path resolves, consumed in order; when it runs out the last
// value is reused, and OutcomeTerminal is the default. onExhaust, when set,
// runs the out-of-band episode effect (raise/terminate) a real leasecontrol
// fan-out would apply through the SessionReclaimer, so a test can model a
// deferred grant recovering a Pending session by flipping the pre-flight gate.
type fakeUsage struct {
	mu            sync.Mutex
	usage         llmproxy.Usage
	calls         int
	exhaustCalls  int // number of exhaustion events observed
	exhaustOnCall int
	outcomes      []llmproxy.Outcome
	onExhaust     func(outcome llmproxy.Outcome)
}

func (f *fakeUsage) RecordUsage(_ context.Context, lease credential.Lease, u llmproxy.Usage) (bool, llmproxy.Outcome) {
	f.mu.Lock()
	f.usage = u
	f.calls++
	exhausted := f.exhaustOnCall != 0 && f.calls == f.exhaustOnCall
	if !exhausted {
		f.mu.Unlock()
		return false, llmproxy.OutcomeGranted
	}
	out := llmproxy.OutcomeTerminal
	if len(f.outcomes) > 0 {
		out = f.outcomes[0]
		if len(f.outcomes) > 1 {
			f.outcomes = f.outcomes[1:]
		}
	}
	f.exhaustCalls++
	hook := f.onExhaust
	_ = lease
	f.mu.Unlock()
	if hook != nil {
		hook(out)
	}
	return true, out
}

func (f *fakeUsage) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeUsage) exhaustionCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.exhaustCalls
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
	if rec.callCount() != 1 || rec.usage.InputTokens != 5 || rec.usage.OutputTokens != 7 {
		t.Errorf("recorded usage = %+v calls=%d, want 5/7 recorded once", rec.usage, rec.callCount())
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
	if rec.callCount() != 1 || rec.usage.InputTokens != 12 || rec.usage.OutputTokens != 34 {
		t.Errorf("streamed usage = %+v calls=%d, want 12/34 recorded once", rec.usage, rec.callCount())
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

// spec: §8.6 line 629 (the gateway LLM Proxy drives the budget-exhaustion
// lease-extension trigger in-process, delivering the exhausting call's
// already-computed response transparently), §11.2 line 44 (budget
// enforcement). These tests exercise the proxy's write-path branch on the
// tri-state Outcome the record path (Usage.RecordUsage) surfaces from its
// single §8.6 extension dispatch. The handler never issues its own extension
// call; it consumes the Outcome the enforcer already resolved, so the
// extension is attempted at most once per exhaustion event (proposal 0023 S4).

// mutableGate is a BudgetGate whose denied session set a test (or a
// recorder's out-of-band effect) can flip, modelling the enforcer's
// deny-next-request state the pre-flight gate reads.
type mutableGate struct {
	mu     sync.Mutex
	denied map[string]bool
}

func newMutableGate() *mutableGate { return &mutableGate{denied: map[string]bool{}} }

func (g *mutableGate) Allow(sessionID string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return !g.denied[sessionID]
}

func (g *mutableGate) deny(sessionID string) {
	g.mu.Lock()
	g.denied[sessionID] = true
	g.mu.Unlock()
}

func (g *mutableGate) allow(sessionID string) {
	g.mu.Lock()
	delete(g.denied, sessionID)
	g.mu.Unlock()
}

// TestOutcomeString pins the human-readable names of the proxy-local §8.6
// extension outcomes so a log or diagnosis line names the state correctly.
func TestOutcomeString_spec_8_6_line_629(t *testing.T) {
	cases := map[llmproxy.Outcome]string{
		llmproxy.OutcomeGranted:  "GRANTED",
		llmproxy.OutcomePending:  "PENDING",
		llmproxy.OutcomeTerminal: "TERMINAL",
		llmproxy.Outcome(99):     "UNKNOWN",
	}
	for out, want := range cases {
		if got := out.String(); got != want {
			t.Errorf("Outcome(%d).String() = %q, want %q", int(out), got, want)
		}
	}
}

// diagnosis: a broken non-streaming Granted branch means a §8.6 extension the
// record path granted within the in-path wait still fails the exhausting
// request or re-issues the upstream call, breaking the runtime-transparency
// contract or double-billing the provider. The handler must deliver the held
// 200 off the record path's returned OutcomeGranted with no second dispatch.
func TestHandlerNonStreamingGrantedDeliversHeldResponse_spec_8_6_line_629(t *testing.T) {
	h := newProxyHarness(t)
	if err := h.leases.Put(handlerLease("lt-g")); err != nil {
		t.Fatalf("seed lease: %v", err)
	}
	// The record path detects exhaustion and surfaces OutcomeGranted (the
	// enforcer's single-dispatch resolution). The handler branches its write
	// path on that returned outcome.
	rec := &fakeUsage{exhaustOnCall: 1, outcomes: []llmproxy.Outcome{llmproxy.OutcomeGranted}}
	h.handler.Usage = rec

	rr := post(h.handler, "lt-g", messagesBody)
	if rr.Code != http.StatusOK {
		t.Fatalf("granted extension must deliver the held 200; status=%d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"id":"msg_1"`) {
		t.Errorf("body = %q, want the held upstream response", rr.Body.String())
	}
	// The held response is the already-computed body: exactly one upstream
	// call and one usage record, no re-issue and no second extension dispatch
	// on the grant. The single record call IS the single §8.6 dispatch.
	if h.forwards() != 1 {
		t.Errorf("forwards = %d, want exactly one upstream call on the transparent path", h.forwards())
	}
	if rec.callCount() != 1 {
		t.Errorf("usage recorded %d times, want exactly one (no duplicate on grant)", rec.callCount())
	}
	if rec.exhaustionCount() != 1 {
		t.Errorf("exhaustion resolved %d times, want exactly one dispatch per exhaustion event", rec.exhaustionCount())
	}
}

// diagnosis: a broken non-streaming Terminal branch means a session whose
// record-path extension hit CEILING_REACHED/REJECTED (or errored) still
// receives its held 200 instead of a fail-closed BUDGET_EXHAUSTED, bypassing
// the ceiling.
func TestHandlerNonStreamingTerminalFailsClosed_spec_8_6_line_712(t *testing.T) {
	h := newProxyHarness(t)
	if err := h.leases.Put(handlerLease("lt-t")); err != nil {
		t.Fatalf("seed lease: %v", err)
	}
	rec := &fakeUsage{exhaustOnCall: 1, outcomes: []llmproxy.Outcome{llmproxy.OutcomeTerminal}}
	h.handler.Usage = rec

	rr := post(h.handler, "lt-t", messagesBody)
	if rr.Code != http.StatusForbidden || errorCode(t, rr) != "BUDGET_EXHAUSTED" {
		t.Fatalf("terminal extension must fail closed; status=%d code=%q", rr.Code, errorCode(t, rr))
	}
	// The exhausting call already completed and recorded its usage exactly
	// once; the terminal branch does not re-issue it or re-dispatch.
	if h.forwards() != 1 || rec.callCount() != 1 {
		t.Errorf("forwards=%d records=%d, want 1/1 (no re-issue on terminal)", h.forwards(), rec.callCount())
	}
}

// diagnosis: a record path that reports exhausted with a non-granted outcome
// (the nil-seam / non-extendable posture surfaces OutcomeTerminal) must fail
// the exhausting request closed, defeating the §11.2 terminate-immediately
// posture only if it wrongly delivered the held 200.
func TestHandlerNonStreamingNonGrantedFailsClosed_spec_11_2_line_44(t *testing.T) {
	h := newProxyHarness(t)
	if err := h.leases.Put(handlerLease("lt-ne")); err != nil {
		t.Fatalf("seed lease: %v", err)
	}
	// The record path reports exhausted with OutcomeTerminal (the enforcer's
	// nil-seam resolution): the proxy fails closed.
	h.handler.Usage = &fakeUsage{exhaustOnCall: 1, outcomes: []llmproxy.Outcome{llmproxy.OutcomeTerminal}}
	rr := post(h.handler, "lt-ne", messagesBody)
	if rr.Code != http.StatusForbidden || errorCode(t, rr) != "BUDGET_EXHAUSTED" {
		t.Fatalf("a non-granted exhaustion must fail closed; status=%d code=%q", rr.Code, errorCode(t, rr))
	}
}

// diagnosis: a broken non-streaming Pending branch means an elicitation-mode
// extension still pending at the in-path deadline either wrongly delivers the
// held 200 (bypassing the ceiling) or terminates the session that should
// recover on the deferred grant.
func TestHandlerNonStreamingPendingDeniesThenRecovers_spec_8_6_line_629(t *testing.T) {
	h := newProxyHarness(t)
	if err := h.leases.Put(handlerLease("lt-p")); err != nil {
		t.Fatalf("seed lease: %v", err)
	}
	gate := newMutableGate()
	// The record path resolves Pending and (as the real enforcer does) sets the
	// session's deny-next-request state the pre-flight gate reads; model that
	// side-effect via the gate so subsequent requests are denied until the
	// deferred grant.
	rec := &fakeUsage{
		exhaustOnCall: 1,
		outcomes:      []llmproxy.Outcome{llmproxy.OutcomePending},
		onExhaust: func(out llmproxy.Outcome) {
			if out == llmproxy.OutcomePending {
				gate.deny("s_h1")
			}
		},
	}
	h.handler.Usage = rec
	h.handler.BudgetGate = gate

	// The exhausting non-streaming request's response is not yet written, so
	// Pending returns BUDGET_EXHAUSTED for the current request.
	rr := post(h.handler, "lt-p", messagesBody)
	if rr.Code != http.StatusForbidden || errorCode(t, rr) != "BUDGET_EXHAUSTED" {
		t.Fatalf("non-streaming Pending must deny the current request; status=%d code=%q", rr.Code, errorCode(t, rr))
	}
	// Every subsequent request is denied by the pre-flight gate while the
	// episode is unresolved; the pre-flight rejection never reaches the record
	// path, so no new extension is dispatched.
	before := rec.exhaustionCount()
	rr = post(h.handler, "lt-p", messagesBody)
	if rr.Code != http.StatusForbidden || errorCode(t, rr) != "BUDGET_EXHAUSTED" {
		t.Fatalf("subsequent request must be denied while pending; status=%d code=%q", rr.Code, errorCode(t, rr))
	}
	if rec.exhaustionCount() != before {
		t.Errorf("pre-flight-denied request must not reach the record path: dispatches %d -> %d", before, rec.exhaustionCount())
	}
	// The out-of-band episode fan-out later applies a deferred grant, clearing
	// the deny state; the session recovers to 200.
	gate.allow("s_h1")
	if rr := post(h.handler, "lt-p", messagesBody); rr.Code != http.StatusOK {
		t.Fatalf("session must recover to 200 after the deferred grant; status=%d body=%s", rr.Code, rr.Body.String())
	}
}

// diagnosis: a broken second-exhaustion path means a session that
// legitimately extends more than once is abandoned before its ceiling — the
// second distinct exhaustion event must resolve a fresh outcome, continuing
// on a grant and failing closed only on a terminal outcome.
func TestHandlerSecondExhaustionResolvesFreshOutcome_spec_8_6_line_629(t *testing.T) {
	h := newProxyHarness(t)
	if err := h.leases.Put(handlerLease("lt-2x")); err != nil {
		t.Fatalf("seed lease: %v", err)
	}
	// The recorder exhausts on call 1 and again later, so two requests each
	// cross a fresh exhaustion boundary; the record path grants the first and
	// hits CEILING_REACHED (terminal) on the second.
	rec := &fakeUsage{exhaustOnCall: 1, outcomes: []llmproxy.Outcome{llmproxy.OutcomeGranted, llmproxy.OutcomeTerminal}}
	h.handler.Usage = rec

	// First exhaustion → granted → held 200.
	if rr := post(h.handler, "lt-2x", messagesBody); rr.Code != http.StatusOK {
		t.Fatalf("first exhaustion (granted) must deliver 200; status=%d", rr.Code)
	}
	// A later request re-exhausts the raised-and-consumed budget: a fresh
	// exhaustion event resolves a fresh outcome, which is terminal here.
	rec.exhaustOnCall = rec.callCount() + 1
	if rr := post(h.handler, "lt-2x", messagesBody); rr.Code != http.StatusForbidden || errorCode(t, rr) != "BUDGET_EXHAUSTED" {
		t.Fatalf("second exhaustion (ceiling reached) must fail closed; status=%d code=%q", rr.Code, errorCode(t, rr))
	}
	if rec.exhaustionCount() != 2 {
		t.Errorf("exhaustion resolved %d times, want two (one per distinct exhaustion)", rec.exhaustionCount())
	}
}

// diagnosis: a broken streaming Granted branch means the committed 200/SSE
// response is corrupted or re-issued, or the session is wrongly terminated
// after a granted streaming exhaustion; the committed stream must stand and
// the session stay alive for its next request.
func TestHandlerStreamingGrantedKeepsSessionAlive_spec_8_6_line_629(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(anthropicSSEStream))
	}))
	defer upstream.Close()

	leases := credleasestore.New()
	if err := leases.Put(handlerLease("lt-sg")); err != nil {
		t.Fatalf("seed lease: %v", err)
	}
	gate := newMutableGate()
	rec := &fakeUsage{exhaustOnCall: 1, outcomes: []llmproxy.Outcome{llmproxy.OutcomeGranted}}
	h := &llmproxy.Handler{
		Leases:      leases,
		Translator:  &llmproxy.AnthropicDirectTranslator{BaseURL: upstream.URL, DefaultAnthropicVersion: "2023-06-01"},
		Forwarder:   &llmproxy.Forwarder{Breaker: &llmproxy.CircuitBreaker{}},
		Credentials: fakeResolver{key: "sk-ant-real", ok: true},
		Usage:       rec,
		BudgetGate:  gate,
	}
	streamBody := `{"model":"claude-3-5-sonnet","messages":[{"role":"user","content":"hi"}],"stream":true}`
	rr := post(h, "lt-sg", streamBody)
	// The committed 200/SSE response stands; the granted extension does not
	// re-issue or corrupt it.
	if rr.Code != http.StatusOK {
		t.Fatalf("streaming granted must leave the committed 200 standing; status=%d", rr.Code)
	}
	if rr.Body.String() != anthropicSSEStream {
		t.Errorf("committed stream must not be re-issued or altered on grant")
	}
	if rec.exhaustionCount() != 1 {
		t.Errorf("exhaustion resolved %d times, want one on streaming exhaustion", rec.exhaustionCount())
	}
	// The session stays alive: the pre-flight gate still admits its next
	// request (the grant applies to it).
	if !gate.Allow("s_h1") {
		t.Errorf("granted streaming exhaustion must keep the session alive for its next request")
	}
}

// diagnosis: a broken streaming Pending branch means a committed 200/SSE
// response is wrongly turned into an error, or the session's subsequent
// requests are not denied while the elicitation resolves out-of-band.
func TestHandlerStreamingPendingCommittedStreamStands_spec_8_6_line_629(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(anthropicSSEStream))
	}))
	defer upstream.Close()

	leases := credleasestore.New()
	if err := leases.Put(handlerLease("lt-sp")); err != nil {
		t.Fatalf("seed lease: %v", err)
	}
	gate := newMutableGate()
	rec := &fakeUsage{
		exhaustOnCall: 1,
		outcomes:      []llmproxy.Outcome{llmproxy.OutcomePending},
		onExhaust: func(out llmproxy.Outcome) {
			if out == llmproxy.OutcomePending {
				gate.deny("s_h1")
			}
		},
	}
	h := &llmproxy.Handler{
		Leases:      leases,
		Translator:  &llmproxy.AnthropicDirectTranslator{BaseURL: upstream.URL, DefaultAnthropicVersion: "2023-06-01"},
		Forwarder:   &llmproxy.Forwarder{Breaker: &llmproxy.CircuitBreaker{}},
		Credentials: fakeResolver{key: "sk-ant-real", ok: true},
		Usage:       rec,
		BudgetGate:  gate,
	}
	streamBody := `{"model":"claude-3-5-sonnet","messages":[{"role":"user","content":"hi"}],"stream":true}`
	// The 200/SSE response is committed before usage settles, so a Pending
	// outcome cannot deny the current streaming request: it stands.
	rr := post(h, "lt-sp", streamBody)
	if rr.Code != http.StatusOK || rr.Body.String() != anthropicSSEStream {
		t.Fatalf("streaming Pending must leave the committed 200/SSE standing; status=%d", rr.Code)
	}
	// The session's subsequent requests are denied by the pre-flight gate
	// while the episode is unresolved (denied before any upstream call, so
	// the streaming-vs-non-streaming body does not matter here).
	if rr := post(h, "lt-sp", streamBody); rr.Code != http.StatusForbidden || errorCode(t, rr) != "BUDGET_EXHAUSTED" {
		t.Fatalf("streaming Pending must deny subsequent requests; status=%d code=%q", rr.Code, errorCode(t, rr))
	}
	// The deferred grant clears the deny state and the session recovers. This
	// upstream only serves SSE, so the recovery request is a streaming one.
	gate.allow("s_h1")
	if rr := post(h, "lt-sp", streamBody); rr.Code != http.StatusOK {
		t.Fatalf("session must recover after the deferred grant; status=%d", rr.Code)
	}
}
