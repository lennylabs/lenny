// SPDX-License-Identifier: MIT

package llmproxy_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/credentials/credleasestore"
	"github.com/lennylabs/lenny/pkg/gateway/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/llmproxy"
)

// spec: §4.8 lines 1055-1056, 1075, §15.1 lines 1012-1013 — the
// PreLLMRequest and PostLLMResponse interceptor phases on the §4.9 LLM
// reverse proxy: a REJECT returns LLM_REQUEST_REJECTED / LLM_RESPONSE_
// REJECTED, a MODIFY rewrites the request body and the response body,
// and a fail-closed interceptor error returns INTERCEPTOR_TIMEOUT.

// fakeInterceptor returns a fixed Result/error and is a built-in so it
// may register at the guardrails priority on any phase.
type fakeInterceptor struct {
	name string
	res  interceptor.Result
	err  error
	fail interceptor.FailPolicy
}

func (f fakeInterceptor) Name() string    { return f.name }
func (f fakeInterceptor) Priority() int32 { return 400 }
func (f fakeInterceptor) Builtin() bool   { return true }
func (f fakeInterceptor) FailPolicy() interceptor.FailPolicy {
	if f.fail == "" {
		return interceptor.FailClosed
	}
	return f.fail
}
func (f fakeInterceptor) Timeout() time.Duration { return 0 }
func (f fakeInterceptor) Intercept(context.Context, interceptor.Request) (interceptor.Result, error) {
	return f.res, f.err
}

// chainWith builds a chain registering ic on phase.
func chainWith(t *testing.T, phase interceptor.Phase, ic interceptor.Interceptor) *interceptor.Chain {
	t.Helper()
	c := interceptor.NewChain()
	if err := c.Register(phase, ic); err != nil {
		t.Fatalf("register %s: %v", phase, err)
	}
	return c
}

func TestPreLLMRequestRejectReturns403(t *testing.T) {
	h := newProxyHarness(t)
	if err := h.leases.Put(handlerLease("lt-1")); err != nil {
		t.Fatalf("seed lease: %v", err)
	}
	h.handler.Interceptors = chainWith(t, interceptor.PhasePreLLMRequest, fakeInterceptor{
		name: "model-deny",
		res:  interceptor.Result{Action: interceptor.ActionReject, Reason: "model not allowed"},
	})

	rr := post(h.handler, "lt-1", messagesBody)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
	if got := errorCode(t, rr); got != llmproxy.CodeLLMRequestRejected {
		t.Errorf("code = %q, want %q", got, llmproxy.CodeLLMRequestRejected)
	}
	// A rejected request must never reach the upstream provider.
	if *h.gotKey != "" {
		t.Error("the proxy forwarded a PreLLMRequest-rejected request upstream")
	}
}

func TestPreLLMRequestModifyRewritesUpstreamBody(t *testing.T) {
	gotBody := new(string)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		*gotBody = string(b)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"msg_1","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	t.Cleanup(upstream.Close)

	leases := credleasestore.New()
	if err := leases.Put(handlerLease("lt-2")); err != nil {
		t.Fatalf("seed lease: %v", err)
	}
	rewritten := `{"model":"claude-3-5-sonnet","messages":[{"role":"user","content":"[redacted]"}]}`
	h := &llmproxy.Handler{
		Leases:      leases,
		Translator:  &llmproxy.AnthropicDirectTranslator{BaseURL: upstream.URL, DefaultAnthropicVersion: "2023-06-01"},
		Forwarder:   &llmproxy.Forwarder{Breaker: &llmproxy.CircuitBreaker{}},
		Credentials: fakeResolver{key: "sk-real", ok: true},
		Interceptors: chainWith(t, interceptor.PhasePreLLMRequest, fakeInterceptor{
			name: "pii-redactor",
			res:  interceptor.Result{Action: interceptor.ActionModify, ModifiedContent: []byte(rewritten)},
		}),
	}

	rr := post(h, "lt-2", messagesBody)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(*gotBody, "[redacted]") {
		t.Errorf("upstream body = %q, want the MODIFY-rewritten content", *gotBody)
	}
}

