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
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/common/scopes"
	"github.com/lennylabs/lenny/pkg/gateway/interceptor"
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

	// Groups carries the §10.6 OIDC groups claimed by this token, copied
	// from the JWT groups claim (Bearer path) or parsed from the
	// comma-separated X-Lenny-Groups dev header (dev-headers path, only
	// under AllowDevRoles). Environment membership is resolved against
	// it.
	Groups []string

	// Scopes is the §25.1 RFC 9068 scope claim parsed off the JWT.
	// Handlers and the MCP adapter call Scopes.Matches(required) to
	// enforce the per-endpoint x-lenny-scope from §15.1. An absent
	// claim yields a zero Set whose Matches always returns true, which
	// the spec maps to "no scope restriction beyond role". The dev-
	// header path leaves Scopes zero (no scope narrowing in dev mode).
	Scopes scopes.Set
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

// HasScope reports whether p's §25.1 scope claim permits the given
// required scope (e.g. the handler's x-lenny-scope from §15.1).
// An absent scope claim defers to the role ceiling — HasScope
// returns true so the standard RBAC check still runs.
func (p Principal) HasScope(required string) bool {
	return p.Scopes.Matches(required)
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

	// TenantClaimName is the JWT payload claim the middleware reads the
	// tenant identifier from in multi-tenant mode. Defaults to
	// "tenant_id" when empty (the §10.2 canonical name). Operators set
	// this via the `auth.tenantIdClaim` Helm value / `--tenant-id-claim`
	// gateway flag when their OIDC provider stamps tenant identity under
	// a different claim name. The middleware reads the standard
	// `claims.TenantID` field when the name is "tenant_id"; for other
	// names it resolves the claim via Claims.ClaimString so it picks up
	// the value from the raw JWT payload.
	// spec: §10.2 line 212. F-10.2.9.
	TenantClaimName string

	// AllowDevHeaders, when true, permits the X-Lenny-Tenant-ID /
	// X-Lenny-User-ID dev-mode headers as a transport. Convenient
	// for the existing tier-3 contract suites that predate the JWT
	// surface.
	AllowDevHeaders bool

	// AllowDevRoles, when true, additionally honours the X-Lenny-Roles
	// and X-Lenny-Groups dev headers so dev-mode callers can claim RBAC
	// roles and §10.6 environment groups. SECURITY: This MUST be false
	// in production — both headers are unauthenticated client-controlled
	// values and X-Lenny-Roles admits self-claiming `platform-admin`.
	// Production deployments leave this false (the dev-header path
	// silently drops Roles and Groups) so RBAC and environment
	// membership are anchored to the Bearer JWT only.
	//
	// AllowDevHeaders without AllowDevRoles is the recommended dev
	// mode: tenant + user_id round-trip through headers for
	// convenience, but role and group claims remain authenticated.
	AllowDevRoles bool

	// RequireAuth, when true, rejects requests that carry neither a
	// Bearer token nor (under AllowDevHeaders) the dev tenant header.
	// When false, unauthenticated requests pass through with no
	// Principal on the context — useful for /healthz and similar.
	RequireAuth bool

	// Revocations, when set, is consulted after a Bearer JWT verifies:
	// a token whose jti has been revoked is rejected even though its
	// signature and expiry are still valid (§13.3 token revocation).
	Revocations RevocationChecker

	// PlaygroundRevocations, when set, is the §27.6 authoritative
	// per-request revocation check for playground-origin bearers. It is
	// consulted on the auth hot path for every verified Bearer whose
	// `origin` claim is `playground` (the claim §27.3 stamps on every
	// session-capability JWT minted through a /playground/* ingress
	// path). A hit rejects with 401 UNAUTHORIZED and
	// details.reason=bearer_revoked; a backing-store error fails closed
	// with 503 REDIS_UNAVAILABLE rather than honoring the bearer.
	// Non-playground bearers (no origin claim) never consult it.
	// spec: §27.6 line 204 / §27.3.1 lines 95-97. F-27.6.3 / F-27.3.1.
	PlaygroundRevocations PlaygroundRevocationChecker

	// AuthFailureSink, when set, receives §4.2 line 185 `auth_failure`
	// audit events for every tenant-claim rejection
	// (TENANT_CLAIM_MISSING, TENANT_NOT_FOUND, TENANT_CLAIM_INVALID_FORMAT).
	// The middleware also writes an INFO log line carrying `user_id`
	// and `jti` for traceability on every such rejection regardless of
	// whether a sink is wired.
	AuthFailureSink AuthFailureSink

	// Clock returns the gateway clock instant the middleware stamps on
	// `auth_failure` audit events. Defaults to time.Now when nil.
	Clock func() time.Time

	// Interceptors, when set, runs the §4.8 PreAuth interceptor chain
	// after the principal is resolved and before the request reaches the
	// inner handler. The built-in AuthEvaluator (priority 100) runs as
	// the sole interceptor at this phase; external interceptors are
	// rejected from PreAuth at registration (§4.8 line 1023). A chain
	// REJECT blocks the request with 403 INTERCEPTOR_REJECTED. nil skips
	// the phase. spec: §4.8 lines 972, 1023, 1046.
	Interceptors *interceptor.Chain

	// PlatformRoles, when set, resolves the §10.2 line 294
	// platform-managed user→role mapping. The middleware consults it for
	// every Bearer principal whose Subject is non-empty; when a mapping
	// row exists for (TenantID, Subject), its Roles fully replace the
	// roles claimed by the JWT, allowing tenant-admins to override
	// OIDC-derived roles within their tenant. Absent the mapping the JWT
	// roles claim stays authoritative.
	// spec: §10.2 line 294. F-10.2.3.
	PlatformRoles PlatformRoleResolver
}

