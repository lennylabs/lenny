// SPDX-License-Identifier: MIT

package jwt

import (
	"errors"
	"testing"
)

// mvClaims builds a minimal valid Claims value for the multi-verifier
// tests. The far-future expiry keeps Verify's clock check stable.
func mvClaims(sub string) Claims {
	return Claims{
		Subject:  sub,
		Audience: []string{"lenny-gateway"},
		Expiry:   farFutureExpiry,
		IssuedAt: farFutureIssued,
	}
}

func TestMultiVerifierAcceptsPrimaryToken(t *testing.T) {
	primary := NewHMACSigner("primary", []byte("primary-secret"))
	secondary := NewHMACSigner("secondary", []byte("secondary-secret"))
	v := NewMultiVerifier(primary, secondary)

	tok, err := primary.Sign(mvClaims("alice@acme.com"))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	claims, err := v.Verify(tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.Subject != "alice@acme.com" {
		t.Errorf("subject = %q, want alice@acme.com", claims.Subject)
	}
}

func TestMultiVerifierAcceptsSecondaryToken(t *testing.T) {
	primary := NewHMACSigner("primary", []byte("primary-secret"))
	secondary := NewHMACSigner("secondary", []byte("secondary-secret"))
	v := NewMultiVerifier(primary, secondary)

	// A token the primary verifier cannot validate must still be
	// accepted when a later member holds its key. This is the §17.4
	// case: the embedded OIDC token is signed by the trusted key, not
	// the Token Service key.
	tok, err := secondary.Sign(mvClaims("bob@acme.com"))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	claims, err := v.Verify(tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.Subject != "bob@acme.com" {
		t.Errorf("subject = %q, want bob@acme.com", claims.Subject)
	}
}

func TestMultiVerifierRejectsTokenSignedByNoMember(t *testing.T) {
	primary := NewHMACSigner("primary", []byte("primary-secret"))
	secondary := NewHMACSigner("secondary", []byte("secondary-secret"))
	v := NewMultiVerifier(primary, secondary)

	stranger := NewHMACSigner("stranger", []byte("stranger-secret"))
	tok, err := stranger.Sign(mvClaims("carol@acme.com"))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err := v.Verify(tok); err == nil {
		t.Fatal("Verify accepted a token no member signed")
	}
}

func TestMultiVerifierSurfacesPrimaryError(t *testing.T) {
	primary := NewHMACSigner("primary", []byte("primary-secret"))
	secondary := NewHMACSigner("secondary", []byte("secondary-secret"))
	v := NewMultiVerifier(primary, secondary)

	// An expired token signed by the primary key: every member rejects
	// it, and the surfaced error must be the primary's "expired" reason
	// rather than the secondary's "signature_mismatch". The auth
	// middleware maps "expired" to a TOKEN_EXPIRED envelope.
	expired := mvClaims("alice@acme.com")
	expired.Expiry = farPastExpiry
	tok, err := primary.Sign(expired)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	_, err = v.Verify(tok)
	if err == nil {
		t.Fatal("Verify accepted an expired token")
	}
	if !IsExpired(err) {
		t.Errorf("Verify error = %v, want an expired *VerifyError", err)
	}
}

func TestMultiVerifierRejectsMalformedToken(t *testing.T) {
	v := NewMultiVerifier(NewHMACSigner("primary", []byte("primary-secret")))
	if _, err := v.Verify("not-a-jwt"); err == nil {
		t.Fatal("Verify accepted a malformed token")
	}
}

func TestMultiVerifierPanicsOnEmptyMembers(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("NewMultiVerifier did not panic on an empty member set")
		}
	}()
	NewMultiVerifier()
}

func TestMultiVerifierPanicsOnNilMember(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("NewMultiVerifier did not panic on a nil member")
		}
	}()
	NewMultiVerifier(NewHMACSigner("primary", []byte("s")), nil)
}

func TestMultiVerifierLen(t *testing.T) {
	v := NewMultiVerifier(
		NewHMACSigner("a", []byte("sa")),
		NewHMACSigner("b", []byte("sb")),
		NewHMACSigner("c", []byte("sc")),
	)
	if v.Len() != 3 {
		t.Errorf("Len = %d, want 3", v.Len())
	}
}

func TestMultiVerifierWithRotatingMember(t *testing.T) {
	// A MultiVerifier over a RotatingVerifier primary plus an HMAC
	// secondary. A token signed under an unknown kid is rejected by the
	// rotating member with unknown_kid; the secondary then verifies it.
	rotating := NewRotatingVerifier(NewHMACSigner("current", []byte("current-secret")), 0)
	secondary := NewHMACSigner("trusted", []byte("trusted-secret"))
	v := NewMultiVerifier(rotating, secondary)

	tok, err := secondary.Sign(mvClaims("dave@acme.com"))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	claims, err := v.Verify(tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.Subject != "dave@acme.com" {
		t.Errorf("subject = %q, want dave@acme.com", claims.Subject)
	}
}

func TestIsUnknownKey(t *testing.T) {
	cases := []struct {
		reason string
		want   bool
	}{
		{"unknown_kid", true},
		{"missing_kid", true},
		{"key_retired", true},
		{"signature_mismatch", false},
		{"expired", false},
		{"malformed", false},
	}
	for _, c := range cases {
		got := IsUnknownKey(&VerifyError{Reason: c.reason})
		if got != c.want {
			t.Errorf("IsUnknownKey(%q) = %v, want %v", c.reason, got, c.want)
		}
	}
	if IsUnknownKey(errors.New("plain error")) {
		t.Error("IsUnknownKey(plain error) = true, want false")
	}
	if IsUnknownKey(nil) {
		t.Error("IsUnknownKey(nil) = true, want false")
	}
}
