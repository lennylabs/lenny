// SPDX-License-Identifier: MIT

package opsserver

import (
	"net/http"

	"github.com/lennylabs/lenny/pkg/auth"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/observability/correlation"
	"github.com/lennylabs/lenny/pkg/ops/conventions"
	"github.com/lennylabs/lenny/pkg/ops/opsservice"
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
	// -> scope gate -> mux. Rate limiting runs first so an authenticated-
	// but-unauthorized caller is still back-pressured. The §25.1 scope gate
	// sits inside the role gate and before the mux so a scope-narrowed token
	// is rejected with 403 SCOPE_FORBIDDEN before any admin handler runs at
	// its full role ceiling (spec: §25.1 lines 92-94, enforcement point 1).
	postAuth := requireAdminRole(s.scopeEnforce(mux))
	// §25.12: the MCP invoker replays a mapped tool request against this
	// post-authentication chain with the caller's already-verified
	// principal on the request context, so the in-process replay passes
	// the same §25.4 role gate and §25.1 scope gate as a direct REST call
	// ("passes through the standard OIDC/JWT middleware and role-based
	// authorization check"). The replay excludes the bearer verifier
	// (there is no bearer to forward in-process) and the per-service-
	// account rate limiter (the outer /mcp/management request already
	// charged the caller's token budget; re-charging on the replay would
	// double-count).
	s.mcpReplay = postAuth
	inner := postAuth
	if cfg.RateLimiter != nil {
		inner = cfg.RateLimiter.Wrap(inner)
	}
	authed := authmw.Wrap(inner, opts)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isUnauthenticatedPath(r.URL.Path) {
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

// isUnauthenticatedPath reports whether p is exempt from the §25.4 OIDC
// gate. The exempt set is the Kubernetes probes (isProbePath) plus the
// §16.9 Prometheus scrape surface (/metrics): a scrape carries no bearer
// token, and the §13.2 NET-045 metrics-scrape NetworkPolicy is its access
// control, mirroring how the gateway serves /metrics unauthenticated.
func isUnauthenticatedPath(p string) bool {
	// The §25.5 subscription_cache_invalidate peer RPC carries no OIDC
	// bearer; it is authenticated by the shared-secret-derived token
	// header in its handler and gated at the network layer. spec: §25.5
	// line 2751.
	return isProbePath(p) || p == "/metrics" || p == opsservice.DefaultCacheInvalidatePath
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

// callerOperationID returns the §25.17 X-Lenny-Operation-ID the
// withCorrelation middleware stamped onto the request context. Handlers
// that record an operationId on a state-changing resource fall back to
// this when the request body omits the field, so a watchdog that sends
// the header on every call (the §25.17 lines 5185-5186 / 5264 pattern)
// has its operation correlated even on the body-less diagnostic GETs and
// on POSTs whose body does not carry the field. An empty string means
// the caller supplied no correlation header.
//
// spec: §25.17 lines 5185-5186, 5264 — "the audit trail shows four calls
// tied to operation 550e8400-...".
func callerOperationID(r *http.Request) string {
	return correlation.From(r.Context()).OperationID
}

// callerAgentName returns the §25.17 X-Lenny-Agent-Name the
// withCorrelation middleware stamped onto the request context, or the
// empty string when the caller supplied no agent-name header.
//
// spec: §25.17 lines 5185-5186 — X-Lenny-Agent-Name: prod-watchdog-us-east-1.
func callerAgentName(r *http.Request) string {
	return correlation.From(r.Context()).AgentName
}
