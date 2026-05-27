// SPDX-License-Identifier: MIT

package playground

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
)

// playgroundAllowedScope is the §27.3 / §10.2 playground_allowed_scope
// ceiling. The minted JWT's scope is the intersection of the subject
// token's scope and this set, never the union, so a playground bearer
// can never be broader than what the subject already held. The set
// covers the session, chat, and discovery operations the playground
// UI needs (§27.5) and nothing else.
var playgroundAllowedScope = []string{
	"sessions:create",
	"sessions:read",
	"sessions:interact",
	"sessions:cancel",
	"runtimes:list",
	"models:list",
	"mcp:connect",
}

// tokenResponse is the §27.3.1 POST /v1/playground/token success
// body.
type tokenResponse struct {
	BearerToken      string `json:"bearerToken"`
	TokenType        string `json:"tokenType"`
	ExpiresInSeconds int64  `json:"expiresInSeconds"`
	Reusable         bool   `json:"reusable"`
	IssuedAt         string `json:"issuedAt"`
}

// handleToken serves POST /v1/playground/token: the §27.3.1
// mode-polymorphic session-capability-JWT mint endpoint. It is the
// single mint surface for all three auth modes; the gateway reads
// playground.authMode from its own configuration and admits the
// caller accordingly (§27.3.1 "Mode enforcement is route-stamped").
func (h *Handler) handleToken(w http.ResponseWriter, r *http.Request) {
	// §27.3.1: the request body is an empty JSON object reserved for
	// future sessionMetadata fields. A malformed body is rejected.
	if !emptyJSONBody(r) {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST",
			"POST /v1/playground/token requires an empty JSON object body {}", nil)
		return
	}
	switch h.cfg.AuthMode {
	case AuthModeOIDC:
		h.mintOIDC(w, r)
	case AuthModeAPIKey:
		h.mintAPIKey(w, r)
	case AuthModeDev:
		h.mintDev(w, r)
	default:
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"the playground auth mode is not configured", nil)
	}
}

// mintOIDC handles the §27.3.1 oidc-mode admission: the caller is
// identified by the lenny_playground_session cookie. An
// Authorization: Bearer header on this endpoint is rejected to
// prevent bearer-to-bearer reissuance.
func (h *Handler) mintOIDC(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "" {
		h.rejectWrongMaterial(w, "bearer")
		return
	}
	id, tenant, ok := parseSessionCookie(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED",
			"no lenny_playground_session cookie", nil)
		return
	}
	if h.sessions == nil {
		writeError(w, http.StatusServiceUnavailable, "LENNY_PLAYGROUND_OIDC_UNAVAILABLE",
			"the playground session store is not configured", nil)
		return
	}
	rec, err := h.sessions.GetSession(r.Context(), tenant, id)
	if err != nil {
		writeErrorReason(w, http.StatusUnauthorized, "UNAUTHORIZED",
			"the playground session record has expired", "playground_session_expired")
		return
	}
	if rec.Invalidated {
		writeErrorReason(w, http.StatusUnauthorized, "UNAUTHORIZED",
			"the playground user has been invalidated", "user_invalidated")
		return
	}
	// §27.3.1: the subject is the validated OIDC principal backing the
	// session record, normalized to typ = user_bearer.
	subject := jwt.Claims{
		Subject:    rec.UserID,
		TenantID:   rec.TenantID,
		CallerType: rec.CallerType,
		Scope:      rec.Scope,
		Typ:        auth.TokenUserBearer,
	}
	h.completeMint(w, r, subject, &recordRef{tenant: tenant, id: id, rec: rec})
}

