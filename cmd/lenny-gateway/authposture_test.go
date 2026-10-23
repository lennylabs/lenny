// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
)

// TestApplyAuthPostureOptionsByDevMode asserts the credential posture each
// value of --dev-mode produces: outside dev mode the middleware requires a
// bearer, refuses the dev-header transport, and carries the public-route
// predicate; in dev mode the dev headers are honoured and unauthenticated
// requests are admitted.
//
// spec: §10.2 (Authentication), §17.4 (dev mode).
func TestApplyAuthPostureOptionsByDevMode(t *testing.T) {
	var prod authmw.Options
	applyAuthPostureOptions(&prod, false)
	if !prod.RequireAuth || prod.AllowDevHeaders || prod.AllowDevRoles || prod.PublicPath == nil {
		t.Fatalf("outside dev mode: RequireAuth=%t AllowDevHeaders=%t AllowDevRoles=%t PublicPath set=%t; want true, false, false, true",
			prod.RequireAuth, prod.AllowDevHeaders, prod.AllowDevRoles, prod.PublicPath != nil)
	}

	dev := authmw.Options{RequireAuth: true}
	applyAuthPostureOptions(&dev, true)
	if dev.RequireAuth || !dev.AllowDevHeaders || !dev.AllowDevRoles || dev.PublicPath != nil {
		t.Fatalf("dev mode: RequireAuth=%t AllowDevHeaders=%t AllowDevRoles=%t PublicPath set=%t; want false, true, true, false",
			dev.RequireAuth, dev.AllowDevHeaders, dev.AllowDevRoles, dev.PublicPath != nil)
	}
}

// publicRouteCases lists one request per route the specification serves
// without credentials.
var publicRouteCases = []struct{ method, path string }{
	{http.MethodGet, "/healthz"},
	{http.MethodGet, "/readyz"},
	{http.MethodGet, "/metrics"},
	{http.MethodGet, "/openapi.yaml"},
	{http.MethodGet, "/openapi.json"},
	{http.MethodGet, "/v1/openapi.json"},
	{http.MethodGet, "/v1/openapi.yaml"},
	{http.MethodGet, "/.well-known/jwks.json"},
	{http.MethodPost, "/v1/oauth/token"},
	{http.MethodGet, "/playground"},
	{http.MethodGet, "/playground/auth/login"},
	{http.MethodGet, "/playground/auth/callback"},
	{http.MethodPost, "/v1/playground/token"},
	{http.MethodGet, "/v1/runtimes/echo/meta/agent-card"},
	{http.MethodGet, "/internal/runtimes/echo/meta/agent-card"},
	{http.MethodGet, "/internal/drain-readiness"},
	{http.MethodGet, "/internal/runtime-upgrade/active"},
	{http.MethodPost, "/internal/audit/node-drain-forced"},
	{http.MethodGet, "/internal/prestop"},
	{http.MethodPost, "/internal/prestop"},
}

// authenticatedRouteCases lists representative routes that require an
// authenticated principal, including near misses of the public patterns.
var authenticatedRouteCases = []struct{ method, path string }{
	{http.MethodGet, "/v1/sessions"},
	{http.MethodPost, "/v1/sessions"},
	{http.MethodGet, "/v1/sessions/sess-1"},
	{http.MethodPost, "/v1/sessions/sess-1/messages"},
	{http.MethodGet, "/v1/blobs/lenny-blob://default/sess-1/x"},
	{http.MethodGet, "/v1/runtimes"},
	{http.MethodGet, "/v1/models"},
	{http.MethodPut, "/v1/runtimes/echo/meta/agent-card"},
	{http.MethodGet, "/v1/admin/tenants"},
	{http.MethodGet, "/v1/admin/me"},
	{http.MethodGet, "/v1/admin/health"},
	{http.MethodGet, "/v1/admin/health/summary"},
	{http.MethodPost, "/mcp"},
	{http.MethodGet, "/mcp/v1/ws"},
	{http.MethodPost, "/mcp/environments/prod"},
	{http.MethodPost, "/mcp/runtimes/echo"},
	{http.MethodGet, "/v1/credentials"},
	{http.MethodPost, "/v1/chat/completions"},
	{http.MethodPost, "/v1/responses"},
	{http.MethodGet, "/v1/stateless/echo"},
	{http.MethodPost, "/healthz"},
	{http.MethodDelete, "/internal/drain-readiness"},
	{http.MethodGet, "/healthz/../v1/sessions"},
	{http.MethodGet, "/playground-admin"},
}

