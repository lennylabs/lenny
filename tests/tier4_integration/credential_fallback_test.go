// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test: the §4.9 credentialPolicy Fallback Flow end
// to end. It drives a live agent-pod LLM request through the reverse
// proxy to a stubbed upstream provider that answers 429 (the
// RATE_LIMITED fault), then walks the per-provider fallback chain across
// the production credential-assignment service: each fault records the
// faulted pool's cooldown, mints a replacement lease from the next pool
// in fallback.order, and increments the session's shared rotation
// counter, until the counter exceeds maxRotationsPerSession and the
// session terminates with CREDENTIAL_FALLBACK_EXHAUSTED and the
// credential.fallback_exhausted audit event.

package tier4_integration_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credcache"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credfallback"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credleasestore"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/llmproxy"
	"github.com/lennylabs/lenny/tests/testinfra/stubs/llmprovider"
)

// fallbackFixture is the §4.9 Fallback Flow wired against production
// components: the credential-assignment service that mints proxy leases,
// the lease store and upstream-credential cache the proxy reads, the
// credfallback.Controller that holds the per-session rotation budget and
// per-pool cooldown, and the llmproxy.Handler served at
// POST /v1/messages. The upstream stub answers 429 on every call.
type fallbackFixture struct {
	handler   *llmproxy.Handler
	assign    *credassign.Service
	fallback  *credfallback.Controller
	rotator   *mintingRotator
	audit     *recordingFallbackAudit
	metrics   *recordingFallbackMetrics
	terminate *recordingTerminator
	server    *httptest.Server
}

const (
	fbSession  = "s-fallback-1"
	fbTenant   = "acme"
	fbProvider = credential.ProviderAnthropicDirect
	// fbBody is a minimal Anthropic Messages request; the stub 429s
	// before the body matters, so any valid request drives the fault.
	fbRequestBody = `{"model":"claude-3-5-sonnet","max_tokens":16,"messages":[{"role":"user","content":"ping"}]}`
)

// mintingRotator is the §4.9 Fallback Flow step-5 replacement mint. It
// mirrors the gateway's proxyFallbackRotator: it leases from the chain's
// next pool through the production credential-assignment service (which
// writes the replacement lease into the shared lease store and caches
// its upstream credential), and records the fresh lease token so the
// test can re-present it as the pod would after RotateCredentials.
type mintingRotator struct {
	assign *credassign.Service
	calls  []rotateRecord
}

type rotateRecord struct {
	faultedPool string
	nextPool    string
	trigger     credential.RotationTrigger
	newToken    string
}

func (r *mintingRotator) Rotate(faulted credential.Lease, nextPool string, trigger credential.RotationTrigger) {
	lease, err := r.assign.Assign(nextPool, faulted.SessionID, "", faulted.TenantID)
	rec := rotateRecord{faultedPool: faulted.PoolID, nextPool: nextPool, trigger: trigger}
	if err == nil && lease.Proxy != nil {
		rec.newToken = lease.Proxy.LeaseToken
	}
	r.calls = append(r.calls, rec)
}

// recordingFallbackAudit captures the §4.9.2 credential.fallback_exhausted
// events the handler emits.
type recordingFallbackAudit struct {
	events []llmproxy.FallbackExhaustedEvent
}

func (a *recordingFallbackAudit) FallbackExhausted(ev llmproxy.FallbackExhaustedEvent) {
	a.events = append(a.events, ev)
}

// recordingFallbackMetrics captures the §16.1 fallback counters.
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

// recordingTerminator captures the §4.9 Fallback Flow step-3 session
// termination.
type recordingTerminator struct {
	sessions []string
	codes    []string
}

func (t *recordingTerminator) TerminateSession(sessionID, code string) {
	t.sessions = append(t.sessions, sessionID)
	t.codes = append(t.codes, code)
}

