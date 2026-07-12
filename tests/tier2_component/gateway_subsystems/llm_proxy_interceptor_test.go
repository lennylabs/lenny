//go:build component

// SPDX-License-Identifier: MIT

// Component-tier coverage for the §4.8 LLM interceptor phases
// (PreLLMRequest, PostLLMResponse) on the §4.9 LLM reverse proxy. The
// unit suite in pkg/gateway/llmproxy/llmproxy covers these phases against
// an in-process httptest upstream; this suite drives the same phases
// through the proxy Handler wired to a real Postgres-backed credential
// lease store and the mock LLM provider recorder (the mocked upstream
// peer), so the proxy-vs-upstream boundary, the phase-only-fires-in-proxy
// contract, the 100ms LLM-phase default timeout, and streaming chunk
// pass-through are exercised on the wire rather than in-process.
package gateway_subsystems_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/llmproxy"
	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor"
	"github.com/lennylabs/lenny/tests/testinfra/stubs/llmprovider"
)

// llmPhaseInterceptor is an external §4.8 interceptor registered on an LLM
// proxy phase. It counts invocations (so a test can assert the phase fires
// exactly once, including once-per-stream for PostLLMResponse) and dispatches
// to fn for its decision. It is external (Builtin() == false, priority > the
// reserved ceiling), which is the class the spec allows on the LLM phases.
type llmPhaseInterceptor struct {
	name  string
	calls atomic.Int32
	fn    func(ctx context.Context, req interceptor.Request) (interceptor.Result, error)
}

func (i *llmPhaseInterceptor) Name() string                       { return i.name }
func (i *llmPhaseInterceptor) Priority() int32                    { return 200 }
func (i *llmPhaseInterceptor) Builtin() bool                      { return false }
func (i *llmPhaseInterceptor) FailPolicy() interceptor.FailPolicy { return interceptor.FailClosed }
func (i *llmPhaseInterceptor) Timeout() time.Duration             { return 0 }

func (i *llmPhaseInterceptor) Intercept(ctx context.Context, req interceptor.Request) (interceptor.Result, error) {
	i.calls.Add(1)
	return i.fn(ctx, req)
}

// registerLLMPhase builds a chain with ic on phase and installs it on the
// handler.
func registerLLMPhase(t *testing.T, h *llmproxy.Handler, phase interceptor.Phase, ic interceptor.Interceptor) {
	t.Helper()
	c := interceptor.NewChain()
	if err := c.Register(phase, ic); err != nil {
		t.Fatalf("register %s interceptor: %v", phase, err)
	}
	h.Interceptors = c
}

