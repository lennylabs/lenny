// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/billing/usagestore"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credleasestore"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/llmproxy"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionbudget"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// The §8.6 budget-exhaustion extension is dispatched exactly once per
// exhaustion event, inside the sessionbudget enforcer's injected seam on the
// record path. The proxy Handler consumes the resolved Outcome the recorder
// surfaces through RecordUsage and drives its write branch off it, never
// issuing its own second ExtendForBudget call. These tests wire the full
// production stack — enforcer(seam) -> proxyUsageRecorder -> llmproxy.Handler
// with BOTH the enforcer seam and the proxy fed from the same request — so a
// regression that re-introduces a handler-side second dispatch fails here
// (the seam would fire twice per exhaustion). The pre-fix handler dispatched
// its own ExtendForBudget after the record path already dispatched one.
//
// spec: §8.6 line 629; §11.2 line 44; proposal 0023 S3/S4/S6.

// countingSeam is a sessionbudget.ExtendOnExhaustion that records how many
// times the enforcer dispatches the §8.6 extension and returns a scripted
// outcome. On Granted it raises the session's budget (as the production
// leasecontrol seam does) so the record boundary stays cleared.
type countingSeam struct {
	calls   atomic.Int32
	outcome sessionbudget.Outcome
	e       *sessionbudget.Enforcer
	delta   int64
}

func (s *countingSeam) fn(_, _ context.Context, _, sessionID string, _, _ int64) sessionbudget.Outcome {
	s.calls.Add(1)
	if s.outcome == sessionbudget.Granted && s.e != nil {
		s.e.RaiseBudget(sessionID, s.delta)
	}
	return s.outcome
}

// bothWiredStack builds the production wiring: a sessionbudget.Enforcer whose
// seam is the counting seam, a proxyUsageRecorder fed by that enforcer, and an
// llmproxy.Handler whose Usage is that recorder and whose BudgetGate is that
// same enforcer. A single request through the handler therefore drives exactly
// one extension dispatch through the enforcer seam.
func bothWiredStack(t *testing.T, seamOutcome sessionbudget.Outcome, budget int64) (*llmproxy.Handler, *countingSeam, *httptest.Server, *int32) {
	t.Helper()
	var upstreamN int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&upstreamN, 1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"msg_1","usage":{"input_tokens":100,"output_tokens":150}}`))
	}))
	t.Cleanup(upstream.Close)

	sessions := memstore.New()
	if err := sessions.Create(context.Background(), sessionstore.Session{
		ID: "s_bw", TenantID: "acme", RuntimeRef: "claude-prod", State: session.StateRunning,
		DelegationLease: &sessionstore.DelegationLease{MaxTokenBudget: budget},
	}); err != nil {
		t.Fatalf("sessions.Create: %v", err)
	}

	seam := &countingSeam{outcome: seamOutcome, delta: 1_000_000}
	enforcer := sessionbudget.New(nopBudgetTerminator{}, seam.fn, nil)
	seam.e = enforcer

	rec := newProxyUsageRecorder(usagestore.NewMemory(), sessions, nil, nil, nil, enforcer)

	leases := credleasestore.New()
	if err := leases.Put(credential.Lease{
		LeaseID:      "cl_bw",
		SessionID:    "s_bw",
		TenantID:     "acme",
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
			LeaseToken:   "lt-bw",
		},
	}); err != nil {
		t.Fatalf("seed lease: %v", err)
	}

	h := &llmproxy.Handler{
		Leases:      leases,
		Translator:  &llmproxy.AnthropicDirectTranslator{BaseURL: upstream.URL, DefaultAnthropicVersion: "2023-06-01"},
		Forwarder:   &llmproxy.Forwarder{Breaker: &llmproxy.CircuitBreaker{}},
		Credentials: bothWiredKey{},
		Usage:       rec,
		BudgetGate:  enforcer,
	}
	return h, seam, upstream, &upstreamN
}

// TestBothWiredGrantedDispatchesOnce proves the double-dispatch regression is
// gone: with the enforcer seam AND the proxy handler both fed from the same
// request, a granted exhaustion dispatches the §8.6 extension exactly once and
// the handler delivers the held 200. The pre-fix handler made its own second
// ExtendForBudget call, so the seam would have fired twice.
func TestBothWiredGrantedDispatchesOnce_spec_8_6_line_629(t *testing.T) {
	// Budget 200; the fake upstream reports 250 tokens, so the first request
	// exhausts the budget and consults the seam once.
	h, seam, _, upstreamN := bothWiredStack(t, sessionbudget.Granted, 200)

	rr := postBW(h, "lt-bw")
	if rr.Code != http.StatusOK {
		t.Fatalf("granted exhaustion must deliver the held 200; status=%d body=%s", rr.Code, rr.Body.String())
	}
	if got := seam.calls.Load(); got != 1 {
		t.Fatalf("the §8.6 extension must be dispatched exactly once per exhaustion, got %d dispatches", got)
	}
	if got := atomic.LoadInt32(upstreamN); got != 1 {
		t.Errorf("upstream called %d times, want exactly one (no re-issue on the transparent path)", got)
	}
}

// TestBothWiredTerminalDispatchesOnceAndFailsClosed proves a terminal seam
// resolution dispatches once and the handler fails the exhausting request
// closed (403 BUDGET_EXHAUSTED), driven by the record path's returned Outcome
// rather than a second handler-side dispatch.
func TestBothWiredTerminalDispatchesOnceAndFailsClosed_spec_8_6_line_712(t *testing.T) {
	h, seam, _, _ := bothWiredStack(t, sessionbudget.Terminal, 200)

	rr := postBW(h, "lt-bw")
	if rr.Code != http.StatusForbidden || errorCodeBW(t, rr) != "BUDGET_EXHAUSTED" {
		t.Fatalf("terminal exhaustion must fail closed; status=%d code=%q", rr.Code, errorCodeBW(t, rr))
	}
	if got := seam.calls.Load(); got != 1 {
		t.Fatalf("the §8.6 extension must be dispatched exactly once, got %d dispatches", got)
	}
	// The next request is denied by the pre-flight gate (the enforcer set the
	// deny flag on the terminal outcome) without re-consulting the seam.
	rr = postBW(h, "lt-bw")
	if rr.Code != http.StatusForbidden || errorCodeBW(t, rr) != "BUDGET_EXHAUSTED" {
		t.Fatalf("terminated session's next request must be pre-flight denied; status=%d", rr.Code)
	}
	if got := seam.calls.Load(); got != 1 {
		t.Fatalf("a pre-flight-denied request must not re-dispatch the extension, got %d dispatches", got)
	}
}

// postBW issues a proxy request carrying the both-wired lease token.
func postBW(h *llmproxy.Handler, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/llm-proxy/v1/messages",
		strings.NewReader(`{"model":"claude-3-5-sonnet","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("x-api-key", token)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func errorCodeBW(t *testing.T, rr *httptest.ResponseRecorder) string {
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

// bothWiredKey is a CredentialResolver returning a fixed upstream key.
type bothWiredKey struct{}

func (bothWiredKey) UpstreamCredential(credential.Lease) (string, bool) {
	return "sk-ant-real-upstream-key", true
}

// nopBudgetTerminator is a sessionbudget.Terminator that does nothing; the
// both-wired tests assert the deny flag through the enforcer's pre-flight gate
// rather than the terminal pipeline.
type nopBudgetTerminator struct{}

func (nopBudgetTerminator) TerminateSession(_ /*sessionID*/, _ /*reason*/ string) {}
