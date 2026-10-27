// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 contract tests for the POST /v1/oauth/token responses the §13.3
// write-before-issue step (1a) re-read produces: 400 invalid_grant with
// subject_token_revoked or actor_token_revoked when a source token is
// revoked between the Token Service's pre-check and the in-lock re-read,
// and 500 token_exchange_failed when the rejection audit cannot be
// written.
package oauth_token_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/gateway/storage/issuedtokenstore"
	"github.com/lennylabs/lenny/pkg/tokenservice"
	"github.com/lennylabs/lenny/tests/testinfra/jwtsign"
)

// revokingAuditStore is a tokenservice.IssuedTokenAuditStore whose
// in-transaction re-read reports the jtis in revoked as revoked, which
// models a source revoked after the Token Service verified it and before
// the issuance transaction took the §11.7 lock.
type revokingAuditStore struct {
	fakeRecorder
	revoked map[string]bool
}

func (s *revokingAuditStore) RecordWithAudit(ctx context.Context, tok issuedtokenstore.IssuedToken,
	sources []issuedtokenstore.SourceToken, _ string, _ json.RawMessage, at time.Time,
) (audit.Row, error) {
	for _, src := range sources {
		if s.revoked[src.JTI] {
			return audit.Row{}, &issuedtokenstore.SourceRevokedError{Role: src.Role, JTI: src.JTI, RevokedAt: at}
		}
	}
	return audit.Row{}, s.Record(ctx, tok)
}

// faultAuditor records token.exchanged payloads and returns err from
// every Append when err is set.
type faultAuditor struct {
	mu   sync.Mutex
	err  error
	rows []json.RawMessage
}

func (a *faultAuditor) Append(_ context.Context, _, _ string, payload json.RawMessage, _ time.Time) (audit.Row, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.err != nil {
		return audit.Row{}, a.err
	}
	a.rows = append(a.rows, append(json.RawMessage(nil), payload...))
	return audit.Row{}, nil
}

func newSourceRevokedServer(t *testing.T, store *revokingAuditStore, auditor *faultAuditor) (*httptest.Server, *jwt.HMACSigner) {
	t.Helper()
	signer := jwt.NewHMACSigner("dev-1", []byte("dev-secret"))
	srv := mustNewServer(t, tokenservice.Options{
		CapabilityTokenTTL: 15 * time.Minute,
		Signer:             jwtsign.HMAC(signer),
		Verifier:           signer,
		Issuer:             "https://lenny.dev.test/token",
		PerDialectCap:      map[string]time.Duration{"lenny-gateway": 24 * time.Hour},
		IssuedTokens:       store,
		Auditor:            auditor,
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, signer
}

// delegationRequest builds an a2a delegation exchange with subject jti
// subj-1 and actor jti actor-1.
func delegationRequest(t *testing.T, signer *jwt.HMACSigner) (caller string, req tokenservice.Request) {
	t.Helper()
	caller = mint(t, signer, jwt.Claims{
		Subject: "alice", TenantID: "acme", JWTID: "caller-1",
		Typ: auth.TokenUserBearer, Scope: "sessions:read",
	})
	subject := mint(t, signer, jwt.Claims{
		Subject: "alice", TenantID: "acme", JWTID: "subj-1",
		Typ: auth.TokenUserBearer, Scope: "sessions:read",
	})
	actor := mint(t, signer, jwt.Claims{
		Subject: "alice", TenantID: "acme", JWTID: "actor-1",
		Typ: auth.TokenSessionCapability, DelegationDepth: 1,
		Scope: "sessions:read", SessionID: "sess_parent",
	})
	return caller, tokenservice.Request{
		GrantType:        "urn:ietf:params:oauth:grant-type:token-exchange",
		SubjectToken:     subject,
		SubjectTokenType: "urn:ietf:params:oauth:token-type:jwt",
		ActorToken:       actor,
		ActorTokenType:   "urn:ietf:params:oauth:token-type:jwt",
		Scope:            "sessions:read",
		Audience:         "lenny-gateway",
	}
}

// spec: 13.3 (Credential Flow), 15.1 (REST API)
// diagnosis: a failure means POST /v1/oauth/token did not answer a source
// token revoked between the pre-check and the in-lock re-read with 400
// invalid_grant carrying the role's reason, or it returned an
// access_token or committed an issued-token record. Check
// persistIssuedToken and rejectRevokedSource in pkg/tokenservice.
func TestOAuthTokenSourceRevokedAtReReadIsInvalidGrant_spec_13_3(t *testing.T) {
	for _, tc := range []struct {
		name, revokedJTI, wantReason string
	}{
		{"subject token", "subj-1", "subject_token_revoked"},
		{"actor token", "actor-1", "actor_token_revoked"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &revokingAuditStore{revoked: map[string]bool{tc.revokedJTI: true}}
			auditor := &faultAuditor{}
			ts, signer := newSourceRevokedServer(t, store, auditor)
			caller, req := delegationRequest(t, signer)

			resp, body := exchange(t, ts, caller, req)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body %v)", resp.StatusCode, body)
			}
			if body["error"] != "invalid_grant" {
				t.Errorf("error = %v, want invalid_grant", body["error"])
			}
			if body["error_description"] != tc.wantReason {
				t.Errorf("error_description = %v, want %s", body["error_description"], tc.wantReason)
			}
			if body["access_token"] != nil {
				t.Error("a rejected exchange returned an access_token")
			}
			if len(store.recorded) != 0 {
				t.Errorf("issued-token records = %d, want 0", len(store.recorded))
			}
			if len(auditor.rows) != 1 {
				t.Fatalf("rejection audit rows = %d, want 1", len(auditor.rows))
			}
			var row struct {
				PolicyResult string `json:"policy_result"`
			}
			_ = json.Unmarshal(auditor.rows[0], &row)
			if row.PolicyResult != "rejected:"+tc.wantReason {
				t.Errorf("policy_result = %q, want rejected:%s", row.PolicyResult, tc.wantReason)
			}
		})
	}
}

// spec: 13.3 (Credential Flow), 15.1 (REST API)
// diagnosis: a failure means a source-revoked rejection whose audit write
// failed was answered with the retryable 400 rather than 500
// token_exchange_failed carrying the intended reason, so a rejection
// could go unrecorded. Check rejectRevokedSource in pkg/tokenservice.
func TestOAuthTokenSourceRevokedAuditFailureIs500_spec_13_3(t *testing.T) {
	store := &revokingAuditStore{revoked: map[string]bool{"subj-1": true}}
	ts, signer := newSourceRevokedServer(t, store, &faultAuditor{err: errors.New("audit chain unavailable")})
	caller, req := delegationRequest(t, signer)

	resp, body := exchange(t, ts, caller, req)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (body %v)", resp.StatusCode, body)
	}
	if body["error"] != "token_exchange_failed" {
		t.Errorf("error = %v, want token_exchange_failed", body["error"])
	}
	desc, _ := body["error_description"].(string)
	if want := "intended rejection: subject_token_revoked"; !strings.Contains(desc, want) {
		t.Errorf("error_description = %q, want it to carry %q", desc, want)
	}
	if body["access_token"] != nil {
		t.Error("a failed exchange returned an access_token")
	}
}
