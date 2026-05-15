// SPDX-License-Identifier: MIT

// Package quota is the §5.75 QuotaEvaluator interceptor. It enforces
// a per-tenant active-session ceiling on the session-creation path
// using the §11.2 quota arithmetic from pkg/quota.
//
// The interceptor is deliberately minimal: it counts the tenant's
// current non-terminal sessions in the SessionStore and rejects a
// new create with `429 QUOTA_EXCEEDED` when the count is at or above
// the configured limit. The §11.2.1 fail-open ceilings, the Redis
// reservation script, and the billing-event emitter ship in Phase 7;
// this interceptor is the always-correct store-counting baseline.
package quota

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	pkgquota "github.com/lennylabs/lenny/pkg/quota"
)

// ActiveCounter reports the number of non-terminal sessions a tenant
// currently holds. The gateway implements this over the
// SessionStore; tests can supply a fake.
type ActiveCounter interface {
	// CountActive returns the tenant's current non-terminal session
	// count.
	CountActive(ctx context.Context, tenantID string) (int64, error)
}

// LimitResolver returns the active-session limit for a tenant. A
// non-positive limit means unlimited per the §11.2 Check semantics.
type LimitResolver interface {
	// ActiveSessionLimit returns the tenant's ceiling. Implementations
	// that have no per-tenant configuration return the platform
	// default.
	ActiveSessionLimit(ctx context.Context, tenantID string) int64
}

// StaticLimit is a LimitResolver that returns the same limit for
// every tenant. A non-positive value disables enforcement.
type StaticLimit int64

// ActiveSessionLimit implements LimitResolver.
func (l StaticLimit) ActiveSessionLimit(context.Context, string) int64 { return int64(l) }

// Options configures the middleware.
type Options struct {
	// Counter reports the tenant's current active-session count.
	// Required — a nil Counter disables enforcement (pass-through).
	Counter ActiveCounter

	// Limits resolves the per-tenant ceiling. Required when Counter
	// is set.
	Limits LimitResolver

	// TenantFromRequest extracts the tenant id from the request.
	// When nil, the X-Lenny-Tenant-ID header is used (defaulting to
	// "default"), matching the session server's tenant resolution.
	TenantFromRequest func(*http.Request) string
}

// Wrap returns the §5.75 QuotaEvaluator middleware around inner. The
// middleware only evaluates session-creation requests
// (POST /v1/sessions and POST /v1/sessions/start); every other
// request passes through untouched.
func Wrap(inner http.Handler, opts Options) http.Handler {
	tenantFn := opts.TenantFromRequest
	if tenantFn == nil {
		tenantFn = defaultTenant
	}
	return &middleware{inner: inner, opts: opts, tenantFn: tenantFn}
}

type middleware struct {
	inner    http.Handler
	opts     Options
	tenantFn func(*http.Request) string
}

func (m *middleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if m.opts.Counter == nil || m.opts.Limits == nil || !isSessionCreate(r) {
		m.inner.ServeHTTP(w, r)
		return
	}
	tenant := m.tenantFn(r)
	limit := m.opts.Limits.ActiveSessionLimit(r.Context(), tenant)
	used, err := m.opts.Counter.CountActive(r.Context(), tenant)
	if err != nil {
		// §11.2.1 fail-open: a counter error must not block session
		// creation. Phase 7 adds the cumulative fail-open timer; the
		// baseline simply admits the request.
		m.inner.ServeHTTP(w, r)
		return
	}
	switch pkgquota.Check(used, limit) {
	case pkgquota.StateHardExceeded:
		writeQuotaError(w, tenant, used, limit)
		return
	default:
		// StateOK + StateSoftWarning both admit. The §11.2.1
		// soft-warning billing event ships in Phase 7.
		m.inner.ServeHTTP(w, r)
	}
}

// isSessionCreate reports whether r is a session-creation request.
func isSessionCreate(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	p := r.URL.Path
	return p == "/v1/sessions" || p == "/v1/sessions/start"
}

func writeQuotaError(w http.ResponseWriter, tenant string, used, limit int64) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusTooManyRequests)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"code":    "QUOTA_EXCEEDED",
			"message": "tenant active-session quota exceeded",
			"details": map[string]any{
				"tenantId": tenant,
				"used":     used,
				"limit":    limit,
			},
		},
	})
}

func defaultTenant(r *http.Request) string {
	if v := r.Header.Get("X-Lenny-Tenant-ID"); v != "" {
		return v
	}
	return "default"
}

// StoreActiveCounter adapts a session lister into an ActiveCounter.
// The gateway wires this over its SessionStore so the active count
// is always read-time accurate.
type StoreActiveCounter struct {
	// List returns every session row for a tenant. The gateway
	// passes sessionstore.Store.List bound through this closure.
	List func(ctx context.Context, tenantID string) ([]session.State, error)
}

// CountActive implements ActiveCounter — it counts non-terminal
// session states.
func (c StoreActiveCounter) CountActive(ctx context.Context, tenantID string) (int64, error) {
	states, err := c.List(ctx, tenantID)
	if err != nil {
		return 0, err
	}
	var n int64
	for _, st := range states {
		if !session.IsTerminal(st) {
			n++
		}
	}
	return n, nil
}
