// SPDX-License-Identifier: MIT

// Package auth is the §10.2 authentication middleware. It validates
// a Bearer JWT against the configured Verifier, runs the §10.2
// tenant-claim extraction state machine, and attaches the resolved
// tenant + subject to the request context so downstream handlers
// (and the audit emitter) can pick them up.
//
// Two transport conventions are supported:
//
//   - Authorization: Bearer <jwt>  — the canonical RFC 6750 path
//   - X-Lenny-Tenant-ID + X-Lenny-User-ID — dev-mode headers, only
//     honoured when the Options.AllowDevHeaders flag is true; used
//     by the existing tier-3 contract suites until they migrate to
//     signed-token fixtures
//
// On rejection the middleware emits the §10.2 error envelopes:
// TENANT_CLAIM_MISSING / TENANT_CLAIM_INVALID_FORMAT (401) and
// TENANT_NOT_FOUND (403).
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
)

// Principal captures the resolved caller identity attached to the
// request context by the middleware.
type Principal struct {
	Subject    string
	TenantID   string
	SessionID  string
	CallerType string
	Typ        auth.TokenType

	// Roles carries the §10.2 RBAC roles claimed by this token, copied
	// verbatim from the JWT roles claim (Bearer path) or parsed from
	// the comma-separated X-Lenny-Roles dev header (dev-headers path).
	Roles []auth.Role
}

// HasRole reports whether p holds r. Endpoints that gate behaviour on
// a specific role (e.g. the §7.1 derive `allowIsolationDowngrade`
// override requires `platform-admin`) call HasRole to authorise.
func (p Principal) HasRole(r auth.Role) bool {
	for _, q := range p.Roles {
		if q == r {
			return true
		}
	}
	return false
}

type principalCtxKey struct{}

// FromContext returns the Principal the middleware attached to ctx,
// or the zero value when no Principal is set (request was passed
// through under dev mode without a token).
func FromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalCtxKey{}).(Principal)
	return p, ok
}

// WithPrincipal returns a copy of ctx carrying p. Exposed for tests
// that bypass the middleware.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalCtxKey{}, p)
}

// Options configures the middleware.
type Options struct {
	// Verifier validates the JWT signature. Required when at least
	// one Bearer-bearing request can land — otherwise the middleware
	// rejects every Bearer with 500.
	Verifier jwt.Verifier

	// Registry resolves whether a tenant exists. Required in
	// MultiTenant mode.
	Registry auth.TenantRegistry

	// MultiTenant flips the §10.2 single- vs multi-tenant claim
	// extraction. When false, every request uses the §10.2 built-in
	// "default" tenant.
	MultiTenant bool

	// AllowDevHeaders, when true, permits the X-Lenny-Tenant-ID /
	// X-Lenny-User-ID dev-mode headers as a transport. Convenient
	// for the existing tier-3 contract suites that predate the JWT
	// surface.
	AllowDevHeaders bool

	// AllowDevRoles, when true, additionally honours the
	// X-Lenny-Roles dev header so dev-mode callers can claim RBAC
	// roles. SECURITY: This MUST be false in production —
	// X-Lenny-Roles is an unauthenticated client-controlled value
	// and admits self-claiming `platform-admin`. Production
	// deployments leave this false (the dev-header path silently
	// drops Roles) so RBAC is anchored to the Bearer JWT only.
	//
	// AllowDevHeaders without AllowDevRoles is the recommended dev
	// mode: tenant + user_id round-trip through headers for
	// convenience, but role claims remain authenticated.
	AllowDevRoles bool

	// RequireAuth, when true, rejects requests that carry neither a
	// Bearer token nor (under AllowDevHeaders) the dev tenant header.
	// When false, unauthenticated requests pass through with no
	// Principal on the context — useful for /healthz and similar.
	RequireAuth bool
}

// Wrap returns the auth middleware around inner.
func Wrap(inner http.Handler, opts Options) http.Handler {
	return &middleware{inner: inner, opts: opts}
}

type middleware struct {
	inner http.Handler
	opts  Options
}