func TestPostLLMResponseRejectReturns502(t *testing.T) {
	h := newProxyHarness(t)
	if err := h.leases.Put(handlerLease("lt-3")); err != nil {
		t.Fatalf("seed lease: %v", err)
	}
	h.handler.Interceptors = chainWith(t, interceptor.PhasePostLLMResponse, fakeInterceptor{
		name: "response-filter",
		res:  interceptor.Result{Action: interceptor.ActionReject, Reason: "unsafe response"},
	})

	rr := post(h.handler, "lt-3", messagesBody)
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body=%s", rr.Code, rr.Body.String())
	}
	if got := errorCode(t, rr); got != llmproxy.CodeLLMResponseRejected {
		t.Errorf("code = %q, want %q", got, llmproxy.CodeLLMResponseRejected)
	}
}

func TestPostLLMResponseModifyRewritesPodBody(t *testing.T) {
	h := newProxyHarness(t)
	if err := h.leases.Put(handlerLease("lt-4")); err != nil {
		t.Fatalf("seed lease: %v", err)
	}
	scrubbed := `{"id":"msg_scrubbed","usage":{"input_tokens":5,"output_tokens":7}}`
	h.handler.Interceptors = chainWith(t, interceptor.PhasePostLLMResponse, fakeInterceptor{
		name: "response-redactor",
		res:  interceptor.Result{Action: interceptor.ActionModify, ModifiedContent: []byte(scrubbed)},
	})

	rr := post(h.handler, "lt-4", messagesBody)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "msg_scrubbed") {
		t.Errorf("pod body = %q, want the MODIFY-rewritten response", rr.Body.String())
	}
}

func TestPreLLMRequestFailClosedReturnsInterceptorTimeout(t *testing.T) {
	h := newProxyHarness(t)
	if err := h.leases.Put(handlerLease("lt-5")); err != nil {
		t.Fatalf("seed lease: %v", err)
	}
	h.handler.Interceptors = chainWith(t, interceptor.PhasePreLLMRequest, fakeInterceptor{
		name: "broken-classifier",
		fail: interceptor.FailClosed,
		err:  context.DeadlineExceeded,
	})

	rr := post(h.handler, "lt-5", messagesBody)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", rr.Code, rr.Body.String())
	}
	if got := errorCode(t, rr); got != interceptor.CodeInterceptorTimeout {
		t.Errorf("code = %q, want %q", got, interceptor.CodeInterceptorTimeout)
	}
}

func TestPostLLMResponseStreamingRejectBeforeHeadersCommitted(t *testing.T) {
	h := newProxyHarness(t)
	if err := h.leases.Put(handlerLease("lt-7")); err != nil {
		t.Fatalf("seed lease: %v", err)
	}
	h.handler.Interceptors = chainWith(t, interceptor.PhasePostLLMResponse, fakeInterceptor{
		name: "stream-filter",
		res:  interceptor.Result{Action: interceptor.ActionReject, Reason: "unsafe stream"},
	})

	streamBody := `{"model":"claude-3-5-sonnet","stream":true,"messages":[{"role":"user","content":"hi"}]}`
	rr := post(h.handler, "lt-7", streamBody)
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body=%s", rr.Code, rr.Body.String())
	}
	if got := errorCode(t, rr); got != llmproxy.CodeLLMResponseRejected {
		t.Errorf("code = %q, want %q", got, llmproxy.CodeLLMResponseRejected)
	}
	// The REJECT runs before the SSE headers are committed, so the error
	// envelope is delivered as JSON rather than an event-stream.
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json (headers must not be committed before the REJECT)", ct)
	}
}

func TestNilChainAndEmptyPhaseArePassthrough(t *testing.T) {
	h := newProxyHarness(t)
	if err := h.leases.Put(handlerLease("lt-6")); err != nil {
		t.Fatalf("seed lease: %v", err)
	}
	// An empty PostLLMResponse chain must not block the PreLLMRequest path.
	h.handler.Interceptors = interceptor.NewChain()

	rr := post(h.handler, "lt-6", messagesBody)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
}
