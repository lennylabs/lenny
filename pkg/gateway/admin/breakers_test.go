// SPDX-License-Identifier: MIT

package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/circuitbreaker"
	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/breakerstore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
)

func newBreakerAdmin(t *testing.T) (*admin.Router, *breakerstore.Memory, *recordingAudit) {
	t.Helper()
	tenants := tenantstore.NewMemory()
	breakers := breakerstore.NewMemory()
	audit := &recordingAudit{}
	router := admin.NewRouter(tenants, admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		Audit: audit,
	}).WithBreakers(breakers)
	return router, breakers, audit
}

func breakerReq(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		buf = bytes.NewReader(b)
	} else {
		buf = bytes.NewReader(nil)
	}
	req := withAdminPrincipal(httptest.NewRequest(method, path, buf))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestOpenBreakerHappyPath(t *testing.T) {
	router, store, audit := newBreakerAdmin(t)
	rr := breakerReq(t, router.Handler(), http.MethodPost, "/v1/admin/circuit-breakers/rt-emergency/open",
		admin.OpenBreakerRequest{
			Reason:    "incident #42",
			LimitTier: "runtime",
			Scope:     admin.ScopePayload{Runtime: "echo"},
		})
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	row, _ := store.Get(context.Background(), "rt-emergency")
	if row.State != circuitbreaker.StateOpen || row.Reason != "incident #42" {
		t.Errorf("stored: %+v", row)
	}
	if len(audit.snapshot()) != 1 || audit.snapshot()[0].Type != "circuit_breaker.opened" {
		t.Errorf("audit: %+v", audit.snapshot())
	}
}

func TestOpenBreakerRejectsInvalidScope(t *testing.T) {
	router, _, _ := newBreakerAdmin(t)
	rr := breakerReq(t, router.Handler(), http.MethodPost, "/v1/admin/circuit-breakers/x/open",
		admin.OpenBreakerRequest{
			LimitTier: "runtime",
			Scope:     admin.ScopePayload{Pool: "wrong-field"},
		})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("invalid scope: got %d, want 422", rr.Code)
	}
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &env)
	if env.Error.Code != "INVALID_BREAKER_SCOPE" {
		t.Errorf("code: %q", env.Error.Code)
	}
}

func TestOpenBreakerRejectsScopeChange(t *testing.T) {
	router, store, _ := newBreakerAdmin(t)
	_, _ = store.Open(context.Background(), circuitbreaker.Breaker{
		Name:      "rt",
		State:     circuitbreaker.StateOpen,
		LimitTier: circuitbreaker.TierRuntime,
		Scope:     circuitbreaker.Scope{Runtime: "echo"},
	})
	rr := breakerReq(t, router.Handler(), http.MethodPost, "/v1/admin/circuit-breakers/rt/open",
		admin.OpenBreakerRequest{
			LimitTier: "runtime",
			Scope:     admin.ScopePayload{Runtime: "different"},
		})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("scope change: got %d", rr.Code)
	}
}

func TestCloseBreaker(t *testing.T) {
	router, store, audit := newBreakerAdmin(t)
	_, _ = store.Open(context.Background(), circuitbreaker.Breaker{
		Name: "rt", State: circuitbreaker.StateOpen,
		LimitTier: circuitbreaker.TierRuntime,
		Scope:     circuitbreaker.Scope{Runtime: "echo"},
	})
	rr := breakerReq(t, router.Handler(), http.MethodPost, "/v1/admin/circuit-breakers/rt/close", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	row, _ := store.Get(context.Background(), "rt")
	if row.State != circuitbreaker.StateClosed {
		t.Errorf("state: %q", row.State)
	}
	if len(audit.snapshot()) != 1 || audit.snapshot()[0].Type != "circuit_breaker.closed" {
		t.Errorf("audit: %+v", audit.snapshot())
	}
}

func TestCloseBreakerMissing(t *testing.T) {
	router, _, _ := newBreakerAdmin(t)
	rr := breakerReq(t, router.Handler(), http.MethodPost, "/v1/admin/circuit-breakers/missing/close", nil)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestListBreakers(t *testing.T) {
	router, store, _ := newBreakerAdmin(t)
	_, _ = store.Open(context.Background(), circuitbreaker.Breaker{
		Name: "rt", State: circuitbreaker.StateOpen,
		LimitTier: circuitbreaker.TierRuntime,
		Scope:     circuitbreaker.Scope{Runtime: "echo"},
	})
	rr := breakerReq(t, router.Handler(), http.MethodGet, "/v1/admin/circuit-breakers", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var resp struct {
		CircuitBreakers []admin.BreakerPayload `json:"circuit_breakers"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if len(resp.CircuitBreakers) != 1 || resp.CircuitBreakers[0].Name != "rt" {
		t.Errorf("List: %+v", resp.CircuitBreakers)
	}
}

func TestBreakerEndpointsRequirePlatformAdmin(t *testing.T) {
	router, store, _ := newBreakerAdmin(t)
	_, _ = store.Open(context.Background(), circuitbreaker.Breaker{
		Name: "rt", State: circuitbreaker.StateOpen,
		LimitTier: circuitbreaker.TierRuntime,
		Scope:     circuitbreaker.Scope{Runtime: "echo"},
	})
	for _, c := range []struct {
		method, path string
	}{
		{http.MethodGet, "/v1/admin/circuit-breakers"},
		{http.MethodGet, "/v1/admin/circuit-breakers/rt"},
		{http.MethodPost, "/v1/admin/circuit-breakers/rt/open"},
		{http.MethodPost, "/v1/admin/circuit-breakers/rt/close"},
	} {
		req := withTenantAdminPrincipal(httptest.NewRequest(c.method, c.path, bytes.NewReader([]byte("{}"))))
		rr := httptest.NewRecorder()
		router.Handler().ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Errorf("%s %s: got %d, want 403", c.method, c.path, rr.Code)
		}
	}
}