// startFallbackFixture wires the fallback flow with the given rotation
// budget and cooldown, registers pools for every name in order (each a
// proxy-mode anthropic_direct pool with a distinct upstream key that
// leaks to no pod), and installs order as the session's fallback chain.
// The upstream stub answers 429 on every request.
func startFallbackFixture(t *testing.T, maxRotations int, cooldown time.Duration, order []string) *fallbackFixture {
	t.Helper()

	upstream := llmprovider.New(t)
	upstream.SetResponseOverride(func(llmprovider.Request) (int, string, map[string]string) {
		return http.StatusTooManyRequests, `{"type":"error","error":{"type":"rate_limit_error"}}`, nil
	})

	leases := credleasestore.New()
	creds := credcache.New()
	assign := credassign.New(leases, creds)

	for _, pool := range order {
		assign.RegisterPool(credassign.Pool{
			Name:         pool,
			Provider:     fbProvider,
			DeliveryMode: credential.DeliveryProxy,
			Strategy:     credential.StrategyLeastLoaded,
			ProxyURL:     "https://lenny-llm-proxy.internal/llm-proxy",
			ProxyDialect: string(credential.ProxyDialectAnthropic),
			Credentials: []credassign.PoolCredential{
				{ID: "cred-" + pool, APIKey: "sk-upstream-" + pool + "-secret", Healthy: true},
			},
		})
	}

	fb := credfallback.NewController(maxRotations, cooldown)
	fb.RegisterChain(fbSession, fbProvider, order)

	rotator := &mintingRotator{assign: assign}
	audit := &recordingFallbackAudit{}
	metrics := &recordingFallbackMetrics{}
	terminate := &recordingTerminator{}

	registry := llmproxy.NewTranslatorRegistry(
		&llmproxy.AnthropicDirectTranslator{
			BaseURL:                 upstream.URL(),
			DefaultAnthropicVersion: "2023-06-01",
		},
	)
	handler := &llmproxy.Handler{
		Leases:             leases,
		Translators:        registry,
		Forwarder:          &llmproxy.Forwarder{Breaker: &llmproxy.CircuitBreaker{}},
		Credentials:        creds,
		Fallback:           fb,
		FallbackRotator:    rotator,
		FallbackAudit:      audit,
		FallbackMetrics:    metrics,
		FallbackTerminator: terminate,
	}
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	return &fallbackFixture{
		handler:   handler,
		assign:    assign,
		fallback:  fb,
		rotator:   rotator,
		audit:     audit,
		metrics:   metrics,
		terminate: terminate,
		server:    srv,
	}
}

// mint leases a proxy credential from pool for the fixture's session and
// returns the pod-facing lease token.
func (f *fallbackFixture) mint(t *testing.T, pool string) string {
	t.Helper()
	lease, err := f.assign.Assign(pool, fbSession, "", fbTenant)
	if err != nil {
		t.Fatalf("mint lease from %s: %v", pool, err)
	}
	if lease.Proxy == nil || lease.Proxy.LeaseToken == "" {
		t.Fatalf("minted lease from %s carries no proxy token", pool)
	}
	return lease.Proxy.LeaseToken
}

// post issues one agent-pod Messages request bearing token and returns
// the proxy response.
func (f *fallbackFixture) post(t *testing.T, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, f.server.URL+"/v1/messages", strings.NewReader(fbRequestBody))
	if err != nil {
		t.Fatalf("build proxy request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("issue proxy request: %v", err)
	}
	return resp
}

func errorCode(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		t.Fatalf("read response body: %v", err)
	}
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("decode error envelope %q: %v", buf.String(), err)
	}
	return env.Error.Code
}

