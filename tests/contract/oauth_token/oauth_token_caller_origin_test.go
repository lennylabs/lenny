// SPDX-License-Identifier: MIT

//go:build contract

// Contract cases for the POST /v1/oauth/token response to a
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
		CapabilityTokenTTL: 15 * time.Minute,
		Signer:             signer,
		Verifier:           ks,
		Issuer:             "https://lenny.dev.test/token",
		PerDialectCap:      map[string]time.Duration{"lenny-gateway": 24 * time.Hour},
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
		Scope:    "tools:sessions:read tools:sessions:write",
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
		Scope:            "tools:sessions:read",
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

// spec: §13.3 (Credential Flow)
// diagnosis: a mint whose subject_token or actor_token carries origin
// "playground" was accepted, or refused with a status, error, or reason
// other than 400 invalid_grant subject_token_playground or
// actor_token_playground. A token derived from a playground bearer
// carries no origin claim, so it escapes the playground logout revocation
// and the playground session caps.
func TestOAuthTokenRefusesPlaygroundOriginSourceTokens(t *testing.T) {
	ts, signer := newKeySetTestServer(t)
	caller := mintKeySet(t, signer, "jti-caller", "")
	plain := mintKeySet(t, signer, "jti-plain", "")
	playground := mintKeySet(t, signer, "jti-pg", jwt.OriginPlayground)

	childOfPlaygroundActor := narrowing(plain)
	childOfPlaygroundActor.ActorToken = playground
	childOfPlaygroundActor.ActorTokenType = "urn:ietf:params:oauth:token-type:jwt"
	for _, c := range []struct {
		name, wantReason string
		req              tokenservice.Request
	}{
		{"playground subject_token", "subject_token_playground", narrowing(playground)},
		{"playground actor_token", "actor_token_playground", childOfPlaygroundActor},
	} {
		t.Run(c.name, func(t *testing.T) {
			resp, body := exchange(t, ts, caller, c.req)
			if resp.StatusCode != http.StatusBadRequest || body["error"] != "invalid_grant" || body["error_description"] != c.wantReason {
				t.Fatalf("status = %d body %v, want 400 invalid_grant %s", resp.StatusCode, body, c.wantReason)
			}
			if _, ok := body["access_token"]; ok {
				t.Errorf("refusal body carries an access_token: %v", body)
			}
		})
	}
}
