// SPDX-License-Identifier: MIT

package playground

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/gateway/storage/issuedtokenstore"
)

// fakeIssuedTokens is an IssuedTokenReader returning one fixed row or
// error, and recording the tenant and jti it was asked for.
type fakeIssuedTokens struct {
	row              issuedtokenstore.IssuedToken
	err              error
	gotTenant, gotID string
}

func (f *fakeIssuedTokens) Get(_ context.Context, tenantID, jti string) (issuedtokenstore.IssuedToken, error) {
	f.gotTenant, f.gotID = tenantID, jti
	return f.row, f.err
}

// countingSigner counts Sign calls and delegates to an HMAC signer.
type countingSigner struct {
	inner *jwt.HMACSigner
	calls atomic.Int32
}

func (c *countingSigner) Sign(_ context.Context, claims jwt.Claims) (string, error) {
	c.calls.Add(1)
	return c.inner.Sign(claims)
}

func (c *countingSigner) KeyID() string { return c.inner.KeyID() }

// postAPIKeyMint posts a pasted user_bearer to an apiKey-mode handler wired
// with issued and returns the response, the decoded envelope, the signer
// call count, and the mint rejections emitted.
func postAPIKeyMint(t *testing.T, issued IssuedTokenReader) (int, errorEnvelope, int32, []MintRejectedEvent) {
	t.Helper()
	verifier := devSigner()
	pasted, err := verifier.Sign(jwt.Claims{
		Subject: "bob", TenantID: "acme", Typ: auth.TokenUserBearer,
		JWTID: "imp-jti-1", Audience: []string{"lenny-gateway"},
		Expiry: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("sign pasted bearer: %v", err)
	}
	signer := &countingSigner{inner: devSigner()}
	audit := NewMemoryAuditEmitter()
	h := New(Config{Enabled: true, AuthMode: AuthModeAPIKey, CapabilityTokenTTL: 900 * time.Second}, Options{
		Signer: signer, Verifier: verifier, IssuedTokens: issued,
	}).WithAuditEmitter(audit)
	srv := httptest.NewServer(h.TokenRoutes())
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/playground/token", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+pasted)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST token: %v", err)
	}
	defer resp.Body.Close()
	var env errorEnvelope
	_ = json.NewDecoder(resp.Body).Decode(&env)
	return resp.StatusCode, env, signer.calls.Load(), audit.MintRejections()
}

// spec: 10.2 (Playground mint invariants, invariant 1), 13.3 (Credential Flow: Write-before-issue ordering, step (1a); Platform-admin impersonation, rule 5)
//
// Before the step (1a) read, a platform-admin impersonation bearer pasted
// into the apiKey form minted a fresh playground JWT that outlived the end
// of the impersonation session. Each branch pins the corrected outcome.
func TestPlaygroundAPIKeyMintAppliesStep1aToSubjectRow_spec_10_2(t *testing.T) {
	revokedAt := time.Now().Add(-time.Minute)
	cases := []struct {
		name       string
		reader     *fakeIssuedTokens
		wantStatus int
		wantCode   string
		wantReason string
		wantSigned bool
	}{
		{
			name:       "live impersonation row is refused",
			reader:     &fakeIssuedTokens{row: issuedtokenstore.IssuedToken{JTI: "imp-jti-1", ActSubject: "alice"}},
			wantStatus: http.StatusUnauthorized,
			wantCode:   "LENNY_PLAYGROUND_BEARER_TYPE_REJECTED",
			wantReason: "subject_token_impersonation",
		},
		{
			name:       "revoked impersonation row is refused as revoked",
			reader:     &fakeIssuedTokens{row: issuedtokenstore.IssuedToken{JTI: "imp-jti-1", ActSubject: "alice", RevokedAt: revokedAt}},
			wantStatus: http.StatusUnauthorized,
			wantCode:   "LENNY_PLAYGROUND_BEARER_TYPE_REJECTED",
			wantReason: "subject_token_revoked",
		},
		{
			name:       "revoked ordinary row is refused",
			reader:     &fakeIssuedTokens{row: issuedtokenstore.IssuedToken{JTI: "imp-jti-1", RevokedAt: revokedAt}},
			wantStatus: http.StatusUnauthorized,
			wantCode:   "LENNY_PLAYGROUND_BEARER_TYPE_REJECTED",
			wantReason: "subject_token_revoked",
		},
		{
			name:       "delegation child passes",
			reader:     &fakeIssuedTokens{row: issuedtokenstore.IssuedToken{JTI: "imp-jti-1", ActSubject: "alice", ParentJTI: "p"}},
			wantStatus: http.StatusOK,
			wantSigned: true,
		},
		{
			name:       "missing row passes",
			reader:     &fakeIssuedTokens{err: issuedtokenstore.ErrNotFound},
			wantStatus: http.StatusOK,
			wantSigned: true,
		},
		{
			name:       "read error fails closed",
			reader:     &fakeIssuedTokens{err: errors.New("postgres unavailable")},
			wantStatus: http.StatusInternalServerError,
			wantCode:   "INTERNAL_ERROR",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, env, signs, rejections := postAPIKeyMint(t, tc.reader)
			if status != tc.wantStatus {
				t.Fatalf("status = %d, want %d; error=%+v", status, tc.wantStatus, env.Error)
			}
			if tc.reader.gotTenant != "acme" || tc.reader.gotID != "imp-jti-1" {
				t.Errorf("row read for (%q, %q), want (acme, imp-jti-1)", tc.reader.gotTenant, tc.reader.gotID)
			}
			if tc.wantSigned != (signs > 0) {
				t.Errorf("signer calls = %d, want signed=%v", signs, tc.wantSigned)
			}
			if tc.wantCode != "" && env.Error.Code != tc.wantCode {
				t.Errorf("error.code = %q, want %q", env.Error.Code, tc.wantCode)
			}
			assertStep1aRejection(t, tc.wantReason, env, rejections)
		})
	}
}

// assertStep1aRejection checks the details key and the audit reason of a
// step (1a) refusal, or that nothing was audited when wantReason is empty.
func assertStep1aRejection(t *testing.T, wantReason string, env errorEnvelope, rejections []MintRejectedEvent) {
	t.Helper()
	if wantReason == "" {
		if len(rejections) != 0 {
			t.Errorf("mint rejections = %+v, want none", rejections)
		}
		return
	}
	if got := env.Error.Details["subjectTyp"]; got != string(auth.TokenUserBearer) {
		t.Errorf("details.subjectTyp = %v, want user_bearer", got)
	}
	if len(rejections) != 1 || rejections[0].InvariantViolated != wantReason {
		t.Fatalf("mint rejections = %+v, want one with reason %s", rejections, wantReason)
	}
	if rejections[0].SubjectJTI != "imp-jti-1" || rejections[0].TenantID != "acme" {
		t.Errorf("rejection names (%q, %q), want (acme, imp-jti-1)", rejections[0].TenantID, rejections[0].SubjectJTI)
	}
}

// spec: 10.2 (Playground mint invariants, invariant 1)
//
// A handler without an issued-token reader mints from a pasted user_bearer
// with no row read, because the gateway registers no impersonation route
// without a Postgres pool.
func TestPlaygroundAPIKeyMintNilIssuedTokensSkipsRead_spec_10_2(t *testing.T) {
	status, env, signs, rejections := postAPIKeyMint(t, nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; error=%+v", status, env.Error)
	}
	if signs != 1 || len(rejections) != 0 {
		t.Errorf("signer calls = %d, rejections = %d; want 1 and 0", signs, len(rejections))
	}
}
