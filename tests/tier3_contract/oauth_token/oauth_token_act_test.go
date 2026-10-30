// SPDX-License-Identifier: MIT

//go:build contract

package oauth_token_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/tokenservice"
)

// accessTokenPayload decodes the payload segment of the access_token in a
// POST /v1/oauth/token response body as raw JSON, so the test reads the
// claim as it travels on the wire rather than through the jwt package.
func accessTokenPayload(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	tok, _ := body["access_token"].(string)
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("access_token has %d segments, want 3", len(parts))
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("payload JSON: %v", err)
	}
	return m
}

// spec: 13.3 (Credential Flow) "RFC 8693 parameter mapping to Lenny claims" actor_token, 13.3 (Credential Flow) "Lenny JWT claim structure" act, 10.2 (Authentication) "JWT claims" typ
// diagnosis: The child token minted for an actor_token exchange did not
//
//	carry the RFC 8693 `act` object of the actor, or a token minted
//	without an actor_token carried an `act` key. Inspect
//	tokenexchange.Validate (Issued.Act) and tokenservice.mintToken.
func TestChildTokenActClaimWireForm(t *testing.T) {
	ts, signer := newTestServer(t)
	subject := mint(t, signer, jwt.Claims{
		Subject: "alice", TenantID: "acme",
		Typ: auth.TokenUserBearer, Scope: "sessions:read",
	})
	actor := mint(t, signer, jwt.Claims{
		Subject: "bob", TenantID: "acme",
		Typ: auth.TokenSessionCapability, DelegationDepth: 1,
		Scope: "sessions:read", SessionID: "sess_parent",
	})

	resp, body := exchange(t, ts, actor, tokenservice.Request{
		GrantType:        "urn:ietf:params:oauth:grant-type:token-exchange",
		SubjectToken:     subject,
		SubjectTokenType: "urn:ietf:params:oauth:token-type:jwt",
		ActorToken:       actor,
		ActorTokenType:   "urn:ietf:params:oauth:token-type:jwt",
		Scope:            "sessions:read",
		Audience:         "lenny-gateway",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("child minting: want 200, got %d (body %v)", resp.StatusCode, body)
	}
	payload := accessTokenPayload(t, body)
	wantAct := map[string]any{
		"sub": "bob", "tenant_id": "acme",
		"session_id": "sess_parent", "delegation_depth": float64(1),
	}
	if !reflect.DeepEqual(payload["act"], wantAct) {
		t.Errorf("act = %v, want %v", payload["act"], wantAct)
	}
	if payload["sub"] != "alice" || payload["typ"] != "a2a_delegation" || payload["delegation_depth"] != float64(2) {
		t.Errorf("child sub/typ/depth = %v/%v/%v, want alice/a2a_delegation/2",
			payload["sub"], payload["typ"], payload["delegation_depth"])
	}

	resp, body = exchange(t, ts, subject, tokenservice.Request{
		GrantType:        "urn:ietf:params:oauth:grant-type:token-exchange",
		SubjectToken:     subject,
		SubjectTokenType: "urn:ietf:params:oauth:token-type:jwt",
		Scope:            "sessions:read",
		Audience:         "lenny-gateway",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rotation: want 200, got %d (body %v)", resp.StatusCode, body)
	}
	if act, ok := accessTokenPayload(t, body)["act"]; ok {
		t.Errorf("rotation payload carries act %v, want the key omitted", act)
	}
}
