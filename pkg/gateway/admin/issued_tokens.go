// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/issuedtokenstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
)

// IssuedTokenRevoker revokes an issued token in the durable §13.3
// issued-token index. *issuedtokenstore.Store satisfies it.
type IssuedTokenRevoker interface {
	Revoke(ctx context.Context, tenantID, jti, reason string, at time.Time) error
}

// RevocationCache is the gateway's in-memory revocation cache. The
// revoke handler updates it immediately so the local replica enforces
// the revocation without waiting for the next rehydration.
type RevocationCache interface {
	Revoke(jti string)
}

// WithIssuedTokens wires the §13.3 token-revocation admin endpoint
// onto the Router.
func (r *Router) WithIssuedTokens(revoker IssuedTokenRevoker, cache RevocationCache) *Router {
	r.tokenRevoker = revoker
	r.revocationCache = cache
	return r
}

// RevokeTokenRequest is the POST /v1/admin/issued-tokens/{jti}/revoke
// request body.
type RevokeTokenRequest struct {
	TenantID string `json:"tenantId"`
	Reason   string `json:"reason"`
}

// handleRevokeToken implements POST /v1/admin/issued-tokens/{jti}/revoke
// — the §13.3 operator-initiated token revocation. The durable
// issued-token record is updated first, then the local revocation
// cache, so the gateway rejects the token on the next request.
func (r *Router) handleRevokeToken(w http.ResponseWriter, req *http.Request) {
	jti := req.PathValue("jti")
	var body RevokeTokenRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
		return
	}
	if body.TenantID == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "tenantId is required", nil)
		return
	}
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}
	err := r.tokenRevoker.Revoke(req.Context(), body.TenantID, jti, body.Reason, r.clock())
	if errors.Is(err, issuedtokenstore.ErrNotFound) {
		writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "issued token not found", nil)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	propagationMode := "postgres_only"
	if r.revocationCache != nil {
		// The local cache update keeps this replica's view current;
		// Postgres remains the authoritative revocation store, so peers
		// fall back to the issued_tokens.revoked_at check. This admin
		// HTTP handler does not itself publish to the §12.6 EventBus, so
		// the §16.7 propagation_mode is recorded as postgres_only. F-16.7.5.
		r.revocationCache.Revoke(jti)
	}
	// spec: §16.7 line 666 — operator-initiated single-token revocation
	// is the `explicit_revoke` reason. The payload carries the §16.7
	// fields (revoked_jti, revocation_reason, propagation_mode) rather
	// than the prior ad-hoc tenantId/reason pair; the free-text operator
	// reason is preserved under details.reason. revoked_sub is not
	// resolved here (the revoker keys by jti) and is omitted; there is no
	// cascade so cascade_root_jti is absent. F-16.7.5.
	r.emit(req.Context(), principal, "token.revoked", jti, map[string]any{
		"tenantId":          body.TenantID,
		"revoked_jti":       jti,
		"revocation_reason": "explicit_revoke",
		"propagation_mode":  propagationMode,
		"reason":            body.Reason,
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"jti":      jti,
		"tenantId": body.TenantID,
		"revoked":  true,
	})
}
