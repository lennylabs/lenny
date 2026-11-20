// SPDX-License-Identifier: MIT

package tokenservice

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	obsaudit "github.com/lennylabs/lenny/pkg/observability/audit"
	"github.com/lennylabs/lenny/pkg/storage/issuedtokenstore"
	"github.com/lennylabs/lenny/tests/testinfra/jwtsign"
)

// identityServer builds an HMAC-signed Server over an in-memory issued-token
// store and a recording auditor, the in-memory dev path that records the
// issued row and then writes the token.exchanged row.
func identityServer(t *testing.T) (*Server, *jwt.HMACSigner, *recordingIssuedTokenStore, *recordingAuditor) {
	t.Helper()
	signer := jwt.NewHMACSigner("dev-1", []byte("dev-secret"))
	store := &recordingIssuedTokenStore{}
	auditor := &recordingAuditor{}
	srv := mustNewServer(t, Options{
		CapabilityTokenTTL: 15 * time.Minute,
		Signer:             jwtsign.HMAC(signer),
		Verifier:           signer,
		Issuer:             "https://lenny.dev.test/token",
		IssuedTokens:       store,
		Auditor:            auditor,
	})
	return srv, signer, store, auditor
}

// identityToken mints an acme user_bearer for sub with the given jti,
// scope, caller_type, and origin.
func identityToken(t *testing.T, signer *jwt.HMACSigner, sub, jti, scope, callerType, origin string) string {
	t.Helper()
	return mintToken(t, signer, jwt.Claims{
		Subject: sub, TenantID: "acme", JWTID: jti, Typ: auth.TokenUserBearer,
		Scope: scope, CallerType: callerType, Origin: origin,
	})
}

// exchangedPayloads returns the token.exchanged payloads the auditor holds.
func exchangedPayloads(t *testing.T, a *recordingAuditor) []exchangeAuditPayload {
	t.Helper()
	var out []exchangeAuditPayload
	for _, r := range a.snapshot() {
		if r.EventType != string(obsaudit.EventTokenExchanged) {
			continue
		}
		var p exchangeAuditPayload
		if err := json.Unmarshal(r.Payload, &p); err != nil {
			t.Fatalf("unmarshal token.exchanged payload: %v", err)
		}
		out = append(out, p)
	}
	return out
}

// spec: §13.3 (Credential Flow)
//
// A playground bearer presented as the subject_token or the actor_token of
// a mint is refused with 400 invalid_grant, no token is recorded, and the
// rejected token.exchanged row names the refused source. The same request
// with a subject and actor that carry no origin claim is accepted.
func TestExchangeRefusesAPlaygroundSubjectOrActor(t *testing.T) {
	srv, signer, store, auditor := identityServer(t)
	caller := identityToken(t, signer, "alice@acme.com", "alice-caller", "", "human", "")
	plain := identityToken(t, signer, "alice@acme.com", "alice-plain", "", "human", "")
	playground := identityToken(t, signer, "alice@acme.com", "alice-pg", "", "human", jwt.OriginPlayground)

	cases := []struct {
		name, subject, actor, wantReason string
	}{
		{"playground subject", playground, "", "subject_token_playground"},
		{"playground actor", plain, playground, "actor_token_playground"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			status, env := decodeExchange(t, doExchange(t, srv, caller, Request{
				GrantType: grantTypeExchange, SubjectToken: c.subject, ActorToken: c.actor,
				Scope: "tools:health:read", Audience: "lenny-gateway",
			}))
			if status != http.StatusBadRequest || env["error"] != "invalid_grant" || env["error_description"] != c.wantReason {
				t.Fatalf("status %d body %v, want 400 invalid_grant %s", status, env, c.wantReason)
			}
			if _, ok := env["access_token"]; ok {
				t.Errorf("refused exchange returned an access_token")
			}
		})
	}
	if n := len(store.snapshot()); n != 0 {
		t.Errorf("issued_tokens rows = %d, want 0", n)
	}
	payloads := exchangedPayloads(t, auditor)
	if len(payloads) != 2 {
		t.Fatalf("token.exchanged rows = %d, want 2", len(payloads))
	}
	for i, want := range []string{"rejected:subject_token_playground", "rejected:actor_token_playground"} {
		if payloads[i].PolicyResult != want || payloads[i].ErrorCode != "invalid_grant" || payloads[i].GrantedScope != nil {
			t.Errorf("row %d = %+v, want %s, invalid_grant, and no granted_scope", i, payloads[i], want)
		}
	}

	status, env := decodeExchange(t, doExchange(t, srv, caller, Request{
		GrantType: grantTypeExchange, SubjectToken: plain, ActorToken: plain,
		Scope: "tools:health:read", Audience: "lenny-gateway",
	}))
	if status != http.StatusOK {
		t.Errorf("subject and actor without origin: status %d body %v, want 200", status, env)
	}
}

