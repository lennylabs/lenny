// SPDX-License-Identifier: MIT

package jwt

import (
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
)

func TestSignVerifyRoundTrip(t *testing.T) {
	s := NewHMACSigner("dev-key-1", []byte("test-secret"))
	in := Claims{
		Subject:   "alice@acme.com",
		Audience:  []string{"lenny-gateway"},
		Expiry:    time.Now().Add(time.Hour).Unix(),
		IssuedAt:  time.Now().Unix(),
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
	tok, _ := s.Sign(Claims{Subject: "alice", Expiry: time.Now().Add(time.Hour).Unix()})
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
	tok, _ := s.Sign(Claims{Subject: "alice", Expiry: time.Now().Add(-10 * time.Second).Unix()})
	_, err := s.Verify(tok)
	if !IsExpired(err) {
		t.Errorf("expected expired, got %v", err)
	}
}

func TestVerifyAdmitsWithinSkew(t *testing.T) {
	s := NewHMACSigner("k", []byte("secret"))
	// Expired by 500ms — within §13.3 ±1s skew.
	exp := time.Now().Unix() // truncates to current whole second; "just expired"
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

func TestNoSecretLeakAcrossSigners(t *testing.T) {
	a := NewHMACSigner("ka", []byte("secret-a"))
	b := NewHMACSigner("kb", []byte("secret-b"))
	tok, _ := a.Sign(Claims{Subject: "alice", Expiry: time.Now().Add(time.Hour).Unix()})
	_, err := b.Verify(tok)
	var ve *VerifyError
	if !errors.As(err, &ve) || ve.Reason != "signature_mismatch" {
		t.Errorf("cross-secret verify must fail, got %v", err)
	}
}