func (m *middleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// 1) Try Bearer.
	if header := r.Header.Get("Authorization"); strings.HasPrefix(header, "Bearer ") {
		m.serveBearer(w, r, strings.TrimPrefix(header, "Bearer "))
		return
	}

	// 2) Optional dev-header fallback.
	if m.opts.AllowDevHeaders {
		tenantHeader := r.Header.Get("X-Lenny-Tenant-ID")
		if tenantHeader != "" || !m.opts.MultiTenant {
			m.serveDevHeaders(w, r)
			return
		}
	}

	// 3) No credentials.
	if m.opts.RequireAuth {
		writeError(w, http.StatusUnauthorized, "AUTH_REQUIRED", "request requires a bearer token", nil)
		return
	}
	m.inner.ServeHTTP(w, r)
}

func (m *middleware) serveBearer(w http.ResponseWriter, r *http.Request, token string) {
	if m.opts.Verifier == nil {
		writeError(w, http.StatusInternalServerError, "AUTH_NOT_CONFIGURED", "gateway has no JWT verifier wired in", nil)
		return
	}
	claims, err := m.opts.Verifier.Verify(token)
	if err != nil {
		var ve *jwt.VerifyError
		if errors.As(err, &ve) && ve.Reason == "expired" {
			writeError(w, http.StatusUnauthorized, "TOKEN_EXPIRED", err.Error(), nil)
			return
		}
		writeError(w, http.StatusUnauthorized, "TOKEN_INVALID", err.Error(), nil)
		return
	}
	tenant, terr := auth.ExtractTenant(auth.ExtractRequest{
		MultiTenant: m.opts.MultiTenant,
		Claim:       claims.TenantID,
		Registry:    m.opts.Registry,
	})
	if terr != nil {
		writeTenantError(w, terr)
		return
	}
	p := Principal{
		Subject:    claims.Subject,
		TenantID:   tenant.TenantID,
		SessionID:  claims.SessionID,
		CallerType: claims.CallerType,
		Typ:        claims.Typ,
		Roles:      append([]auth.Role(nil), claims.Roles...),
	}
	ctx := WithPrincipal(r.Context(), p)
	// Echo the resolved tenant via the dev header path so handlers
	// that read the dev header (sessionserver in particular) see the
	// authenticated tenant without depending on the context key.
	r = r.WithContext(ctx)
	r.Header.Set("X-Lenny-Tenant-ID", p.TenantID)
	m.inner.ServeHTTP(w, r)
}

func (m *middleware) serveDevHeaders(w http.ResponseWriter, r *http.Request) {
	tenantHeader := r.Header.Get("X-Lenny-Tenant-ID")
	tenant, err := auth.ExtractTenant(auth.ExtractRequest{
		MultiTenant: m.opts.MultiTenant,
		Claim:       tenantHeader,
		Registry:    m.opts.Registry,
	})
	if err != nil {
		writeTenantError(w, err)
		return
	}
	p := Principal{
		Subject:  r.Header.Get("X-Lenny-User-ID"),
		TenantID: tenant.TenantID,
	}
	if m.opts.AllowDevRoles {
		p.Roles = parseRolesHeader(r.Header.Get("X-Lenny-Roles"))
	}
	ctx := WithPrincipal(r.Context(), p)
	r = r.WithContext(ctx)
	r.Header.Set("X-Lenny-Tenant-ID", p.TenantID)
	m.inner.ServeHTTP(w, r)
}

// parseRolesHeader parses the comma-separated X-Lenny-Roles dev header
// value into a slice of auth.Role values. Whitespace and empty entries
// are skipped; unknown role names are dropped silently — the dev
// header is convenience only, not a security boundary, and the
// downstream authorization check still rejects callers without the
// required role regardless of what they claim.
func parseRolesHeader(v string) []auth.Role {
	if v == "" {
		return nil
	}
	out := make([]auth.Role, 0, 1)
	for _, raw := range strings.Split(v, ",") {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		role := auth.Role(name)
		if role.IsValid() {
			out = append(out, role)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func writeTenantError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrTenantClaimMissing):
		writeError(w, http.StatusUnauthorized, "TENANT_CLAIM_MISSING", err.Error(), nil)
	case errors.Is(err, auth.ErrTenantNotFound):
		writeError(w, http.StatusForbidden, "TENANT_NOT_FOUND", err.Error(), nil)
	default:
		var fe *auth.TenantIDFormatError
		if errors.As(err, &fe) {
			writeError(w, http.StatusUnauthorized, "TENANT_CLAIM_INVALID_FORMAT", err.Error(), map[string]any{
				"value": fe.Value,
			})
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
	}
}

func writeError(w http.ResponseWriter, status int, code, message string, details map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"code": code, "message": message, "details": details},
	})
}