// TestPublicPathMatcherAdmitsOnlySpecPublicRoutes asserts the public-route
// predicate admits each route the specification serves without
// credentials and no authenticated route, including method mismatches and
// a non-canonical path that resolves to an authenticated route.
//
// spec: §10.2 (Authentication), §15.1 (OpenAPI spec endpoint), §5.1
// (publishedMetadata visibility levels), §27.3 (playground authentication).
func TestPublicPathMatcherAdmitsOnlySpecPublicRoutes(t *testing.T) {
	public := newPublicPathMatcher()
	for _, c := range publicRouteCases {
		if !public(httptest.NewRequest(c.method, c.path, nil)) {
			t.Errorf("%s %s: not public, want public", c.method, c.path)
		}
	}
	for _, c := range authenticatedRouteCases {
		req := httptest.NewRequest(c.method, "/", nil)
		req.URL.Path = c.path
		if public(req) {
			t.Errorf("%s %s: public, want authenticated", c.method, c.path)
		}
	}
}

// postureRecorder is the inner handler of the posture tests: it records
// the principal and the tenant header the auth middleware let through.
type postureRecorder struct {
	called    bool
	principal authmw.Principal
	hasPrinc  bool
	tenantHdr string
	rolesHdr  string
}

func (p *postureRecorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.called = true
	p.principal, p.hasPrinc = authmw.FromContext(r.Context())
	p.tenantHdr = r.Header.Get("X-Lenny-Tenant-ID")
	p.rolesHdr = r.Header.Get("X-Lenny-Roles")
	w.WriteHeader(http.StatusOK)
}

// postureMiddleware builds the auth middleware the way buildHTTPSurface
// does for the tenancy and dev-mode flags, with an HMAC bearer verifier.
func postureMiddleware(t *testing.T, devMode bool, inner http.Handler) http.Handler {
	t.Helper()
	opts := authmw.Options{Verifier: jwt.NewHMACSigner("posture", []byte("posture-secret-0123456789abcdef"))}
	applyTenantAuthOptions(&opts, false, devMode, nil)
	applyAuthPostureOptions(&opts, devMode)
	return authmw.Wrap(inner, opts)
}

