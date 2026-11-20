// SPDX-License-Identifier: MIT

//go:build security

package security_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/common/scopes"
	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/tokenservice"
	kmsstub "github.com/lennylabs/lenny/tests/testinfra/stubs/kms"
)

// spec: §13.3 (Credential Flow), §25.1 (Design Philosophy and Agent Model)
// diagnosis: the holder of a bearer narrowed to tools:health:read
// exchanged it on POST /v1/oauth/token without a scope parameter and
// received a token whose scope claim is broader than the bearer's, or
// absent, which the scope gates read as unrestricted. The exchange then
// widens a narrowed token to the full reach of its roles. Inspect the scope
// resolution in tokenexchange.Validate.
func TestNarrowedBearerCannotWidenItsScopeByOmittingScopeOnExchange(t *testing.T) {
	stub := kmsstub.New(t)
	ks, signer, err := jwt.LoadSigning(context.Background(), stub.AsProvider(), map[string]string{kms.AliasJWTSigning: "key-1"}, nil)
	if err != nil {
		t.Fatalf("LoadSigning: %v", err)
	}
	srv, err := tokenservice.NewServer(tokenservice.Options{
		CapabilityTokenTTL: 15 * time.Minute,
		Signer:             signer, Verifier: ks, Issuer: "https://lenny.dev.test/token",
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	now := time.Now()
	narrowed, err := signer.Sign(context.Background(), jwt.Claims{
		Subject: "alice@acme.com", TenantID: "acme", Typ: auth.TokenUserBearer,
		Roles: []auth.Role{auth.RolePlatformAdmin}, Scope: "tools:health:read",
		Audience: []string{"lenny-gateway"}, IssuedAt: now.Unix(), Expiry: now.Add(time.Hour).Unix(),
		JWTID: "jti-narrowed",
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	body := fmt.Sprintf(`{"grant_type":%q,"subject_token":%q,"audience":"lenny-gateway"}`,
		"urn:ietf:params:oauth:grant-type:token-exchange", narrowed)
	req := httptest.NewRequest(http.MethodPost, "/v1/oauth/token", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+narrowed)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", w.Code, w.Body.String())
	}

	var out tokenservice.Response
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	claims, err := ks.Verify(out.AccessToken)
	if err != nil {
		t.Fatalf("verify issued token: %v", err)
	}
	set, err := scopes.Parse(claims.Scope)
	if err != nil {
		t.Fatalf("parse issued scope %q: %v", claims.Scope, err)
	}
	if !set.Present() {
		t.Fatal("issued token carries no scope claim; the scope gates read it as unrestricted")
	}
	if claims.Scope != "tools:health:read" {
		t.Errorf("issued scope claim = %q, want tools:health:read", claims.Scope)
	}
	if set.Matches("tools:pool:write") {
		t.Error("issued token matches tools:pool:write, which the subject did not hold")
	}
}
