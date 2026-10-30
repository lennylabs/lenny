// SPDX-License-Identifier: MIT

package uploadtoken_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/uploadtoken"
)

// spec: §7.1 uploadToken format + security properties.

func newRing(t *testing.T) *uploadtoken.KeyRing {
	t.Helper()
	return uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "k1", Secret: []byte("secret-1")})
}

func TestIssueAndVerifyRoundTrip(t *testing.T) {
	ring := newRing(t)
	fixed := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return fixed }
	issuer := uploadtoken.NewIssuer(ring, clock)
	verifier := uploadtoken.NewVerifier(ring, uploadtoken.NewMemoryTracker(), clock)

	tok, err := issuer.Issue("sess_abc", time.Minute)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 || parts[0] != "sess_abc" {
		t.Errorf("wire format: got %q", tok)
	}

	parsed, err := verifier.Verify(tok, "sess_abc")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if parsed.SessionID != "sess_abc" {
		t.Errorf("parsed sessionID: got %q", parsed.SessionID)
	}
	if !parsed.Expiry.Equal(fixed.Add(time.Minute)) {
		t.Errorf("expiry: got %v, want %v", parsed.Expiry, fixed.Add(time.Minute))
	}
	if parsed.KeyID != "k1" {
		t.Errorf("keyID: got %q, want k1", parsed.KeyID)
	}
}

func TestIssueDefaultsToSpecTTL(t *testing.T) {
	ring := newRing(t)
	fixed := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return fixed }
	issuer := uploadtoken.NewIssuer(ring, clock)
	verifier := uploadtoken.NewVerifier(ring, nil, clock)

	tok, err := issuer.Issue("sess_abc", 0)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	parsed, _ := verifier.Verify(tok, "sess_abc")
	if !parsed.Expiry.Equal(fixed.Add(uploadtoken.DefaultTTL)) {
		t.Errorf("default TTL: got expiry %v, want %v", parsed.Expiry, fixed.Add(uploadtoken.DefaultTTL))
	}
}

func TestVerifyRejectsExpiredToken(t *testing.T) {
	ring := newRing(t)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	issuer := uploadtoken.NewIssuer(ring, func() time.Time { return t0 })
	tok, _ := issuer.Issue("sess_abc", time.Second)

	verifier := uploadtoken.NewVerifier(ring, nil, func() time.Time { return t0.Add(10 * time.Second) })
	_, err := verifier.Verify(tok, "sess_abc")
	if !errors.Is(err, uploadtoken.ErrExpired) {
		t.Errorf("Verify: got %v, want ErrExpired", err)
	}
}

func TestVerifyRejectsSessionMismatch(t *testing.T) {
	ring := newRing(t)
	clock := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	issuer := uploadtoken.NewIssuer(ring, clock)
	verifier := uploadtoken.NewVerifier(ring, nil, clock)
	tok, _ := issuer.Issue("sess_abc", time.Minute)
	_, err := verifier.Verify(tok, "sess_xyz")
	if !errors.Is(err, uploadtoken.ErrSessionMismatch) {
		t.Errorf("Verify: got %v, want ErrSessionMismatch", err)
	}
}

func TestVerifyRejectsTamperedHMAC(t *testing.T) {
	ring := newRing(t)
	clock := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	issuer := uploadtoken.NewIssuer(ring, clock)
	verifier := uploadtoken.NewVerifier(ring, nil, clock)
	tok, _ := issuer.Issue("sess_abc", time.Minute)
	// Flip last char.
	tampered := tok[:len(tok)-1] + "0"
	if tampered == tok {
		tampered = tok[:len(tok)-1] + "1"
	}
	_, err := verifier.Verify(tampered, "sess_abc")
	if !errors.Is(err, uploadtoken.ErrInvalid) {
		t.Errorf("Verify: got %v, want ErrInvalid", err)
	}
}

func TestVerifyRejectsMalformedToken(t *testing.T) {
	ring := newRing(t)
	verifier := uploadtoken.NewVerifier(ring, nil, nil)
	for _, tok := range []string{
		"",
		"only.two",
		"a.b.c.d",
		"a..c",
		"a.notnumeric.deadbeef",
		"a.123.zznoteven_hex",
	} {
		_, err := verifier.Verify(tok, "sess_abc")
		if !errors.Is(err, uploadtoken.ErrInvalid) {
			t.Errorf("Verify(%q): got %v, want ErrInvalid", tok, err)
		}
	}
}