// mintAPIKey handles the §27.3.1 apiKey-mode admission: the caller
// presents a pasted bearer in Authorization: Bearer. A cookie on this
// endpoint is rejected. The pasted bearer is validated by the
// standard gateway auth chain and must be a user_bearer.
func (h *Handler) mintAPIKey(w http.ResponseWriter, r *http.Request) {
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		h.rejectWrongMaterial(w, "cookie")
		return
	}
	if _, err := r.Cookie(sessionCookie); err == nil {
		h.rejectWrongMaterial(w, "both")
		return
	}
	if h.verifier == nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"the playground bearer verifier is not configured", nil)
		return
	}
	token := strings.TrimPrefix(header, "Bearer ")
	claims, err := h.verifier.Verify(token)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED",
			"the pasted bearer token did not validate: "+err.Error(), nil)
		return
	}
	// §10.2 tenant-claim extraction. In multi-tenant mode the tenant
	// claim is required and must name a provisioned tenant. Each
	// rejection emits the §10.2 line 243
	// `playground.bearer_mint_rejected` audit event + metric.
	if h.cfg.MultiTenant {
		if claims.TenantID == "" {
			h.emitMintRejected(r, "", claims.JWTID, string(claims.Typ), "tenant_claim_missing")
			writeError(w, http.StatusUnauthorized, "TENANT_CLAIM_MISSING",
				"the pasted bearer token carries no tenant_id claim", nil)
			return
		}
		if !tenantIDPattern.MatchString(claims.TenantID) {
			h.emitMintRejected(r, claims.TenantID, claims.JWTID, string(claims.Typ), "tenant_claim_invalid_format")
			writeError(w, http.StatusUnauthorized, "TENANT_CLAIM_INVALID_FORMAT",
				"the pasted bearer token tenant_id claim fails the format regex", nil)
			return
		}
		if h.tenants != nil {
			ok, regErr := h.tenants.IsRegistered(claims.TenantID)
			if regErr != nil {
				writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", regErr.Error(), nil)
				return
			}
			if !ok {
				h.emitMintRejected(r, claims.TenantID, claims.JWTID, string(claims.Typ), "tenant_not_found")
				writeError(w, http.StatusForbidden, "TENANT_NOT_FOUND",
					"the pasted bearer token tenant_id does not name a provisioned tenant", nil)
				return
			}
		}
	}
	// §27.3 / §10.2 playground mint invariant: the pasted bearer's typ
	// MUST be user_bearer. A session_capability, a2a_delegation, or
	// service_token is rejected so a narrowly-scoped capability JWT
	// cannot be re-minted into a broader playground JWT.
	// spec: §10.2 lines 235-243 (invariant 1 + reject-audit contract).
	if claims.Typ != auth.TokenUserBearer {
		h.emitMintRejected(r, claims.TenantID, claims.JWTID, string(claims.Typ), "subject_typ_invalid")
		writeError(w, http.StatusUnauthorized, "LENNY_PLAYGROUND_BEARER_TYPE_REJECTED",
			"the pasted bearer token typ must be user_bearer, got "+string(claims.Typ),
			map[string]any{"presentedTyp": string(claims.Typ)})
		return
	}
	subject := jwt.Claims{
		Subject:    claims.Subject,
		TenantID:   claims.TenantID,
		CallerType: claims.CallerType,
		Scope:      claims.Scope,
		Roles:      claims.Roles,
		Groups:     claims.Groups,
		Typ:        auth.TokenUserBearer,
	}
	h.completeMint(w, r, subject, nil)
}

// devCallerType is the §13.3 caller_type the synthetic dev-mode
// playground principal carries.
const devCallerType = "human"

// mintDev handles the §27.3.1 dev-mode admission: no admission
// material is required. The subject is synthesized from
// playground.devTenantId and a synthetic dev-user principal. Any
// cookie or bearer presented in dev mode is ignored.
func (h *Handler) mintDev(w http.ResponseWriter, r *http.Request) {
	subject := jwt.Claims{
		Subject:    DevUserPrincipal,
		TenantID:   h.cfg.DevTenantID,
		CallerType: devCallerType,
		Scope:      strings.Join(playgroundAllowedScope, " "),
		Typ:        auth.TokenUserBearer,
	}
	h.completeMint(w, r, subject, nil)
}

// recordRef bundles the oidc-mode session record so completeMint can
// rewrite it in place with the new bearer jti after a successful mint
// (§27.3.1 silent-refresh tracking).
type recordRef struct {
	tenant string
	id     string
	rec    SessionRecord
}