// spec: 4.9 (Fallback Flow: a per-provider credential fault records the
//
//	faulted pool's cooldown, increments the session's shared rotation
//	counter, and walks credentialPolicy.providerPools.{provider}.
//	fallback.order to the next pool; when the counter exceeds
//	maxRotationsPerSession the session terminates with
//	CREDENTIAL_FALLBACK_EXHAUSTED and the credential.fallback_exhausted
//	audit event is emitted)
//
// spec: spec/04_system-components.md lines 1408-1423.
//
// diagnosis: the §4.9 Fallback Flow diverged end to end. A rate-limit
//
//	(RATE_LIMITED) fault reported by a runtime through the LLM reverse
//	proxy did not walk the per-provider fallback chain, the faulted pool
//	was not placed on cooldown, the shared rotation counter did not
//	terminate the session once it exceeded maxRotationsPerSession, or the
//	terminal CREDENTIAL_FALLBACK_EXHAUSTED error plus its audit event and
//	exhaustion metric did not fire. The budget or cooldown accounting
//	that bounds runaway rotation on a failing provider is broken.
func TestCredentialFallbackChainWalkToExhaustion(t *testing.T) {
	// Budget of 2 with a four-pool chain and a long cooldown: the budget
	// is the binding constraint, so the third fault terminates the
	// session even though a fourth pool remains available. This isolates
	// the maxRotationsPerSession terminal condition from cooldown
	// exhaustion. spec: spec/04_system-components.md lines 1414-1417.
	const maxRotations = 2
	order := []string{"pool-primary", "pool-b1", "pool-b2", "pool-b3"}
	fx := startFallbackFixture(t, maxRotations, time.Hour, order)

	// The pod starts on the primary pool's lease.
	token := fx.mint(t, "pool-primary")

	// Fault 1: primary 429s. The chain rotates to pool-b1; the pod sees
	// the auth-layer upstream error (502 PROVIDER_AUTH_FAILED), not the
	// terminal error, and retries against the rotated lease.
	resp := fx.post(t, token)
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("fault 1 status = %d, want 502 (non-terminal); code=%s", resp.StatusCode, errorCode(t, resp))
	}
	if code := errorCode(t, resp); code != "PROVIDER_AUTH_FAILED" {
		t.Errorf("fault 1 code = %q, want PROVIDER_AUTH_FAILED", code)
	}
	if got := fx.fallback.RotationCount(fbSession); got != 1 {
		t.Errorf("rotation count after fault 1 = %d, want 1", got)
	}
	// Fallback Flow step 2: the faulted pool is on cooldown.
	if !fx.fallback.CoolingDown(fbSession, fbProvider, "pool-primary") {
		t.Error("pool-primary not on cooldown after its fault (step 2 degraded-lease cooldown)")
	}
	// Fallback Flow step 4: rotation walked fallback.order to pool-b1.
	if n := len(fx.rotator.calls); n != 1 {
		t.Fatalf("rotator calls after fault 1 = %d, want 1", n)
	}
	if got := fx.rotator.calls[0]; got.nextPool != "pool-b1" {
		t.Errorf("fault 1 rotated to %q, want pool-b1", got.nextPool)
	}
	if got := fx.rotator.calls[0].trigger; got != credential.TriggerFaultAuthExpired {
		t.Errorf("fault 1 trigger = %q, want fault_auth_expired", got)
	}
	token = fx.rotator.calls[0].newToken
	if token == "" {
		t.Fatal("fault 1 replacement mint produced no lease token")
	}

	// Fault 2: pool-b1 429s. Rotation walks to pool-b2, counter at 2 (at
	// the budget, not over it), still non-terminal.
	resp = fx.post(t, token)
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("fault 2 status = %d, want 502 (non-terminal); code=%s", resp.StatusCode, errorCode(t, resp))
	}
	resp.Body.Close()
	if got := fx.fallback.RotationCount(fbSession); got != 2 {
		t.Errorf("rotation count after fault 2 = %d, want 2", got)
	}
	if !fx.fallback.CoolingDown(fbSession, fbProvider, "pool-b1") {
		t.Error("pool-b1 not on cooldown after its fault")
	}
	if n := len(fx.rotator.calls); n != 2 {
		t.Fatalf("rotator calls after fault 2 = %d, want 2", n)
	}
	if got := fx.rotator.calls[1].nextPool; got != "pool-b2" {
		t.Errorf("fault 2 rotated to %q, want pool-b2", got)
	}
	token = fx.rotator.calls[1].newToken
	if token == "" {
		t.Fatal("fault 2 replacement mint produced no lease token")
	}

	// Fault 3: pool-b2 429s. The counter reaches 3, which exceeds
	// maxRotationsPerSession (2), so the chain is exhausted even though
	// pool-b3 was never faulted and is not on cooldown. The pod receives
	// the terminal CREDENTIAL_FALLBACK_EXHAUSTED error (403 / POLICY).
	// spec: spec/04_system-components.md lines 1415-1419.
	resp = fx.post(t, token)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("fault 3 status = %d, want 403 (terminal); code=%s", resp.StatusCode, errorCode(t, resp))
	}
	if code := errorCode(t, resp); code != llmproxy.CodeFallbackExhausted {
		t.Errorf("fault 3 code = %q, want %s", code, llmproxy.CodeFallbackExhausted)
	}
	// No rotation was attempted on exhaustion: the budget stops the walk
	// before pool-b3, which remains available and off cooldown.
	if n := len(fx.rotator.calls); n != 2 {
		t.Errorf("rotator calls after exhaustion = %d, want 2 (no rotation past the budget)", n)
	}
	if fx.fallback.CoolingDown(fbSession, fbProvider, "pool-b3") {
		t.Error("pool-b3 on cooldown, but it was never faulted; the budget should have stopped the walk before it")
	}

	// The credential.fallback_exhausted audit event carries the
	// spec-named fields. spec: spec/04_system-components.md lines 1417-1420.
	if n := len(fx.audit.events); n != 1 {
		t.Fatalf("fallback_exhausted audit events = %d, want 1", n)
	}
	ev := fx.audit.events[0]
	if ev.SessionID != fbSession {
		t.Errorf("audit session_id = %q, want %q", ev.SessionID, fbSession)
	}
	if ev.RotationCount != 3 {
		t.Errorf("audit rotation_count = %d, want 3 (the counter that exceeded the budget)", ev.RotationCount)
	}
	if ev.LastFailureReason == "" {
		t.Error("audit last_failure_reason is empty, want the terminal fault reason")
	}
	if len(ev.ChainAttempted) != len(order) {
		t.Errorf("audit fallback_chain_attempted = %v, want the full chain %v", ev.ChainAttempted, order)
	}

	// The §16.1 exhaustion counter fired exactly once; the rotation
	// counter fired once per non-terminal fault.
	if n := len(fx.metrics.exhausted); n != 1 {
		t.Errorf("exhaustion metric increments = %d, want 1", n)
	}
	if n := len(fx.metrics.rotations); n != 2 {
		t.Errorf("rotation metric increments = %d, want 2", n)
	}

	// The session was terminated with the terminal code.
	if n := len(fx.terminate.sessions); n != 1 || fx.terminate.sessions[0] != fbSession {
		t.Errorf("terminated sessions = %v, want [%s]", fx.terminate.sessions, fbSession)
	}
	if n := len(fx.terminate.codes); n != 1 || fx.terminate.codes[0] != llmproxy.CodeFallbackExhausted {
		t.Errorf("termination codes = %v, want [%s]", fx.terminate.codes, llmproxy.CodeFallbackExhausted)
	}
}

