// SPDX-License-Identifier: MIT

//go:build contract

// Contract tests for the POST /v1/oauth/token responses that the token
// rule fixes of change 0168 add: the step (1a) refusals of a token whose
// user is invalidated and of an exchange whose parent is at the lineage
// depth cap, and the recursive-revocation request's refusal of an
// invalidated caller and its retryable 503 REVOCATION_TIMEOUT.
package oauth_token_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/storage/issuedtokenstore"
	"github.com/lennylabs/lenny/pkg/tokenservice"
	"github.com/lennylabs/lenny/tests/testinfra/jwtsign"
)

// refusingStore is a tokenservice store whose issuance transaction returns
// mintErr and whose cascade returns cascadeErr, standing in for the
// Postgres store's step (1a) and cascade outcomes.
type refusingStore struct {
	fakeRecorder
	mintErr    error
	cascadeErr error
}

func (s *refusingStore) RecordWithAudit(ctx context.Context, tok issuedtokenstore.IssuedToken,
	_ []issuedtokenstore.SourceToken, _ string, _ json.RawMessage, _ time.Time,
) (audit.Row, error) {
	if s.mintErr != nil {
		return audit.Row{}, s.mintErr
	}
	return audit.Row{}, s.Record(ctx, tok)
}

func (s *refusingStore) RevokeLineage(context.Context, issuedtokenstore.LineageRevocation) (issuedtokenstore.CascadeResult, error) {
	return issuedtokenstore.CascadeResult{}, s.cascadeErr
}

func newRefusingServer(t *testing.T, store *refusingStore) (*httptest.Server, *jwt.HMACSigner) {
	t.Helper()
	signer := jwt.NewHMACSigner("dev-1", []byte("dev-secret"))
	srv := mustNewServer(t, tokenservice.Options{
		CapabilityTokenTTL: 15 * time.Minute,
		Signer:             jwtsign.HMAC(signer),
		Verifier:           signer,
		Issuer:             "https://lenny.dev.test/token",
		PerDialectCap:      map[string]time.Duration{"lenny-gateway": 24 * time.Hour},
		IssuedTokens:       store,
		Auditor:            &faultAuditor{},
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, signer
}

// spec: 13.3 (Credential Flow), 15.1 (REST API)
// diagnosis: a failure means POST /v1/oauth/token did not answer a step
// (1a) refusal of an invalidated user with 400 invalid_grant
// subject_user_invalidated or actor_user_invalidated, or 401
// invalid_client caller_user_invalidated, or a parent at the lineage depth
// cap with 400 invalid_grant lineage_depth_exceeded, or it returned an
// access_token. Check sourceRefusalReason and callerRefusal in
// pkg/tokenservice.
func TestOAuthTokenInvalidatedUserAndDepthRefusalsOnTheWire(t *testing.T) {
	for _, tc := range []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
		wantReason string
	}{
		{
			"invalidated subject user", &issuedtokenstore.SourceUserInvalidatedError{Role: issuedtokenstore.SourceSubjectToken},
			http.StatusBadRequest, "invalid_grant", "subject_user_invalidated",
		},
		{
			"invalidated actor user", &issuedtokenstore.SourceUserInvalidatedError{Role: issuedtokenstore.SourceActorToken},
			http.StatusBadRequest, "invalid_grant", "actor_user_invalidated",
		},
		{
			"invalidated caller user", &issuedtokenstore.SourceUserInvalidatedError{Role: issuedtokenstore.SourceCallerToken},
			http.StatusUnauthorized, "invalid_client", "caller_user_invalidated",
		},
		{
			"parent at the depth cap", &issuedtokenstore.LineageDepthError{ParentJTI: "actor-1", Depth: 32, Max: 32},
			http.StatusBadRequest, "invalid_grant", "lineage_depth_exceeded",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts, signer := newRefusingServer(t, &refusingStore{mintErr: tc.err})
			caller, req := delegationRequest(t, signer)
			resp, body := exchange(t, ts, caller, req)
			if resp.StatusCode != tc.wantStatus || body["error"] != tc.wantCode || body["error_description"] != tc.wantReason {
				t.Fatalf("status %d body %v, want %d %s %s", resp.StatusCode, body, tc.wantStatus, tc.wantCode, tc.wantReason)
			}
			if body["access_token"] != nil {
				t.Error("a refused exchange returned an access_token")
			}
		})
	}
}

// spec: 13.3 (Credential Flow), 15.1 (REST API)
// diagnosis: a failure means a recursive-revocation request whose cascade
// timed out was not answered with 503 REVOCATION_TIMEOUT in the Lenny
// error envelope with retryable true and Retry-After, or an invalidated
// caller was not answered with 401 invalid_client caller_user_invalidated.
// Check writeCascadeError in pkg/tokenservice.
func TestOAuthTokenRevocationTimeoutAndInvalidatedCallerOnTheWire(t *testing.T) {
	revoke := func(t *testing.T, cascadeErr error) (*http.Response, map[string]any) {
		t.Helper()
		ts, signer := newRefusingServer(t, &refusingStore{cascadeErr: cascadeErr})
		alice := mint(t, signer, jwt.Claims{Subject: "alice", TenantID: "acme", JWTID: "alice-1", Typ: auth.TokenUserBearer})
		return exchange(t, ts, alice, tokenservice.Request{
			GrantType:          "urn:ietf:params:oauth:grant-type:token-exchange",
			SubjectToken:       alice,
			RequestedTokenType: "urn:ietf:params:oauth:token-type:access_token:revoked",
		})
	}
	resp, body := revoke(t, issuedtokenstore.ErrRevocationTimeout)
	envelope, _ := body["error"].(map[string]any)
	if resp.StatusCode != http.StatusServiceUnavailable || envelope["code"] != "REVOCATION_TIMEOUT" || envelope["retryable"] != true {
		t.Errorf("timeout: status %d body %v, want 503 REVOCATION_TIMEOUT retryable", resp.StatusCode, body)
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Error("timeout: no Retry-After header")
	}
	resp, body = revoke(t, issuedtokenstore.ErrCallerUserInvalidated)
	if resp.StatusCode != http.StatusUnauthorized || body["error"] != "invalid_client" || body["error_description"] != "caller_user_invalidated" {
		t.Errorf("invalidated caller: status %d body %v, want 401 invalid_client caller_user_invalidated", resp.StatusCode, body)
	}
}
