// SPDX-License-Identifier: MIT

package devauth

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
)

// spec: 17.4 (the CLI mints the dev bearer for the built-in user with the
// dev.local audience and the platform-admin role).
func TestIssueAndVerifyRoundTrip(t *testing.T) {
	s, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tok, err := s.Issue(time.Hour)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	claims, err := s.Verify(tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.Subject != BuiltInUser {
		t.Errorf("subject = %q, want %q", claims.Subject, BuiltInUser)
	}
	if claims.TenantID != BuiltInTenant {
		t.Errorf("tenant = %q, want %q", claims.TenantID, BuiltInTenant)
	}
	// §17.4: the dev signer issues dev.local-audience tokens.
	if len(claims.Audience) != 1 || claims.Audience[0] != Audience {
		t.Errorf("audience = %v, want [%s]", claims.Audience, Audience)
	}
	// The built-in user holds platform-admin so it can drive the admin
	// API in Embedded Mode.
	if !claims.HasRole(auth.RolePlatformAdmin) {
		t.Errorf("token does not carry the platform-admin role: %v", claims.Roles)
	}
}

// spec: 17.4 (a non-positive TTL clamps to the short-lived default).
func TestIssueDefaultsTTL(t *testing.T) {
	s, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tok, err := s.Issue(0)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	claims, err := s.Verify(tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	want := time.Now().Add(DefaultTokenTTL)
	if got := claims.ExpiryTime(); got.Sub(want) > time.Minute || want.Sub(got) > time.Minute {
		t.Errorf("expiry %s not within a minute of the default-TTL expiry %s", got, want)
	}
}

// spec: 17.4 (a token signed by a different key is rejected).
func TestVerifyRejectsForeignSignature(t *testing.T) {
	s, err := New()
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
	// s does not share other's signing key, so Verify rejects the token
	// on a signature mismatch.
	if _, err := s.Verify(tok); err == nil {
		t.Error("expected Verify to reject a token signed by a different signer")
	}
}

// spec: 17.4 (the persisted dev key is reused across processes so `lenny
// token print` mints tokens the running stack accepts).
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
		t.Errorf("loaded signer failed to verify a token from the original: %v", err)
	}
}

// spec: 17.4 (the dev signing key rotates per `lenny up`).
func TestPersistedKeyRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "signing.key")
	first, err := NewWithPersistedKey(path, true)
	if err != nil {
		t.Fatalf("NewWithPersistedKey first: %v", err)
	}
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
	// A token from the pre-rotation signer must not verify against the
	// rotated key.
	if _, err := second.Verify(tok); err == nil {
		t.Error("expected the rotated signer to reject a pre-rotation token")
	}
}

// spec: 17.4 (a missing key file generates and writes a fresh key rather
// than erroring).
func TestLoadMissingKeyRotates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.key")
	s, err := NewWithPersistedKey(path, false)
	if err != nil {
		t.Fatalf("NewWithPersistedKey: %v", err)
	}
	if s.KeyID() == "" {
		t.Error("expected a generated key id")
	}
	if !strings.HasPrefix(s.KeyID(), "embedded-") {
		t.Errorf("key id %q lacks the embedded- prefix", s.KeyID())
	}
}

// TestPersistedKeyFileLoadableByGatewayVerifier checks the §17.4
// cross-package contract: the in-cluster gateway's
// --bearer-trust-hmac-key-file path reads the signer's persisted key file
// with jwt.LoadHMACKeyFile, and the resulting verifier accepts a token
// from Signer.Issue. `lenny up` mounts the same key file into the gateway
// pod so a CLI-minted bearer verifies on the §10.2 Authorization header.
//
// spec: 17.4 (the gateway trusts the persisted dev key through
// --bearer-trust-hmac-key-file), 10.2 (Bearer verifier).
func TestPersistedKeyFileLoadableByGatewayVerifier(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "signing.key")
	s, err := NewWithPersistedKey(keyPath, true)
	if err != nil {
		t.Fatalf("NewWithPersistedKey: %v", err)
	}
	tok, err := s.Issue(time.Hour)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	// The gateway loads the same file the signer persisted.
	verifier, err := jwt.LoadHMACKeyFile(keyPath)
	if err != nil {
		t.Fatalf("jwt.LoadHMACKeyFile: %v", err)
	}
	if verifier.KeyID() != s.KeyID() {
		t.Errorf("loaded kid = %q, want %q", verifier.KeyID(), s.KeyID())
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
	// and this trusted key second. An embedded token must verify through
	// that composite even though the primary key cannot validate it.
	primary := jwt.NewHMACSigner("token-service", []byte("token-service-key"))
	multi := jwt.NewMultiVerifier(primary, verifier)
	if _, err := multi.Verify(tok); err != nil {
		t.Fatalf("MultiVerifier rejected the embedded token: %v", err)
	}
}
