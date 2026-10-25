// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 contract cases for the POST /v1/oauth/token response to a
// playground-origin token. The Token Service is built as
// cmd/lenny-token-service builds it: the §10.2 key set and the
// breaker-wrapped remote ES256 signer from one jwt.LoadSigning call,
// here over the testinfra KMS stub, with the key set as the verifier.

package oauth_token_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/tokenservice"
	kmsstub "github.com/lennylabs/lenny/tests/testinfra/stubs/kms"
)

// newKeySetTestServer serves the Token Service handler over the key set
// and the shared signer, and returns the signer so the test mints its
// tokens under the same key.
func newKeySetTestServer(t *testing.T) (*httptest.Server, jwt.Signer) {
	t.Helper()
	stub := kmsstub.New(t)
	ks, signer, err := jwt.LoadSigning(context.Background(), stub.AsProvider(), map[string]string{kms.AliasJWTSigning: "key-1"}, nil)
	if err != nil {
		t.Fatalf("LoadSigning: %v", err)
	}
	srv := mustNewServer(t, tokenservice.Options{
		Signer:        signer,
		Verifier:      ks,
		Issuer:        "https://lenny.dev.test/token",
		PerDialectCap: map[string]time.Duration{"lenny-gateway": 24 * time.Hour},
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, signer
}

// mintKeySet signs a user bearer for alice in acme under the shared key,
// with origin as its §27.3 origin claim (empty for none).
func mintKeySet(t *testing.T, signer jwt.Signer, jti, origin string) string {
	t.Helper()
	now := time.Now()
	tok, err := signer.Sign(context.Background(), jwt.Claims{
		Subject:  "alice@acme.com",
		TenantID: "acme",
		Typ:      auth.TokenUserBearer,
		Scope:    "sessions:read sessions:write",
		Audience: []string{"lenny-gateway"},
		IssuedAt: now.Unix(),
		Expiry:   now.Add(time.Hour).Unix(),
		JWTID:    jti,
		Origin:   origin,
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return tok
}

// narrowing is a scope-narrowing exchange request for subject.
func narrowing(subject string) tokenservice.Request {
	return tokenservice.Request{
		GrantType:        "urn:ietf:params:oauth:grant-type:token-exchange",
		SubjectToken:     subject,
		SubjectTokenType: "urn:ietf:params:oauth:token-type:jwt",
		Scope:            "sessions:read",
		Audience:         "lenny-gateway",
	}
}

// spec: 13.3 (Credential Flow) "Client authentication on /v1/oauth/token", 27.3 (Authentication)
// diagnosis: a caller token carrying origin "playground" was accepted as
// the caller credential, or refused with a status or error other than
// 401 invalid_client, or the refusal body carried an access_token. The
// Token Service must refuse a playground bearer as the caller, because a
// child minted from it carries no origin claim and escapes the §27.3.1
// logout revocation and the §27.6 caps.
func TestOAuthTokenRefusesPlaygroundOriginCaller_spec_13_3(t *testing.T) {
	ts, signer := newKeySetTestServer(t)
	caller := mintKeySet(t, signer, "jti-pg-caller", jwt.OriginPlayground)
	subject := mintKeySet(t, signer, "jti-subject", "")

	resp, body := exchange(t, ts, caller, narrowing(subject))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body %v", resp.StatusCode, body)
	}
	if body["error"] != "invalid_client" {
		t.Errorf("error = %v, want invalid_client", body["error"])
	}
	if _, ok := body["access_token"]; ok {
		t.Errorf("refusal body carries an access_token: %v", body)
	}
}

// spec: 13.3 (Credential Flow) "Client authentication on /v1/oauth/token", 8.2 (Delegation Mechanism)
// diagnosis: a non-playground caller presenting a playground-origin
// subject_token was refused. §8.2 makes the playground bearer the
// subject_token of a delegation from a playground session, so only the
// caller token takes the origin check; an origin check that also reaches
// the subject_token breaks that delegation.
func TestOAuthTokenAcceptsPlaygroundOriginSubjectToken_spec_13_3(t *testing.T) {
	ts, signer := newKeySetTestServer(t)
	caller := mintKeySet(t, signer, "jti-caller", "")
	subject := mintKeySet(t, signer, "jti-pg-subject", jwt.OriginPlayground)

	resp, body := exchange(t, ts, caller, narrowing(subject))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %v", resp.StatusCode, body)
	}
	if tok, _ := body["access_token"].(string); tok == "" {
		t.Errorf("no access_token in 200 body: %v", body)
	}
}
