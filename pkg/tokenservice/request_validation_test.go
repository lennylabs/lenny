// SPDX-License-Identifier: MIT

package tokenservice

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
)

// These tests characterize the §13.3 request-prologue error envelopes
// that the R13 decomposition lifted out of the inline (*Server).handle
// body into the authenticateCaller, parseAndValidateRequest, and
// verifyExchangeTokens helpers. Each pins the exact HTTP status and RFC
// 8693 error code the prologue returns, so the extraction is provably
// behavior-preserving and the early-rejection branches carry coverage.
// spec: §13 token service (§13.3 token exchange request validation).

// rawExchange drives the handler with full control over the
// Authorization header, the Content-Type, and the request body, so the
// missing-header and malformed-body branches can be exercised. A nil
// authHeader omits the Authorization header entirely.
func rawExchange(t *testing.T, srv *Server, authHeader *string, contentType string, body []byte) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/oauth/token", bytes.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if authHeader != nil {
		req.Header.Set("Authorization", *authHeader)
	}
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	resp := w.Result()
	defer resp.Body.Close()
	var env map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&env)
	return resp.StatusCode, env
}

func newValidationTestServer() (*Server, *jwt.HMACSigner) {
	signer := jwt.NewHMACSigner("dev-1", []byte("dev-secret"))
	return NewServer(Options{
		Signer: signer,
		Issuer: "https://lenny.dev.test/token",
	}), signer
}

// spec: §13.3 — a request with no Authorization: Bearer caller token is
// rejected with 401 invalid_client before any token work runs.
func TestHandlerMissingCallerTokenReturns401(t *testing.T) {
	srv, _ := newValidationTestServer()
	b, _ := json.Marshal(Request{GrantType: grantTypeExchange, SubjectToken: "x"})

	status, env := rawExchange(t, srv, nil, "application/json", b)
	if status != http.StatusUnauthorized {
		t.Fatalf("status=%d, want 401", status)
	}
	if env["error"] != "invalid_client" {
		t.Errorf("error=%v, want invalid_client", env["error"])
	}
}

// spec: §13.3 — a request whose caller token fails verification is
// rejected with 401 invalid_client.
func TestHandlerInvalidCallerTokenReturns401(t *testing.T) {
	srv, _ := newValidationTestServer()
	bad := "Bearer not-a-real-jwt"
	b, _ := json.Marshal(Request{GrantType: grantTypeExchange, SubjectToken: "x"})

	status, env := rawExchange(t, srv, &bad, "application/json", b)
	if status != http.StatusUnauthorized {
		t.Fatalf("status=%d, want 401", status)
	}
	if env["error"] != "invalid_client" {
		t.Errorf("error=%v, want invalid_client", env["error"])
	}
}

// spec: §13.3 — a malformed JSON body is rejected with 400
// invalid_request after the caller token is verified.
func TestHandlerMalformedBodyReturns400InvalidRequest(t *testing.T) {
	srv, signer := newValidationTestServer()
	callerTok := mintValidationCaller(t, signer)
	auth := "Bearer " + callerTok

	status, env := rawExchange(t, srv, &auth, "application/json", []byte("{not json"))
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", status)
	}
	if env["error"] != "invalid_request" {
		t.Errorf("error=%v, want invalid_request", env["error"])
	}
}

// spec: §13.3 — a grant_type other than the token-exchange grant is
// rejected with 400 unsupported_grant_type.
func TestHandlerWrongGrantTypeReturns400(t *testing.T) {
	srv, signer := newValidationTestServer()
	callerTok := mintValidationCaller(t, signer)

	resp := doExchange(t, srv, callerTok, Request{
		GrantType: "authorization_code", SubjectToken: callerTok,
		Audience: "lenny-gateway",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", resp.StatusCode)
	}
	var env map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&env)
	if env["error"] != "unsupported_grant_type" {
		t.Errorf("error=%v, want unsupported_grant_type", env["error"])
	}
}

// spec: §13.3 — a request with the right grant but no subject_token is
// rejected with 400 invalid_request.
func TestHandlerMissingSubjectTokenReturns400(t *testing.T) {
	srv, signer := newValidationTestServer()
	callerTok := mintValidationCaller(t, signer)

	resp := doExchange(t, srv, callerTok, Request{
		GrantType: grantTypeExchange, SubjectToken: "",
		Audience: "lenny-gateway",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", resp.StatusCode)
	}
	var env map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&env)
	if env["error"] != "invalid_request" {
		t.Errorf("error=%v, want invalid_request", env["error"])
	}
}

// spec: §13.3 — a subject_token that fails verification is rejected with
// 400 invalid_grant.
func TestHandlerUnverifiableSubjectTokenReturns400InvalidGrant(t *testing.T) {
	srv, signer := newValidationTestServer()
	callerTok := mintValidationCaller(t, signer)

	resp := doExchange(t, srv, callerTok, Request{
		GrantType: grantTypeExchange, SubjectToken: "garbage-subject-token",
		Audience: "lenny-gateway",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", resp.StatusCode)
	}
	var env map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&env)
	if env["error"] != "invalid_grant" {
		t.Errorf("error=%v, want invalid_grant", env["error"])
	}
}

// spec: §13.3 — an actor_token that fails verification is rejected with
// 400 invalid_grant (the optional actor branch of verifyExchangeTokens).
func TestHandlerUnverifiableActorTokenReturns400InvalidGrant(t *testing.T) {
	srv, signer := newValidationTestServer()
	callerTok := mintValidationCaller(t, signer)

	resp := doExchange(t, srv, callerTok, Request{
		GrantType: grantTypeExchange, SubjectToken: callerTok,
		ActorToken: "garbage-actor-token",
		Audience:   "lenny-gateway",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", resp.StatusCode)
	}
	var env map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&env)
	if env["error"] != "invalid_grant" {
		t.Errorf("error=%v, want invalid_grant", env["error"])
	}
}

func mintValidationCaller(t *testing.T, signer *jwt.HMACSigner) string {
	t.Helper()
	farFuture := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	tok, err := signer.Sign(jwt.Claims{
		Subject: "alice@acme.com", TenantID: "acme", Typ: auth.TokenUserBearer,
		Scope: "sessions:read", Audience: []string{"lenny-gateway"},
		Expiry: farFuture,
	})
	if err != nil {
		t.Fatalf("mint caller: %v", err)
	}
	return tok
}
