// SPDX-License-Identifier: MIT

package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/interceptor"
)

// stubPreAuth is a built-in interceptor test double for the PreAuth
// phase. A built-in may register on PreAuth where external interceptors
// cannot (§4.8 line 1023).
type stubPreAuth struct {
	action interceptor.Action
	err    error
	fail   interceptor.FailPolicy
}

func (s stubPreAuth) Name() string           { return "stub" }
func (s stubPreAuth) Priority() int32        { return 100 }
func (s stubPreAuth) Builtin() bool          { return true }
func (s stubPreAuth) Timeout() time.Duration { return 0 }
func (s stubPreAuth) FailPolicy() interceptor.FailPolicy {
	if s.fail == "" {
		return interceptor.FailClosed
	}
	return s.fail
}

func (s stubPreAuth) Intercept(_ context.Context, _ interceptor.Request) (interceptor.Result, error) {
	if s.err != nil {
		return interceptor.Result{}, s.err
	}
	return interceptor.Result{Action: s.action}, nil
}

func chainWith(t *testing.T, ic interceptor.Interceptor) *interceptor.Chain {
	t.Helper()
	c := interceptor.NewChain()
	if err := c.Register(interceptor.PhasePreAuth, ic); err != nil {
		t.Fatalf("register PreAuth stub: %v", err)
	}
	return c
}

// spec: §4.8 line 1046 — the auth middleware runs the PreAuth chain
// after the principal resolves; an ALLOW passes the request through to
// the inner handler.
func TestPreAuthChain_Allow_spec_4_8_1046(t *testing.T) {
	inner, got := captureHandler()
	h := Wrap(inner, Options{
		MultiTenant:     true,
		AllowDevHeaders: true,
		Registry:        permissiveRegistry{},
		Interceptors:    chainWith(t, stubPreAuth{action: interceptor.ActionAllow}),
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	req.Header.Set("X-Lenny-User-ID", "alice@acme.com")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rr.Code)
	}
	if got.TenantID != "acme" {
		t.Fatalf("inner handler saw tenant %q, want acme", got.TenantID)
	}
}

// spec: §4.8 line 1046, §15.1 — a deliberate PreAuth REJECT blocks the
// request with 403 INTERCEPTOR_REJECTED and the inner handler never runs.
func TestPreAuthChain_Reject_spec_4_8_1046(t *testing.T) {
	reached := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusNoContent)
	})
	h := Wrap(inner, Options{
		MultiTenant:     true,
		AllowDevHeaders: true,
		Registry:        permissiveRegistry{},
		Interceptors:    chainWith(t, stubPreAuth{action: interceptor.ActionReject}),
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rr.Code)
	}
	if reached {
		t.Fatal("inner handler ran despite a PreAuth REJECT")
	}
}

// spec: §15.1 — a fail-closed PreAuth interceptor error surfaces as 503
// INTERCEPTOR_TIMEOUT (TRANSIENT, retryable), distinct from a deliberate
// 403 REJECT.
func TestPreAuthChain_FailClosedTimeout_spec_15_1(t *testing.T) {
	inner, _ := captureHandler()
	h := Wrap(inner, Options{
		MultiTenant:     true,
		AllowDevHeaders: true,
		Registry:        permissiveRegistry{},
		Interceptors:    chainWith(t, stubPreAuth{err: errors.New("boom"), fail: interceptor.FailClosed}),
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
}

// A nil chain leaves the request path unchanged.
func TestPreAuthChain_NilChainPassthrough(t *testing.T) {
	inner, _ := captureHandler()
	h := Wrap(inner, Options{
		MultiTenant:     true,
		AllowDevHeaders: true,
		Registry:        permissiveRegistry{},
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rr.Code)
	}
}
