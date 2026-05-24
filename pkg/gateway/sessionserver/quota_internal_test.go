// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/interceptor"
)

// failClosedInterceptor errors on every call and is fail-closed, so the
// chain surfaces a CodeInterceptorTimeout REJECT.
type failClosedInterceptor struct{ timeout time.Duration }

func (f failClosedInterceptor) Name() string                       { return "slow-classifier" }
func (f failClosedInterceptor) Priority() int32                    { return 150 }
func (f failClosedInterceptor) Builtin() bool                      { return false }
func (f failClosedInterceptor) FailPolicy() interceptor.FailPolicy { return interceptor.FailClosed }
func (f failClosedInterceptor) Timeout() time.Duration             { return f.timeout }
func (f failClosedInterceptor) Intercept(context.Context, interceptor.Request) (interceptor.Result, error) {
	return interceptor.Result{}, errors.New("classifier unreachable")
}

// rejectInterceptor returns a deliberate REJECT (no timeout code).
type rejectInterceptor struct{}

func (rejectInterceptor) Name() string                       { return "policy-block" }
func (rejectInterceptor) Priority() int32                    { return 150 }
func (rejectInterceptor) Builtin() bool                      { return false }
func (rejectInterceptor) FailPolicy() interceptor.FailPolicy { return interceptor.FailClosed }
func (rejectInterceptor) Timeout() time.Duration             { return 0 }
func (rejectInterceptor) Intercept(context.Context, interceptor.Request) (interceptor.Result, error) {
	return interceptor.Result{Action: interceptor.ActionReject, Reason: "denied"}, nil
}

func decodeEnvelope(t *testing.T, rec *httptest.ResponseRecorder) errorBody {
	t.Helper()
	var env errorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	return env.Error
}

// spec: §4.8 line 1032, §15.1 line 1008 — a fail-closed interceptor
// timeout/error on the session-create path returns 503
// INTERCEPTOR_TIMEOUT (TRANSIENT, retryable) carrying interceptor_ref,
// phase, and timeout_ms, distinct from the 429 a deliberate REJECT gives.
func TestRequirePolicyChainTimeoutReturns503(t *testing.T) {
	chain := interceptor.NewChain()
	if err := chain.Register(interceptor.PhasePostAuth, failClosedInterceptor{timeout: 75 * time.Millisecond}); err != nil {
		t.Fatalf("register: %v", err)
	}
	s := &Server{interceptors: chain}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", nil)

	if ok := s.requirePolicyChain(rec, req, "acme"); ok {
		t.Fatal("requirePolicyChain admitted a fail-closed timeout")
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	body := decodeEnvelope(t, rec)
	if body.Code != interceptor.CodeInterceptorTimeout {
		t.Errorf("code = %q, want INTERCEPTOR_TIMEOUT", body.Code)
	}
	if !body.Retryable {
		t.Error("retryable = false, want true")
	}
	if got := body.Details["interceptor_ref"]; got != "slow-classifier" {
		t.Errorf("interceptor_ref = %v, want slow-classifier", got)
	}
	if got := body.Details["phase"]; got != string(interceptor.PhasePostAuth) {
		t.Errorf("phase = %v, want PostAuth", got)
	}
	if got, ok := body.Details["timeout_ms"].(float64); !ok || got != 75 {
		t.Errorf("timeout_ms = %v (ok=%v), want 75", body.Details["timeout_ms"], ok)
	}
}

// A deliberate REJECT still returns 429 QUOTA_EXCEEDED, not the 503
// timeout envelope.
func TestRequirePolicyChainDeliberateRejectReturns429(t *testing.T) {
	chain := interceptor.NewChain()
	if err := chain.Register(interceptor.PhasePostAuth, rejectInterceptor{}); err != nil {
		t.Fatalf("register: %v", err)
	}
	s := &Server{interceptors: chain}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", nil)

	if ok := s.requirePolicyChain(rec, req, "acme"); ok {
		t.Fatal("requirePolicyChain admitted a REJECT")
	}
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
	if body := decodeEnvelope(t, rec); body.Code != "QUOTA_EXCEEDED" {
		t.Errorf("code = %q, want QUOTA_EXCEEDED", body.Code)
	}
}
