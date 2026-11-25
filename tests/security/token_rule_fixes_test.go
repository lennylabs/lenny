// SPDX-License-Identifier: MIT

//go:build security

// Security tests for the token rule fixes of change 0168 against a real
// Token Service over a real Postgres issued-token store: the token
// endpoint refuses the tokens of a fully revoked user even when they have
// no issued_tokens row, and a recursive revocation by a caller without
// platform-admin writes no cross_tenant_read event.
package security

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/tokenservice"
)

// signAs signs a user bearer for sub with jti and scope in the harness
// tenant without recording it, as an upstream identity provider would.
func (h *sourceRevokedHarness) signAs(t *testing.T, sub, jti, scope string) string {
	t.Helper()
	now := time.Now().UTC()
	tok, err := h.signer.Sign(jwt.Claims{
		Subject: sub, TenantID: h.tenant, JWTID: jti, Typ: auth.TokenUserBearer,
		Scope: scope, Audience: []string{"lenny-gateway"},
		IssuedAt: now.Add(-time.Minute).Unix(), Expiry: now.Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("Sign %s: %v", jti, err)
	}
	return tok
}

// fullyRevoke commits the full-revoke invalidation level on sub's user row,
// as the admin API does before step 1 of the full revoke.
func (h *sourceRevokedHarness) fullyRevoke(t *testing.T, sub string) {
	t.Helper()
	ctx := context.Background()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant', $1, true)", h.tenant); err != nil {
		t.Fatalf("set tenant: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO users (tenant_id, subject, deleted_at) VALUES ($1, $2, now())
		 ON CONFLICT (tenant_id, subject) DO UPDATE SET deleted_at = now()`, h.tenant, sub); err != nil {
		t.Fatalf("invalidate %s: %v", sub, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// spec: §13.3 (Credential Flow), §11.4 (User Invalidation)
// diagnosis: after a full revoke of alice, an upstream token of hers that
// has no issued_tokens row still authenticated a mint as the caller, was
// exchanged as a subject_token, or authenticated a recursive-revocation
// request (FM-16 of the M3 readiness review). Check recheckSourceUsers in
// pkg/storage/issuedtokenstore and writeCallerRefusal in pkg/tokenservice.
func TestTokenServiceRefusesTheUnrecordedTokensOfAFullyRevokedUser(t *testing.T) {
	h := newSourceRevokedHarness(t)
	const wide = "tools:sessions:read tools:sessions:write"
	aliceID := h.signAs(t, "alice@acme.com", "alice-idp", wide)
	aliceOther := h.signAs(t, "alice@acme.com", "alice-idp-2", wide)
	bob := h.signAs(t, "bob@acme.com", "bob-idp", wide)
	h.fullyRevoke(t, "alice@acme.com")

	code, body := h.exchange(t, aliceID, tokenservice.Request{
		GrantType: exchangeGrant, SubjectToken: bob, Scope: "tools:sessions:read", Audience: "lenny-gateway",
	})
	if code != http.StatusUnauthorized || body["error"] != "invalid_client" || body["error_description"] != "caller_user_invalidated" {
		t.Errorf("caller: status %d body %v, want 401 invalid_client caller_user_invalidated", code, body)
	}
	code, body = h.exchange(t, bob, tokenservice.Request{
		GrantType: exchangeGrant, SubjectToken: aliceID, Scope: "tools:sessions:read", Audience: "lenny-gateway",
	})
	if code != http.StatusBadRequest || body["error"] != "invalid_grant" || body["error_description"] != "subject_user_invalidated" {
		t.Errorf("subject: status %d body %v, want 400 invalid_grant subject_user_invalidated", code, body)
	}
	if body["access_token"] != nil {
		t.Error("a token of a fully revoked user was exchanged")
	}
	code, body = h.exchange(t, aliceID, tokenservice.Request{
		GrantType: exchangeGrant, SubjectToken: aliceOther,
		RequestedTokenType: "urn:ietf:params:oauth:token-type:access_token:revoked",
	})
	if code != http.StatusUnauthorized || body["error_description"] != "caller_user_invalidated" {
		t.Errorf("revocation caller: status %d body %v, want 401 caller_user_invalidated", code, body)
	}
}
