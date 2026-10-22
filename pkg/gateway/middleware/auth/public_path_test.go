// SPDX-License-Identifier: MIT

package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
)

// identityRecorder records the principal and the identity headers that
// reach the inner handler.
type identityRecorder struct {
	called  bool
	hasP    bool
	p       Principal
	headers map[string]string
}

func (rec *identityRecorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rec.called = true
	rec.p, rec.hasP = FromContext(r.Context())
	rec.headers = map[string]string{}
	for _, name := range identityHeaders {
		if v := r.Header.Get(name); v != "" {
			rec.headers[name] = v
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// withClientIdentity sets every identity header a client might forge.
func withClientIdentity(r *http.Request) *http.Request {
	r.Header.Set("X-Lenny-Tenant-ID", "acme")
	r.Header.Set("X-Lenny-User-ID", "mallory")
	r.Header.Set("X-Lenny-Roles", "platform-admin")
	r.Header.Set("X-Lenny-Groups", "admins")
	return r
}

// staticRegistry is a tenant registry holding a fixed tenant set.
type staticRegistry map[string]bool

func (s staticRegistry) IsRegistered(tenantID string) (bool, error) { return s[tenantID], nil }

// publicHealthz is a PublicPath predicate admitting /healthz alone.
func publicHealthz(r *http.Request) bool { return r.URL.Path == "/healthz" }

// TestRequireAuthAdmitsPublicPathWithoutPrincipal asserts that under
// RequireAuth a request with no credentials reaches the handler on a
// route PublicPath admits, with no principal and no identity headers, and
// is refused with 401 UNAUTHORIZED on any other route.
//
// spec: §10.2 (Authentication), §15.1 (UNAUTHORIZED error code).
func TestRequireAuthAdmitsPublicPathWithoutPrincipal(t *testing.T) {
	rec := &identityRecorder{}
	h := Wrap(rec, Options{RequireAuth: true, PublicPath: publicHealthz})

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, withClientIdentity(httptest.NewRequest(http.MethodGet, "/healthz", nil)))
	if !rec.called || rr.Code != http.StatusNoContent {
		t.Fatalf("public path: status %d, handler called %t; want the handler to run", rr.Code, rec.called)
	}
	if rec.hasP || len(rec.headers) != 0 {
		t.Fatalf("public path: principal %t headers %v; want no principal and no identity headers", rec.hasP, rec.headers)
	}

	rec = &identityRecorder{}
	h = Wrap(rec, Options{RequireAuth: true, PublicPath: publicHealthz})
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, withClientIdentity(httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)))
	if rec.called || rr.Code != http.StatusUnauthorized {
		t.Fatalf("authenticated path: status %d, handler called %t; want 401 and no handler", rr.Code, rec.called)
	}
}

// TestRequireAuthWithoutPublicPathRefusesEveryRoute asserts that a nil
// PublicPath treats every route as authenticated, so the zero value fails
// closed.
//
// spec: §10.2 (Authentication).
func TestRequireAuthWithoutPublicPathRefusesEveryRoute(t *testing.T) {
	rec := &identityRecorder{}
	h := Wrap(rec, Options{RequireAuth: true})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.called || rr.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, handler called %t; want 401 and no handler", rr.Code, rec.called)
	}
}

// TestPublicPathStillVerifiesPresentedBearer asserts that a public route
// does not exempt a presented bearer from verification: a forged token is
// refused rather than ignored.
//
// spec: §10.2 (Authentication).
func TestPublicPathStillVerifiesPresentedBearer(t *testing.T) {
	forged, err := jwt.NewHMACSigner("forger", []byte("forger-secret")).Sign(jwt.Claims{
		Subject:  "mallory",
		TenantID: "acme",
		Expiry:   time.Now().Add(time.Minute).Unix(),
	})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	rec := &identityRecorder{}
	h := Wrap(rec, Options{
		RequireAuth: true,
		PublicPath:  publicHealthz,
		Verifier:    jwt.NewHMACSigner("trusted", []byte("trusted-secret")),
	})
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("Authorization", "Bearer "+forged)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rec.called || rr.Code != http.StatusUnauthorized {
		t.Fatalf("forged bearer on a public path: status %d, handler called %t; want 401 and no handler", rr.Code, rec.called)
	}
}

// TestIdentityHeadersStrippedOutsideDevHeaders asserts that without
// AllowDevHeaders no client-sent identity header reaches the handler: on
// an unauthenticated pass-through every one is removed, and on the Bearer
// path X-Lenny-Tenant-ID carries the verified tenant rather than the
// client's value. Before F-10.2.21 the header passed through untouched
// and the session server read it as the tenant.
//
// spec: §10.2 (Authentication; `tenant_id` extraction).
func TestIdentityHeadersStrippedOutsideDevHeaders(t *testing.T) {
	rec := &identityRecorder{}
	h := Wrap(rec, Options{})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, withClientIdentity(httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)))
	if !rec.called || rec.hasP || len(rec.headers) != 0 {
		t.Fatalf("unauthenticated pass-through: called %t principal %t headers %v; want the handler with no principal and no identity headers",
			rec.called, rec.hasP, rec.headers)
	}

	signer := jwt.NewHMACSigner("trusted", []byte("trusted-secret"))
	tok, err := signer.Sign(jwt.Claims{
		Subject:  "alice@acme.com",
		TenantID: "globex",
		Typ:      auth.TokenUserBearer,
		Expiry:   time.Now().Add(time.Minute).Unix(),
	})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	rec = &identityRecorder{}
	h = Wrap(rec, Options{
		RequireAuth: true,
		MultiTenant: true,
		Verifier:    signer,
		Registry:    staticRegistry{"globex": true, "acme": true},
	})
	req := withClientIdentity(httptest.NewRequest(http.MethodGet, "/v1/sessions", nil))
	req.Header.Set("Authorization", "Bearer "+tok)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if !rec.called || rec.p.TenantID != "globex" {
		t.Fatalf("bearer: status %d principal tenant %q; want globex (%s)", rr.Code, rec.p.TenantID, rr.Body.String())
	}
	want := map[string]string{"X-Lenny-Tenant-ID": "globex"}
	if len(rec.headers) != len(want) || rec.headers["X-Lenny-Tenant-ID"] != "globex" {
		t.Fatalf("bearer: identity headers %v; want only %v", rec.headers, want)
	}
	if len(rec.p.Roles) != 0 {
		t.Fatalf("bearer: roles %v; the X-Lenny-Roles header must not grant roles", rec.p.Roles)
	}
}

// TestDevHeadersHonouredUnderAllowDevHeaders asserts that dev mode keeps
// the dev-header transport: the tenant header names the tenant.
//
// spec: §17.4 (dev mode), §18 Phase 4 (the minimal gateway reads the
// tenant from X-Lenny-Tenant-ID).
func TestDevHeadersHonouredUnderAllowDevHeaders(t *testing.T) {
	rec := &identityRecorder{}
	h := Wrap(rec, Options{AllowDevHeaders: true, DevHeaderNamesTenant: true})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, withClientIdentity(httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)))
	if !rec.called || rec.p.TenantID != "acme" || rec.p.Subject != "mallory" {
		t.Fatalf("dev headers: status %d principal %+v; want tenant acme subject mallory", rr.Code, rec.p)
	}
}
