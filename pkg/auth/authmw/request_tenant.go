// SPDX-License-Identifier: MIT

package authmw

import (
	"context"
	"net/http"

	"github.com/lennylabs/lenny/pkg/auth"
)

// serviceTenantKey is the context key under which an in-process service
// entry point records the tenant its caller passed.
type serviceTenantKey struct{}

// WithServiceTenant records tenant as the service tenant of a request that
// an in-process service entry point builds, such as the session core's
// ServiceCall. An empty tenant leaves ctx unchanged, so the request resolves
// only through its principal. A tenant that fails the §10.2 format check is
// not recorded either, so a malformed value fails closed instead of
// reaching a store query.
func WithServiceTenant(ctx context.Context, tenant string) context.Context {
	if tenant == "" || auth.ValidateTenantID(tenant) != nil {
		return ctx
	}
	return context.WithValue(ctx, serviceTenantKey{}, tenant)
}

// ServiceTenant returns the service tenant WithServiceTenant recorded, or
// the empty string.
func ServiceTenant(ctx context.Context) string {
	if v, ok := ctx.Value(serviceTenantKey{}).(string); ok {
		return v
	}
	return ""
}

// RequestTenant resolves the tenant a request runs as and reports whether
// it resolved one. The order is:
//
//  1. The principal's TenantID, which the middleware recorded (canonical).
//  2. The service tenant an in-process service caller recorded through
//     WithServiceTenant. Those callers run inside the gateway on behalf of
//     an already-authenticated request. A service tenant resolves the
//     tenant only; it admits no session operation that needs a principal
//     subject.
//  3. Only when devHeader is true (the gateway's --dev-mode): the
//     X-Lenny-Tenant-ID dev header when its value passes the §10.2 format
//     check, and otherwise the default tenant, as the §18 Phase 4 minimal
//     gateway does.
//
// Outside dev mode a request with neither a principal tenant nor a service
// tenant resolves no tenant. It does not fall back to the client-sent
// header or to the default tenant, because either would let an
// unauthenticated caller choose the tenant whose rows it reads
// (F-10.2.21). The tenant it returns is a §10.2-valid identifier or the
// default tenant, so a handler uses it in store queries and §4.5 blob URIs
// without validating it again.
//
// spec: §10.2 (Authentication); §17.4 (dev mode).
func RequestTenant(r *http.Request, devHeader bool) (string, bool) {
	if p, ok := FromContext(r.Context()); ok && p.TenantID != "" {
		return p.TenantID, true
	}
	if t := ServiceTenant(r.Context()); t != "" {
		return t, true
	}
	if !devHeader {
		return "", false
	}
	if v := r.Header.Get("X-Lenny-Tenant-ID"); v != "" {
		if err := auth.ValidateTenantID(v); err == nil {
			return v, true
		}
	}
	return auth.DefaultTenantID, true
}
