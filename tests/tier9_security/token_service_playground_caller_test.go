// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security test for the §13.3 Token Service caller-credential
// rule. The Token Service handler is built as cmd/lenny-token-service
// builds it, from one jwt.LoadSigning call over the testinfra KMS stub
// with the key set as its verifier. A playground bearer presented as the
// caller token must be refused before any token is signed, because a
// child minted from it carries no origin claim and so escapes the
// §27.3.1 logout revocation and the §27.6 caps.
package tier9_security_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/tokenservice"
	kmsstub "github.com/lennylabs/lenny/tests/testinfra/stubs/kms"
)

// signPlaygroundCase signs a user bearer for alice in acme under signer
// with origin as its §27.3 origin claim (empty for none).
func signPlaygroundCase(t *testing.T, signer jwt.Signer, jti, origin string) string {
	t.Helper()
	now := time.Now()
	tok, err := signer.Sign(context.Background(), jwt.Claims{
		Subject: "alice@acme.com", TenantID: "acme", Typ: auth.TokenUserBearer,
		Scope: "sessions:read sessions:write", Audience: []string{"lenny-gateway"},
		IssuedAt: now.Unix(), Expiry: now.Add(time.Hour).Unix(), JWTID: jti, Origin: origin,
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return tok
}

// spec: 13.3 (Credential Flow) "Client authentication on /v1/oauth/token", 27.3 (Authentication)
// diagnosis: a playground bearer was accepted as the caller credential on
// POST /v1/oauth/token, or the Token Service reached its KMS signer before
// refusing it. Either lets a holder of a playground bearer obtain a child
// token that carries no origin claim, which §27.3.1 logout revocation does
// not name and the §27.6 caps do not bind.
func TestTokenServiceRefusesPlaygroundCallerBeforeSigning_spec_13_3(t *testing.T) {
	stub := kmsstub.New(t)
	ks, signer, err := jwt.LoadSigning(context.Background(), stub.AsProvider(), map[string]string{kms.AliasJWTSigning: "key-1"}, nil)
	if err != nil {
		t.Fatalf("LoadSigning: %v", err)
	}
	srv, err := tokenservice.NewServer(tokenservice.Options{
		Signer: signer, Verifier: ks, Issuer: "https://lenny.dev.test/token",
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	caller := signPlaygroundCase(t, signer, "jti-pg-caller", jwt.OriginPlayground)
	subject := signPlaygroundCase(t, signer, "jti-subject", "")
	signsBefore := stub.SignCalls()

	body := fmt.Sprintf(`{"grant_type":%q,"subject_token":%q,"scope":"sessions:read","audience":"lenny-gateway"}`,
		"urn:ietf:params:oauth:grant-type:token-exchange", subject)
	req := httptest.NewRequest(http.MethodPost, "/v1/oauth/token", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+caller)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"invalid_client"`) {
		t.Errorf("body = %s, want error invalid_client", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "access_token") {
		t.Errorf("refusal body carries an access_token: %s", w.Body.String())
	}
	if got := stub.SignCalls() - signsBefore; got != 0 {
		t.Errorf("KMS Sign calls during the refused exchange = %d, want 0", got)
	}
}
