// SPDX-License-Identifier: MIT

//go:build contract

package oauth_token_test

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/tokenservice"
)

// spec: §13.3 (Credential Flow)
// diagnosis: a child mint whose issued scope the actor token's scope does
// not grant was accepted, or its refusal did not carry 400 invalid_scope
// with reason scope_exceeds_actor (the scenario CX-6 of change 0165's
// model). Inspect checkActorCoversChild in pkg/tokenexchange.
func TestAChildBroaderThanItsActorIsRefusedOverHTTP(t *testing.T) {
	cases := []struct {
		name, scope string
		wantStatus  int
	}{
		{"no scope from an unrestricted subject", "", http.StatusBadRequest},
		{"a value outside the actor", "tools:sessions:read", http.StatusBadRequest},
		{"a value inside the actor", "tools:health:read", http.StatusOK},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ts, signer := newTestServer(t)
			subject := mint(t, signer, jwt.Claims{Subject: "alice@acme.com", TenantID: "acme", JWTID: "alice-root", Typ: auth.TokenUserBearer})
			actor := mint(t, signer, jwt.Claims{
				Subject: "alice@acme.com", TenantID: "acme", JWTID: "alice-narrow",
				Typ: auth.TokenUserBearer, Scope: "tools:health:read",
			})
			resp, body := exchange(t, ts, actor, tokenservice.Request{
				GrantType:        "urn:ietf:params:oauth:grant-type:token-exchange",
				SubjectToken:     subject,
				SubjectTokenType: "urn:ietf:params:oauth:token-type:jwt",
				ActorToken:       actor,
				ActorTokenType:   "urn:ietf:params:oauth:token-type:jwt",
				Scope:            c.scope,
			})
			if resp.StatusCode != c.wantStatus {
				t.Fatalf("status = %d body %v, want %d", resp.StatusCode, body, c.wantStatus)
			}
			if c.wantStatus == http.StatusOK {
				return
			}
			if body["error"] != "invalid_scope" || body["error_description"] != "scope_exceeds_actor" {
				t.Errorf("error = %v %v, want invalid_scope scope_exceeds_actor", body["error"], body["error_description"])
			}
			if _, ok := body["access_token"]; ok {
				t.Error("refusal body carries an access_token")
			}
		})
	}
}

// spec: §13.3 (Credential Flow)
// diagnosis: an exchange without audience issued a token with no aud
// claim, or an exchange without an actor_token dropped the subject's act
// claim (gap G1 and the scenario CX-9 of change 0165's model). Inspect
// requestedAudience in pkg/tokenservice and issuedAct in
// pkg/tokenexchange.
func TestExchangeDefaultsTheAudienceAndKeepsActOverHTTP(t *testing.T) {
	ts, signer := newTestServer(t)
	act := &jwt.ActorClaim{Subject: "bob@acme.com", TenantID: "acme", SessionID: "sess_bob", DelegationDepth: 0}
	child := mint(t, signer, jwt.Claims{
		Subject: "alice@acme.com", TenantID: "acme", JWTID: "child-1",
		Typ: auth.TokenA2ADelegation, DelegationDepth: 1, Act: act,
		Scope: "tools:health:read tools:sessions:read",
	})
	resp, body := exchange(t, ts, child, tokenservice.Request{
		GrantType:        "urn:ietf:params:oauth:grant-type:token-exchange",
		SubjectToken:     child,
		SubjectTokenType: "urn:ietf:params:oauth:token-type:jwt",
		Scope:            "tools:health:read",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body %v, want 200", resp.StatusCode, body)
	}
	issued, err := signer.Verify(body["access_token"].(string))
	if err != nil {
		t.Fatalf("verify issued token: %v", err)
	}
	if !reflect.DeepEqual([]string(issued.Audience), []string{"lenny-gateway"}) {
		t.Errorf("aud = %v, want [lenny-gateway]", issued.Audience)
	}
	if !reflect.DeepEqual(issued.Act, act) {
		got, _ := json.Marshal(issued.Act)
		t.Errorf("act = %s, want the subject's delegator bob", got)
	}
}

// spec: §13.3 (Credential Flow)
// diagnosis: a mint whose caller token the in-lock re-read found revoked
// was not refused with 401 invalid_client and reason caller_token_revoked
// (the scenario CX-10 of change 0165's model), or it returned a token or
// committed a record. Inspect exchangeSources and rejectRevokedSource in
// pkg/tokenservice.
func TestARevokedCallerIsRefusedOverHTTP(t *testing.T) {
	store := &revokingAuditStore{revoked: map[string]bool{"caller-1": true}}
	auditor := &faultAuditor{}
	ts, signer := newSourceRevokedServer(t, store, auditor)
	caller, req := delegationRequest(t, signer)
	resp, body := exchange(t, ts, caller, req)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (body %v)", resp.StatusCode, body)
	}
	if body["error"] != "invalid_client" || body["error_description"] != "caller_token_revoked" {
		t.Errorf("error = %v %v, want invalid_client caller_token_revoked", body["error"], body["error_description"])
	}
	if body["access_token"] != nil || len(store.recorded) != 0 {
		t.Error("a refused mint returned a token or committed a record")
	}
}
