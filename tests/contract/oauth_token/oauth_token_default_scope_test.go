// SPDX-License-Identifier: MIT

//go:build contract

package oauth_token_test

import (
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/tokenservice"
)

// spec: §13.3 (Credential Flow)
// diagnosis: POST /v1/oauth/token issued a scope other than the one the
// exchange asked for: an exchange without scope did not receive the
// subject token's scope (an empty claim is unrestricted), a subset was
// altered, or a value outside the subject's scope was accepted. Inspect
// tokenexchange.Validate and its scope resolution.
func TestExchangeScopeDefaultsToTheSubjectAndNarrowsOnlyBySubset(t *testing.T) {
	const subjectScope = "tools:health:read sessions:read"
	cases := []struct {
		name       string
		scope      string
		wantStatus int
		wantError  string
		wantScope  string
	}{
		{name: "no scope parameter", scope: "", wantStatus: http.StatusOK, wantScope: subjectScope},
		{name: "subset", scope: "sessions:read", wantStatus: http.StatusOK, wantScope: "sessions:read"},
		{name: "value outside the subject", scope: "sessions:read tools:pool:write", wantStatus: http.StatusBadRequest, wantError: "invalid_scope"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts, signer := newTestServer(t)
			caller := mint(t, signer, jwt.Claims{
				Subject: "alice@acme.com", TenantID: "acme", JWTID: "tok-caller",
				Typ: auth.TokenUserBearer, Scope: subjectScope,
			})
			subject := mint(t, signer, jwt.Claims{
				Subject: "alice@acme.com", TenantID: "acme", JWTID: "tok-subject",
				Typ: auth.TokenUserBearer, Scope: subjectScope,
			})
			resp, body := exchange(t, ts, caller, tokenservice.Request{
				GrantType:        "urn:ietf:params:oauth:grant-type:token-exchange",
				SubjectToken:     subject,
				SubjectTokenType: "urn:ietf:params:oauth:token-type:jwt",
				Scope:            tc.scope,
				Audience:         "lenny-gateway",
			})
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body %v)", resp.StatusCode, tc.wantStatus, body)
			}
			if tc.wantError != "" {
				if got, _ := body["error"].(string); got != tc.wantError {
					t.Errorf("error = %q, want %q", got, tc.wantError)
				}
				if _, issued := body["access_token"]; issued {
					t.Error("a refused exchange returned an access_token")
				}
				return
			}
			if got, _ := body["scope"].(string); got != tc.wantScope {
				t.Errorf("response scope = %q, want %q", got, tc.wantScope)
			}
			tok, _ := body["access_token"].(string)
			claims, err := signer.Verify(tok)
			if err != nil {
				t.Fatalf("verify issued token: %v", err)
			}
			if claims.Scope != tc.wantScope {
				t.Errorf("issued scope claim = %q, want %q", claims.Scope, tc.wantScope)
			}
		})
	}
}

// spec: §13.3 (Credential Flow), §25.1 (Design Philosophy and Agent Model)
// diagnosis: an exchange without scope from a subject that carries no
// scope claim issued a scope claim. The subject has no scope restriction,
// so the issued token carries none either; a claim here means the Token
// Service invented a scope the subject never had.
func TestExchangeWithoutScopeFromAnUnscopedSubjectIssuesNoScopeClaim(t *testing.T) {
	ts, signer := newTestServer(t)
	caller := mint(t, signer, jwt.Claims{
		Subject: "alice@acme.com", TenantID: "acme", JWTID: "tok-caller", Typ: auth.TokenUserBearer,
	})
	subject := mint(t, signer, jwt.Claims{
		Subject: "alice@acme.com", TenantID: "acme", JWTID: "tok-subject", Typ: auth.TokenUserBearer,
	})
	resp, body := exchange(t, ts, caller, tokenservice.Request{
		GrantType:        "urn:ietf:params:oauth:grant-type:token-exchange",
		SubjectToken:     subject,
		SubjectTokenType: "urn:ietf:params:oauth:token-type:jwt",
		Audience:         "lenny-gateway",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %v)", resp.StatusCode, body)
	}
	if _, present := body["scope"]; present {
		t.Errorf("response carries scope %v, want none", body["scope"])
	}
	tok, _ := body["access_token"].(string)
	claims, err := signer.Verify(tok)
	if err != nil {
		t.Fatalf("verify issued token: %v", err)
	}
	if claims.Scope != "" {
		t.Errorf("issued scope claim = %q, want absent", claims.Scope)
	}
}
