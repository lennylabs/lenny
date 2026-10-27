// SPDX-License-Identifier: MIT

package tokenservice

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/gateway/storage/issuedtokenstore"
	obsaudit "github.com/lennylabs/lenny/pkg/observability/audit"
)

// assertOAuthRejection asserts the response status, the RFC 6749 error
// code, and that the error_description carries the reason, and that no
// access_token was returned.
func assertOAuthRejection(t *testing.T, resp *http.Response, status int, code, reason string) {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if resp.StatusCode != status {
		t.Fatalf("status = %d, want %d (body %v)", resp.StatusCode, status, body)
	}
	if body["error"] != code {
		t.Errorf("error = %v, want %s", body["error"], code)
	}
	if desc, _ := body["error_description"].(string); !strings.Contains(desc, reason) {
		t.Errorf("error_description = %q, want it to carry %q", desc, reason)
	}
	if _, ok := body["access_token"]; ok {
		t.Errorf("rejection carried an access_token: %v", body)
	}
}

// assertSingleRejectionAudit asserts that rows hold exactly one
// token.exchanged row, with the given policy_result, error_code
// invalid_grant, and no jti, because the discarded token was never
// issued.
func assertSingleRejectionAudit(t *testing.T, rows []recordedAudit, policyResult string) {
	t.Helper()
	var exchanged []exchangeAuditPayload
	for _, r := range rows {
		if r.EventType != string(obsaudit.EventTokenExchanged) {
			continue
		}
		var p exchangeAuditPayload
		if err := json.Unmarshal(r.Payload, &p); err != nil {
			t.Fatalf("unmarshal token.exchanged payload: %v", err)
		}
		exchanged = append(exchanged, p)
	}
	if len(exchanged) != 1 {
		t.Fatalf("token.exchanged rows = %d, want 1 rejection row", len(exchanged))
	}
	p := exchanged[0]
	if p.PolicyResult != policyResult {
		t.Errorf("policy_result = %q, want %q", p.PolicyResult, policyResult)
	}
	if p.ErrorCode != "invalid_grant" {
		t.Errorf("error_code = %q, want invalid_grant", p.ErrorCode)
	}
	if p.JTI != "" {
		t.Errorf("rejection row names jti %q; the discarded token was never issued", p.JTI)
	}
}

// recheckClaims returns user-bearer claims for jti in tenant acme.
func recheckClaims(jti string) jwt.Claims {
	return jwt.Claims{
		Subject: "alice@acme.com", TenantID: "acme", JWTID: jti,
		Typ: auth.TokenUserBearer, Scope: "sessions:read sessions:write", Audience: []string{"lenny-gateway"},
	}
}

