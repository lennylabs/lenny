// SPDX-License-Identifier: MIT

package oidc_test

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/stubs/oidc"
)

// spec: 12.2.3 (Admin Plane / OIDC verifier)
// diagnosis: A discovery document missing fields or returning non-200
//
//	from the stub means the OIDC test helper is wrong. The
//	gateway's OIDC verifier expects every documented field.
func TestStubDiscovery(t *testing.T) {
	t.Parallel()
	stub := oidc.New(t)
	resp, err := http.Get(stub.Issuer() + "/.well-known/openid-configuration")
	if err != nil {
		t.Fatalf("get discovery: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("discovery status: want 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("decode discovery: %v\nbody: %s", err, body)
	}
	for _, field := range []string{"issuer", "authorization_endpoint", "token_endpoint", "jwks_uri"} {
		if _, ok := doc[field]; !ok {
			t.Errorf("discovery missing field %q", field)
		}
	}
}

// spec: 12.2.3 (JWKS exposes RSA key for token verification)
// diagnosis: The JWKS endpoint returned malformed keys. The stub's
//
//	key generation or its JWK encoding is broken.
func TestStubJWKS(t *testing.T) {
	t.Parallel()
	stub := oidc.New(t)
	resp, err := http.Get(stub.JWKSURL())
	if err != nil {
		t.Fatalf("get JWKS: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var jwks struct {
		Keys []map[string]any `json:"keys"`
	}
	if err := json.Unmarshal(body, &jwks); err != nil {
		t.Fatalf("decode JWKS: %v\nbody: %s", err, body)
	}
	if len(jwks.Keys) != 1 {
		t.Fatalf("expected 1 key, got %d", len(jwks.Keys))
	}
	for _, want := range []string{"kty", "use", "alg", "kid", "n", "e"} {
		if _, ok := jwks.Keys[0][want]; !ok {
			t.Errorf("JWK missing field %q", want)
		}
	}
}

// spec: 12.2.3 (token mint produces an RS256 JWT)
// diagnosis: MintToken returned a malformed string. The stub's
//
//	header/payload encoding or signature application is wrong.
func TestStubMintToken(t *testing.T) {
	t.Parallel()
	stub := oidc.New(t)
	tok := stub.MintToken(oidc.MintOptions{Subject: "alice", TenantID: "acme"})
	if tok == "" {
		t.Fatal("MintToken returned empty string")
	}
	// JWT shape: 3 base64url segments separated by dots.
	dots := 0
	for _, c := range tok {
		if c == '.' {
			dots++
		}
	}
	if dots != 2 {
		t.Errorf("token shape: want 3 segments / 2 dots, got %d dots", dots)
	}
}
