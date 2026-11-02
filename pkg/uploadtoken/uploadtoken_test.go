// SPDX-License-Identifier: MIT

package uploadtoken_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/uploadtoken"
)

// spec: §7.1 uploadToken format + security properties.

// issue mints a token from a fixed-clock issuer and returns the token and the
// row fields the gateway persists.
func issue(t *testing.T, sessionID string, ttl time.Duration, now time.Time) (string, uploadtoken.ParsedToken) {
	t.Helper()
	tok, parsed, err := uploadtoken.NewIssuer(func() time.Time { return now }).IssueDetailed(sessionID, ttl)
	if err != nil {
		t.Fatalf("IssueDetailed: %v", err)
	}
	return tok, parsed
}

func verifierAt(now time.Time) *uploadtoken.Verifier {
	return uploadtoken.NewVerifier(func() time.Time { return now })
}

// sha256Hex is the row digest of tok.
func sha256Hex(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

var testNow = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// spec: §7.1 (uploadToken format and security properties)
func TestIssueAndVerifyRoundTrip(t *testing.T) {
	tok, parsed := issue(t, "sess_abc", time.Minute, testNow)
	parts := strings.Split(tok, ".")
	if len(parts) != 3 || parts[0] != "sess_abc" {
		t.Errorf("wire format: got %q", tok)
	}
	if parsed.SessionID != "sess_abc" {
		t.Errorf("parsed sessionID: got %q", parsed.SessionID)
	}
	if !parsed.Expiry.Equal(testNow.Add(time.Minute)) {
		t.Errorf("expiry: got %v, want %v", parsed.Expiry, testNow.Add(time.Minute))
	}
	if parsed.Digest != sha256Hex(tok) {
		t.Errorf("digest: got %q, want the SHA-256 hex of the whole token", parsed.Digest)
	}
	if err := verifierAt(testNow).Verify(tok, "sess_abc", parsed.Digest, parsed.Expiry); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// spec: §7.1 (uploadToken format and security properties)
func TestIssueDefaultsToSpecTTL(t *testing.T) {
	_, parsed := issue(t, "sess_abc", 0, testNow)
	if !parsed.Expiry.Equal(testNow.Add(uploadtoken.DefaultTTL)) {
		t.Errorf("default TTL: got expiry %v, want %v", parsed.Expiry, testNow.Add(uploadtoken.DefaultTTL))
	}
}

// spec: §7.1 (uploadToken format and security properties)
func TestIssuedSecretIs256RandomBits_spec_7_1(t *testing.T) {
	first, _ := issue(t, "sess_abc", time.Minute, testNow)
	second, _ := issue(t, "sess_abc", time.Minute, testNow)
	if first == second {
		t.Error("two tokens issued for one session at one instant are equal")
	}
	secret := strings.Split(first, ".")[2]
	if len(secret) != 64 {
		t.Errorf("third segment has %d characters, want 64", len(secret))
	}
	if _, err := hex.DecodeString(secret); err != nil {
		t.Errorf("third segment is not hexadecimal: %v", err)
	}
}

// spec: §7.1 (uploadToken format and security properties)
func TestVerifyRejectsExpiredToken(t *testing.T) {
	tok, parsed := issue(t, "sess_abc", time.Second, testNow)
	err := verifierAt(testNow.Add(10*time.Second)).Verify(tok, "sess_abc", parsed.Digest, parsed.Expiry)
	if !errors.Is(err, uploadtoken.ErrExpired) {
		t.Errorf("Verify: got %v, want ErrExpired", err)
	}
}

// spec: §7.1 (uploadToken format and security properties)
func TestVerifyExpiryBoundaryUsesRowExpiry_spec_7_1(t *testing.T) {
	tok, parsed := issue(t, "sess_abc", time.Hour, testNow)
	rowExpiry := testNow.Add(time.Minute)
	if err := verifierAt(rowExpiry).Verify(tok, "sess_abc", parsed.Digest, rowExpiry); err != nil {
		t.Errorf("Verify at the rowExpiry instant: %v", err)
	}
	// The embedded expiry is an hour out, so this refusal proves the row
	// value is the one enforced.
	err := verifierAt(rowExpiry.Add(time.Second)).Verify(tok, "sess_abc", parsed.Digest, rowExpiry)
	if !errors.Is(err, uploadtoken.ErrExpired) {
		t.Errorf("Verify one second past rowExpiry: got %v, want ErrExpired", err)
	}
}

// spec: §7.1 (uploadToken format and security properties)
func TestVerifyRejectsSessionMismatch(t *testing.T) {
	tok, parsed := issue(t, "sess_abc", time.Minute, testNow)
	err := verifierAt(testNow).Verify(tok, "sess_xyz", parsed.Digest, parsed.Expiry)
	if !errors.Is(err, uploadtoken.ErrSessionMismatch) {
		t.Errorf("Verify: got %v, want ErrSessionMismatch", err)
	}
}

// A guessed secret naming a foreign session reaches the scope check ahead of
// the digest check, so it answers 403 MISMATCH rather than 401 INVALID.
//
// spec: §7.1 (Check order)
func TestVerifyScopeRunsAheadOfDigest_spec_7_1(t *testing.T) {
	guessed := "sess_other.1893456000." + strings.Repeat("ab", 32)
	_, parsed := issue(t, "sess_abc", time.Minute, testNow)
	err := verifierAt(testNow).Verify(guessed, "sess_abc", parsed.Digest, parsed.Expiry)
	if !errors.Is(err, uploadtoken.ErrSessionMismatch) {
		t.Errorf("Verify: got %v, want ErrSessionMismatch", err)
	}
}

// spec: §7.1 (Check order)
func TestVerifyDigestRunsAheadOfExpiry_spec_7_1(t *testing.T) {
	tok, parsed := issue(t, "sess_abc", time.Minute, testNow)
	err := verifierAt(testNow.Add(time.Hour)).Verify(tok+"00", "sess_abc", parsed.Digest, parsed.Expiry)
	if !errors.Is(err, uploadtoken.ErrInvalid) {
		t.Errorf("Verify: got %v, want ErrInvalid", err)
	}
}

// spec: §7.1 (uploadToken format and security properties)
func TestVerifyRejectsTamperedSecret(t *testing.T) {
	tok, parsed := issue(t, "sess_abc", time.Minute, testNow)
	// Flip last char.
	tampered := tok[:len(tok)-1] + "0"
	if tampered == tok {
		tampered = tok[:len(tok)-1] + "1"
	}
	err := verifierAt(testNow).Verify(tampered, "sess_abc", parsed.Digest, parsed.Expiry)
	if !errors.Is(err, uploadtoken.ErrInvalid) {
		t.Errorf("Verify: got %v, want ErrInvalid", err)
	}
}

// The digest covers the whole token, so a client cannot extend the embedded
// expiry.
//
// spec: §7.1 (uploadToken format and security properties)
func TestVerifyRejectsEditedExpirySegment_spec_7_1(t *testing.T) {
	tok, parsed := issue(t, "sess_abc", time.Minute, testNow)
	parts := strings.Split(tok, ".")
	edited := parts[0] + ".4102444800." + parts[2]
	err := verifierAt(testNow).Verify(edited, "sess_abc", parsed.Digest, parsed.Expiry)
	if !errors.Is(err, uploadtoken.ErrInvalid) {
		t.Errorf("Verify: got %v, want ErrInvalid", err)
	}
}

// spec: §7.1 (Authentication)
func TestVerifyRejectsEmptyRowDigest_spec_7_1(t *testing.T) {
	tok, parsed := issue(t, "sess_abc", time.Minute, testNow)
	err := verifierAt(testNow).Verify(tok, "sess_abc", "", parsed.Expiry)
	if !errors.Is(err, uploadtoken.ErrInvalid) {
		t.Errorf("Verify: got %v, want ErrInvalid", err)
	}
}

// spec: §7.1 (Authentication)
func TestVerifyRejectsMalformedToken(t *testing.T) {
	verifier := uploadtoken.NewVerifier(nil)
	digest := sha256Hex("a.123.deadbeef")
	for _, tok := range []string{
		"",
		"only.two",
		"a.b.c.d",
		"a..c",
		"a.notnumeric.deadbeef",
		"a.123.zznoteven_hex",
	} {
		err := verifier.Verify(tok, "a", digest, testNow.Add(100*365*24*time.Hour))
		if !errors.Is(err, uploadtoken.ErrInvalid) {
			t.Errorf("Verify(%q): got %v, want ErrInvalid", tok, err)
		}
	}
}

func TestIssueRejectsEmptyOrDottedSessionID(t *testing.T) {
	issuer := uploadtoken.NewIssuer(nil)
	if _, err := issuer.Issue("", time.Minute); err == nil {
		t.Error("empty sess_id should error")
	}
	if _, err := issuer.Issue("sess.with.dots", time.Minute); err == nil {
		t.Error("dotted sess_id should error")
	}
}
