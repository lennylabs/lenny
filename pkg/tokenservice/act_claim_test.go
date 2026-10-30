// SPDX-License-Identifier: MIT

package tokenservice

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/tests/testinfra/jwtsign"
)

// spec: 13.3 (Credential Flow) "RFC 8693 parameter mapping to Lenny claims" actor_token, 13.3 (Credential Flow) "Lenny JWT claim structure" act, 10.2 (Authentication) "JWT claims" typ
//
// The Token Service stamps the RFC 8693 `act` claim on the token it mints
// for an exchange that carries an actor_token, naming the actor's sub,
// tenant_id, session_id, and delegation_depth, and the child is an
// a2a_delegation token at the actor's depth + 1. Every exchange without an
// actor_token (self-rotation, scope narrowing, credential-lease issuance,
// and an exchange of an a2a_delegation subject) mints a payload with no
// `act` key. F-13.3.23.
func TestMintedTokenCarriesActOnlyWithActorToken_spec_13_3(t *testing.T) {
	signer := jwt.NewHMACSigner("dev-1", []byte("dev-secret"))
	alice := mintToken(t, signer, lineageClaims("alice@acme.com", "alice-1", auth.TokenUserBearer))
	bobClaims := lineageClaims("bob@acme.com", "bob-1", auth.TokenSessionCapability)
	bobClaims.SessionID = "sess_bob"
	bobClaims.DelegationDepth = 1
	bob := mintToken(t, signer, bobClaims)
	capability := mintToken(t, signer, lineageClaims("alice@acme.com", "cap-1", auth.TokenSessionCapability))
	childClaims := lineageClaims("alice@acme.com", "child-1", auth.TokenA2ADelegation)
	childClaims.DelegationDepth = 2
	childClaims.Act = &jwt.ActorClaim{Subject: "bob@acme.com", TenantID: "acme", SessionID: "sess_bob", DelegationDepth: 1}
	child := mintToken(t, signer, childClaims)

	cases := []struct {
		name      string
		caller    string
		req       Request
		wantAct   *jwt.ActorClaim
		wantTyp   auth.TokenType
		wantDepth int
	}{
		{
			name: "delegation mint with a distinct actor", caller: bob,
			req:       Request{SubjectToken: alice, ActorToken: bob, Scope: "sessions:read", Audience: "lenny-gateway"},
			wantAct:   &jwt.ActorClaim{Subject: "bob@acme.com", TenantID: "acme", SessionID: "sess_bob", DelegationDepth: 1},
			wantTyp:   auth.TokenA2ADelegation,
			wantDepth: 2,
		},
		{
			name: "delegation mint whose actor is the subject", caller: alice,
			req:       Request{SubjectToken: alice, ActorToken: alice, Scope: "sessions:read", Audience: "lenny-gateway"},
			wantAct:   &jwt.ActorClaim{Subject: "alice@acme.com", TenantID: "acme", DelegationDepth: 0},
			wantTyp:   auth.TokenA2ADelegation,
			wantDepth: 1,
		},
		{
			name: "self rotation", caller: alice,
			req:     Request{SubjectToken: alice, Scope: "sessions:read sessions:write", Audience: "lenny-gateway"},
			wantTyp: auth.TokenUserBearer,
		},
		{
			name: "scope narrowing", caller: bob,
			req:     Request{SubjectToken: alice, Scope: "sessions:read", Audience: "lenny-gateway"},
			wantTyp: auth.TokenUserBearer,
		},
		{
			name: "credential lease issuance", caller: bob,
			req:     Request{SubjectToken: capability, Scope: "sessions:read", Audience: "lenny-gateway"},
			wantTyp: auth.TokenSessionCapability,
		},
		{
			name: "exchange of an a2a_delegation subject without an actor", caller: alice,
			req:       Request{SubjectToken: child, Scope: "sessions:read", Audience: "lenny-gateway"},
			wantTyp:   auth.TokenA2ADelegation,
			wantDepth: 2,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := mustNewServer(t, Options{
				CapabilityTokenTTL: 15 * time.Minute,
				Signer:             jwtsign.HMAC(signer),
				Verifier:           signer,
				Issuer:             "https://lenny.dev.test/token",
			})
			c.req.GrantType = grantTypeExchange
			resp := doExchange(t, srv, c.caller, c.req)
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", resp.StatusCode)
			}
			var body Response
			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			_, present := issuedPayload(t, body.AccessToken)["act"]
			if present != (c.wantAct != nil) {
				t.Fatalf("payload act present = %v, want %v", present, c.wantAct != nil)
			}
			out, err := signer.Verify(body.AccessToken)
			if err != nil {
				t.Fatalf("verify issued token: %v", err)
			}
			if !reflect.DeepEqual(out.Act, c.wantAct) {
				t.Errorf("act = %+v, want %+v", out.Act, c.wantAct)
			}
			if out.Typ != c.wantTyp {
				t.Errorf("typ = %q, want %q", out.Typ, c.wantTyp)
			}
			if out.DelegationDepth != c.wantDepth {
				t.Errorf("delegation_depth = %d, want %d", out.DelegationDepth, c.wantDepth)
			}
			if out.Subject != "alice@acme.com" {
				t.Errorf("sub = %q, want the subject alice@acme.com", out.Subject)
			}
		})
	}
}

// issuedPayload decodes the payload segment of an issued compact JWT.
func issuedPayload(t *testing.T, token string) map[string]json.RawMessage {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("issued token has %d segments, want 3", len(parts))
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("payload JSON: %v", err)
	}
	return m
}