// PlatformRoleResolver is the §10.2 line 294 platform-managed
// user→role mapping. The auth middleware consults it for every
// authenticated Bearer principal; when a mapping row exists, its Roles
// override the JWT claim.
type PlatformRoleResolver interface {
	// ResolveRoles returns the platform-managed roles for
	// (tenantID, subject). The found return is true when a mapping row
	// exists for the principal (in which case Roles fully replaces the
	// OIDC claim); false means no mapping exists and the OIDC claim
	// stays authoritative. Implementations return the underlying error
	// on transport-level failures so the middleware can fail closed.
	ResolveRoles(ctx context.Context, tenantID, subject string) (roles []auth.Role, found bool, err error)
}

// RevocationChecker reports whether a token's jti has been revoked.
type RevocationChecker interface {
	IsRevoked(jti string) bool
}

// playgroundOriginClaim is the §27.3 `origin` claim value stamped on
// every session-capability JWT minted through a /playground/* ingress
// path. The auth middleware keys the §27.6 per-request revocation check
// on this claim, not on authMode. It is duplicated here as a literal
// rather than imported from the playground package to avoid an import
// cycle (the playground package depends on the auth chain).
// spec: §27.3 line 63.
const playgroundOriginClaim = "playground"

// PlaygroundRevocationChecker is the §27.6 / §27.3.1 authoritative
// per-request revocation check for playground-origin bearers. The auth
// middleware consults it for every verified Bearer carrying the
// `origin: "playground"` claim. The error return is non-nil only when
// the backing store (Redis) is unreachable; §27.3.1 line 97 specifies
// the middleware fails closed (503) on that error rather than honoring
// the bearer. spec: §27.6 line 204 / §27.3.1 lines 95-97.
type PlaygroundRevocationChecker interface {
	IsBearerRevoked(ctx context.Context, tenant, jti string) (bool, error)
}

// AuthFailureEvent captures the payload of an §4.2 line 185
// `auth_failure` audit event emitted by the middleware on tenant-claim
// rejection.
type AuthFailureEvent struct {
	// Reason is the §4.2 line 185 error envelope code:
	// TENANT_CLAIM_MISSING, TENANT_NOT_FOUND, or
	// TENANT_CLAIM_INVALID_FORMAT.
	Reason string

	// TenantID carries the inferred tenant identifier when present
	// (the value of the OIDC claim before validation, or the resolved
	// tenant for the dev-header path). Empty when no claim was carried.
	TenantID string

	// UserID is the JWT `sub` claim copied verbatim for traceability.
	UserID string

	// JTI is the JWT `jti` claim copied verbatim for traceability.
	JTI string

	// At is the gateway clock instant the rejection fired.
	At time.Time
}

