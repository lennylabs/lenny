// SPDX-License-Identifier: MIT

package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"testing"
	"time"
)

// sign reproduces the §14 X-Lenny-Signature header value for a test.
func sign(secret, body string, t time.Time) string {
	unix := strconv.FormatInt(t.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(unix))
	mac.Write([]byte("."))
	mac.Write([]byte(body))
	return "t=" + unix + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}

// fixedClock returns a clock pinned to at.
func fixedClock(at time.Time) func() time.Time {
	return func() time.Time { return at }
}

// TestVerifyAcceptsValidSignature confirms a correctly-signed,
// in-window delivery verifies.
func TestVerifyAcceptsValidSignature(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	secret := "wh-secret"
	body := `{"event":"session.completed"}`
	v, err := NewWithSecret([]byte(secret), WithClock(fixedClock(now)))
	if err != nil {
		t.Fatalf("NewWithSecret: %v", err)
	}
	if err := v.Verify([]byte(body), sign(secret, body, now)); err != nil {
		t.Errorf("Verify: want nil for a valid signature, got %v", err)
	}
}

// TestVerifyRejectsForgedSignature confirms a signature under the
// wrong secret is rejected.
func TestVerifyRejectsForgedSignature(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	body := `{"event":"session.completed"}`
	v, err := NewWithSecret([]byte("real-secret"), WithClock(fixedClock(now)))
	if err != nil {
		t.Fatalf("NewWithSecret: %v", err)
	}
	err = v.Verify([]byte(body), sign("attacker-secret", body, now))
	if !errors.Is(err, ErrSignatureMismatch) {
		t.Errorf("Verify: want ErrSignatureMismatch, got %v", err)
	}
}

// TestVerifyRejectsStaleSignature confirms a delivery timestamp
// outside the replay window is rejected even with a correct HMAC.
func TestVerifyRejectsStaleSignature(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	secret := "wh-secret"
	body := `{"event":"session.completed"}`
	v, err := NewWithSecret([]byte(secret), WithClock(fixedClock(now)))
	if err != nil {
		t.Fatalf("NewWithSecret: %v", err)
	}
	stale := sign(secret, body, now.Add(-10*time.Minute))
	if err := v.Verify([]byte(body), stale); !errors.Is(err, ErrReplayWindow) {
		t.Errorf("Verify: want ErrReplayWindow, got %v", err)
	}
}

// TestVerifyRejectsMalformedHeader confirms a header that does not
// parse is rejected.
func TestVerifyRejectsMalformedHeader(t *testing.T) {
	v, err := NewWithSecret([]byte("wh-secret"))
	if err != nil {
		t.Fatalf("NewWithSecret: %v", err)
	}
	if err := v.Verify([]byte("body"), "garbage"); !errors.Is(err, ErrMalformedSignature) {
		t.Errorf("Verify: want ErrMalformedSignature, got %v", err)
	}
}

// TestVerifyRejectsMissingHeader confirms an empty header is
// rejected with ErrMissingSignature.
func TestVerifyRejectsMissingHeader(t *testing.T) {
	v, err := NewWithSecret([]byte("wh-secret"))
	if err != nil {
		t.Fatalf("NewWithSecret: %v", err)
	}
	if err := v.Verify([]byte("body"), ""); !errors.Is(err, ErrMissingSignature) {
		t.Errorf("Verify: want ErrMissingSignature, got %v", err)
	}
}

// TestVerifyAcceptsRotatedSecret confirms a Verifier holding two
// secrets accepts a delivery signed with either one.
func TestVerifyAcceptsRotatedSecret(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	body := `{"event":"x"}`
	v, err := New([][]byte{[]byte("old-secret"), []byte("new-secret")}, WithClock(fixedClock(now)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := v.Verify([]byte(body), sign("old-secret", body, now)); err != nil {
		t.Errorf("Verify with old secret: want nil, got %v", err)
	}
	if err := v.Verify([]byte(body), sign("new-secret", body, now)); err != nil {
		t.Errorf("Verify with new secret: want nil, got %v", err)
	}
}

// TestNewRejectsNoSecret confirms New rejects an empty secret list.
func TestNewRejectsNoSecret(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Error("New(nil): want error")
	}
}
