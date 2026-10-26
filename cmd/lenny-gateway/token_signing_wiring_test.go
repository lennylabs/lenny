// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/kms/providerflags"
)

// tokenSigningFlags returns the flags buildTokenSigningStores reads, on
// the local KMS provider over masterKeyFile, with the dev trust hook set
// to hmacKeyFile (empty for none) and the bearer iss/aud checks set to
// issuer and audiences.
func tokenSigningFlags(masterKeyFile, hmacKeyFile, issuer, audiences string) *gatewayFlags {
	devMode := true
	tenantClaim := "tenant_id"
	return &gatewayFlags{
		devMode:                 &devMode,
		tenantIDClaim:           &tenantClaim,
		bearerTrustHMACKeyFile:  &hmacKeyFile,
		bearerExpectedIssuer:    &issuer,
		bearerExpectedAudiences: &audiences,
		kmsOpts: &providerflags.Options{
			Provider:      providerflags.ProviderLocal,
			MasterKeyFile: masterKeyFile,
			AliasToKey:    map[string]string{kms.AliasJWTSigning: "local-key-1"},
		},
	}
}

// writeGatewayHMACKeyFile persists an embedded OIDC HMAC key in the file
// format --bearer-trust-hmac-key-file reads.
func writeGatewayHMACKeyFile(t *testing.T, kid string, secret []byte) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"keyId": kid, "secret": secret})
	if err != nil {
		t.Fatalf("marshal key file: %v", err)
	}
	path := filepath.Join(t.TempDir(), "hmac.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write key file: %v", err)
	}
	return path
}

// hmacSigner adapts the embedded OIDC HMAC signer, which signs without a
// context, to jwt.Signer.
type hmacSigner struct{ *jwt.HMACSigner }

func (h hmacSigner) Sign(_ context.Context, c jwt.Claims) (string, error) {
	return h.HMACSigner.Sign(c)
}

// signedClaims signs a bearer for alice in acme with audience aud and
// issuer iss through signer.
func signedClaims(t *testing.T, signer jwt.Signer, iss, aud string) string {
	t.Helper()
	now := time.Now()
	tok, err := signer.Sign(context.Background(), jwt.Claims{
		Subject: "alice@acme.com", TenantID: "acme", Issuer: iss, Audience: []string{aud},
		IssuedAt: now.Unix(), Expiry: now.Add(time.Hour).Unix(), JWTID: "jti-" + aud,
	})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return tok
}

// TestBuildTokenSigningStoresComposesTheBearerVerifier_spec_10_2 drives
// the gateway startup step that loads the §10.2 key set and signer from
// the KMS flags. Two replicas built over one master key file share the
// signing key, so a bearer one signs verifies through the other's bearer
// verifier. With the dev trust hook set, an embedded-key token carrying
// aud=dev.local is admitted and one carrying a foreign audience is
// refused. With --bearer-expected-issuer set, a key-set token from a
// different issuer is refused.
//
// spec: 10.2 (Authentication) "Signing topology", 10.2 (Authentication) "Verifier key set and rotation", 17.4 (Local Development Mode)
func TestBuildTokenSigningStoresComposesTheBearerVerifier_spec_10_2(t *testing.T) {
	master := filepath.Join(t.TempDir(), "master.key")
	embedded := hmacSigner{jwt.NewHMACSigner("embedded-kid", []byte("embedded-signing-secret-0123456789"))}
	hmacFile := writeGatewayHMACKeyFile(t, "embedded-kid", []byte("embedded-signing-secret-0123456789"))

	a := &gatewayWiring{f: tokenSigningFlags(master, hmacFile, "", "")}
	a.buildTokenSigningStores(nil)
	b := &gatewayWiring{f: tokenSigningFlags(master, "", "", "")}
	b.buildTokenSigningStores(nil)
	if a.jwtKeySet == nil || a.jwtSigner == nil || a.bearerVerifier == nil || a.kmsProvider == nil {
		t.Fatalf("buildTokenSigningStores left the key set, signer, verifier, or provider unset: %+v", a)
	}
	if a.jwtKeySet.Signing().KeyID != b.jwtKeySet.Signing().KeyID {
		t.Fatalf("replicas over one master key file hold kids %s and %s", a.jwtKeySet.Signing().KeyID, b.jwtKeySet.Signing().KeyID)
	}
	if _, err := b.bearerVerifier.Verify(signedClaims(t, a.jwtSigner, "", "lenny")); err != nil {
		t.Errorf("a bearer signed on replica a is refused on replica b: %v", err)
	}
	if _, err := a.bearerVerifier.Verify(signedClaims(t, embedded, "", embeddedOIDCAudience)); err != nil {
		t.Errorf("an embedded-key token with aud=%s is refused: %v", embeddedOIDCAudience, err)
	}
	if _, err := a.bearerVerifier.Verify(signedClaims(t, embedded, "", "elsewhere")); err == nil {
		t.Error("an embedded-key token with a foreign audience is admitted")
	}
	if _, err := b.bearerVerifier.Verify(signedClaims(t, embedded, "", embeddedOIDCAudience)); err == nil {
		t.Error("a replica without the dev trust hook admits an embedded-key token")
	}

	c := &gatewayWiring{f: tokenSigningFlags(master, "", "https://lenny.acme.com", "lenny")}
	c.buildTokenSigningStores(nil)
	if _, err := c.bearerVerifier.Verify(signedClaims(t, c.jwtSigner, "https://lenny.acme.com", "lenny")); err != nil {
		t.Errorf("a key-set token with the expected issuer and audience is refused: %v", err)
	}
	if _, err := c.bearerVerifier.Verify(signedClaims(t, c.jwtSigner, "https://other.example", "lenny")); err == nil {
		t.Error("a key-set token from an unexpected issuer is admitted under --bearer-expected-issuer")
	}
	if got := c.expectedAuds; len(got) != 1 || got[0] != "lenny" {
		t.Errorf("expectedAuds = %v, want [lenny]", got)
	}
}
