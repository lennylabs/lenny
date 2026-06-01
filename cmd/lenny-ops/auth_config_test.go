// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/lennylabs/lenny/pkg/auth/jwt"
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

// spec: §25.4 line 1562 — a production lenny-ops with no verify key
// configured is a fatal misconfiguration; buildAuthConfig reports an
// error so the binary refuses to serve the platform-admin surface
// anonymously.
func TestBuildAuthConfig_ProductionRequiresKey(t *testing.T) {
	cfg, err := buildAuthConfig("", "", false, true, 20, 50)
	if err == nil {
		t.Fatal("expected error when production has no bearer trust key")
	}
	if cfg != nil {
		t.Fatalf("cfg = %v, want nil on error", cfg)
	}
}

// spec: §25.4 line 1562 — outside production, a missing key leaves the
// surface unauthenticated (nil config, no error). This pins the dev
// fallback.
func TestBuildAuthConfig_DevWithoutKeyIsOpen(t *testing.T) {
	cfg, err := buildAuthConfig("", "", false, false, 20, 50)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg != nil {
		t.Fatalf("cfg = %v, want nil (unauthenticated dev surface)", cfg)
	}
}

// spec: §25.4 lines 1562-1564 / §17 security.oidc.issuerUrl — a
// configured key yields a verifier; when an issuer is set the verifier
// rejects a bearer whose iss claim differs and admits a matching one.
func TestBuildAuthConfig_WithKeyAndIssuer(t *testing.T) {
	const secret = "ops-test-secret"
	cfg, err := buildAuthConfig(writeHMACKeyFile(t, secret), "https://idp.acme.com", false, true, 20, 50)
	if err != nil {
		t.Fatalf("buildAuthConfig: %v", err)
	}
	if cfg == nil || cfg.Options.Verifier == nil {
		t.Fatal("expected a configured verifier")
	}
	if cfg.RateLimiter == nil {
		t.Fatal("expected a rate limiter")
	}

	signer := jwt.NewHMACSigner("k1", []byte(secret))
	bad, _ := signer.Sign(jwt.Claims{Subject: "alice@acme.com", Issuer: "https://evil.example"})
	if _, verr := cfg.Options.Verifier.Verify(bad); verr == nil {
		t.Error("issuer mismatch should be rejected")
	}
	good, _ := signer.Sign(jwt.Claims{Subject: "alice@acme.com", Issuer: "https://idp.acme.com"})
	if _, verr := cfg.Options.Verifier.Verify(good); verr != nil {
		t.Errorf("matching issuer rejected: %v", verr)
	}
}

// spec: §25.4 line 1562 — a key file path that does not resolve to a
// valid HMAC key is a startup error rather than a silently-open surface.
func TestBuildAuthConfig_BadKeyFileErrors(t *testing.T) {
	if _, err := buildAuthConfig(filepath.Join(t.TempDir(), "missing.json"), "", false, false, 20, 50); err == nil {
		t.Fatal("expected error for unreadable key file")
	}
}
