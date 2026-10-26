// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/kms"
)

// writeHMACKeyFile writes a key file in the format jwt.LoadHMACKeyFile
// reads (keyId + base64 secret) and returns its path.
func writeHMACKeyFile(t *testing.T, secret string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"keyId": "k1", "secret": []byte(secret)})
	if err != nil {
		t.Fatalf("marshal key file: %v", err)
	}
	p := filepath.Join(t.TempDir(), "key.json")
	if err := os.WriteFile(p, raw, 0o600); err != nil {
		t.Fatalf("write key file: %v", err)
	}
	return p
}

// testKeySet builds the key set and signer a gateway sharing the local
// master key seed would hold, through the production construction.
func testKeySet(t *testing.T, seed byte) (*jwt.KeySet, jwt.Signer) {
	t.Helper()
	p, err := kms.NewLocal(bytes.Repeat([]byte{seed}, kms.DEKSize))
	if err != nil {
		t.Fatalf("kms.NewLocal: %v", err)
	}
	p.SetSigningReference(kms.AliasJWTSigning, "local-key-1")
	ks, signer, err := jwt.LoadSigning(context.Background(), p, map[string]string{kms.AliasJWTSigning: "local-key-1"}, nil)
	if err != nil {
		t.Fatalf("LoadSigning: %v", err)
	}
	return ks, signer
}

// opsClaims returns platform-admin claims for jti with the given issuer
// and origin.
func opsClaims(jti, issuer, origin string) jwt.Claims {
	now := time.Now()
	return jwt.Claims{
		Subject:  "alice@acme.com",
		Issuer:   issuer,
		TenantID: auth.DefaultTenantID,
		IssuedAt: now.Unix(),
		Expiry:   now.Add(time.Hour).Unix(),
		JWTID:    jti,
		Typ:      auth.TokenUserBearer,
		Roles:    []auth.Role{auth.RolePlatformAdmin},
		Origin:   origin,
	}
}

// mintKeySetToken signs claims through the key-set signer.
func mintKeySetToken(t *testing.T, signer jwt.Signer, claims jwt.Claims) string {
	t.Helper()
	tok, err := signer.Sign(context.Background(), claims)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return tok
}

// mintDevToken signs claims with the dev trust-hook HMAC key.
func mintDevToken(t *testing.T, secret string, claims jwt.Claims) string {
	t.Helper()
	tok, err := jwt.NewHMACSigner("k1", []byte(secret)).Sign(claims)
	if err != nil {
		t.Fatalf("sign dev token: %v", err)
	}
	return tok
}

// authInputsFor returns authInputs over keySet with an empty revocation set.
func authInputsFor(keySet *jwt.KeySet, hmacKeyFile, issuer string, production bool) authInputs {
	return authInputs{
		keySet:      keySet,
		revocations: newOpsRevocation(),
		hmacKeyFile: hmacKeyFile,
		issuer:      issuer,
		production:  production,
		rps:         20,
		burst:       50,
	}
}

// TestBuildAuthConfig_ProductionRequiresKey pins that --production
// without --bearer-trust-hmac-key-file authenticates through the §10.2
// key set: buildAuthConfig returns a config whose verifier is the key set
// and which accepts a Lenny-issued token. It previously refused to start
// because it required an HMAC key no component signs with.
//
// spec: 25.4 (The lenny-ops Service: Authentication), 10.2 (Authentication)
func TestBuildAuthConfig_ProductionRequiresKey(t *testing.T) {
	keySet, signer := testKeySet(t, 0x31)
	cfg, err := buildAuthConfig(authInputsFor(keySet, "", "", true))
	if err != nil {
		t.Fatalf("buildAuthConfig: %v", err)
	}
	if cfg == nil {
		t.Fatal("production returned no auth config")
	}
	if cfg.Options.Verifier != keySet {
		t.Fatalf("verifier = %T, want the key set", cfg.Options.Verifier)
	}
	if cfg.Options.RevocationScope != nil {
		t.Error("a key-set-only verifier needs no revocation scope")
	}
	if cfg.Options.AllowDevHeaders || cfg.Options.AllowDevRoles {
		t.Error("production must not admit the dev identity headers")
	}
	if cfg.RateLimiter == nil {
		t.Error("expected a rate limiter")
	}
	tok := mintKeySetToken(t, signer, opsClaims("jti-prod", "https://lenny.acme.com", ""))
	if _, err := cfg.Options.Verifier.Verify(tok); err != nil {
		t.Errorf("key-set token refused: %v", err)
	}
}