// spec: §13.3 (Credential Flow)
//
// The recursive-revocation request accepts a playground bearer as its
// subject_token, because revoking it derives no token.
func TestRevocationRequestAcceptsAPlaygroundSubject(t *testing.T) {
	store := &fakeRevocationStore{revoked: []issuedtokenstore.RevokedToken{{JTI: "pg-1", Subject: "alice@acme.com", IsRoot: true}}}
	srv, signer := revocationServer(t, store, &recordingAuditor{}, nil)
	caller := identityToken(t, signer, "alice@acme.com", "alice-caller", "", "human", "")
	subject := identityToken(t, signer, "alice@acme.com", "pg-1", "", "human", jwt.OriginPlayground)

	resp := doExchange(t, srv, caller, Request{
		GrantType: grantTypeExchange, SubjectToken: subject, RequestedTokenType: revokedTokenType,
	})
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if store.gotRoot != "pg-1" {
		t.Errorf("cascade root = %q, want pg-1", store.gotRoot)
	}
}

// spec: §13.3 (Credential Flow), §25.1 (Design Philosophy and Agent Model)
//
// An exchange narrows a subject without a scope claim to the requested
// canonical set, signs that set as the scope claim, and records the subject
// as the parent of the issued token.
func TestExchangeNarrowsASubjectWithoutAScopeClaim(t *testing.T) {
	srv, signer, store, _ := identityServer(t)
	admin := identityToken(t, signer, "alice@acme.com", "alice-admin", "", "human", "")

	status, env := decodeExchange(t, doExchange(t, srv, admin, Request{
		GrantType: grantTypeExchange, SubjectToken: admin,
		Scope: "tools:health:* tools:diagnostics:read", Audience: "lenny-gateway",
	}))
	if status != http.StatusOK {
		t.Fatalf("status %d body %v, want 200", status, env)
	}
	tok, _ := env["access_token"].(string)
	claims, err := signer.Verify(tok)
	if err != nil {
		t.Fatalf("verify issued token: %v", err)
	}
	if claims.Scope != "tools:health:* tools:diagnostics:read" {
		t.Errorf("issued scope = %q, want the requested set", claims.Scope)
	}
	recs := store.snapshot()
	if len(recs) != 1 || recs[0].ParentJTI != "alice-admin" || recs[0].ActSubject != "" {
		t.Errorf("issued rows = %+v, want one row whose parent is alice-admin and act_sub empty", recs)
	}
}

// spec: §13.3 (Credential Flow)
//
// A caller whose sub differs from the subject's, or whose caller_type
// differs when both tokens carry one, is refused with 400 invalid_scope
// unless it requests a scope strictly narrower than the subject's. An equal
// scope, a request without scope, and a request for tools:* from a subject
// without a claim are refused; a narrower scope is accepted and records the
// subject as its parent. A caller without a caller_type and the same sub
// may take an equal scope.
func TestADifferentCallerMustNarrowTheSubjectScope(t *testing.T) {
	const subjectScope = "tools:health:read tools:sessions:read"
	cases := []struct {
		name                 string
		callerSub, callerTyp string
		subjectScope         string
		scope                string
		wantStatus           int
	}{
		{"other sub with an equal scope", "bob@acme.com", "human", subjectScope, subjectScope, http.StatusBadRequest},
		{"other sub without scope", "bob@acme.com", "human", subjectScope, "", http.StatusBadRequest},
		{"other sub with a narrower scope", "bob@acme.com", "human", subjectScope, "tools:health:read", http.StatusOK},
		{"other sub with tools:* from an unscoped subject", "bob@acme.com", "human", "", "tools:*", http.StatusBadRequest},
		{"other sub with a domain scope from an unscoped subject", "bob@acme.com", "human", "", "tools:health:*", http.StatusOK},
		{"other caller_type with an equal scope", "alice@acme.com", "agent", subjectScope, subjectScope, http.StatusBadRequest},
		{"other caller_type with a narrower scope", "alice@acme.com", "agent", subjectScope, "tools:sessions:read", http.StatusOK},
		{"same sub and no caller_type with an equal scope", "alice@acme.com", "", subjectScope, subjectScope, http.StatusOK},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv, signer, store, _ := identityServer(t)
			caller := identityToken(t, signer, c.callerSub, "caller-1", "", c.callerTyp, "")
			subject := identityToken(t, signer, "alice@acme.com", "alice-1", c.subjectScope, "human", "")
			status, env := decodeExchange(t, doExchange(t, srv, caller, Request{
				GrantType: grantTypeExchange, SubjectToken: subject, Scope: c.scope, Audience: "lenny-gateway",
			}))
			if status != c.wantStatus {
				t.Fatalf("status %d body %v, want %d", status, env, c.wantStatus)
			}
			if status != http.StatusOK {
				if env["error"] != "invalid_scope" || env["error_description"] != "scope_not_narrowed" {
					t.Errorf("error %v %v, want invalid_scope scope_not_narrowed", env["error"], env["error_description"])
				}
				return
			}
			recs := store.snapshot()
			if len(recs) != 1 || recs[0].ParentJTI != "alice-1" {
				t.Errorf("issued rows = %+v, want one row whose parent is alice-1", recs)
			}
		})
	}
}

