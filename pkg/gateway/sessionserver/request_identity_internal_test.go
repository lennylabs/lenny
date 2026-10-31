// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/auth"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// identityRequest builds a request with an optional principal, an optional
// X-Lenny-Tenant-ID header, and an optional service tenant.
func identityRequest(method, target, principalTenant, header, serviceTenant string) *http.Request {
	r := httptest.NewRequest(method, target, nil)
	ctx := r.Context()
	if principalTenant != "" {
		ctx = authmw.WithPrincipal(ctx, authmw.Principal{Subject: "alice@acme.com", TenantID: principalTenant})
	}
	ctx = contextWithServiceTenant(ctx, serviceTenant)
	r = r.WithContext(ctx)
	if header != "" {
		r.Header.Set("X-Lenny-Tenant-ID", header)
	}
	return r
}

// TestLookupTenantResolutionOrder covers lookupTenant in both postures.
// Outside dev mode only the principal and an in-process service tenant
// resolve a tenant; the header never does, and a request with neither
// resolves none instead of falling back to `default`. In dev mode the
// header names the tenant when well formed, and `default` is the fallback.
//
// spec: §10.2 (Authentication), §17.4 (dev mode), §18 Phase 4 (the
// minimal gateway reads the tenant from X-Lenny-Tenant-ID).
func TestLookupTenantResolutionOrder(t *testing.T) {
	cases := []struct {
		name                       string
		dev                        bool
		principal, header, service string
		want                       string
		wantOK                     bool
	}{
		{name: "strict principal", principal: "globex", header: "acme", want: "globex", wantOK: true},
		{name: "strict header only", header: "acme", wantOK: false},
		{name: "strict nothing", wantOK: false},
		{name: "strict service tenant", header: "acme", service: "globex", want: "globex", wantOK: true},
		{name: "strict principal over service", principal: "globex", service: "acme", want: "globex", wantOK: true},
		{name: "strict malformed service tenant", service: "../acme", wantOK: false},
		{name: "dev header", dev: true, header: "acme", want: "acme", wantOK: true},
		{name: "dev principal over header", dev: true, principal: "globex", header: "acme", want: "globex", wantOK: true},
		{name: "dev malformed header", dev: true, header: "acme/../globex", want: auth.DefaultTenantID, wantOK: true},
		{name: "dev nothing", dev: true, want: auth.DefaultTenantID, wantOK: true},
	}
	for _, tc := range cases {
		s := &Server{devTenantHeader: tc.dev}
		got, ok := s.lookupTenant(identityRequest(http.MethodGet, "/v1/sessions", tc.principal, tc.header, tc.service))
		if got != tc.want || ok != tc.wantOK {
			t.Errorf("%s: lookupTenant = (%q, %t), want (%q, %t)", tc.name, got, ok, tc.want, tc.wantOK)
		}
		if rt := s.resolveTenant(identityRequest(http.MethodGet, "/v1/sessions", tc.principal, tc.header, tc.service)); rt != tc.want {
			t.Errorf("%s: resolveTenant = %q, want %q", tc.name, rt, tc.want)
		}
	}
}

// errorCode decodes error.code from a recorded envelope.
func errorCode(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.NewDecoder(rr.Body).Decode(&body)
	return body.Error.Code
}

// TestHandlerRefusesUnresolvedTenantOutsideDevMode asserts the session
// server's own fail-closed gate: outside dev mode a request on a
// tenant-scoped route with no principal is refused with 401 UNAUTHORIZED
// even when it names a tenant through X-Lenny-Tenant-ID, and the same
// request with a principal is served. This is the F-10.2.21 defect: the
// header named the tenant of an unauthenticated request.
//
// spec: §10.2 (Authentication), §15.1 (UNAUTHORIZED error code).
func TestHandlerRefusesUnresolvedTenantOutsideDevMode(t *testing.T) {
	h := asOwner(New(memstore.New(), Options{}).Handler())
	for _, target := range []struct{ method, path string }{
		{http.MethodGet, "/v1/sessions"},
		{http.MethodGet, "/v1/sessions/sess-1"},
		{http.MethodPost, "/v1/sessions"},
		{http.MethodDelete, "/v1/sessions/sess-1"},
		{http.MethodGet, "/v1/runtimes"},
		{http.MethodGet, "/v1/usage"},
	} {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, identityRequest(target.method, target.path, "", "acme", ""))
		if rr.Code != http.StatusUnauthorized || errorCode(t, rr) != "UNAUTHORIZED" {
			t.Errorf("%s %s with header only: status %d, want 401 UNAUTHORIZED", target.method, target.path, rr.Code)
		}
	}

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, identityRequest(http.MethodGet, "/v1/sessions", "acme", "", ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /v1/sessions with a principal: status %d, want 200 (%s)", rr.Code, rr.Body.String())
	}
}

