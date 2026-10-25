// SPDX-License-Identifier: MIT

package tokenservice

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/kms"
	kmsstub "github.com/lennylabs/lenny/tests/testinfra/stubs/kms"
)

// mustNewServer builds a Server through NewServer and fails the test
// when NewServer refuses the options.
func mustNewServer(t testing.TB, opts Options) *Server {
	t.Helper()
	srv, err := NewServer(opts)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return srv
}

// keySetServer builds a Server the way cmd/lenny-token-service does: the
// key set and the breaker-wrapped remote signer from one LoadSigning
// call over the testinfra KMS stub, with the key set as the verifier.
// It returns the server and the signer, which mints the test's caller,
// subject, and actor tokens under the same key.
func keySetServer(t *testing.T) (*Server, jwt.Signer) {
	t.Helper()
	stub := kmsstub.New(t)
	ks, signer, err := jwt.LoadSigning(context.Background(), stub.AsProvider(), map[string]string{kms.AliasJWTSigning: "key-1"}, nil)
	if err != nil {
		t.Fatalf("LoadSigning: %v", err)
	}
	return mustNewServer(t, Options{
		Signer:   signer,
		Verifier: ks,
		Issuer:   "https://lenny.dev.test/token",
	}), signer
}

// signKeySetToken signs a user bearer for alice in acme through signer,
// with origin as its §27.3 origin claim (empty for none).
func signKeySetToken(t *testing.T, signer jwt.Signer, jti, origin string) string {
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

// decodeExchange returns the status and the decoded JSON body of resp.
func decodeExchange(t *testing.T, resp *http.Response) (int, map[string]any) {
	t.Helper()
	defer resp.Body.Close()
	var env map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp.StatusCode, env
}

// narrowRequest is a scope-narrowing exchange of subject.
func narrowRequest(subject string) Request {
	return Request{
		GrantType:    grantTypeExchange,
		SubjectToken: subject,
		Scope:        "sessions:read",
		Audience:     "lenny-gateway",
	}
}

// spec: 10.2 (Authentication) "Verifier key set and rotation", 13.3 (Credential Flow)
//
// A remote signer cannot verify, so a Server built without a verifier
// would reject every token. NewServer refuses the options instead of
// falling back to the signer.
func TestNewServerRefusesNilVerifier_spec_10_2(t *testing.T) {
	_, signer := keySetServer(t)
	srv, err := NewServer(Options{Signer: signer, Issuer: "https://lenny.dev.test/token"})
	if !errors.Is(err, ErrNoVerifier) {
		t.Fatalf("NewServer err = %v, want ErrNoVerifier", err)
	}
	if srv != nil {
		t.Errorf("NewServer returned a server with no verifier")
	}
	if _, err := NewServer(Options{}); !errors.Is(err, ErrNoVerifier) {
		t.Errorf("NewServer(Options{}) err = %v, want ErrNoVerifier", err)
	}
}

// spec: 13.3 (Credential Flow) "Client authentication on /v1/oauth/token", 27.3 (Authentication)
//
// A key-set caller token carrying origin "playground" is refused with
// 401 invalid_client and no access_token, while the same token without
// the claim is accepted, so the claim is the only difference between
// the two outcomes.
func TestPlaygroundOriginCallerRefusedInvalidClient_spec_13_3(t *testing.T) {
	srv, signer := keySetServer(t)

	playground := signKeySetToken(t, signer, "jti-pg-caller", jwt.OriginPlayground)
	status, env := decodeExchange(t, doExchange(t, srv, playground, narrowRequest(playground)))
	if status != http.StatusUnauthorized {
		t.Fatalf("playground caller status = %d, want 401; body %v", status, env)
	}
	if env["error"] != "invalid_client" {
		t.Errorf("playground caller error = %v, want invalid_client", env["error"])
	}
	if _, ok := env["access_token"]; ok {
		t.Errorf("playground caller received an access_token")
	}

	plain := signKeySetToken(t, signer, "jti-plain-caller", "")
	status, env = decodeExchange(t, doExchange(t, srv, plain, narrowRequest(plain)))
	if status != http.StatusOK {
		t.Fatalf("caller without origin status = %d, want 200; body %v", status, env)
	}
	if tok, _ := env["access_token"].(string); tok == "" {
		t.Errorf("caller without origin received no access_token; body %v", env)
	}
}

// spec: 13.3 (Credential Flow) "Client authentication on /v1/oauth/token", 8.2 (Delegation Mechanism)
//
// The origin check applies to the caller token only. A caller token
// with no origin claim presenting a playground-origin subject_token,
// as a delegation from a playground session does, receives an
// access_token, which separates the caller-only refusal from an origin
// check in the verifier that also authenticates the subject_token.
func TestPlaygroundOriginSubjectTokenAccepted_spec_13_3(t *testing.T) {
	srv, signer := keySetServer(t)
	caller := signKeySetToken(t, signer, "jti-caller", "")
	subject := signKeySetToken(t, signer, "jti-pg-subject", jwt.OriginPlayground)

	status, env := decodeExchange(t, doExchange(t, srv, caller, narrowRequest(subject)))
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %v", status, env)
	}
	tok, _ := env["access_token"].(string)
	if tok == "" {
		t.Fatalf("no access_token; body %v", env)
	}
	if _, err := srv.verifier.Verify(tok); err != nil {
		t.Errorf("issued token does not verify against the key set: %v", err)
	}
}