// spec: 13.3 (Credential Flow), 8.2 (Delegation Mechanism) — the step
// (1a) re-read rejects an exchange whose subject or actor token is
// revoked between the pre-check and the in-lock re-read: the handler
// returns 400 invalid_grant with the role's reason, discards the signed
// token, and writes one rejection token.exchanged row.
func TestHandlerSourceRevokedAtInLockReRead_spec_13_3(t *testing.T) {
	revokedAt := time.Unix(1_700_000_000, 0)
	cases := []struct {
		name        string
		revokedJTI  string
		withActor   bool
		wantReason  string
		wantSources []issuedtokenstore.SourceToken
	}{
		{
			name:       "subject revoked on a scope narrowing",
			revokedJTI: "subj-1",
			wantReason: "subject_token_revoked",
			wantSources: []issuedtokenstore.SourceToken{
				{Role: issuedtokenstore.SourceSubjectToken, JTI: "subj-1"},
			},
		},
		{
			name:       "actor revoked on a delegation mint",
			revokedJTI: "actor-1",
			withActor:  true,
			wantReason: "actor_token_revoked",
			wantSources: []issuedtokenstore.SourceToken{
				{Role: issuedtokenstore.SourceSubjectToken, JTI: "subj-1"},
				{Role: issuedtokenstore.SourceActorToken, JTI: "actor-1"},
			},
		},
		{
			name:       "subject revoked on a delegation mint",
			revokedJTI: "subj-1",
			withActor:  true,
			wantReason: "subject_token_revoked",
			wantSources: []issuedtokenstore.SourceToken{
				{Role: issuedtokenstore.SourceSubjectToken, JTI: "subj-1"},
				{Role: issuedtokenstore.SourceActorToken, JTI: "actor-1"},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &txStore{revoked: map[string]time.Time{tc.revokedJTI: revokedAt}}
			auditor := &recordingAuditor{}
			srv, signer := rotationServer(t, store, auditor, nil)

			caller := mintToken(t, signer, recheckClaims("caller-1"))
			req := Request{
				GrantType: grantTypeExchange, SubjectToken: mintToken(t, signer, recheckClaims("subj-1")),
				Scope: "sessions:read", Audience: "lenny-gateway",
			}
			if tc.withActor {
				req.ActorToken = mintToken(t, signer, recheckClaims("actor-1"))
			}
			resp := doExchange(t, srv, caller, req)
			assertOAuthRejection(t, resp, http.StatusBadRequest, "invalid_grant", tc.wantReason)
			if !reflect.DeepEqual(store.sources, tc.wantSources) {
				t.Errorf("re-read sources = %+v, want %+v", store.sources, tc.wantSources)
			}
			if len(store.records) != 0 || len(store.audits) != 0 {
				t.Errorf("store committed records=%d audits=%d, want none", len(store.records), len(store.audits))
			}
			assertSingleRejectionAudit(t, auditor.snapshot(), "rejected:"+tc.wantReason)
		})
	}
}

// spec: 13.3 (Credential Flow) — when the rejection audit for a revoked
// source cannot be written, the handler fails closed with 500
// token_exchange_failed and the intended reason in the description,
// rather than the retryable 400.
func TestHandlerSourceRevokedRejectionAuditFailure_spec_13_3(t *testing.T) {
	for _, tc := range []struct {
		name, revokedJTI, wantReason string
	}{
		{"subject", "subj-1", "subject_token_revoked"},
		{"actor", "actor-1", "actor_token_revoked"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &txStore{revoked: map[string]time.Time{tc.revokedJTI: time.Unix(1_700_000_000, 0)}}
			srv, signer := rotationServer(t, store, &failingAuditor{err: errors.New("audit chain unavailable")}, nil)

			resp := doExchange(t, srv, mintToken(t, signer, recheckClaims("caller-1")), Request{
				GrantType:    grantTypeExchange,
				SubjectToken: mintToken(t, signer, recheckClaims("subj-1")),
				ActorToken:   mintToken(t, signer, recheckClaims("actor-1")),
				Scope:        "sessions:read", Audience: "lenny-gateway",
			})
			assertOAuthRejection(t, resp, http.StatusInternalServerError, "token_exchange_failed", tc.wantReason)
		})
	}
}

