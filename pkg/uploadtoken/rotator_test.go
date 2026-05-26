// SPDX-License-Identifier: MIT

package uploadtoken

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// spec: §7.1 line 67 — Rotator drives KeyRing.Rotate on its configured
// cadence (default 24h) and pushes the just-displaced key into the
// overlap window before the §7.1 5-min sweep drops it. RotateOnce
// validates one tick in isolation.
func TestRotatorRotateOnceInstallsNewActiveKey_spec_7_1_13(t *testing.T) {
	base := time.Date(2026, 5, 26, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return base }
	ring := NewKeyRing(SigningKey{KeyID: "boot", Secret: []byte("boot-secret")})
	seq := 0
	rot := NewRotator(ring, RotatorOptions{
		Interval: time.Hour, Overlap: 5 * time.Minute, Clock: clock,
		KeyGen: func() (SigningKey, error) {
			seq++
			return SigningKey{KeyID: fmt.Sprintf("k-%d", seq), Secret: []byte(fmt.Sprintf("s-%d", seq))}, nil
		},
	})
	if err := rot.RotateOnce(); err != nil {
		t.Fatalf("RotateOnce: %v", err)
	}
	if ring.Active().KeyID != "k-1" {
		t.Errorf("active key = %q, want k-1", ring.Active().KeyID)
	}
	if rot.PendingExpiryCount() != 1 {
		t.Errorf("pendingExpiry = %d, want 1 (boot key enters overlap window)", rot.PendingExpiryCount())
	}
}

// spec: §7.1 line 67 — keys inside the overlap window verify
// uploadTokens minted before rotation. Verify hits the overlap key
// when it carries the matching signature.
func TestRotatorPreservesOverlapVerification_spec_7_1_13(t *testing.T) {
	base := time.Date(2026, 5, 26, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return base }
	ring := NewKeyRing(SigningKey{KeyID: "boot", Secret: []byte("boot-secret")})
	issuer := NewIssuer(ring, clock)
	// Mint a token under the boot key.
	tok, _, err := issuer.IssueDetailed("sess_x", time.Minute)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	// Rotate to a fresh key; the boot key enters the overlap window.
	rot := NewRotator(ring, RotatorOptions{
		Interval: time.Hour, Overlap: 5 * time.Minute, Clock: clock,
		KeyGen: func() (SigningKey, error) {
			return SigningKey{KeyID: "k-1", Secret: []byte("new-secret")}, nil
		},
	})
	if err := rot.RotateOnce(); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	// Token minted under the boot key must still verify under the
	// post-rotation ring: §7.1 line 67 "the gateway keeps the previous
	// key valid during a short overlap window".
	v := NewVerifier(ring, nil, clock)
	if _, err := v.Verify(tok, "sess_x"); err != nil {
		t.Errorf("token minted before rotation does not verify after rotation: %v", err)
	}
}