// spec: 4.9 (Fallback Flow step 2: cooldownOnRateLimit keeps a
//
//	rate-limited pool out of the selection until the cooldown elapses, so
//	consecutive faults walk to distinct pools rather than re-selecting the
//	degraded one)
//
// spec: spec/04_system-components.md lines 1413, 1423-1427.
//
// diagnosis: the §4.9 cooldown accounting diverged. A pool that reported
//
//	RATE_LIMITED was re-selected as a fallback target before its cooldown
//	elapsed, so the chain looped on a known-degraded credential instead of
//	advancing through fallback.order.
func TestCredentialFallbackCooldownSkipsDegradedPool(t *testing.T) {
	order := []string{"pool-primary", "pool-b1"}
	fx := startFallbackFixture(t, 5, time.Hour, order)
	token := fx.mint(t, "pool-primary")

	// Fault 1 on primary rotates to pool-b1 and places primary on
	// cooldown. spec: spec/04_system-components.md line 1413.
	resp := fx.post(t, token)
	resp.Body.Close()
	if got := fx.rotator.calls[0].nextPool; got != "pool-b1" {
		t.Fatalf("fault 1 rotated to %q, want pool-b1", got)
	}
	if !fx.fallback.CoolingDown(fbSession, fbProvider, "pool-primary") {
		t.Fatal("pool-primary not on cooldown after its rate-limit fault")
	}
	token = fx.rotator.calls[0].newToken

	// Fault 2 on pool-b1: the degraded primary is still within its
	// cooldown window, so it is not re-selected as a fallback target.
	// With no other pool remaining, the chain is exhausted by cooldown
	// (distinct from the budget path above), and the terminal error is
	// returned. On exhaustion the session's fallback state is released,
	// so cooldown is asserted before this fault, not after.
	resp = fx.post(t, token)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("fault 2 status = %d, want 403 (cooldown-exhausted); code=%s", resp.StatusCode, errorCode(t, resp))
	}
	if code := errorCode(t, resp); code != llmproxy.CodeFallbackExhausted {
		t.Errorf("fault 2 code = %q, want %s", code, llmproxy.CodeFallbackExhausted)
	}
	// Only the one rotation happened: cooldown prevented re-selecting the
	// primary as a fallback for pool-b1's fault, so the chain exhausted
	// rather than looping on the known-degraded credential.
	if n := len(fx.rotator.calls); n != 1 {
		t.Errorf("rotator calls = %d, want 1 (cooldown blocked a second rotation)", n)
	}
}