// TestHandlerServesTenantlessRoutesWithoutPrincipal asserts that the
// tenant gate leaves the §5.1 metadata routes to their own handlers: the
// public entry route is served without a principal, and the internal
// route answers a missing principal with the same 404 as a missing entry.
//
// spec: §5.1 (publishedMetadata visibility levels).
func TestHandlerServesTenantlessRoutesWithoutPrincipal(t *testing.T) {
	h := asOwner(New(memstore.New(), Options{}).Handler())
	for _, path := range []string{"/v1/runtimes/echo/meta/agent-card", "/internal/runtimes/echo/meta/agent-card"} {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, identityRequest(http.MethodGet, path, "", "", ""))
		if rr.Code != http.StatusNotFound || errorCode(t, rr) != "RESOURCE_NOT_FOUND" {
			t.Errorf("GET %s without a principal: status %d, want the handler's 404 RESOURCE_NOT_FOUND", path, rr.Code)
		}
	}
}

// TestHandlerDevModeKeepsHeaderTenant asserts that under DevTenantHeader
// a header-only request is served as the tenant it names.
//
// spec: §17.4 (dev mode), §18 Phase 4 (the minimal gateway reads the
// tenant from X-Lenny-Tenant-ID).
func TestHandlerDevModeKeepsHeaderTenant(t *testing.T) {
	h := asOwner(New(memstore.New(), Options{DevTenantHeader: true}).Handler())
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, identityRequest(http.MethodGet, "/v1/sessions", "", "acme", ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("dev header request: status %d, want 200 (%s)", rr.Code, rr.Body.String())
	}
}

// TestServiceEntryPointsFailClosedWithoutTenant asserts that the
// in-process service layer refuses a call that carries neither a
// principal nor a tenant outside dev mode, and serves one that passes a
// tenant.
//
// spec: §10.2 (Authentication), §15.2.1 rule 1 (shared service layer).
func TestServiceEntryPointsFailClosedWithoutTenant(t *testing.T) {
	s := New(memstore.New(), Options{})
	ctx := context.Background()

	if _, err := s.CreateSessionService(ctx, "", CreateSessionRequest{RuntimeRef: "echo"}); err == nil || err.Code != "UNAUTHORIZED" {
		t.Fatalf("CreateSessionService with no tenant: err %v, want UNAUTHORIZED", err)
	}
	if _, err := s.CreateAndStartService(ctx, "", CreateSessionRequest{RuntimeRef: "echo"}); err == nil || err.Code != "UNAUTHORIZED" {
		t.Fatalf("CreateAndStartService with no tenant: err %v, want UNAUTHORIZED", err)
	}
	if _, err := s.ServiceCall(ctx, "", http.MethodGet, "/v1/sessions", nil, "", nil); err == nil || err.Code != "UNAUTHORIZED" {
		t.Fatalf("ServiceCall with no tenant: err %v, want UNAUTHORIZED", err)
	}
	if _, err := s.ServiceCall(ownerCtx(ctx), "acme", http.MethodGet, "/v1/sessions", nil, "", nil); err != nil {
		t.Fatalf("ServiceCall with tenant acme: err %v, want success", err)
	}
	// spec: §10.2 (Session ownership) rule 3 — a service tenant resolves
	// the tenant only, so a list with no principal subject is refused.
	if _, err := s.ServiceCall(ctx, "acme", http.MethodGet, "/v1/sessions", nil, "", nil); err == nil || err.Code != "UNAUTHORIZED" {
		t.Fatalf("ServiceCall with tenant acme and no principal: err %v, want UNAUTHORIZED", err)
	}
}

// TestOwnerFromPrincipal_spec_10_2 pins the owner source on the create
// paths: the principal's Subject, and errNoPrincipalSubject for a context
// with no principal or a principal whose Subject is empty.
//
// spec: 10.2 (Session ownership)
func TestOwnerFromPrincipal_spec_10_2(t *testing.T) {
	for _, tc := range []struct {
		name    string
		ctx     context.Context
		want    string
		wantErr error
	}{
		{"subject", authmw.WithPrincipal(context.Background(), authmw.Principal{Subject: "alice@acme.com", TenantID: "acme"}), "alice@acme.com", nil},
		{"no principal", context.Background(), "", errNoPrincipalSubject},
		{"service tenant only", contextWithServiceTenant(context.Background(), "acme"), "", errNoPrincipalSubject},
		{"empty subject", authmw.WithPrincipal(context.Background(), authmw.Principal{TenantID: "acme"}), "", errNoPrincipalSubject},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ownerFromPrincipal(tc.ctx)
			if got != tc.want || !errors.Is(err, tc.wantErr) {
				t.Errorf("ownerFromPrincipal = %q, %v; want %q, %v", got, err, tc.want, tc.wantErr)
			}
		})
	}
}
