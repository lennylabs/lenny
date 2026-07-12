// SPDX-License-Identifier: MIT

package opsserver

import (
	"net/http"
	"sync"

	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/ops/conventions"
	"github.com/lennylabs/lenny/pkg/ops/mcp"
)

// opsRouteScopes resolves the §25.1 per-route x-lenny-scope for a
// lenny-ops admin REST request before the mux dispatches a handler. The
// gateway admin API enforces the same scope layer through its served
// OpenAPI document (pkg/gateway/externalapi/admin scope gate); lenny-ops
// serves its own admin REST surface, so it resolves the mapping from the
// §25.12 management-tool registry, which carries each operability
// endpoint's method, path template, and canonical tools:<domain>:<action>
// scope. Building the resolver from the same generated inventory the MCP
// tools/call layer enforces keeps the REST and MCP scope gates in lockstep
// and prevents drift from the REST surface the tools mirror.
//
// spec: §25.1 lines 92-94 (Admin API middleware checks scopes before
// routing to the handler), §15.1 (x-lenny-scope per operation).
type opsRouteScopes struct {
	mux *http.ServeMux
}

// scopeForPattern holds the required scope for one registered
// (method, path-template) pattern so a matched request recovers the scope
// without re-walking the registry.
type scopeForPattern struct{ scope string }

func (scopeForPattern) ServeHTTP(http.ResponseWriter, *http.Request) {}

var (
	opsRouteScopesOnce sync.Once
	opsRouteScopesInst *opsRouteScopes
)

// sharedOpsRouteScopes builds the process-wide route-to-scope resolver
// once from the §25.12 management-tool registry. The resolver is
// read-only after construction, so a single shared instance serves every
// concurrent admin request.
func sharedOpsRouteScopes() *opsRouteScopes {
	opsRouteScopesOnce.Do(func() {
		rs := &opsRouteScopes{mux: http.NewServeMux()}
		seen := make(map[string]bool)
		for _, t := range mcp.NewRegistry().All() {
			if t.Scope == "" || t.Method == "" || t.Path == "" {
				continue
			}
			// The §25.12 tool path template ({param}) is syntactically the
			// single-segment wildcard http.ServeMux matches, so it registers
			// verbatim under the canonical HTTP method. Dedupe defends against
			// two tools declaring the same method+path (a ServeMux.Handle on a
			// duplicate pattern panics).
			pattern := t.Method + " " + t.Path
			if seen[pattern] {
				continue
			}
			seen[pattern] = true
			rs.mux.Handle(pattern, scopeForPattern{scope: t.Scope})
		}
		opsRouteScopesInst = rs
	})
	return opsRouteScopesInst
}

// requiredScope returns the §25.1 scope the (method, path) route requires
// and whether the registry declares one. An unmatched route, or a route
// with no declared scope, returns ("", false): the caller defers to the
// §25.4 role ceiling, matching the §25.1 line 90 absent-scope semantics.
func (rs *opsRouteScopes) requiredScope(method, path string) (string, bool) {
	if rs == nil || rs.mux == nil {
		return "", false
	}
	req, err := http.NewRequest(method, path, nil)
	if err != nil {
		return "", false
	}
	h, pattern := rs.mux.Handler(req)
	if pattern == "" {
		return "", false
	}
	sp, ok := h.(scopeForPattern)
	if !ok {
		return "", false
	}
	return sp.scope, true
}

// scopeEnforce wraps the lenny-ops admin mux with the §25.1
// enforcement-point-1 scope gate: every request resolves the matched
// route's required x-lenny-scope from the §25.12 registry, reads the
// caller's parsed scope claim off the authenticated Principal, and rejects
// a present-but-narrower claim with 403 SCOPE_FORBIDDEN before any handler
// runs. An absent scope claim (Principal.Scopes.Present() == false) or a
// route with no declared scope defers to the role ceiling, matching the
// §25.1 line 90 absent-claim semantics. It runs inside the OIDC auth
// wrapper so the Principal (and its parsed Scopes) is already attached.
//
// spec: §25.1 lines 89 (403 SCOPE_FORBIDDEN listing the caller's active
// scopes), 90 (absent claim defers to the role ceiling), 92-94 (Admin API
// middleware checks scopes before routing to the handler).
func (s *Server) scopeEnforce(inner http.Handler) http.Handler {
	rs := sharedOpsRouteScopes()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		required, declared := rs.requiredScope(r.Method, r.URL.Path)
		if !declared {
			inner.ServeHTTP(w, r)
			return
		}
		p, _ := authmw.FromContext(r.Context())
		if p.Scopes.Present() && !p.Scopes.Matches(required) {
			writeOpsScopeForbidden(w, required, p.Scopes.Raw)
			return
		}
		inner.ServeHTTP(w, r)
	})
}

// writeOpsScopeForbidden emits the §25.1 line 89 SCOPE_FORBIDDEN envelope
// carrying details.requiredScope and details.activeScope so an agent sees
// exactly which scope its token lacks and which scopes it does carry. The
// AUTH category and the details keys match the §25.12 MCP tools/call
// scope-rejection payload so the two operability surfaces speak one shape.
//
// spec: §25.1 line 89, §25.2 (error category), §25.12 (MCP scope
// rejection payload).
func writeOpsScopeForbidden(w http.ResponseWriter, requiredScope, activeScope string) {
	conventions.WriteErrorWithDetails(w, http.StatusForbidden, "SCOPE_FORBIDDEN",
		conventions.CategoryAuth,
		"caller's scope claim does not grant the scope required by this endpoint",
		map[string]any{
			"requiredScope": requiredScope,
			"activeScope":   activeScope,
		})
}