func TestVerifyAcceptsOverlapKey(t *testing.T) {
	// Issue with key1; rotate to key2; the overlap window should
	// admit the original token until the operator explicitly expires
	// key1.
	ring := uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "k1", Secret: []byte("first")})
	clock := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	issuer := uploadtoken.NewIssuer(ring, clock)
	tok, _ := issuer.Issue("sess_abc", time.Hour)

	ring.Rotate(uploadtoken.SigningKey{KeyID: "k2", Secret: []byte("second")})

	verifier := uploadtoken.NewVerifier(ring, nil, clock)
	parsed, err := verifier.Verify(tok, "sess_abc")
	if err != nil {
		t.Fatalf("Verify after rotate: %v", err)
	}
	if parsed.KeyID != "k1" {
		t.Errorf("keyID: got %q, want k1", parsed.KeyID)
	}

	if dropped := ring.Expire("k1"); dropped != 1 {
		t.Errorf("Expire dropped: got %d, want 1", dropped)
	}
	if _, err := verifier.Verify(tok, "sess_abc"); !errors.Is(err, uploadtoken.ErrInvalid) {
		t.Errorf("Verify after Expire(k1): got %v, want ErrInvalid", err)
	}
}

func TestRotateOnlyOnePreviousKeyIsAdded(t *testing.T) {
	// Multiple rotations push each previous key onto the overlap
	// slice. Verify can pick any of them.
	ring := uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "k1", Secret: []byte("a")})
	clock := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	t1, _ := uploadtoken.NewIssuer(ring, clock).Issue("sess_abc", time.Hour)
	ring.Rotate(uploadtoken.SigningKey{KeyID: "k2", Secret: []byte("b")})
	t2, _ := uploadtoken.NewIssuer(ring, clock).Issue("sess_abc", time.Hour)
	ring.Rotate(uploadtoken.SigningKey{KeyID: "k3", Secret: []byte("c")})
	t3, _ := uploadtoken.NewIssuer(ring, clock).Issue("sess_abc", time.Hour)

	verifier := uploadtoken.NewVerifier(ring, nil, clock)
	for _, c := range []struct {
		token string
		key   string
	}{
		{t1, "k1"},
		{t2, "k2"},
		{t3, "k3"},
	} {
		parsed, err := verifier.Verify(c.token, "sess_abc")
		if err != nil {
			t.Errorf("Verify %s: %v", c.key, err)
			continue
		}
		if parsed.KeyID != c.key {
			t.Errorf("token signed by %s validated under %s", c.key, parsed.KeyID)
		}
	}
}

func TestConsumeMarksDigestSingleUse(t *testing.T) {
	ring := newRing(t)
	clock := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	tracker := uploadtoken.NewMemoryTracker()
	issuer := uploadtoken.NewIssuer(ring, clock)
	verifier := uploadtoken.NewVerifier(ring, tracker, clock)

	tok, _ := issuer.Issue("sess_abc", time.Minute)
	parsed, err := verifier.Verify(tok, "sess_abc")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if err := verifier.Consume(parsed); err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if _, err := verifier.Verify(tok, "sess_abc"); !errors.Is(err, uploadtoken.ErrConsumed) {
		t.Errorf("Verify after Consume: got %v, want ErrConsumed", err)
	}
	if err := verifier.Consume(parsed); !errors.Is(err, uploadtoken.ErrConsumed) {
		t.Errorf("re-Consume: got %v, want ErrConsumed", err)
	}
}

func TestMemoryTrackerSweepExpired(t *testing.T) {
	tr := uploadtoken.NewMemoryTracker()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	_ = tr.MarkConsumed("a", now.Add(-time.Minute))
	_ = tr.MarkConsumed("b", now.Add(time.Minute))

	dropped := tr.Sweep(now)
	if dropped != 1 {
		t.Errorf("Sweep dropped: got %d, want 1", dropped)
	}
	if tr.IsConsumed("a") {
		t.Error("a should have been swept")
	}
	if !tr.IsConsumed("b") {
		t.Error("b should be retained (still in window)")
	}
}

func TestIssueRejectsEmptyOrDottedSessionID(t *testing.T) {
	issuer := uploadtoken.NewIssuer(newRing(t), nil)
	if _, err := issuer.Issue("", time.Minute); err == nil {
		t.Error("empty sess_id should error")
	}
	if _, err := issuer.Issue("sess.with.dots", time.Minute); err == nil {
		t.Error("dotted sess_id should error")
	}
}

func TestActiveKeyIsCurrent(t *testing.T) {
	ring := uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "k1"})
	if ring.Active().KeyID != "k1" {
		t.Errorf("Active.KeyID: got %q, want k1", ring.Active().KeyID)
	}
	ring.Rotate(uploadtoken.SigningKey{KeyID: "k2"})
	if ring.Active().KeyID != "k2" {
		t.Errorf("Active.KeyID after Rotate: got %q, want k2", ring.Active().KeyID)
	}
}