// postureBearer signs a user bearer for alice on the default tenant.
func postureBearer(t *testing.T) string {
	t.Helper()
	now := time.Now()
	tok, err := jwt.NewHMACSigner("posture", []byte("posture-secret-0123456789abcdef")).Sign(jwt.Claims{
		Subject:  "alice@acme.com",
		TenantID: auth.DefaultTenantID,
		Roles:    []auth.Role{auth.RoleUser},
		Typ:      auth.TokenUserBearer,
		IssuedAt: now.Unix(),
		Expiry:   now.Add(5 * time.Minute).Unix(),
		JWTID:    "posture-" + now.Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatalf("sign bearer: %v", err)
	}
	return tok
}

// TestProductionPostureRefusesHeaderOnlyRequests asserts that, outside dev
// mode, a request with no bearer that names a tenant, user, and role
// through the X-Lenny-* headers is refused with 401 UNAUTHORIZED on every
// authenticated route, and that no handler runs. This is the F-10.2.21
// defect: the middleware admitted such a request and the session handlers
// took the tenant from the header.
//
// spec: §10.2 (Authentication), §15.1 (UNAUTHORIZED error code).
func TestProductionPostureRefusesHeaderOnlyRequests(t *testing.T) {
	for _, c := range authenticatedRouteCases {
		rec := &postureRecorder{}
		h := postureMiddleware(t, false, rec)
		req := httptest.NewRequest(c.method, "/", nil)
		req.URL.Path = c.path
		req.Header.Set("X-Lenny-Tenant-ID", "acme")
		req.Header.Set("X-Lenny-User-ID", "mallory")
		req.Header.Set("X-Lenny-Roles", "platform-admin")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized || rec.called {
			t.Errorf("%s %s: status %d, handler called %t; want 401 and no handler", c.method, c.path, rr.Code, rec.called)
			continue
		}
		var body struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.NewDecoder(rr.Body).Decode(&body); err != nil || body.Error.Code != "UNAUTHORIZED" {
			t.Errorf("%s %s: error code %q (decode err %v), want UNAUTHORIZED", c.method, c.path, body.Error.Code, err)
		}
	}
}

// TestProductionPostureKeepsPublicRoutesPublic asserts that, outside dev
// mode, every spec-public route reaches its handler without credentials,
// with no principal and with every client-sent identity header removed.
//
// spec: §10.2 (Authentication), §15.1 (OpenAPI spec endpoint), §5.1
// (publishedMetadata visibility levels).
func TestProductionPostureKeepsPublicRoutesPublic(t *testing.T) {
	for _, c := range publicRouteCases {
		rec := &postureRecorder{}
		h := postureMiddleware(t, false, rec)
		req := httptest.NewRequest(c.method, c.path, nil)
		req.Header.Set("X-Lenny-Tenant-ID", "acme")
		req.Header.Set("X-Lenny-Roles", "platform-admin")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK || !rec.called {
			t.Errorf("%s %s: status %d, handler called %t; want the handler to run", c.method, c.path, rr.Code, rec.called)
			continue
		}
		if rec.hasPrinc || rec.tenantHdr != "" || rec.rolesHdr != "" {
			t.Errorf("%s %s: principal=%t tenant header %q roles header %q; want no principal and no identity headers",
				c.method, c.path, rec.hasPrinc, rec.tenantHdr, rec.rolesHdr)
		}
	}
}

// TestProductionPostureBearerOverridesTenantHeader asserts that, outside
// dev mode, an authenticated request runs as its bearer's tenant and the
// tenant header a handler reads is the middleware's echo of that tenant
// rather than the value the client sent.
//
// spec: §10.2 (Authentication; `tenant_id` extraction).
func TestProductionPostureBearerOverridesTenantHeader(t *testing.T) {
	rec := &postureRecorder{}
	h := postureMiddleware(t, false, rec)
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+postureBearer(t))
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	req.Header.Set("X-Lenny-Roles", "platform-admin")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || !rec.hasPrinc {
		t.Fatalf("bearer request: status %d principal %t; want 200 with a principal (%s)", rr.Code, rec.hasPrinc, rr.Body.String())
	}
	if rec.principal.TenantID != auth.DefaultTenantID || rec.tenantHdr != auth.DefaultTenantID {
		t.Fatalf("bearer request: principal tenant %q header %q; want %q for both", rec.principal.TenantID, rec.tenantHdr, auth.DefaultTenantID)
	}
	if rec.rolesHdr != "" {
		t.Fatalf("bearer request: roles header %q reached the handler; want it removed", rec.rolesHdr)
	}
}

// TestDevPostureHonoursTenantHeader asserts that dev mode keeps the
// dev-header transport: a header-only request reaches the handler as the
// tenant it names, and a request with no header runs as `default`.
//
// spec: §17.4 (dev mode), §18 Phase 4 (the minimal gateway reads the
// tenant from X-Lenny-Tenant-ID).
func TestDevPostureHonoursTenantHeader(t *testing.T) {
	for _, tc := range []struct{ header, want string }{{"acme", "acme"}, {"", auth.DefaultTenantID}} {
		rec := &postureRecorder{}
		h := postureMiddleware(t, true, rec)
		req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
		if tc.header != "" {
			req.Header.Set("X-Lenny-Tenant-ID", tc.header)
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK || !rec.hasPrinc || rec.principal.TenantID != tc.want {
			t.Errorf("dev header %q: status %d principal %t tenant %q; want 200 as %q",
				tc.header, rr.Code, rec.hasPrinc, rec.principal.TenantID, tc.want)
		}
	}
}
