// SPDX-License-Identifier: MIT

package jwt

import (
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
)

// JWT tests pin expiry values against a fixed-far-future anchor so
// the suite is reproducible regardless of wall-clock. Verify's
// internal time.Now check still uses real time; both farFutureExpiry
// (year 2099) and farPastExpiry (year 2000) sit far enough on either
// side of any plausible test-run timestamp that the comparisons stay
// stable.
var (
	farFutureExpiry = time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	farFutureIssued = time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC).Add(-time.Hour).Unix()
	farPastExpiry   = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
)

func TestSignVerifyRoundTrip(t *testing.T) {
	s := NewHMACSigner("dev-key-1", []byte("test-secret"))
	in := Claims{
		Subject:   "alice@acme.com",
		Audience:  []string{"lenny-gateway"},
		Expiry:    farFutureExpiry,
		IssuedAt:  farFutureIssued,
		JWTID:     "jti-1",
		TenantID:  "acme",
		SessionID: "sess_1",
		Typ:       auth.TokenUserBearer,
	}
	tok, err := s.Sign(in)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	out, err := s.Verify(tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if out.Subject != in.Subject || out.TenantID != in.TenantID || out.Typ != in.Typ {
		t.Errorf("round trip lost claims:\n in:  %+v\n out: %+v", in, out)
	}
}

func TestVerifyRejectsTamperedSignature(t *testing.T) {
	s := NewHMACSigner("k", []byte("secret"))
	tok, _ := s.Sign(Claims{Subject: "alice", Expiry: farFutureExpiry})
	// Flip the last char of the signature.
	tampered := tok[:len(tok)-1]
	if tok[len(tok)-1] == 'A' {
		tampered += "B"
	} else {
		tampered += "A"
	}
	_, err := s.Verify(tampered)
	var ve *VerifyError
	if !errors.As(err, &ve) || ve.Reason != "signature_mismatch" {
		t.Errorf("expected signature_mismatch, got %v", err)
	}
}

func TestVerifyRejectsExpired(t *testing.T) {
	s := NewHMACSigner("k", []byte("secret"))
	tok, _ := s.Sign(Claims{Subject: "alice", Expiry: farPastExpiry})
	_, err := s.Verify(tok)
	if !IsExpired(err) {
		t.Errorf("expected expired, got %v", err)
	}
}

func TestVerifyAdmitsWithinSkew(t *testing.T) {
	s := NewHMACSigner("k", []byte("secret"))
	// Expired by 500ms — within §13.3 ±1s skew.
	exp := time.Now().Unix() // lint-determinism:exempt — §13.3 skew boundary requires the wall-clock "now"
	tok, _ := s.Sign(Claims{Subject: "alice", Expiry: exp})
	if _, err := s.Verify(tok); err != nil {
		t.Errorf("token at boundary should admit per §13.3 skew, got %v", err)
	}
}

func TestVerifyRejectsMalformed(t *testing.T) {
	s := NewHMACSigner("k", []byte("secret"))
	cases := []string{
		"",
		"only.one.dot",
		"too.many.dots.here.now",
		"not-base64.payload.signature",
	}
	for _, tok := range cases {
		_, err := s.Verify(tok)
		var ve *VerifyError
		if !errors.As(err, &ve) {
			t.Errorf("token %q: want *VerifyError, got %v", tok, err)
		}
	}
}

func TestSignerKeyID(t *testing.T) {
	s := NewHMACSigner("dev-key-1", []byte("x"))
	if s.KeyID() != "dev-key-1" {
		t.Errorf("KeyID: want dev-key-1, got %q", s.KeyID())
	}
}

func TestClaimsRolesRoundTrip(t *testing.T) {
	s := NewHMACSigner("k", []byte("secret"))
	in := Claims{
		Subject: "alice@acme.com",
		Expiry:  farFutureExpiry,
		Roles:   []auth.Role{auth.RolePlatformAdmin, auth.RoleUser},
	}
	tok, err := s.Sign(in)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	out, err := s.Verify(tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(out.Roles) != 2 || out.Roles[0] != auth.RolePlatformAdmin || out.Roles[1] != auth.RoleUser {
		t.Errorf("Roles round-trip: got %v, want [platform-admin user]", out.Roles)
	}
}

func TestClaimsHasRole(t *testing.T) {
	c := Claims{Roles: []auth.Role{auth.RoleTenantAdmin, auth.RoleUser}}
	if !c.HasRole(auth.RoleTenantAdmin) {
		t.Error("HasRole(tenant-admin) should be true")
	}
	if !c.HasRole(auth.RoleUser) {
		t.Error("HasRole(user) should be true")
	}
	if c.HasRole(auth.RolePlatformAdmin) {
		t.Error("HasRole(platform-admin) should be false")
	}
	if (Claims{}).HasRole(auth.RolePlatformAdmin) {
		t.Error("HasRole on zero Claims should be false")
	}
}

func TestNoSecretLeakAcrossSigners(t *testing.T) {
	a := NewHMACSigner("ka", []byte("secret-a"))
	b := NewHMACSigner("kb", []byte("secret-b"))
	tok, _ := a.Sign(Claims{Subject: "alice", Expiry: farFutureExpiry})
	_, err := b.Verify(tok)
	var ve *VerifyError
	if !errors.As(err, &ve) || ve.Reason != "signature_mismatch" {
		t.Errorf("cross-secret verify must fail, got %v", err)
	}
}