// spec: §7.1 line 67 — the overlap-window sweep drops keys whose
// deadline has elapsed. SweepOnce removes them from the ring and from
// the pending queue; subsequent Verify against the old key returns
// ErrInvalid (no key in the ring accepts the signature).
func TestRotatorSweepsExpiredOverlapKeys_spec_7_1_13(t *testing.T) {
	now := time.Date(2026, 5, 26, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	ring := NewKeyRing(SigningKey{KeyID: "boot", Secret: []byte("boot")})
	issuer := NewIssuer(ring, clock)
	tok, _, err := issuer.IssueDetailed("sess_x", time.Hour)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	rot := NewRotator(ring, RotatorOptions{
		Interval: time.Hour, Overlap: time.Minute, Clock: func() time.Time { return now },
		KeyGen: func() (SigningKey, error) {
			return SigningKey{KeyID: "k-1", Secret: []byte("new")}, nil
		},
	})
	if err := rot.RotateOnce(); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	// Advance the clock past the overlap deadline.
	now = now.Add(2 * time.Minute)
	expired := rot.SweepOnce()
	if len(expired) != 1 || expired[0] != "boot" {
		t.Errorf("expired = %v, want [boot]", expired)
	}
	if rot.PendingExpiryCount() != 0 {
		t.Errorf("pending = %d, want 0 after sweep", rot.PendingExpiryCount())
	}
	// The boot-signed token must no longer verify.
	v := NewVerifier(ring, nil, clock)
	if _, err := v.Verify(tok, "sess_x"); err == nil {
		t.Errorf("token signed by the swept key still verifies; ring did not drop it")
	}
}

// spec: §7.1 — within the overlap window the displaced key still
// verifies, so SweepOnce is a no-op when the deadline has not been
// reached.
func TestRotatorSweepSkipsLiveOverlapKeys_spec_7_1_13(t *testing.T) {
	now := time.Date(2026, 5, 26, 0, 0, 0, 0, time.UTC)
	ring := NewKeyRing(SigningKey{KeyID: "boot", Secret: []byte("boot")})
	rot := NewRotator(ring, RotatorOptions{
		Interval: time.Hour, Overlap: time.Minute, Clock: func() time.Time { return now },
		KeyGen: func() (SigningKey, error) { return SigningKey{KeyID: "k-1", Secret: []byte("k")}, nil },
	})
	_ = rot.RotateOnce()
	// Advance only halfway into the overlap window.
	now = now.Add(30 * time.Second)
	if expired := rot.SweepOnce(); len(expired) != 0 {
		t.Errorf("sweep dropped %v before the overlap deadline; want no expiry", expired)
	}
	if rot.PendingExpiryCount() != 1 {
		t.Errorf("pending = %d, want 1", rot.PendingExpiryCount())
	}
}

// spec: §7.1 — Rotator hooks publish lifecycle events. Each rotation
// fires OnRotate with (active, displaced) and each sweep fires
// OnExpire with the dropped key IDs.
func TestRotatorHooks_spec_7_1_13(t *testing.T) {
	now := time.Date(2026, 5, 26, 0, 0, 0, 0, time.UTC)
	ring := NewKeyRing(SigningKey{KeyID: "boot", Secret: []byte("b")})
	var rotEvents [][2]string
	var expEvents [][]string
	rot := NewRotator(ring, RotatorOptions{
		Interval: time.Hour, Overlap: time.Second, Clock: func() time.Time { return now },
		KeyGen: func() (SigningKey, error) {
			return SigningKey{KeyID: "k-1", Secret: []byte("k")}, nil
		},
		OnRotate: func(active, displaced SigningKey) {
			rotEvents = append(rotEvents, [2]string{active.KeyID, displaced.KeyID})
		},
		OnExpire: func(expired []string) { expEvents = append(expEvents, expired) },
	})
	_ = rot.RotateOnce()
	now = now.Add(2 * time.Second)
	rot.SweepOnce()
	if len(rotEvents) != 1 || rotEvents[0] != [2]string{"k-1", "boot"} {
		t.Errorf("rotate events = %v, want [[k-1 boot]]", rotEvents)
	}
	if len(expEvents) != 1 || len(expEvents[0]) != 1 || expEvents[0][0] != "boot" {
		t.Errorf("expire events = %v, want [[boot]]", expEvents)
	}
}

// spec: §7.1 — the Run loop terminates promptly on ctx cancellation.
// The rotation/sweep tickers must not block process shutdown.
func TestRotatorRunStopsOnContextCancel_spec_7_1_13(t *testing.T) {
	ring := NewKeyRing(SigningKey{KeyID: "boot", Secret: []byte("b")})
	rot := NewRotator(ring, RotatorOptions{Interval: time.Hour, Overlap: time.Minute})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { rot.Run(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return within 2s of ctx cancel")
	}
}

// spec: §7.1 — a KeyGen failure (transient crypto/rand miss) leaves
// the ring untouched. The active key stays the previous active key
// and no overlap entry is queued.
func TestRotatorKeyGenFailureIsAtomic_spec_7_1_13(t *testing.T) {
	ring := NewKeyRing(SigningKey{KeyID: "boot", Secret: []byte("b")})
	rot := NewRotator(ring, RotatorOptions{
		Interval: time.Hour, Overlap: time.Minute,
		KeyGen: func() (SigningKey, error) { return SigningKey{}, fmt.Errorf("inject") },
	})
	if err := rot.RotateOnce(); err == nil {
		t.Fatalf("RotateOnce: expected an error from the injected KeyGen failure")
	}
	if ring.Active().KeyID != "boot" {
		t.Errorf("active key = %q after a failing KeyGen, want boot (unchanged)", ring.Active().KeyID)
	}
	if rot.PendingExpiryCount() != 0 {
		t.Errorf("pending overlap = %d after a failing KeyGen, want 0", rot.PendingExpiryCount())
	}
}
