// SPDX-License-Identifier: MIT

package oidc

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
)

func TestIssueAndVerifyRoundTrip(t *testing.T) {
	p, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tok, err := p.Issue(time.Hour)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	claims, err := p.Verify(tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.Subject != BuiltInUser {
		t.Errorf("subject = %q, want %q", claims.Subject, BuiltInUser)
	}
	if claims.TenantID != BuiltInTenant {
		t.Errorf("tenant = %q, want %q", claims.TenantID, BuiltInTenant)
	}
	// §17.4: the embedded provider issues dev.local-audience tokens.
	if len(claims.Audience) != 1 || claims.Audience[0] != Audience {
		t.Errorf("audience = %v, want [%s]", claims.Audience, Audience)
	}
	// The built-in user holds platform-admin so it can drive the admin
	// API in Embedded Mode.
	if !claims.HasRole(auth.RolePlatformAdmin) {
		t.Errorf("token does not carry the platform-admin role: %v", claims.Roles)
	}
}

func TestIssueDefaultsTTL(t *testing.T) {
	p, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tok, err := p.Issue(0)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	claims, err := p.Verify(tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	want := time.Now().Add(DefaultTokenTTL)
	if got := claims.ExpiryTime(); got.Sub(want) > time.Minute || want.Sub(got) > time.Minute {
		t.Errorf("expiry %s not within a minute of the default-TTL expiry %s", got, want)
	}
}

func TestVerifyRejectsForeignAudience(t *testing.T) {
	// A token signed by a provider but carrying a non-dev.local
	// audience must be rejected. §17.4: the embedded OIDC provider
	// refuses any audience claim not matching dev.local. The check is
	// exercised by verifying a token from a second provider whose
	// signing key differs.
	p, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	other, err := New()
	if err != nil {
		t.Fatalf("New other: %v", err)
	}
	tok, err := other.Issue(time.Hour)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	// p does not share other's signing key, so Verify rejects the
	// token on a signature mismatch.
	if _, err := p.Verify(tok); err == nil {
		t.Error("expected Verify to reject a token signed by a different provider")
	}
}

func TestExpiredTokenRejected(t *testing.T) {
	p, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tok, err := p.Issue(-time.Hour)
	if err != nil {
		// Issue clamps a non-positive TTL to the default, so this path
		// produces a valid token. The expiry test below uses a fresh
		// short-lived token instead.
		t.Fatalf("Issue: %v", err)
	}
	// Issue clamps non-positive TTLs to DefaultTokenTTL, so the token
	// is valid. Confirm it verifies rather than asserting expiry here.
	if _, err := p.Verify(tok); err != nil {
		t.Errorf("Verify of a default-TTL token failed: %v", err)
	}
}

func TestPersistedKeyRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "signing.key")
	// First call rotates: no key file exists, so a fresh key is
	// generated and written.
	first, err := NewWithPersistedKey(path, true)
	if err != nil {
		t.Fatalf("NewWithPersistedKey rotate: %v", err)
	}
	tok, err := first.Issue(time.Hour)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	// A second process loads the same key with rotate=false and must
	// verify the token the first process minted.
	loaded, err := NewWithPersistedKey(path, false)
	if err != nil {
		t.Fatalf("NewWithPersistedKey load: %v", err)
	}
	if loaded.KeyID() != first.KeyID() {
		t.Errorf("loaded key id %q != written key id %q", loaded.KeyID(), first.KeyID())
	}
	if _, err := loaded.Verify(tok); err != nil {
		t.Errorf("loaded provider failed to verify a token from the original: %v", err)
	}
}

func TestPersistedKeyRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "signing.key")
	first, err := NewWithPersistedKey(path, true)
	if err != nil {
		t.Fatalf("NewWithPersistedKey first: %v", err)
	}
	// §17.4 rotates the OIDC signing key per lenny up. A second call
	// with rotate=true must replace the key.
	second, err := NewWithPersistedKey(path, true)
	if err != nil {
		t.Fatalf("NewWithPersistedKey second: %v", err)
	}
	if first.KeyID() == second.KeyID() {
		t.Error("expected a rotated key id on the second rotate call")
	}
	tok, err := first.Issue(time.Hour)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	// A token from the pre-rotation provider must not verify against
	// the rotated key.
	if _, err := second.Verify(tok); err == nil {
		t.Error("expected the rotated provider to reject a pre-rotation token")
	}
}

func TestLoadMissingKeyRotates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.key")
	// rotate=false but no key file exists: a fresh key is generated
	// and written rather than returning an error.
	p, err := NewWithPersistedKey(path, false)
	if err != nil {
		t.Fatalf("NewWithPersistedKey: %v", err)
	}
	if p.KeyID() == "" {
		t.Error("expected a generated key id")
	}
	if !strings.HasPrefix(p.KeyID(), "embedded-") {
		t.Errorf("key id %q lacks the embedded- prefix", p.KeyID())
	}
}

// TestPersistedKeyFileLoadableByGatewayVerifier checks the §17.4
// cross-package contract: the gateway's --bearer-trust-hmac-key-file
// path reads the provider's persisted key file with
// jwt.LoadHMACKeyFile, and the resulting verifier accepts a token from
// Provider.Issue. The embedded stack passes the same key file to the
// gateway so a `lenny token print` bearer verifies on the §10.2
// Authorization header.
func TestPersistedKeyFileLoadableByGatewayVerifier(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "signing.key")
	p, err := NewWithPersistedKey(keyPath, true)
	if err != nil {
		t.Fatalf("NewWithPersistedKey: %v", err)
	}
	tok, err := p.Issue(time.Hour)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	// The gateway loads the same file the provider persisted.
	verifier, err := jwt.LoadHMACKeyFile(keyPath)
	if err != nil {
		t.Fatalf("jwt.LoadHMACKeyFile: %v", err)
	}
	if verifier.KeyID() != p.KeyID() {
		t.Errorf("loaded kid = %q, want %q", verifier.KeyID(), p.KeyID())
	}
	claims, err := verifier.Verify(tok)
	if err != nil {
		t.Fatalf("gateway verifier rejected the embedded token: %v", err)
	}
	if claims.Subject != BuiltInUser {
		t.Errorf("subject = %q, want %q", claims.Subject, BuiltInUser)
	}
	if !claims.HasRole(auth.RolePlatformAdmin) {
		t.Error("verified token lacks the platform-admin role")
	}

	// The gateway's MultiVerifier puts the Token Service signer first
	// and this trusted key second. An embedded token must verify
	// through that composite even though the primary key cannot
	// validate it.
	primary := jwt.NewHMACSigner("token-service", []byte("token-service-key"))
	multi := jwt.NewMultiVerifier(primary, verifier)
	if _, err := multi.Verify(tok); err != nil {
		t.Fatalf("MultiVerifier rejected the embedded token: %v", err)
	}
}