// spec: §13.3 (Credential Flow)
//
// An a2a_delegation child mint by a caller other than the subject's owner
// keeps its own rules: it is accepted without a narrower scope, and its
// parent is the actor token.
func TestAChildMintByAnotherCallerIsUnaffectedByTheCallerRule(t *testing.T) {
	srv, signer, store, _ := identityServer(t)
	bob := identityToken(t, signer, "bob@acme.com", "bob-1", "tools:sessions:read", "agent", "")
	alice := identityToken(t, signer, "alice@acme.com", "alice-1", "tools:sessions:read", "human", "")

	status, env := decodeExchange(t, doExchange(t, srv, bob, Request{
		GrantType: grantTypeExchange, SubjectToken: alice, ActorToken: bob, Audience: "lenny-gateway",
	}))
	if status != http.StatusOK {
		t.Fatalf("status %d body %v, want 200", status, env)
	}
	recs := store.snapshot()
	if len(recs) != 1 || recs[0].ParentJTI != "bob-1" || recs[0].ActSubject != "bob@acme.com" {
		t.Errorf("issued rows = %+v, want one row whose parent is bob-1 and act_sub bob", recs)
	}
}

// spec: §16.7 (Section 25 Audit Events)
//
// The token.exchanged row of an accepted exchange without scope carries no
// requested_scope and the subject's scope as granted_scope; an accepted
// narrowing carries both; a rejected exchange carries requested_scope and
// no granted_scope. The payload has no scope field.
func TestTokenExchangedRowCarriesRequestedAndGrantedScope(t *testing.T) {
	srv, signer, _, auditor := identityServer(t)
	alice := identityToken(t, signer, "alice@acme.com", "alice-1", "tools:health:read tools:sessions:read", "human", "")

	for _, scope := range []string{"", "tools:health:read", "tools:pool:read"} {
		resp := doExchange(t, srv, alice, Request{
			GrantType: grantTypeExchange, SubjectToken: alice, Scope: scope, Audience: "lenny-gateway",
		})
		_ = resp.Body.Close()
	}
	payloads := exchangedPayloads(t, auditor)
	if len(payloads) != 3 {
		t.Fatalf("token.exchanged rows = %d, want 3", len(payloads))
	}
	want := []struct {
		requested, granted []string
		policy             string
	}{
		{nil, []string{"tools:health:read", "tools:sessions:read"}, "accepted"},
		{[]string{"tools:health:read"}, []string{"tools:health:read"}, "accepted"},
		{[]string{"tools:pool:read"}, nil, "rejected:scope_not_subset"},
	}
	for i, w := range want {
		p := payloads[i]
		if !reflect.DeepEqual(p.RequestedScope, w.requested) || !reflect.DeepEqual(p.GrantedScope, w.granted) || p.PolicyResult != w.policy {
			t.Errorf("row %d = requested %v granted %v policy %q, want %v %v %q", i, p.RequestedScope, p.GrantedScope, p.PolicyResult, w.requested, w.granted, w.policy)
		}
	}
	var raw map[string]any
	if err := json.Unmarshal(auditor.snapshot()[0].Payload, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := raw["scope"]; ok {
		t.Errorf("payload carries a scope field: %v", raw)
	}
	if _, ok := raw["granted_scope"]; !ok {
		t.Errorf("payload lacks granted_scope: %v", raw)
	}
}
