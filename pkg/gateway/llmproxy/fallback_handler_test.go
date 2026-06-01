// SPDX-License-Identifier: MIT

package llmproxy_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credfallback"
	"github.com/lennylabs/lenny/pkg/gateway/credleasestore"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy"
)

// recordingRotator captures the §4.9 Fallback Flow replacement rotations
// the handler drives.
type recordingRotator struct {
	calls []rotateCall
}

type rotateCall struct {
	sessionID string
	nextPool  string
	trigger   credential.RotationTrigger
}

func (r *recordingRotator) Rotate(faulted credential.Lease, nextPool string, trigger credential.RotationTrigger) {
	r.calls = append(r.calls, rotateCall{faulted.SessionID, nextPool, trigger})
}

// recordingFallbackAudit captures the credential.fallback_exhausted
// events the handler emits.
type recordingFallbackAudit struct {
	events []llmproxy.FallbackExhaustedEvent
}

func (a *recordingFallbackAudit) FallbackExhausted(ev llmproxy.FallbackExhaustedEvent) {
	a.events = append(a.events, ev)
}

// recordingFallbackMetrics captures the §4.9 fallback counters.
type recordingFallbackMetrics struct {
	rotations []string
	exhausted []string
}

func (m *recordingFallbackMetrics) IncCredentialRotation(errorType string) {
	m.rotations = append(m.rotations, errorType)
}

func (m *recordingFallbackMetrics) IncCredentialFallbackExhausted(pool, provider, errorType string) {
	m.exhausted = append(m.exhausted, errorType)
}

// newRateLimitUpstream returns a fake upstream that always answers 429,
// the §4.9 RATE_LIMITED fault the translator folds into auth_failed.
func newRateLimitUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"rate_limit_error"}}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func fallbackLease(token string) credential.Lease {
	return credential.Lease{
		LeaseID:      "cl_fb1",
		SessionID:    "s_fb1",
		Provider:     credential.ProviderAnthropicDirect,
		Source:       credential.SourcePool,
		PoolID:       "claude-prod",
		CredentialID: "key-1",
		TenantID:     "acme",
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

const fbBody = `{"model":"claude-3-5-sonnet","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`

func newFallbackHandler(t *testing.T, ctl *credfallback.Controller, rot llmproxy.FallbackRotator, aud llmproxy.FallbackAuditSink, met llmproxy.FallbackMetrics) (*llmproxy.Handler, *credleasestore.Store) {
	t.Helper()
	upstream := newRateLimitUpstream(t)
	leases := credleasestore.New()
	h := &llmproxy.Handler{
		Leases:          leases,
		Translator:      &llmproxy.AnthropicDirectTranslator{BaseURL: upstream.URL, DefaultAnthropicVersion: "2023-06-01"},
		Forwarder:       &llmproxy.Forwarder{Breaker: &llmproxy.CircuitBreaker{}},
		Credentials:     fakeResolver{key: "sk-ant-real", ok: true},
		Fallback:        ctl,
		FallbackRotator: rot,
		FallbackAudit:   aud,
		FallbackMetrics: met,
	}
	return h, leases
}

// spec: §4.9 lines 1383-1411 — a rate-limit fault on a multi-pool chain
// rotates the lease to the next pool and surfaces the upstream error so
// the pod retries against the rotated credential.
func TestHandlerFaultRotatesToNextPool(t *testing.T) {
	ctl := credfallback.NewController(3, time.Hour)
	ctl.RegisterChain("s_fb1", credential.ProviderAnthropicDirect, []string{"claude-prod", "claude-backup"})
	rot := &recordingRotator{}
	met := &recordingFallbackMetrics{}
	h, leases := newFallbackHandler(t, ctl, rot, nil, met)
	if err := leases.Put(fallbackLease("lt-fb")); err != nil {
		t.Fatalf("seed lease: %v", err)
	}

	rr := post(h, "lt-fb", fbBody)

	// The pod sees the upstream auth-layer error, not a terminal one.
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 (PROVIDER_AUTH_FAILED); body=%s", rr.Code, rr.Body.String())
	}
	if code := errorCode(t, rr); code != "PROVIDER_AUTH_FAILED" {
		t.Errorf("code = %q, want PROVIDER_AUTH_FAILED", code)
	}
	if len(rot.calls) != 1 || rot.calls[0].nextPool != "claude-backup" {
		t.Fatalf("rotator calls = %+v, want one rotation to claude-backup", rot.calls)
	}
	if rot.calls[0].trigger != credential.TriggerFaultAuthExpired {
		t.Errorf("trigger = %q, want fault_auth_expired", rot.calls[0].trigger)
	}
	if len(met.rotations) != 1 {
		t.Errorf("rotation metric increments = %d, want 1", len(met.rotations))
	}
}

// spec: §4.9 lines 1393-1396 — once the chain is exhausted the session
// is terminated with CREDENTIAL_FALLBACK_EXHAUSTED, the audit event is
// emitted, and the exhaustion counter increments.
func TestHandlerFaultExhaustsChain(t *testing.T) {
	// Budget of 1 with a single fallback pool: the first fault rotates,
	// the second exhausts.
	ctl := credfallback.NewController(1, time.Hour)
	ctl.RegisterChain("s_fb1", credential.ProviderAnthropicDirect, []string{"claude-prod", "claude-backup"})
	rot := &recordingRotator{}
	aud := &recordingFallbackAudit{}
	met := &recordingFallbackMetrics{}
	h, leases := newFallbackHandler(t, ctl, rot, aud, met)
	if err := leases.Put(fallbackLease("lt-fb")); err != nil {
		t.Fatalf("seed lease: %v", err)
	}

	// First fault: rotates to backup, pod sees the upstream error.
	if rr := post(h, "lt-fb", fbBody); rr.Code != http.StatusBadGateway {
		t.Fatalf("first fault status = %d, want 502", rr.Code)
	}
	// Second fault: budget of 1 exceeded -> exhausted, terminal error.
	rr := post(h, "lt-fb", fbBody)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("second fault status = %d, want 403 (terminal); body=%s", rr.Code, rr.Body.String())
	}
	if code := errorCode(t, rr); code != llmproxy.CodeFallbackExhausted {
		t.Errorf("code = %q, want %s", code, llmproxy.CodeFallbackExhausted)
	}
	if len(aud.events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(aud.events))
	}
	ev := aud.events[0]
	if ev.SessionID != "s_fb1" || ev.TenantID != "acme" {
		t.Errorf("audit event session/tenant = %q/%q, want s_fb1/acme", ev.SessionID, ev.TenantID)
	}
	if ev.RotationCount != 2 {
		t.Errorf("audit rotation_count = %d, want 2", ev.RotationCount)
	}
	if len(ev.ChainAttempted) != 2 {
		t.Errorf("audit fallback_chain_attempted = %v, want both pools", ev.ChainAttempted)
	}
	if len(met.exhausted) != 1 {
		t.Errorf("exhausted metric increments = %d, want 1", len(met.exhausted))
	}
}

// A nil Fallback controller leaves the proxy on its pre-fallback path:
// the upstream error is surfaced with no rotation and no panic.
func TestHandlerNilFallbackIsInert(t *testing.T) {
	h, leases := newFallbackHandler(t, nil, nil, nil, nil)
	if err := leases.Put(fallbackLease("lt-fb")); err != nil {
		t.Fatalf("seed lease: %v", err)
	}
	rr := post(h, "lt-fb", fbBody)
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rr.Code)
	}
}