// TestBuildAuthConfig_ProductionRefusesDevTrustHook pins that the dev
// embedded-OIDC trust hook is fatal under --production.
//
// spec: 25.4 (The lenny-ops Service: Authentication), 17.4 (Dev mode guard rails)
func TestBuildAuthConfig_ProductionRefusesDevTrustHook(t *testing.T) {
	keySet, _ := testKeySet(t, 0x31)
	cfg, err := buildAuthConfig(authInputsFor(keySet, writeHMACKeyFile(t, "s"), "", true))
	if !errors.Is(err, errDevTrustInProduction) {
		t.Fatalf("err = %v, want errDevTrustInProduction", err)
	}
	if cfg != nil {
		t.Fatalf("cfg = %v, want nil on error", cfg)
	}
}

// TestBuildAuthConfig_DevWithoutKeyIsOpen pins that a non-production
// process without the trust hook serves the surface unauthenticated.
//
// spec: 25.4 (The lenny-ops Service: Authentication)
func TestBuildAuthConfig_DevWithoutKeyIsOpen(t *testing.T) {
	keySet, _ := testKeySet(t, 0x31)
	cfg, err := buildAuthConfig(authInputsFor(keySet, "", "", false))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg != nil {
		t.Fatalf("cfg = %v, want nil (unauthenticated dev surface)", cfg)
	}
}

// TestBuildAuthConfig_WithKeyAndIssuer pins that with the dev trust hook
// and --oidc-issuer-url set, the issuer check binds only the dev trust
// member: a dev token with another issuer is refused and one with the
// configured issuer is accepted, while a key-set token whose issuer
// differs is accepted, because Lenny-issued tokens verify whatever their
// issuer.
//
// spec: 25.4 (The lenny-ops Service: Authentication), 10.2 (Authentication), 17.4
func TestBuildAuthConfig_WithKeyAndIssuer(t *testing.T) {
	const secret = "ops-test-secret"
	keySet, signer := testKeySet(t, 0x31)
	cfg, err := buildAuthConfig(authInputsFor(keySet, writeHMACKeyFile(t, secret), "https://idp.acme.com", false))
	if err != nil {
		t.Fatalf("buildAuthConfig: %v", err)
	}
	if cfg == nil || cfg.Options.Verifier == nil {
		t.Fatal("expected a configured verifier")
	}
	v := cfg.Options.Verifier
	if _, err := v.Verify(mintDevToken(t, secret, opsClaims("jti-bad", "https://evil.example", ""))); err == nil {
		t.Error("dev token with a foreign issuer should be rejected")
	}
	if _, err := v.Verify(mintDevToken(t, secret, opsClaims("jti-good", "https://idp.acme.com", ""))); err != nil {
		t.Errorf("dev token with the configured issuer rejected: %v", err)
	}
	if _, err := v.Verify(mintKeySetToken(t, signer, opsClaims("jti-ks", "https://lenny.internal", ""))); err != nil {
		t.Errorf("key-set token with a different issuer rejected: %v", err)
	}
	_, foreignSigner := testKeySet(t, 0x77)
	if _, err := v.Verify(mintKeySetToken(t, foreignSigner, opsClaims("jti-foreign", "", ""))); err == nil {
		t.Error("token signed by a key outside the set should be rejected")
	}
}

// TestBuildAuthConfig_BadKeyFileErrors pins that an unreadable trust key
// file is a startup error rather than a silently open surface.
//
// spec: 25.4 (The lenny-ops Service: Authentication)
func TestBuildAuthConfig_BadKeyFileErrors(t *testing.T) {
	keySet, _ := testKeySet(t, 0x31)
	if _, err := buildAuthConfig(authInputsFor(keySet, filepath.Join(t.TempDir(), "missing.json"), "", false)); err == nil {
		t.Fatal("expected error for unreadable key file")
	}
}
