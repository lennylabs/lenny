// SPDX-License-Identifier: MIT

//go:build security

// Security tests for the token rule fixes of change 0167, each reproducing
// a counterexample of change 0165's formal model against a real Token
// Service over a real Postgres issued-token store, and showing it is now
// refused or closed.
package security

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/common/seqname"
	"github.com/lennylabs/lenny/pkg/storage/issuedtokenstore"
	"github.com/lennylabs/lenny/pkg/tokenservice"
)

const exchangeGrant = "urn:ietf:params:oauth:grant-type:token-exchange"

// signUnrecorded signs an alice bearer for jti in the harness tenant
// without recording it, as a token minted outside /v1/oauth/token.
func (h *sourceRevokedHarness) signUnrecorded(t *testing.T, jti, scope string) string {
	t.Helper()
	now := time.Now().UTC()
	tok, err := h.signer.Sign(jwt.Claims{
		Subject: "alice@acme.com", TenantID: h.tenant, JWTID: jti, Typ: auth.TokenUserBearer,
		Scope: scope, Audience: []string{"lenny-gateway"},
		IssuedAt: now.Add(-time.Minute).Unix(), Expiry: now.Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("Sign %s: %v", jti, err)
	}
	return tok
}

// spec: §13.3 (Credential Flow)
// diagnosis: alice rotated her token T away, which revoked it, and T then
// authenticated a mint as the caller token (the scenario CX-10 of change
// 0165's model). Check that pkg/tokenservice lists the caller token in the
// step (1a) re-read and maps its revocation to 401 invalid_client.
func TestTokenServiceRefusesARevokedCallerOnAMint(t *testing.T) {
	h := newSourceRevokedHarness(t)
	rotated := h.record(t, "rotated-caller", auth.TokenUserBearer, 0)
	h.rotateAway(t, rotated)
	subject := h.record(t, "subject-1", auth.TokenUserBearer, 0)
	code, body := h.exchange(t, rotated, tokenservice.Request{
		GrantType: exchangeGrant, SubjectToken: subject, Scope: "tools:sessions:read", Audience: "lenny-gateway",
	})
	if code != http.StatusUnauthorized || body["error"] != "invalid_client" || body["error_description"] != "caller_token_revoked" {
		t.Fatalf("status %d body %v, want 401 invalid_client caller_token_revoked", code, body)
	}
	if body["access_token"] != nil {
		t.Error("a revoked caller token authenticated a mint")
	}
}

// spec: §13.3 (Credential Flow)
// diagnosis: a token with no issued_tokens row could not be named as the
// root of a recursive revocation, so the tokens derived from it stayed
// live (the scenario CX-5 of change 0165's model), or the recorded root
// was still accepted as a subject_token afterwards. Check revocationRoot in
// pkg/tokenservice and RevokeLineage in pkg/storage/issuedtokenstore.
func TestTokenServiceRevokesFromARootWithoutARow(t *testing.T) {
	h := newSourceRevokedHarness(t)
	ctx := context.Background()
	root := h.signUnrecorded(t, "rowless-root", "")
	now := time.Now().UTC()
	if err := h.store.Record(ctx, issuedtokenstore.IssuedToken{
		JTI: "derived-1", TenantID: h.tenant, Subject: "alice@acme.com", TokenHash: []byte("derived-1"),
		IssuedAt: now, ExpiresAt: now.Add(time.Hour), ParentJTI: "rowless-root",
	}); err != nil {
		t.Fatalf("Record derived: %v", err)
	}
	caller := h.record(t, "caller-1", auth.TokenUserBearer, 0)
	code, body := h.exchange(t, caller, tokenservice.Request{
		GrantType: exchangeGrant, SubjectToken: root,
		RequestedTokenType: "urn:ietf:params:oauth:token-type:access_token:revoked",
	})
	if code != http.StatusOK || body["revoked_count"] != float64(2) {
		t.Fatalf("status %d body %v, want 200 revoking the root and its derived token", code, body)
	}
	for _, jti := range []string{"rowless-root", "derived-1"} {
		row, err := h.store.Get(ctx, h.tenant, jti)
		if err != nil || !row.Revoked() {
			t.Errorf("%s: row %+v err %v, want a revoked row", jti, row, err)
		}
	}
	code, body = h.exchange(t, caller, tokenservice.Request{
		GrantType: exchangeGrant, SubjectToken: root, Scope: "tools:sessions:read", Audience: "lenny-gateway",
	})
	if code != http.StatusBadRequest || body["error_description"] != "subject_token_revoked" {
		t.Errorf("exchange of the revoked root: status %d body %v, want 400 subject_token_revoked", code, body)
	}
}

// spec: §13.3 (Credential Flow)
// diagnosis: revoking the platform-admin bearer that started an
// impersonation in another tenant left the impersonation bearer live (the
// scenario CX-4 of change 0165's model), or the revocation, whose caller
// holds no platform-admin role, wrote a cross_tenant_read event, which
// only the __all__ sentinel's platform-admin paths write (TOKX-I17). Check
// revokeImpersonationsFrom in pkg/tokenservice and RevokeImpersonationStep
// in pkg/storage/issuedtokenstore.
func TestTokenServiceRevocationEndsTheImpersonationsItReaches(t *testing.T) {
	h := newSourceRevokedHarness(t)
	ctx := context.Background()
	for _, tenant := range []string{"globex", "platform"} {
		if _, err := h.pool.Exec(ctx,
			`INSERT INTO tenants (id, genesis_nonce) VALUES ($1, $2) ON CONFLICT DO NOTHING`, tenant, []byte{0x01}); err != nil {
			t.Fatalf("seed tenant %s: %v", tenant, err)
		}
		if _, err := h.pool.Exec(ctx,
			"CREATE SEQUENCE IF NOT EXISTS "+seqname.AuditSequenceName(tenant)+" START WITH 1 INCREMENT BY 1 NO CYCLE"); err != nil {
			t.Fatalf("provision audit sequence for %s: %v", tenant, err)
		}
	}
	admin := h.record(t, "admin-bearer", auth.TokenUserBearer, 0)
	now := time.Now().UTC()
	if err := h.store.RecordImpersonation(ctx, issuedtokenstore.IssuedToken{
		JTI: "imp-1", TenantID: "globex", Subject: "bob@globex.com", TokenHash: []byte("imp-1"),
		IssuedAt: now, ExpiresAt: now.Add(30 * time.Minute), ActSubject: "alice@acme.com",
		ImpersonatorTenantID: h.tenant, ImpersonatorJTI: "admin-bearer",
	}, h.tenant); err != nil {
		t.Fatalf("RecordImpersonation: %v", err)
	}
	code, body := h.exchange(t, admin, tokenservice.Request{
		GrantType: exchangeGrant, SubjectToken: admin,
		RequestedTokenType: "urn:ietf:params:oauth:token-type:access_token:revoked",
	})
	if code != http.StatusOK || body["revoked_count"] != float64(1) {
		t.Fatalf("status %d body %v, want 200 revoking the admin bearer", code, body)
	}
	row, err := h.store.Get(ctx, "globex", "imp-1")
	if err != nil || !row.Revoked() || row.RevokedReason != "cascade_from_parent" {
		t.Errorf("impersonation row %+v err %v, want revoked with cascade_from_parent", row, err)
	}
	var events int
	if err := h.pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE event_type = 'cross_tenant_read'`).Scan(&events); err != nil {
		t.Fatalf("count cross_tenant_read events: %v", err)
	}
	if events != 0 {
		t.Errorf("cross_tenant_read events = %d, want 0", events)
	}
}

// spec: §13.3 (Credential Flow)
// diagnosis: alice's agent, holding a token narrowed to tools:me:read,
// minted a child of her unrestricted token that is broader than the agent's
// own token (the scenario CX-6 of change 0165's model). Check
// checkActorCoversChild in pkg/tokenexchange.
func TestTokenServiceRefusesAChildBroaderThanItsActor(t *testing.T) {
	h := newSourceRevokedHarness(t)
	root := h.signUnrecorded(t, "alice-root", "")
	narrow := h.signUnrecorded(t, "alice-narrow", "tools:me:read")
	code, body := h.exchange(t, narrow, tokenservice.Request{
		GrantType: exchangeGrant, SubjectToken: root, ActorToken: narrow, Audience: "lenny-gateway",
	})
	if code != http.StatusBadRequest || body["error"] != "invalid_scope" || body["error_description"] != "scope_exceeds_actor" {
		t.Fatalf("status %d body %v, want 400 invalid_scope scope_exceeds_actor", code, body)
	}
	if body["access_token"] != nil {
		t.Error("a child broader than its actor was issued")
	}
}