// AuthFailureSink receives an `auth_failure` audit event for every
// tenant-claim rejection. Implementations must be non-blocking — the
// middleware does not wait for delivery.
type AuthFailureSink interface {
	EmitAuthFailure(ctx context.Context, event AuthFailureEvent)
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
		// spec: §15.1 line 986 — UNAUTHORIZED (401) is the §15.1 catalog
		// code for "Missing or invalid authentication credentials". The
		// details.reason discriminates the missing-bearer case so
		// callers scripting against the §15.1 catalog get a stable code
		// while operators retain the diagnostic.
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED",
			"request requires a bearer token",
			map[string]any{"reason": "auth_required"})
		return
	}
	m.inner.ServeHTTP(w, r)
}

func (m *middleware) serveBearer(w http.ResponseWriter, r *http.Request, token string) {
	if m.opts.Verifier == nil {
		// spec: §15.1 line 1016 — gateway misconfiguration surfaces as
		// the canonical INTERNAL_ERROR (500); details.reason names the
		// configuration gap for operator triage.
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"gateway has no JWT verifier wired in",
			map[string]any{"reason": "auth_not_configured"})
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
	// §13.3 revocation: a signature- and expiry-valid token is still
	// rejected when its jti has been revoked.
	if m.opts.Revocations != nil && m.opts.Revocations.IsRevoked(claims.JWTID) {
		writeError(w, http.StatusUnauthorized, "TOKEN_REVOKED",
			"the presented token has been revoked", nil)
		return
	}
	// spec: §10.2 line 212 — the OIDC claim used to derive tenant_id is
	// configurable via `auth.tenantIdClaim`. Default is `tenant_id`; an
	// operator can point at any string claim. F-10.2.9.
	claimName := m.opts.TenantClaimName
	if claimName == "" {
		claimName = "tenant_id"
	}
	rawTenant := claims.TenantID
	if claimName != "tenant_id" {
		rawTenant = claims.ClaimString(claimName)
	}
	tenant, terr := auth.ExtractTenant(auth.ExtractRequest{
		MultiTenant: m.opts.MultiTenant,
		Claim:       rawTenant,
		Registry:    m.opts.Registry,
	})
	if terr != nil {
		m.writeTenantError(r.Context(), w, terr, rawTenant, claims.Subject, claims.JWTID)
		return
	}
	// §27.6 line 204 / §27.3.1 lines 95-97 — authoritative per-request
	// revocation check for playground-origin bearers. Every request
	// carrying the `origin: "playground"` claim MUST consult
	// pg:revoked:{jti} on the auth hot path before the bearer is honored,
	// so a logout/idle/admin revocation on one replica cannot be bypassed
	// by presenting the same bearer to a peer. A hit is 401 with
	// details.reason=bearer_revoked; a store-unreachable error fails
	// closed (503 REDIS_UNAVAILABLE). Non-playground bearers carry no
	// origin claim and skip the check entirely. F-27.6.3 / F-27.3.1.
	if m.opts.PlaygroundRevocations != nil && claims.Origin == playgroundOriginClaim {
		revoked, rerr := m.opts.PlaygroundRevocations.IsBearerRevoked(r.Context(), tenant.TenantID, claims.JWTID)
		if rerr != nil {
			writeError(w, http.StatusServiceUnavailable, "REDIS_UNAVAILABLE",
				"the playground revocation store is unreachable; the bearer cannot be honored",
				map[string]any{"reason": "revocation_store_unavailable"})
			return
		}
		if revoked {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED",
				"the presented playground bearer has been revoked",
				map[string]any{"reason": "bearer_revoked"})
			return
		}
	}
	// §25.1: parse the RFC 9068 scope claim into a typed Set. A
	// malformed claim rejects the token with TOKEN_INVALID so a
	// downstream handler never sees a half-parsed scope.
	scopeSet, perr := scopes.Parse(claims.Scope)
	if perr != nil {
		writeError(w, http.StatusUnauthorized, "TOKEN_INVALID",
			"scope claim malformed: "+perr.Error(), nil)
		return
	}
	p := Principal{
		Subject:    claims.Subject,
		TenantID:   tenant.TenantID,
		SessionID:  claims.SessionID,
		CallerType: claims.CallerType,
		Typ:        claims.Typ,
		Roles:      append([]auth.Role(nil), claims.Roles...),
		Groups:     append([]string(nil), claims.Groups...),
		Scopes:     scopeSet,
	}
	// spec: §10.2 line 294 — the platform-managed user→role mapping
	// takes precedence over OIDC-derived roles. When the resolver
	// returns a row for (tenant, subject), its Roles override the JWT
	// claim wholesale; on a transport error the request fails closed
	// (we cannot safely fall back to the unmodified OIDC roles when we
	// do not know whether a platform override exists). F-10.2.3.
	if m.opts.PlatformRoles != nil && p.Subject != "" {
		roles, found, perr := m.opts.PlatformRoles.ResolveRoles(r.Context(), p.TenantID, p.Subject)
		if perr != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
				"platform role resolution failed: "+perr.Error(),
				map[string]any{"reason": "platform_role_lookup_failed"})
			return
		}
		if found {
			p.Roles = append([]auth.Role(nil), roles...)
		}
	}
	ctx := WithPrincipal(r.Context(), p)
	// Echo the resolved tenant via the dev header path so handlers
	// that read the dev header (sessionserver in particular) see the
	// authenticated tenant without depending on the context key.
	r = r.WithContext(ctx)
	r.Header.Set("X-Lenny-Tenant-ID", p.TenantID)
	if !m.runPreAuth(w, r, p) {
		return
	}
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
		// Dev-header path: no JWT means no sub / jti claim. The
		// rejection still produces an audit row + INFO log so the §4.2
		// line 185 observability contract is honoured uniformly across
		// transports; user_id falls back to the X-Lenny-User-ID header
		// when present so dev integrations carry traceability too.
		m.writeTenantError(r.Context(), w, err, tenantHeader, r.Header.Get("X-Lenny-User-ID"), "")
		return
	}
	p := Principal{
		Subject:  r.Header.Get("X-Lenny-User-ID"),
		TenantID: tenant.TenantID,
	}
	if m.opts.AllowDevRoles {
		p.Roles = parseRolesHeader(r.Header.Get("X-Lenny-Roles"))
		p.Groups = parseGroupsHeader(r.Header.Get("X-Lenny-Groups"))
	}
	ctx := WithPrincipal(r.Context(), p)
	r = r.WithContext(ctx)
	r.Header.Set("X-Lenny-Tenant-ID", p.TenantID)
	if !m.runPreAuth(w, r, p) {
		return
	}
	m.inner.ServeHTTP(w, r)
}

