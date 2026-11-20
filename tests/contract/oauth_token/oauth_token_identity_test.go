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

// spec: §13.3 (Credential Flow), §25.1 (Design Philosophy and Agent Model)
// diagnosis: POST /v1/oauth/token did not read the requested scope by the
// scope grammar: a narrowing of a subject without a scope claim, or of a
// tools:<domain>:* subject, was refused, a value outside the grant or a
// non-canonical value was accepted, or the response scope differs from the
// request. Inspect tokenexchange.Validate and grantsAll.
func TestExchangeNarrowsByTheScopeGrammarOverHTTP(t *testing.T) {
	cases := []struct {
		name, subjectScope, scope string
		wantStatus                int
	}{
		{"unscoped subject to a domain wildcard and a scope", "", "tools:health:* tools:diagnostics:read", http.StatusOK},
		{"domain wildcard subject to one action", "tools:pool:*", "tools:pool:scale", http.StatusOK},
		{"domain wildcard subject to another domain", "tools:pool:*", "tools:health:read", http.StatusBadRequest},
		{"unscoped subject to a non-canonical value", "", "sessions:read", http.StatusBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ts, signer := newTestServer(t)
			subject := mint(t, signer, jwt.Claims{
				Subject: "alice@acme.com", TenantID: "acme", JWTID: "alice-1",
				Typ: auth.TokenUserBearer, Scope: c.subjectScope,
			})
			resp, body := exchange(t, ts, subject, tokenservice.Request{
				GrantType:        "urn:ietf:params:oauth:grant-type:token-exchange",
				SubjectToken:     subject,
				SubjectTokenType: "urn:ietf:params:oauth:token-type:jwt",
				Scope:            c.scope,
				Audience:         "lenny-gateway",
			})
			if resp.StatusCode != c.wantStatus {
				t.Fatalf("status = %d body %v, want %d", resp.StatusCode, body, c.wantStatus)
			}
			if c.wantStatus != http.StatusOK {
				if body["error"] != "invalid_scope" {
					t.Errorf("error = %v, want invalid_scope", body["error"])
				}
				return
			}
			if got, _ := body["scope"].(string); got != c.scope {
				t.Errorf("response scope = %q, want %q", got, c.scope)
			}
		})
	}
}

// spec: §13.3 (Credential Flow)
// diagnosis: a caller whose sub differs from the subject token's obtained
// a token at the subject's scope (equal scope or no scope), or was refused
// a strictly narrower scope, or the refusal was not 400 invalid_scope
// scope_not_narrowed. A different caller must narrow, so it cannot copy
// another user's token with a fresh jti. Inspect checkCallerIdentity.
func TestADifferentCallerMustNarrowOverHTTP(t *testing.T) {
	const subjectScope = "tools:health:read tools:sessions:read"
	cases := []struct {
		name, scope string
		wantStatus  int
	}{
		{"equal scope", subjectScope, http.StatusBadRequest},
		{"no scope", "", http.StatusBadRequest},
		{"strictly narrower scope", "tools:health:read", http.StatusOK},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ts, signer := newTestServer(t)
			bob := mint(t, signer, jwt.Claims{Subject: "bob@acme.com", TenantID: "acme", JWTID: "bob-1", Typ: auth.TokenUserBearer})
			alice := mint(t, signer, jwt.Claims{
				Subject: "alice@acme.com", TenantID: "acme", JWTID: "alice-1",
				Typ: auth.TokenUserBearer, Scope: subjectScope,
			})
			resp, body := exchange(t, ts, bob, tokenservice.Request{
				GrantType:        "urn:ietf:params:oauth:grant-type:token-exchange",
				SubjectToken:     alice,
				SubjectTokenType: "urn:ietf:params:oauth:token-type:jwt",
				Scope:            c.scope,
				Audience:         "lenny-gateway",
			})
			if resp.StatusCode != c.wantStatus {
				t.Fatalf("status = %d body %v, want %d", resp.StatusCode, body, c.wantStatus)
			}
			if c.wantStatus != http.StatusOK {
				if body["error"] != "invalid_scope" || body["error_description"] != "scope_not_narrowed" {
					t.Errorf("error = %v %v, want invalid_scope scope_not_narrowed", body["error"], body["error_description"])
				}
				if _, ok := body["access_token"]; ok {
					t.Errorf("refusal body carries an access_token")
				}
				return
			}
			if got, _ := body["scope"].(string); got != c.scope {
				t.Errorf("response scope = %q, want %q", got, c.scope)
			}
		})
	}
}
