// SPDX-License-Identifier: MIT

package admin

import (
	"net/http"
	"sync"

	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/openapi"
)

// routeScopes lazily builds the §15.1 route-to-scope registry once per
// process from the served OpenAPI document. The registry is read-only
// after construction, so a single shared instance is safe to consult
// concurrently across every admin request. Building it here rather than
// in NewRouter keeps the directly-constructed test Routers working
// without a wiring step.
var (
	routeScopesOnce sync.Once
	routeScopes     *openapi.RouteScopes
)

func sharedRouteScopes() *openapi.RouteScopes {
	routeScopesOnce.Do(func() {
		routeScopes = openapi.NewRouteScopes()
	})
	return routeScopes
}

// scopeGate is the §25.1 enforcement-point-1 wrapper around the admin
// serve mux. It resolves the matched route's required `x-lenny-scope`
// from the served document and rejects a present-but-narrower scope
// claim before the mux dispatches a handler. It embeds the underlying
// *http.ServeMux so route-introspection callers (the openapi
// completeness walk) can reach the registered patterns through Mux().
type scopeGate struct {
	mux        *http.ServeMux
	routeScope *openapi.RouteScopes
}

// Mux returns the wrapped serve mux so a caller that needs to enumerate
// the registered route patterns (the §15.1 openapi-completeness walk)
// can reach them past the scope gate. The MuxUnwrapper interface lets a
// caller recover the mux without depending on this concrete type.
func (g *scopeGate) Mux() *http.ServeMux { return g.mux }

// MuxUnwrapper is satisfied by a handler that wraps an *http.ServeMux and
// can hand it back for route introspection. The §15.1 completeness test
// uses it to walk the registered patterns past the §25.1 scope gate.
type MuxUnwrapper interface {
	Mux() *http.ServeMux
}

func (g *scopeGate) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	required, declared := g.routeScope.RequiredScope(req.Method, req.URL.Path)
	if !declared {
		// The matched route declares no route-level scope (or no
		// template matched); the role gate on the wrapped handler
		// decides. spec: §25.1 line 94.
		g.mux.ServeHTTP(w, req)
		return
	}
	p, _ := authmw.FromContext(req.Context())
	// An absent scope claim defers to the role ceiling (Matches returns
	// true for an empty Set). A present claim that does not grant the
	// required scope is rejected before routing.
	if p.Scopes.Present() && !p.Scopes.Matches(required) {
		writeScopeForbidden(w, required, p.Scopes.Raw)
		return
	}
	g.mux.ServeHTTP(w, req)
}

// enforceScopes wraps the admin mux with the §25.1 enforcement-point-1
// scope gate: every request resolves the matched route's required
// `x-lenny-scope` from the served document, reads the caller's parsed
// `scope` claim off the Principal, and rejects a present-but-narrower
// claim with `403 SCOPE_FORBIDDEN` before any handler runs. The gate
// runs before routing so a scope-narrowed token never reaches a
// destructive handler at its full role ceiling.
//
// An absent scope claim (Principal.Scopes.Present() == false) defers to
// the role ceiling: Scopes.Matches returns true for every required
// scope, so the wrapped handler runs and the existing role gate
// decides. A route the document declares no scope for (or that resolves
// to no template) likewise defers to the role ceiling. The two
// behaviours match the §25.1 line 90 absent-claim semantics.
//
// The check uses the request method and path against the same
// http.ServeMux pattern engine the live admin mux routes on (via
// openapi.RouteScopes), so the resolved scope is the one the matched
// route declares.
//
// spec: §15.1 (scope enforcement before routing, line 914,920;
// SCOPE_FORBIDDEN, line 1030), §25.1 (middleware checks scopes before
// routing, line 94).
func (r *Router) enforceScopes(mux *http.ServeMux) http.Handler {
	return &scopeGate{mux: mux, routeScope: sharedRouteScopes()}
}

// writeScopeForbidden emits the §15.1 line 1030 SCOPE_FORBIDDEN envelope
// carrying details.requiredScope and details.activeScope so an agent can
// see exactly which scope its token lacks and which scopes it does
// carry. spec: §15.1 line 1030.
func writeScopeForbidden(w http.ResponseWriter, requiredScope, activeScope string) {
	writeError(w, http.StatusForbidden, "SCOPE_FORBIDDEN",
		"caller's scope claim does not grant the scope required by this endpoint",
		map[string]any{
			"requiredScope": requiredScope,
			"activeScope":   activeScope,
		})
}
