// SPDX-License-Identifier: MIT

package main

import (
	"net/http"

	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
)

// publicRoutePatterns are the gateway routes the specification serves
// without credentials. Each one either carries no tenant data or runs its
// own admission check. Every other route on the gateway listener requires
// an authenticated principal outside dev mode.
//
//   - GET /healthz and GET /readyz are the kubelet liveness and readiness
//     probes, and GET /metrics is the Prometheus scrape target. None of
//     their callers holds a bearer (§10.1, §10.4, §16.1).
//   - The OpenAPI document is served "no authentication required" at
//     /openapi.yaml and /openapi.json (§15.1). /v1/openapi.json and
//     /v1/openapi.yaml serve the same document from the same handler.
//   - /v1/oauth/ is the reverse proxy to the Token Service, which
//     authenticates the subject and actor tokens itself (§4.3, §13.3).
//   - /playground, /playground/, and /v1/playground/token are the §27.3
//     playground gatekeepers: the OIDC login and callback pages, and the
//     cookie, bearer, or dev-mode admission to the bearer mint.
//   - GET /v1/runtimes/{name}/meta/{key} serves a runtime's public
//     publishedMetadata entry, which §5.1 serves with no auth.
//   - GET /internal/runtimes/{name}/meta/{key} requires a session JWT, but
//     §5.1 requires a not-authorized request to receive the same response
//     as a not-found one, so the handler refuses a missing principal with
//     its 404 rather than the middleware refusing it with a 401.
//   - GET /internal/drain-readiness, GET /internal/runtime-upgrade/active,
//     and POST /internal/audit/node-drain-forced are admission-webhook
//     callbacks on the gateway's internal port, reachable only through the
//     §13.2 NetworkPolicy sub-rules, and /internal/prestop is the kubelet
//     preStop hook (§12.5, §10.5, §10.1).
//
// The patterns use http.ServeMux syntax so that a request matches exactly
// when the gateway's own mux would route it to the public handler. A
// non-canonical path such as /healthz/../v1/sessions resolves to the
// canonical /v1/sessions and is therefore not public.
var publicRoutePatterns = []string{
	"GET /healthz",
	"GET /readyz",
	"GET /metrics",
	"/openapi.yaml",
	"/openapi.json",
	"/v1/openapi.json",
	"/v1/openapi.yaml",
	"/v1/oauth/",
	"/playground",
	"/playground/",
	"/v1/playground/token",
	"GET /v1/runtimes/{name}/meta/{key}",
	"GET /internal/runtimes/{name}/meta/{key}",
	"GET /internal/drain-readiness",
	"GET /internal/runtime-upgrade/active",
	"POST /internal/audit/node-drain-forced",
	"/internal/prestop",
}

// newPublicPathMatcher returns the authmw.Options.PublicPath predicate for
// publicRoutePatterns. It registers the patterns on a private ServeMux and
// reports whether that mux would route the request to one of them.
//
// spec: §10.2 (Authentication); §15.1 (OpenAPI spec endpoint); §5.1
// (publishedMetadata visibility levels); §27.3 (playground authentication).
func newPublicPathMatcher() func(*http.Request) bool {
	mux := http.NewServeMux()
	noop := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	for _, p := range publicRoutePatterns {
		mux.Handle(p, noop)
	}
	return func(r *http.Request) bool {
		_, pattern := mux.Handler(r)
		return pattern != ""
	}
}

// applyAuthPostureOptions sets the credential-requirement fields of the
// auth middleware options from the gateway's dev-mode flag.
//
// Outside dev mode the gateway requires a verified bearer on every route
// except publicRoutePatterns, and it ignores the X-Lenny-* identity
// headers: the middleware deletes them from every request, so a client
// cannot name a tenant, user, role, or group by header. Before this
// helper the middleware accepted the dev headers regardless of
// --dev-mode and never required credentials, so an unauthenticated
// request reached the session handlers, which then took the tenant from
// the client-sent X-Lenny-Tenant-ID header (F-10.2.21).
//
// In dev mode the dev-header transport stays available and
// unauthenticated requests are admitted, as §17.4 describes for local
// development.
//
// spec: §10.2 (Authentication); §17.4 (dev mode).
func applyAuthPostureOptions(opts *authmw.Options, devMode bool) {
	opts.AllowDevHeaders = devMode
	opts.AllowDevRoles = devMode
	opts.RequireAuth = !devMode
	opts.PublicPath = nil
	if !devMode {
		opts.PublicPath = newPublicPathMatcher()
	}
}
