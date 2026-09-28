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

// resolveTenant returns the tenant id for this request, preferring
// the §10.2 authenticated Principal over any client-supplied header.
// The order is:
//
//  1. Principal.TenantID from auth middleware (canonical).
//  2. X-Lenny-Tenant-ID dev header — only honoured when its value
//     passes the §10.2 format check; rejected values fall through
//     so the request lands on the default tenant instead of
//     reaching the store with an attacker-controlled identifier.
//  3. "default" per §10.2 single-tenant mode.
//
// The returned tenant id is always either a §10.2-valid identifier
// or `default`. Handlers can therefore use it directly in store
// queries and §4.5 blob URIs without re-validating.
func (s *Server) resolveTenant(r *http.Request) string {
	if p, ok := getPrincipal(r); ok && p.TenantID != "" {
		return p.TenantID
	}
	if v := r.Header.Get("X-Lenny-Tenant-ID"); v != "" {
		if err := authValidateTenantID(v); err == nil {
			return v
		}
	}
	return "default"
}

// Context-typed alias to satisfy go vet's pattern.
type ctxKey struct{}

func contextWithTenant(ctx context.Context, tenant string) context.Context {
	return context.WithValue(ctx, ctxKey{}, tenant)
}

func tenantFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(ctxKey{}).(string); ok {
		return v
	}
	return ""
}

var (
	_ = contextWithTenant
	_ = tenantFromContext
)
