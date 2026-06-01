// SPDX-License-Identifier: MIT

package opsserver

import (
	"net/http"

	"github.com/lennylabs/lenny/pkg/auth"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/ops/conventions"
)

// AuthConfig configures the §25.4 lines 1562-1564 lenny-ops
// authentication and role gate. lenny-ops validates JWT tokens using
// the same OIDC verifier as the gateway admin API and requires the
// platform-admin or tenant-admin role on every endpoint except the
// Kubernetes probes.
//
// A nil *AuthConfig on Options leaves the operability surface
// unauthenticated. That is valid only for dev / embedded single-node
// use; the production binary always supplies a verifier and refuses to
// start without one (see cmd/lenny-ops). The header-trust fallbacks in
// the caller* helpers below are reachable only when no AuthConfig is
// wired.
type AuthConfig struct {
	// Options carries the §10.2 bearer-verification middleware
	// configuration. The Verifier validates the JWT against the same OIDC
	// issuer/JWKS the gateway admin API trusts. opsserver forces
	// RequireAuth so the operability surface admits no anonymous request.
	Options authmw.Options

	// RateLimiter, when non-nil, applies the §25.4 line 2001 per-service-
	// account token-bucket rate limit after authentication. It keys on the
	// authenticated sub claim, so it sits inside the auth wrapper.
	RateLimiter *RateLimiter
}

// withOpsAuth wraps the operability mux in the §25.4 OIDC
// authentication + role gate. The Kubernetes probe endpoints (/healthz,
// /readyz) are exempt: they carry no bearer and the kubelet cannot
// attach one. Every other path requires a verified bearer
// (RequireAuth) whose principal holds platform-admin or tenant-admin.
//
// spec: §25.4 line 1562 ("Requires platform-admin or tenant-admin role
// on all endpoints. No anonymous access except /healthz.").
func (s *Server) withOpsAuth(mux http.Handler, cfg *AuthConfig) http.Handler {
	opts := cfg.Options
	// The operability surface never serves an anonymous request, so force
	// RequireAuth regardless of how the verifier was configured.
	opts.RequireAuth = true

	// Order after the bearer is verified: rate limit (per sub) -> role gate
	// -> mux. Rate limiting runs first so an authenticated-but-unauthorized
	// caller is still back-pressured.
	inner := requireAdminRole(mux)
	if cfg.RateLimiter != nil {
		inner = cfg.RateLimiter.Wrap(inner)
	}
	authed := authmw.Wrap(inner, opts)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isProbePath(r.URL.Path) {
			mux.ServeHTTP(w, r)
			return
		}
		authed.ServeHTTP(w, r)
	})
}

// isProbePath reports whether p is a Kubernetes liveness/readiness probe
// endpoint exempt from authentication. §25.4 line 1562 names /healthz;
// /readyz is its companion readiness probe and is exempted on the same
// rationale (a kubelet probe carries no bearer token).
func isProbePath(p string) bool {
	return p == "/healthz" || p == "/readyz"
}

// requireAdminRole rejects any request whose principal does not hold the
// platform-admin or tenant-admin role with the §25.2 canonical 403
// envelope. It runs after the bearer middleware has attached the
// principal, so an absent principal (which RequireAuth already rejects
// upstream) also fails closed here.
//
// spec: §25.4 line 1562.
func requireAdminRole(inner http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := authmw.FromContext(r.Context())
		if !ok || !(p.HasRole(auth.RolePlatformAdmin) || p.HasRole(auth.RoleTenantAdmin)) {
			conventions.WriteError(w, http.StatusForbidden, "FORBIDDEN",
				conventions.CategoryPermanent,
				"lenny-ops requires the platform-admin or tenant-admin role")
			return
		}
		inner.ServeHTTP(w, r)
	})
}

// callerPrincipal returns the §10.2 verified principal the auth
// middleware attached to the request, and whether one is present. When
// no AuthConfig is wired (dev / embedded) the second return is false and
// the caller* helpers fall back to the dev headers.
func callerPrincipal(r *http.Request) (authmw.Principal, bool) {
	return authmw.FromContext(r.Context())
}
