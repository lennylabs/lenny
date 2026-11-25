// SPDX-License-Identifier: MIT

package tokenservice

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
)

// exchangeWithoutScope posts an exchange of subject, authenticated by
// caller, whose request carries no scope parameter. It returns the status,
// the decoded RFC 8693 response, and the verified claims of the issued
// token (zero when the exchange failed).
func exchangeWithoutScope(t *testing.T, srv *Server, signer *jwt.HMACSigner, caller, subject string) (int, Response, jwt.Claims) {
	t.Helper()
	resp := doExchange(t, srv, caller, Request{
		GrantType:    grantTypeExchange,
		SubjectToken: subject,
		Audience:     "lenny-gateway",
	})
	defer resp.Body.Close()
	var out Response
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, out, jwt.Claims{}
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	claims, err := signer.Verify(out.AccessToken)
	if err != nil {
		t.Fatalf("verify issued token: %v", err)
	}
	return resp.StatusCode, out, claims
}

// spec: §13.3 (Credential Flow)
func TestExchangeWithoutScopeFromANarrowedSubjectIssuesTheSubjectScope(t *testing.T) {
	store := &fakeRotationStore{}
	srv, signer := rotationServer(t, store, &recordingAuditor{}, nil)
	caller := mintToken(t, signer, jwt.Claims{
		Subject: "alice@acme.com", TenantID: "acme", JWTID: "tok-caller",
		Typ: auth.TokenUserBearer, Scope: "tools:health:read tools:sessions:read",
	})
	subject := mintToken(t, signer, jwt.Claims{
		Subject: "alice@acme.com", TenantID: "acme", JWTID: "tok-subject",
		Typ: auth.TokenUserBearer, Scope: "tools:health:read tools:sessions:read",
	})

	status, out, claims := exchangeWithoutScope(t, srv, signer, caller, subject)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	const want = "tools:health:read tools:sessions:read"
	if claims.Scope != want {
		t.Errorf("issued scope claim = %q, want %q", claims.Scope, want)
	}
	if out.Scope != want {
		t.Errorf("response scope = %q, want %q", out.Scope, want)
	}
	if len(store.minted) != 1 || !reflect.DeepEqual(store.minted[0].Scope, []string{"tools:health:read", "tools:sessions:read"}) {
		t.Errorf("issued_tokens row scope = %+v, want the subject scope", store.minted)
	}
	if store.gotPrevJTI != "" {
		t.Errorf("a scope-narrowing exchange by another caller revoked %q", store.gotPrevJTI)
	}
}

// spec: §13.3 (Credential Flow), §25.1 (Design Philosophy and Agent Model)
func TestExchangeWithoutScopeFromASubjectWithoutAScopeClaimIssuesNoClaim(t *testing.T) {
	srv, signer := newAuditTestServer(t)
	caller := mintToken(t, signer, jwt.Claims{
		Subject: "alice@acme.com", TenantID: "acme", JWTID: "tok-caller", Typ: auth.TokenUserBearer,
	})
	subject := mintToken(t, signer, jwt.Claims{
		Subject: "alice@acme.com", TenantID: "acme", JWTID: "tok-subject", Typ: auth.TokenUserBearer,
	})

	status, out, claims := exchangeWithoutScope(t, srv, signer, caller, subject)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if claims.Scope != "" {
		t.Errorf("issued scope claim = %q, want absent", claims.Scope)
	}
	if out.Scope != "" {
		t.Errorf("response scope = %q, want absent", out.Scope)
	}
}

// spec: §13.3 (Credential Flow)
func TestSelfRotationWithoutScopeFromANarrowedSubjectRevokesThePreviousToken(t *testing.T) {
	store := &fakeRotationStore{prevRevoked: true, prevSub: "alice@acme.com"}
	srv, signer := rotationServer(t, store, &recordingAuditor{}, nil)
	tok := mintToken(t, signer, jwt.Claims{
		Subject: "alice@acme.com", TenantID: "acme", JWTID: "tok-1",
		Typ: auth.TokenUserBearer, Scope: "tools:health:read",
	})

	status, _, claims := exchangeWithoutScope(t, srv, signer, tok, tok)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if claims.Scope != "tools:health:read" {
		t.Errorf("rotated scope claim = %q, want tools:health:read", claims.Scope)
	}
	if store.gotPrevJTI != "tok-1" || store.gotReason != revocationReasonRotationReplaced {
		t.Errorf("rotation revoked (%q, %q), want (tok-1, %s)", store.gotPrevJTI, store.gotReason, revocationReasonRotationReplaced)
	}
}
