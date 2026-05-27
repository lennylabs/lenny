// SPDX-License-Identifier: MIT

package jwt

import (
	"errors"
	"testing"
	"time"
)

// spec: §10.2 line 237 — ClaimChecker layers iss / aud onto the
// standard auth chain. Each subtest pins one branch.

func newCCSigner() *HMACSigner {
	return NewHMACSigner("cc-test", []byte("cc-secret"))
}

func TestClaimCheckerAcceptsMatchingClaims(t *testing.T) {
	s := newCCSigner()
	tok, err := s.Sign(Claims{
		Issuer:   "https://lenny.example/token",
		Audience: []string{"lenny-gateway", "lenny-admin"},
		Expiry:   time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	cc := NewClaimChecker(s, ExpectedClaims{
		Issuer:    "https://lenny.example/token",
		Audiences: []string{"lenny-gateway"},
	})
	if _, err := cc.Verify(tok); err != nil {
		t.Fatalf("Verify(matching): %v", err)
	}
}

func TestClaimCheckerRejectsIssuerMismatch(t *testing.T) {
	s := newCCSigner()
	tok, _ := s.Sign(Claims{
		Issuer:   "https://attacker.example/token",
		Audience: []string{"lenny-gateway"},
		Expiry:   time.Now().Add(time.Hour).Unix(),
	})
	cc := NewClaimChecker(s, ExpectedClaims{Issuer: "https://lenny.example/token"})
	_, err := cc.Verify(tok)
	var ve *VerifyError
	if !errors.As(err, &ve) || ve.Reason != "issuer_mismatch" {
		t.Fatalf("Verify err: got %v, want VerifyError{issuer_mismatch}", err)
	}
}

func TestClaimCheckerRejectsAudienceDisjoint(t *testing.T) {
	s := newCCSigner()
	tok, _ := s.Sign(Claims{
		Audience: []string{"lenny-admin"},
		Expiry:   time.Now().Add(time.Hour).Unix(),
	})
	cc := NewClaimChecker(s, ExpectedClaims{Audiences: []string{"lenny-gateway"}})
	_, err := cc.Verify(tok)
	var ve *VerifyError
	if !errors.As(err, &ve) || ve.Reason != "audience_mismatch" {
		t.Fatalf("Verify err: got %v, want VerifyError{audience_mismatch}", err)
	}
}

func TestClaimCheckerEmptyExpectedSkips(t *testing.T) {
	s := newCCSigner()
	tok, _ := s.Sign(Claims{Expiry: time.Now().Add(time.Hour).Unix()})
	cc := NewClaimChecker(s, ExpectedClaims{})
	if _, err := cc.Verify(tok); err != nil {
		t.Fatalf("Verify(empty-expected): %v", err)
	}
}

func TestClaimCheckerPassesThroughInnerError(t *testing.T) {
	s := newCCSigner()
	// Expired token; inner verifier reports "expired" and the wrapper
	// must propagate that — not mask it with an issuer-mismatch.
	tok, _ := s.Sign(Claims{Expiry: time.Now().Add(-time.Hour).Unix()})
	cc := NewClaimChecker(s, ExpectedClaims{Issuer: "https://lenny.example/token"})
	_, err := cc.Verify(tok)
	var ve *VerifyError
	if !errors.As(err, &ve) || ve.Reason != "expired" {
		t.Fatalf("Verify err: got %v, want VerifyError{expired}", err)
	}
}

// spec: §10.2 line 237 — nbf is part of the standard auth chain. A
// token whose not-before lies in the future (beyond the ±skew window)
// is rejected with reason=not_yet_valid.
func TestHMACVerifyRejectsFutureNotBefore(t *testing.T) {
	s := newCCSigner()
	tok, _ := s.Sign(Claims{
		NotBefore: time.Now().Add(time.Hour).Unix(),
		Expiry:    time.Now().Add(2 * time.Hour).Unix(),
	})
	_, err := s.Verify(tok)
	var ve *VerifyError
	if !errors.As(err, &ve) || ve.Reason != "not_yet_valid" {
		t.Fatalf("Verify err: got %v, want VerifyError{not_yet_valid}", err)
	}
}

// A nbf in the past is accepted (the token is valid).
func TestHMACVerifyAcceptsPastNotBefore(t *testing.T) {
	s := newCCSigner()
	tok, _ := s.Sign(Claims{
		NotBefore: time.Now().Add(-time.Hour).Unix(),
		Expiry:    time.Now().Add(time.Hour).Unix(),
	})
	if _, err := s.Verify(tok); err != nil {
		t.Fatalf("Verify(past-nbf): %v", err)
	}
}

// An nbf within the ±JWTSkewAllowance window is accepted; the skew
// tolerance mirrors the exp check so clock drift across replicas does
// not cause spurious not_yet_valid rejections.
func TestHMACVerifyAcceptsNotBeforeWithinSkew(t *testing.T) {
	s := newCCSigner()
	tok, _ := s.Sign(Claims{
		NotBefore: time.Now().Add(JWTSkewAllowance / 2).Unix(),
		Expiry:    time.Now().Add(time.Hour).Unix(),
	})
	if _, err := s.Verify(tok); err != nil {
		t.Fatalf("Verify(nbf-within-skew): %v", err)
	}
}
