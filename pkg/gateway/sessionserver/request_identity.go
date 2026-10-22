// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"net/http"

	"github.com/lennylabs/lenny/pkg/auth"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
)

// getPrincipal exposes the auth middleware's Principal lookup so the
// session handlers stay decoupled from the middleware package's
// internal context key naming.
func getPrincipal(r *http.Request) (authmw.Principal, bool) {
	return authmw.FromContext(r.Context())
}

// authValidateTenantID re-exports auth.ValidateTenantID under a name
// that does not collide with the local `auth` middleware alias.
func authValidateTenantID(s string) error { return auth.ValidateTenantID(s) }

// resolveTenant returns the tenant id for this request. It is the tenant
// lookupTenant resolves, or the empty string when lookupTenant resolves
// none. Handler refuses such a request with 401 UNAUTHORIZED before any
// tenant-scoped handler runs (see requireTenant), so a handler reached
// through Handler never sees the empty string.
//
// The returned tenant id is otherwise always a §10.2-valid identifier or
// `default`. Handlers can therefore use it directly in store queries and
// §4.5 blob URIs without re-validating.
func (s *Server) resolveTenant(r *http.Request) string {
	tenant, _ := s.lookupTenant(r)
	return tenant
}

// lookupTenant resolves the tenant a request runs as and reports whether
// it resolved one. The order is:
//
//  1. Principal.TenantID from the auth middleware (canonical).
//  2. The tenant an in-process service caller passed to ServiceCall,
//     CreateSessionService, or CreateAndStartService. Those callers run
//     inside the gateway on behalf of an already-authenticated request.
//  3. Only when the server runs with DevTenantHeader (the gateway's
//     --dev-mode): the X-Lenny-Tenant-ID dev header when its value passes
//     the §10.2 format check, and otherwise `default`, as the §18 Phase 4
//     minimal gateway does.
//
// Outside dev mode a request with neither a principal nor a service
// tenant resolves no tenant. It does not fall back to the client-sent
// header or to `default`, because either would let an unauthenticated
// caller choose the tenant whose rows it reads (F-10.2.21).
//
// spec: §10.2 (Authentication); §17.4 (dev mode).
func (s *Server) lookupTenant(r *http.Request) (string, bool) {
	if p, ok := getPrincipal(r); ok && p.TenantID != "" {
		return p.TenantID, true
	}
	if t := serviceTenantFromContext(r.Context()); t != "" {
		return t, true
	}
	if !s.devTenantHeader {
		return "", false
	}
	if v := r.Header.Get("X-Lenny-Tenant-ID"); v != "" {
		if err := authValidateTenantID(v); err == nil {
			return v, true
		}
	}
	return auth.DefaultTenantID, true
}

// tenantlessRoutes are the routes Handler serves without a resolved
// tenant. GET /v1/runtimes/{name}/meta/{key} serves a public
// publishedMetadata entry with no auth (§5.1). GET
// /internal/runtimes/{name}/meta/{key} checks its own principal and
// answers a missing one with the same 404 as a missing entry, because
// §5.1 requires a not-authorized response to match a not-found one.
var tenantlessRoutes = map[string]bool{
	"GET /v1/runtimes/{name}/meta/{key}":       true,
	"GET /internal/runtimes/{name}/meta/{key}": true,
}

// requireTenant wraps the session mux so that a request on a
// tenant-scoped route reaches its handler only when lookupTenant resolves
// a tenant. Any other request is refused with the §15.1 UNAUTHORIZED
// (401) code, the response the auth middleware gives a request with no
// credentials. The gateway's middleware already refuses such a request
// outside dev mode, so this gate is the server's own fail-closed check for
// a caller that mounts the handler without the middleware or behind a
// misconfigured one. A path the mux does not route passes through to the
// mux's own 404 or 405.
//
// spec: §10.2 (Authentication); §15.1 (UNAUTHORIZED); §5.1
// (publishedMetadata visibility levels).
func (s *Server) requireTenant(mux *http.ServeMux) http.Handler {
	return &tenantGate{s: s, mux: mux}
}

// tenantGate is the handler requireTenant returns. It exposes the wrapped
// mux through Mux, the admin.MuxUnwrapper convention, so route
// introspection in the contract and OpenAPI-completeness tests reaches the
// registered patterns past the gate.
type tenantGate struct {
	s   *Server
	mux *http.ServeMux
}

// Mux returns the wrapped session mux.
func (g *tenantGate) Mux() *http.ServeMux { return g.mux }

func (g *tenantGate) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if _, pattern := g.mux.Handler(r); pattern != "" && !tenantlessRoutes[pattern] {
		if _, ok := g.s.lookupTenant(r); !ok {
			g.s.writeUnauthenticated(w)
			return
		}
	}
	g.mux.ServeHTTP(w, r)
}

// guardTenant applies the requireTenant check to a single handler, for
// the in-process service entry points that call a handler directly
// rather than through the mux.
func (s *Server) guardTenant(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.lookupTenant(r); !ok {
			s.writeUnauthenticated(w)
			return
		}
		next(w, r)
	}
}

// writeUnauthenticated writes the §15.1 UNAUTHORIZED envelope for a
// request that carries no authenticated principal. details.reason matches
// the auth middleware's missing-credential rejection.
func (s *Server) writeUnauthenticated(w http.ResponseWriter) {
	s.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED",
		"request requires a bearer token",
		map[string]any{"reason": "auth_required"})
}

// serviceTenantKey is the context key under which an in-process service
// entry point records the tenant its caller passed.
type serviceTenantKey struct{}

// contextWithServiceTenant records tenant as the request's service
// tenant. An empty tenant leaves ctx unchanged, so the request resolves
// only through its principal. A tenant that fails the §10.2 format check
// is not recorded either, so a malformed value fails closed instead of
// reaching a store query.
func contextWithServiceTenant(ctx context.Context, tenant string) context.Context {
	if tenant == "" || authValidateTenantID(tenant) != nil {
		return ctx
	}
	return context.WithValue(ctx, serviceTenantKey{}, tenant)
}

// serviceTenantFromContext returns the service tenant recorded by
// contextWithServiceTenant, or the empty string.
func serviceTenantFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(serviceTenantKey{}).(string); ok {
		return v
	}
	return ""
}