// runPreAuth runs the §4.8 PreAuth interceptor chain over the resolved
// principal. It returns true when the request may proceed (the chain
// admitted it, or no chain is wired); on a REJECT it writes the error
// envelope and returns false. The PreAuth content payload is the
// authenticated identity, carried as metadata so AuthEvaluator and any
// later phase read the same tenant_id / user_id keys.
//
// spec: §4.8 line 1046 (the PreAuth immutability row) and lines
// 1025–1028 (authenticated identity available to the chain).
func (m *middleware) runPreAuth(w http.ResponseWriter, r *http.Request, p Principal) bool {
	if m.opts.Interceptors == nil {
		return true
	}
	res := m.opts.Interceptors.Run(r.Context(), interceptor.Request{
		Phase:     interceptor.PhasePreAuth,
		SessionID: p.SessionID,
		TenantID:  p.TenantID,
		Metadata: map[string]string{
			"tenant_id": p.TenantID,
			"user_id":   p.Subject,
		},
	})
	if res.Action != interceptor.ActionReject {
		return true
	}
	// spec: §15.1 — a fail-closed interceptor timeout/error is surfaced
	// as 503 INTERCEPTOR_TIMEOUT (TRANSIENT, retryable); a deliberate
	// REJECT is 403 INTERCEPTOR_REJECTED.
	if res.Code == interceptor.CodeInterceptorTimeout {
		writeError(w, http.StatusServiceUnavailable, interceptor.CodeInterceptorTimeout, res.Reason,
			map[string]any{
				"interceptor_ref": res.RejectedBy,
				"phase":           string(interceptor.PhasePreAuth),
				"timeout_ms":      res.TimeoutMs,
			})
		return false
	}
	writeError(w, http.StatusForbidden, "INTERCEPTOR_REJECTED", res.Reason,
		map[string]any{"interceptor_ref": res.RejectedBy, "phase": string(interceptor.PhasePreAuth)})
	return false
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

// parseGroupsHeader parses the comma-separated X-Lenny-Groups dev
// header into a §10.6 group-name slice. Whitespace and empty entries
// are skipped. Like the roles header it is convenience only, not a
// security boundary; production anchors group membership to the
// Bearer JWT.
func parseGroupsHeader(v string) []string {
	if v == "" {
		return nil
	}
	out := make([]string, 0, 1)
	for _, raw := range strings.Split(v, ",") {
		name := strings.TrimSpace(raw)
		if name != "" {
			out = append(out, name)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// writeTenantError maps a §10.2 tenant-extraction error to the
// HTTP envelope and, for the three §4.2 line 185 rejection reasons
// (TENANT_CLAIM_MISSING / TENANT_NOT_FOUND / TENANT_CLAIM_INVALID_FORMAT),
// emits an INFO log line with `user_id` and `jti` for traceability and
// an `auth_failure` audit event via the configured sink. The audit
// payload carries the inferred tenant identifier when one was
// presented; for TENANT_CLAIM_MISSING the value is empty.
//
// spec: §4.2 line 185 ("Both rejection reasons are logged (INFO level,
// with user_id and jti for traceability) and emitted as auth_failure
// audit events.")
func (m *middleware) writeTenantError(ctx context.Context, w http.ResponseWriter, err error, tenantID, userID, jti string) {
	switch {
	case errors.Is(err, auth.ErrTenantClaimMissing):
		writeError(w, http.StatusUnauthorized, "TENANT_CLAIM_MISSING", err.Error(), nil)
		m.recordAuthFailure(ctx, "TENANT_CLAIM_MISSING", tenantID, userID, jti)
	case errors.Is(err, auth.ErrTenantNotFound):
		writeError(w, http.StatusForbidden, "TENANT_NOT_FOUND", err.Error(), nil)
		m.recordAuthFailure(ctx, "TENANT_NOT_FOUND", tenantID, userID, jti)
	default:
		var fe *auth.TenantIDFormatError
		if errors.As(err, &fe) {
			writeError(w, http.StatusUnauthorized, "TENANT_CLAIM_INVALID_FORMAT", err.Error(), map[string]any{
				"value": fe.Value,
			})
			m.recordAuthFailure(ctx, "TENANT_CLAIM_INVALID_FORMAT", fe.Value, userID, jti)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
	}
}

// recordAuthFailure writes the §4.2 line 185 INFO log line and emits
// the `auth_failure` audit event. Logging always fires; the audit
// emission is a no-op when no sink is wired.
func (m *middleware) recordAuthFailure(ctx context.Context, reason, tenantID, userID, jti string) {
	log.Printf("auth: %s tenant_id=%q user_id=%q jti=%q",
		reason, tenantID, userID, jti)
	if m.opts.AuthFailureSink == nil {
		return
	}
	clk := m.opts.Clock
	if clk == nil {
		clk = func() time.Time { return time.Now().UTC() }
	}
	m.opts.AuthFailureSink.EmitAuthFailure(ctx, AuthFailureEvent{
		Reason:   reason,
		TenantID: tenantID,
		UserID:   userID,
		JTI:      jti,
		At:       clk(),
	})
}

// AuthFailureEventType is the §4.2 line 185 event-type identifier the
// middleware emits on tenant-claim rejection. The string is fixed by
// the spec; the §16.7 catalog only enumerates §25-introduced events
// so this constant is the source of truth for the auth-failure name.
const AuthFailureEventType = "auth_failure"

func writeError(w http.ResponseWriter, status int, code, message string, details map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"code": code, "message": message, "details": details},
	})
}