// spec: §4.8 line 1099 ("The PreLLMRequest and PostLLMResponse phases fire
// exclusively in the LLM reverse proxy path ... On REJECT, PreLLMRequest
// returns LLM_REQUEST_REJECTED to the pod; PostLLMResponse returns
// LLM_RESPONSE_REJECTED"), §4.8 line 1080 (PostLLMResponse "For streaming
// responses, fires once on the initial response metadata — individual stream
// chunks are not intercepted ... individual stream chunks pass through
// unmodified"), §4.8 line 1099 ("The default timeout for LLM interceptor
// phases is 100ms"), §4.9 (LLM reverse proxy, proxy mode).
//
// diagnosis: the §4.8 LLM interceptor phases, driven through the proxy
// Handler against the real Postgres-backed lease store and the mock upstream
// provider, diverged from §4.8. A failure means one of: a PreLLMRequest
// REJECT did not map to 403 LLM_REQUEST_REJECTED or still dialed upstream; a
// streaming PostLLMResponse REJECT did not map to 502 LLM_RESPONSE_REJECTED
// or committed the SSE stream before rejecting; a streaming PostLLMResponse
// MODIFY leaked into the relayed chunks or fired more than once on the
// stream; or a fail-closed LLM-phase interceptor did not honor the 100ms
// LLM-phase default deadline (falling back to the 500ms generic default).
func TestLLMProxyInterceptorPhasesOnRealStore(t *testing.T) {
	store := realStore(t)

	// The denied model the PreLLMRequest denylist interceptor rejects; a
	// different model is admitted.
	const deniedModel = "claude-3-5-sonnet"
	const allowedModel = "claude-3-5-haiku"

	t.Run("PreLLMRequest model denylist rejects with LLM_REQUEST_REJECTED and never dials upstream", func(t *testing.T) {
		lease := storeProxyLease(t, store, "lt-"+newUUID(t))
		h, upstream := newHandler(t, store, false, nil)
		ic := &llmPhaseInterceptor{
			name: "model-denylist",
			fn: func(_ context.Context, req interceptor.Request) (interceptor.Result, error) {
				var body struct {
					Model string `json:"model"`
				}
				if err := json.Unmarshal(req.Content, &body); err != nil {
					return interceptor.Result{}, err
				}
				if body.Model == deniedModel {
					return interceptor.Result{Action: interceptor.ActionReject, Reason: "model is on the denylist"}, nil
				}
				return interceptor.Result{Action: interceptor.ActionAllow}, nil
			},
		}
		registerLLMPhase(t, h, interceptor.PhasePreLLMRequest, ic)

		body := `{"model":"` + deniedModel + `","stream":false,"messages":[{"role":"user","content":"blocked"}]}`
		rr := postToken(h, lease.Proxy.LeaseToken, body)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403 (body %q)", rr.Code, rr.Body.String())
		}
		if !strings.Contains(rr.Body.String(), llmproxy.CodeLLMRequestRejected) {
			t.Errorf("error body %q, want %s", rr.Body.String(), llmproxy.CodeLLMRequestRejected)
		}
		// PreLLMRequest runs before credential injection and forwarding, so a
		// rejected request must never reach the upstream provider.
		if reqs := upstream.Requests(); len(reqs) != 0 {
			t.Errorf("a PreLLMRequest-rejected request reached the upstream provider: %d request(s)", len(reqs))
		}
		// The proxy is the sole invocation site for the phase: exactly one
		// request drove exactly one PreLLMRequest evaluation.
		if got := ic.calls.Load(); got != 1 {
			t.Errorf("PreLLMRequest interceptor call count = %d, want 1 (the phase fires once per proxy request)", got)
		}
	})

	t.Run("PreLLMRequest admits a non-denied model and forwards to upstream", func(t *testing.T) {
		lease := storeProxyLease(t, store, "lt-"+newUUID(t))
		h, upstream := newHandler(t, store, false, nil)
		ic := &llmPhaseInterceptor{
			name: "model-denylist",
			fn: func(_ context.Context, req interceptor.Request) (interceptor.Result, error) {
				var body struct {
					Model string `json:"model"`
				}
				_ = json.Unmarshal(req.Content, &body)
				if body.Model == deniedModel {
					return interceptor.Result{Action: interceptor.ActionReject, Reason: "denied"}, nil
				}
				return interceptor.Result{Action: interceptor.ActionAllow}, nil
			},
		}
		registerLLMPhase(t, h, interceptor.PhasePreLLMRequest, ic)

		body := `{"model":"` + allowedModel + `","stream":false,"messages":[{"role":"user","content":"allowed-echo"}]}`
		rr := postToken(h, lease.Proxy.LeaseToken, body)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %q)", rr.Code, rr.Body.String())
		}
		if _, ok := upstream.LastRequest(); !ok {
			t.Error("an admitted request did not reach the upstream provider")
		}
	})

	t.Run("streaming PostLLMResponse REJECT returns LLM_RESPONSE_REJECTED before the SSE stream commits", func(t *testing.T) {
		lease := storeProxyLease(t, store, "lt-"+newUUID(t))
		h, _ := newHandler(t, store, false, nil)
		ic := &llmPhaseInterceptor{
			name: "response-filter",
			fn: func(context.Context, interceptor.Request) (interceptor.Result, error) {
				return interceptor.Result{Action: interceptor.ActionReject, Reason: "unsafe response"}, nil
			},
		}
		registerLLMPhase(t, h, interceptor.PhasePostLLMResponse, ic)

		rr := postToken(h, lease.Proxy.LeaseToken, messagesRequest("streamed", true))
		if rr.Code != http.StatusBadGateway {
			t.Fatalf("status = %d, want 502 (body %q)", rr.Code, rr.Body.String())
		}
		if !strings.Contains(rr.Body.String(), llmproxy.CodeLLMResponseRejected) {
			t.Errorf("error body %q, want %s", rr.Body.String(), llmproxy.CodeLLMResponseRejected)
		}
		// The REJECT fires on the initial response metadata before the SSE
		// headers are committed, so the pod sees a JSON error envelope, never
		// an event-stream.
		if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Errorf("Content-Type = %q, want application/json (SSE headers must not commit before a REJECT)", ct)
		}
		// PostLLMResponse fires once on the initial stream metadata, not once
		// per chunk.
		if got := ic.calls.Load(); got != 1 {
			t.Errorf("PostLLMResponse interceptor call count = %d, want 1 (fires once on the initial stream metadata)", got)
		}
	})

	t.Run("streaming PostLLMResponse MODIFY leaves stream chunks unmodified", func(t *testing.T) {
		lease := storeProxyLease(t, store, "lt-"+newUUID(t))
		h, _ := newHandler(t, store, false, nil)
		ic := &llmPhaseInterceptor{
			name: "metadata-rewriter",
			fn: func(context.Context, interceptor.Request) (interceptor.Result, error) {
				// A MODIFY on the initial stream metadata must not alter the
				// relayed data chunks (§4.8 line 1080). The rewritten content
				// is a marker that must never surface in the SSE the pod sees.
				return interceptor.Result{
					Action:          interceptor.ActionModify,
					ModifiedContent: []byte(`{"status":200,"headers":{"x-metadata-rewritten":["yes"]}}`),
				}, nil
			},
		}
		registerLLMPhase(t, h, interceptor.PhasePostLLMResponse, ic)

		rr := postToken(h, lease.Proxy.LeaseToken, messagesRequest("hello-passthrough", true))
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %q)", rr.Code, rr.Body.String())
		}
		if ct := rr.Header().Get("Content-Type"); ct != "text/event-stream" {
			t.Errorf("Content-Type = %q, want text/event-stream", ct)
		}
		events, err := llmprovider.ReadSSE(rr.Body)
		if err != nil {
			t.Fatalf("parse relayed SSE: %v", err)
		}
		var sawDelta bool
		for _, e := range events {
			if e.Event == "content_block_delta" && strings.Contains(e.Data, "hello-passthrough") {
				sawDelta = true
			}
		}
		if !sawDelta {
			t.Errorf("relayed SSE dropped the echoed delta; chunks did not pass through unmodified: %+v", events)
		}
		// The MODIFY targeted only the metadata; its marker must not leak into
		// any relayed chunk.
		if strings.Contains(rr.Body.String(), "x-metadata-rewritten") {
			t.Errorf("PostLLMResponse MODIFY content leaked into the relayed stream chunks: %s", rr.Body.String())
		}
		if got := ic.calls.Load(); got != 1 {
			t.Errorf("PostLLMResponse interceptor call count = %d, want 1 (fires once on the initial stream metadata)", got)
		}
	})

	t.Run("fail-closed LLM-phase interceptor honors the 100ms default timeout", func(t *testing.T) {
		lease := storeProxyLease(t, store, "lt-"+newUUID(t))
		h, upstream := newHandler(t, store, false, nil)
		// The interceptor blocks until its per-call deadline elapses, then
		// returns the deadline error. With Timeout() == 0 the LLM-phase
		// default of 100ms applies; a fail-closed deadline maps to
		// INTERCEPTOR_TIMEOUT. The elapsed time bounds prove the 100ms LLM
		// default was applied rather than the 500ms generic default.
		ic := &llmPhaseInterceptor{
			name: "slow-classifier",
			fn: func(ctx context.Context, _ interceptor.Request) (interceptor.Result, error) {
				<-ctx.Done()
				return interceptor.Result{}, ctx.Err()
			},
		}
		registerLLMPhase(t, h, interceptor.PhasePreLLMRequest, ic)

		start := time.Now()
		rr := postToken(h, lease.Proxy.LeaseToken, messagesRequest("slow", false))
		elapsed := time.Since(start)

		if rr.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503 (body %q)", rr.Code, rr.Body.String())
		}
		if !strings.Contains(rr.Body.String(), interceptor.CodeInterceptorTimeout) {
			t.Errorf("error body %q, want %s", rr.Body.String(), interceptor.CodeInterceptorTimeout)
		}
		if elapsed < interceptor.DefaultLLMTimeout {
			t.Errorf("elapsed %v < the 100ms LLM-phase deadline; the interceptor did not run to its deadline", elapsed)
		}
		if elapsed >= interceptor.DefaultTimeout {
			t.Errorf("elapsed %v reached the 500ms generic default; the tighter 100ms LLM-phase default was not applied", elapsed)
		}
		// A timed-out PreLLMRequest fails closed before the upstream call.
		if reqs := upstream.Requests(); len(reqs) != 0 {
			t.Errorf("a timed-out PreLLMRequest reached the upstream provider: %d request(s)", len(reqs))
		}
	})
}
