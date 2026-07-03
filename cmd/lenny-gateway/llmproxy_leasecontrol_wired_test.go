// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/billing/usagestore"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credleasestore"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/llmproxy"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/leasecontrol"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionbudget"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// This test wires the full production §8.6 budget-exhaustion path end to end
// with the REAL leasecontrol.Service (no seam fake): llmproxy.Handler ->
// proxyUsageRecorder -> sessionbudget.Enforcer whose seam is
// leaseExtendSeam(leasecontrol.Service), exactly the S6 composition-root
// wiring. It closes the proposal 0023 chicken-and-egg gap: a proxied LLM call
// whose settlement exhausts the session budget triggers an in-process lease
// extension, and on an auto-mode grant the proxy delivers the already-computed
// response of that same call — with no second upstream call and no duplicate
// usage record. The pre-fix composition root wired a nil seam, so the same
// exhausting call would fail closed with 403 BUDGET_EXHAUSTED; this test would
// fail against that code.
//
// spec: §8.6 line 629; §11.2 line 44; proposal 0023 S6.

// TestLeaseControlWiredGrantDeliversHeldResponse proves the end-to-end
// transparent path: an auto-mode tree grants the in-process extension, so the
// exhausting non-streaming call's held 200 reaches the runtime and the upstream
// is called exactly once.
//
// spec: 8.6 (gateway LLM Proxy in-process budget-exhaustion trigger), 11.2 (mid-session budget enforcement)
// diagnosis: the §8.6 in-process extension seam is not wired from the sessionbudget enforcer to leasecontrol.Service — a proxy-mode budget exhaustion fails closed with BUDGET_EXHAUSTED instead of transparently delivering the held response after an auto-mode grant (proposal 0023 S6 wiring reverted or broken).
func TestLeaseControlWiredGrantDeliversHeldResponse_spec_8_6_line_629(t *testing.T) {
	var upstreamN int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&upstreamN, 1)
		w.WriteHeader(http.StatusOK)
		// 250 tokens on a 200 budget exhausts on the first call.
		_, _ = w.Write([]byte(`{"id":"msg_lc","usage":{"input_tokens":100,"output_tokens":150}}`))
	}))
	t.Cleanup(upstream.Close)

	sessions := memstore.New()
	if err := sessions.Create(context.Background(), memSession("s_lc", 200)); err != nil {
		t.Fatalf("sessions.Create: %v", err)
	}

	// A real auto-mode leasecontrol.Service with a registered tree keyed on the
	// session id (the root session), with headroom under its ceiling so the
	// auto-mode extension grants synchronously within the in-path wait.
	svc, _ := autoExtendService(t, "s_lc", leasecontrol.ApprovalModeAuto)

	term := &recordingTerminator{}
	enforcer := sessionbudget.New(term, nil, nil)
	enforcer.SetExtendOnExhaustion(leaseExtendSeam(svc))
	svc.SetReclaimer(enforcer)

	rec := newProxyUsageRecorder(usagestore.NewMemory(), sessions, nil, nil, nil, enforcer)

	leases := credleasestore.New()
	if err := leases.Put(proxyLease("cl_lc", "s_lc", "lt-lc")); err != nil {
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

	rr := postBW(h, "lt-lc")
	if rr.Code != http.StatusOK {
		t.Fatalf("a granted in-process extension must deliver the held 200; status=%d body=%s", rr.Code, rr.Body.String())
	}
	if got := atomic.LoadInt32(&upstreamN); got != 1 {
		t.Fatalf("the transparent path must issue exactly one upstream call, got %d", got)
	}
	if len(term.terminated) != 0 {
		t.Fatalf("a granted extension must not terminate the session, terminated=%v", term.terminated)
	}
	// The extension raised the session's budget, so its next request is
	// admitted by the pre-flight gate.
	if !enforcer.Allow("s_lc") {
		t.Fatalf("a granted-extension session must be admitted by the pre-flight gate")
	}
}

// TestLeaseControlWiredNoTreeFailsClosed proves the wired path fails closed
// when the extension cannot be resolved: with no tree registered for the
// session, leasecontrol.ExtendForBudget errors, the seam maps that to Terminal,
// and the exhausting non-streaming call returns 403 BUDGET_EXHAUSTED. This
// distinguishes the granted end-to-end path above from the fail-closed path, so
// the grant test is not trivially satisfied.
//
// spec: 8.6 (fail-closed extension outcome), 11.2 (terminate on terminal outcome)
// diagnosis: the §8.6 extension seam does not fail closed when the tree budget is unresolvable — an errored ExtendForBudget must map to Terminal (deny + terminate), not silently grant tokens or leak an over-budget session (proposal 0023 fail-closed posture).
func TestLeaseControlWiredNoTreeFailsClosed_spec_8_6_line_629(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"msg_lc","usage":{"input_tokens":100,"output_tokens":150}}`))
	}))
	t.Cleanup(upstream.Close)

	sessions := memstore.New()
	if err := sessions.Create(context.Background(), memSession("s_notree", 200)); err != nil {
		t.Fatalf("sessions.Create: %v", err)
	}

	// A service whose budget source has NO tree for s_notree: ExtendForBudget
	// cannot resolve the tenant/tree and fails closed.
	budgets := leasecontrol.NewMemoryBudgetSource()
	svc, err := leasecontrol.NewService(leasecontrol.Options{Budgets: budgets, Tenants: budgets})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	term := &recordingTerminator{}
	enforcer := sessionbudget.New(term, nil, nil)
	enforcer.SetExtendOnExhaustion(leaseExtendSeam(svc))
	svc.SetReclaimer(enforcer)

	rec := newProxyUsageRecorder(usagestore.NewMemory(), sessions, nil, nil, nil, enforcer)
	leases := credleasestore.New()
	if err := leases.Put(proxyLease("cl_nt", "s_notree", "lt-nt")); err != nil {
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

	rr := postBW(h, "lt-nt")
	if rr.Code != http.StatusForbidden || errorCodeBW(t, rr) != "BUDGET_EXHAUSTED" {
		t.Fatalf("an unresolvable extension must fail closed with 403 BUDGET_EXHAUSTED; status=%d code=%q", rr.Code, errorCodeBW(t, rr))
	}
	if len(term.terminated) != 1 || term.terminated[0] != "s_notree" {
		t.Fatalf("a terminal extension must terminate the session, terminated=%v", term.terminated)
	}
}

// memSession builds a running proxy-mode session with a token budget.
func memSession(id string, budget int64) sessionstore.Session {
	return sessionstore.Session{
		ID: id, TenantID: "acme", RuntimeRef: "claude-prod", State: session.StateRunning,
		DelegationLease: &sessionstore.DelegationLease{MaxTokenBudget: budget},
	}
}

// proxyLease builds a proxy-mode lease carrying token as its bearer.
func proxyLease(leaseID, sessionID, token string) credential.Lease {
	return credential.Lease{
		LeaseID:      leaseID,
		SessionID:    sessionID,
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
			LeaseToken:   token,
		},
	}
}