// completeMint applies the §10.2 playground mint invariants, mints
// the session-capability JWT with the origin claim, records the
// minted jti on the oidc session record, emits the §27.3.1 audit
// event, and writes the response.
func (h *Handler) completeMint(w http.ResponseWriter, r *http.Request, subject jwt.Claims, ref *recordRef) {
	if h.signer == nil {
		writeError(w, http.StatusServiceUnavailable, "KMS_SIGNING_UNAVAILABLE",
			"the playground JWT signer is not configured", nil)
		return
	}
	now := h.now().UTC()
	exp := now.Add(h.cfg.BearerTTL)
	jti := newJTI(now)

	// §27.3 / §10.2 invariant: scope is the intersection of the
	// subject scope and playground_allowed_scope, never the union.
	narrowed := intersectScope(subject.Scope, playgroundAllowedScope)

	// §27.3 / §27.3.1: the origin: "playground" claim is stamped on the
	// minted session-capability JWT in all three auth modes. The
	// attachment is driven by the /playground/* ingress route, not by
	// the subject material.
	out := jwt.Claims{
		Issuer:     "https://lenny.dev.local/playground",
		Subject:    subject.Subject,
		Audience:   []string{"lenny-gateway"},
		Expiry:     exp.Unix(),
		NotBefore:  now.Unix(),
		IssuedAt:   now.Unix(),
		JWTID:      jti,
		TenantID:   subject.TenantID,
		CallerType: subject.CallerType,
		Scope:      narrowed,
		Roles:      subject.Roles,
		Groups:     subject.Groups,
		Typ:        auth.TokenSessionCapability,
		Origin:     PlaygroundOrigin,
	}
	signed, err := h.signer.Sign(out)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "KMS_SIGNING_UNAVAILABLE",
			"the playground JWT mint failed: "+err.Error(), nil)
		return
	}

	// §27.3.1: rewrite the oidc session record in place with the new
	// bearer jti so logout revokes it. The cookie-anchored TTL is not
	// reset.
	if ref != nil && h.sessions != nil {
		updated := ref.rec
		updated.BearerJTIs = appendBoundedJTI(updated.BearerJTIs, jti, h.maxTrackedJTIs())
		updated.CurrentExp = exp.Unix()
		ttl := time.Unix(updated.IssuedAt.Add(h.cfg.OIDCSessionTTL).Unix(), 0).Sub(h.now())
		if ttl < 0 {
			ttl = 0
		}
		if err := h.sessions.PutSession(r.Context(), ref.tenant, ref.id, updated, ttl); err != nil {
			writeError(w, http.StatusServiceUnavailable, "REDIS_UNAVAILABLE",
				"the playground session-record update did not commit: "+err.Error(), nil)
			return
		}
	}

	h.emitBearerMintedAudit(r, subject.TenantID, subject.Subject, jti, ref)

	writeJSON(w, http.StatusOK, tokenResponse{
		BearerToken:      signed,
		TokenType:        "Bearer",
		ExpiresInSeconds: int64(h.cfg.BearerTTL / time.Second),
		Reusable:         true,
		IssuedAt:         now.Format(time.RFC3339),
	})
}

// maxTrackedJTIs returns the §27.3.1 bound on the number of bearer
// jtis a session record tracks: ⌈oidcSessionTtlSeconds /
// bearerTtlSeconds⌉ + 1.
func (h *Handler) maxTrackedJTIs() int {
	if h.cfg.BearerTTL <= 0 {
		return 2
	}
	n := int((h.cfg.OIDCSessionTTL + h.cfg.BearerTTL - 1) / h.cfg.BearerTTL)
	return n + 1
}

// appendBoundedJTI appends jti to jtis, dropping the oldest entries
// so the slice never exceeds maxN.
func appendBoundedJTI(jtis []string, jti string, maxN int) []string {
	jtis = append(jtis, jti)
	if len(jtis) > maxN {
		jtis = jtis[len(jtis)-maxN:]
	}
	return jtis
}

// rejectWrongMaterial writes the §27.3.1
// LENNY_PLAYGROUND_WRONG_AUTH_MATERIAL error envelope. presented is
// the material the caller supplied (cookie, bearer, or both).
func (h *Handler) rejectWrongMaterial(w http.ResponseWriter, presented string) {
	writeError(w, http.StatusBadRequest, "LENNY_PLAYGROUND_WRONG_AUTH_MATERIAL",
		"the presented auth material does not match the configured playground auth mode",
		map[string]any{
			"configuredAuthMode": string(h.cfg.AuthMode),
			"presentedMaterial":  presented,
		})
}

// intersectScope returns the space-joined intersection of the
// subjectScope string and the allowed set, sorted for determinism.
// An empty subjectScope yields the empty string (the subject held no
// scope, so the intersection is empty).
func intersectScope(subjectScope string, allowed []string) string {
	want := map[string]bool{}
	for _, s := range allowed {
		want[s] = true
	}
	have := map[string]bool{}
	for _, s := range strings.Fields(subjectScope) {
		have[s] = true
	}
	var out []string
	for s := range have {
		if want[s] {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return strings.Join(out, " ")
}

// emptyJSONBody reports whether the request body is empty or an empty
// JSON object. §27.3.1 requires callers to send {}.
func emptyJSONBody(r *http.Request) bool {
	if r.Body == nil {
		return true
	}
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 4096))
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		// An empty body decodes to io.EOF, which is acceptable.
		return strings.Contains(err.Error(), "EOF")
	}
	return len(m) == 0
}

// writeJSON writes v as a JSON response with the supplied status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