// spec: 13.3 (Credential Flow) — an exchange whose sources are live
// passes the re-read and mints; the store receives the subject jti.
func TestHandlerLiveSourcesPassReRead_spec_13_3(t *testing.T) {
	store := &txStore{revoked: map[string]time.Time{"other-jti": time.Unix(1_700_000_000, 0)}}
	srv, signer := rotationServer(t, store, &recordingAuditor{}, nil)

	resp := doExchange(t, srv, mintToken(t, signer, recheckClaims("caller-1")), Request{
		GrantType:    grantTypeExchange,
		SubjectToken: mintToken(t, signer, recheckClaims("subj-1")),
		Scope:        "sessions:read", Audience: "lenny-gateway",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if len(store.records) != 1 {
		t.Errorf("records = %d, want 1", len(store.records))
	}
	want := []issuedtokenstore.SourceToken{{Role: issuedtokenstore.SourceSubjectToken, JTI: "subj-1"}}
	if !reflect.DeepEqual(store.sources, want) {
		t.Errorf("re-read sources = %+v, want %+v", store.sources, want)
	}
}

// spec: 13.3 (Credential Flow) — SourceRevokedError.Reason is the role
// followed by _revoked.
func TestSourceRevokedErrorReason_spec_13_3(t *testing.T) {
	for role, want := range map[issuedtokenstore.SourceRole]string{
		issuedtokenstore.SourceSubjectToken: "subject_token_revoked",
		issuedtokenstore.SourceActorToken:   "actor_token_revoked",
	} {
		e := &issuedtokenstore.SourceRevokedError{Role: role, JTI: "j", RevokedAt: time.Unix(0, 0)}
		if got := e.Reason(); got != want {
			t.Errorf("Reason(%s) = %q, want %q", role, got, want)
		}
		if !strings.Contains(e.Error(), string(role)) {
			t.Errorf("Error() = %q, want it to name %s", e.Error(), role)
		}
	}
}

// spec: 13.3 (Credential Flow: Write-before-issue ordering, step (1a);
// Platform-admin impersonation, rule 5) — before the impersonation
// refusal, an impersonation bearer presented as subject_token or
// actor_token minted a token that outlived the end of the session. The
// handler now returns 400 invalid_grant with the role's impersonation
// reason, commits nothing, and writes one rejection token.exchanged row;
// a revoked actor still outranks a live impersonation subject.
func TestHandlerRefusesImpersonationSource_spec_13_3(t *testing.T) {
	revokedAt := time.Unix(1_700_000_000, 0)
	cases := []struct {
		name          string
		impersonation map[string]bool
		revoked       map[string]time.Time
		wantReason    string
	}{
		{name: "impersonation subject", impersonation: map[string]bool{"subj-1": true}, wantReason: "subject_token_impersonation"},
		{name: "impersonation actor", impersonation: map[string]bool{"actor-1": true}, wantReason: "actor_token_impersonation"},
		{
			name:          "revoked actor outranks impersonation subject",
			impersonation: map[string]bool{"subj-1": true},
			revoked:       map[string]time.Time{"actor-1": revokedAt},
			wantReason:    "actor_token_revoked",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &txStore{revoked: tc.revoked, impersonation: tc.impersonation}
			auditor := &recordingAuditor{}
			srv, signer := rotationServer(t, store, auditor, nil)

			resp := doExchange(t, srv, mintToken(t, signer, recheckClaims("caller-1")), Request{
				GrantType:    grantTypeExchange,
				SubjectToken: mintToken(t, signer, recheckClaims("subj-1")),
				ActorToken:   mintToken(t, signer, recheckClaims("actor-1")),
				Scope:        "sessions:read", Audience: "lenny-gateway",
			})
			assertOAuthRejection(t, resp, http.StatusBadRequest, "invalid_grant", tc.wantReason)
			if len(store.records) != 0 || len(store.audits) != 0 {
				t.Errorf("store committed records=%d audits=%d, want none", len(store.records), len(store.audits))
			}
			assertSingleRejectionAudit(t, auditor.snapshot(), "rejected:"+tc.wantReason)
		})
	}
}

// spec: 13.3 (Credential Flow: Write-before-issue ordering) — when the
// rejection audit for an impersonation source cannot be written, the
// handler fails closed with 500 token_exchange_failed carrying the reason.
func TestHandlerImpersonationRejectionAuditFailure_spec_13_3(t *testing.T) {
	store := &txStore{impersonation: map[string]bool{"subj-1": true}}
	srv, signer := rotationServer(t, store, &failingAuditor{err: errors.New("audit chain unavailable")}, nil)
	resp := doExchange(t, srv, mintToken(t, signer, recheckClaims("caller-1")), Request{
		GrantType:    grantTypeExchange,
		SubjectToken: mintToken(t, signer, recheckClaims("subj-1")),
		Scope:        "sessions:read", Audience: "lenny-gateway",
	})
	assertOAuthRejection(t, resp, http.StatusInternalServerError, "token_exchange_failed", "subject_token_impersonation")
}
