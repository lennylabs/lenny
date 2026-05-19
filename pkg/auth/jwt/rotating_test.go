// SPDX-License-Identifier: MIT

package jwt

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// Rotation tests pin the verifier's clock with an injected now func so
// overlap-window boundaries are exercised deterministically without
// sleeping. The per-key HMAC Verify path still consults the real wall
// clock for the exp check, so token expiry values use a far-future
// anchor (rotatingFutureExpiry) that no plausible test-run timestamp
// reaches.
var rotatingFutureExpiry = time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC).Unix()

// withClock replaces the verifier's now func and returns v for chaining.
// It is the test seam for the §10.3 overlap window: a fake clock lets a
// test step "now" across retiredAt + overlapWindow without real time.
func withClock(v *RotatingVerifier, now func() time.Time) *RotatingVerifier {
	v.mu.Lock()
	v.now = now
	v.mu.Unlock()
	return v
}

// spec: 10.3
// diagnosis: a token signed by the rotating verifier's current key did
// not verify. The current key must always validate its own tokens.
func TestRotatingVerifierCurrentKeyVerifies(t *testing.T) {
	t.Parallel()
	cur := NewHMACSigner("kid-1", []byte("secret-1"))
	v := NewRotatingVerifier(cur, DefaultOverlapWindow)

	tok, err := cur.Sign(Claims{Subject: "alice@acme.com", Expiry: rotatingFutureExpiry})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	got, err := v.Verify(tok)
	if err != nil {
		t.Fatalf("Verify current-key token: %v", err)
	}
	if got.Subject != "alice@acme.com" {
		t.Errorf("verified subject: got %q, want alice@acme.com", got.Subject)
	}
	if v.CurrentKeyID() != "kid-1" {
		t.Errorf("CurrentKeyID: got %q, want kid-1", v.CurrentKeyID())
	}
}

// spec: 10.3
// diagnosis: after a rotation, a token signed by the now-previous key
// was rejected even though the overlap window was still open. The
// §10.3 zero-downtime property requires old-key tokens to keep
// verifying inside the overlap window.
func TestRotatingVerifierPreviousKeyVerifiesWithinOverlap(t *testing.T) {
	t.Parallel()
	oldKey := NewHMACSigner("kid-1", []byte("secret-1"))
	newKey := NewHMACSigner("kid-2", []byte("secret-2"))

	// A token minted before the rotation, under the old key.
	oldTok, err := oldKey.Sign(Claims{Subject: "bob@acme.com", Expiry: rotatingFutureExpiry})
	if err != nil {
		t.Fatalf("Sign old: %v", err)
	}

	clock := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	v := withClock(NewRotatingVerifier(oldKey, time.Hour), func() time.Time { return clock })

	if err := v.Rotate(newKey); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if v.CurrentKeyID() != "kid-2" {
		t.Errorf("CurrentKeyID after Rotate: got %q, want kid-2", v.CurrentKeyID())
	}
	prev := v.PreviousKeyIDs()
	if len(prev) != 1 || prev[0] != "kid-1" {
		t.Errorf("PreviousKeyIDs after Rotate: got %v, want [kid-1]", prev)
	}

	// Advance the clock to just before the overlap window closes.
	clock = clock.Add(time.Hour - time.Second)
	got, err := v.Verify(oldTok)
	if err != nil {
		t.Fatalf("Verify previous-key token inside overlap window: %v", err)
	}
	if got.Subject != "bob@acme.com" {
		t.Errorf("verified subject: got %q, want bob@acme.com", got.Subject)
	}

	// The new current key also verifies its own tokens.
	newTok, err := newKey.Sign(Claims{Subject: "carol@acme.com", Expiry: rotatingFutureExpiry})
	if err != nil {
		t.Fatalf("Sign new: %v", err)
	}
	if _, err := v.Verify(newTok); err != nil {
		t.Errorf("Verify current-key token after rotation: %v", err)
	}
}

// spec: 10.3
// diagnosis: past the §10.3 overlap window, a token signed by a retired
// key was still accepted. A window-closed previous key must reject with
// the distinct "key_retired" reason.
func TestRotatingVerifierPreviousKeyRejectedPastOverlap(t *testing.T) {
	t.Parallel()
	oldKey := NewHMACSigner("kid-1", []byte("secret-1"))
	newKey := NewHMACSigner("kid-2", []byte("secret-2"))

	oldTok, err := oldKey.Sign(Claims{Subject: "bob@acme.com", Expiry: rotatingFutureExpiry})
	if err != nil {
		t.Fatalf("Sign old: %v", err)
	}

	clock := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	v := withClock(NewRotatingVerifier(oldKey, time.Hour), func() time.Time { return clock })
	if err := v.Rotate(newKey); err != nil {
		t.Fatalf("Rotate: %v", err)
	}

	// Step the clock to exactly retiredAt + overlapWindow: the window is
	// closed at the boundary (Verify uses now.Before(retiredAt+overlap)).
	clock = clock.Add(time.Hour)
	_, err = v.Verify(oldTok)
	if !IsKeyRetired(err) {
		t.Fatalf("Verify at the overlap boundary: want key_retired, got %v", err)
	}
	var ve *VerifyError
	if !errors.As(err, &ve) || ve.Reason != "key_retired" {
		t.Fatalf("want *VerifyError reason key_retired, got %v", err)
	}
	if !strings.Contains(ve.Detail, "kid-1") {
		t.Errorf("key_retired detail should name the kid, got %q", ve.Detail)
	}

	// Well past the window, still rejected.
	clock = clock.Add(48 * time.Hour)
	if _, err := v.Verify(oldTok); !IsKeyRetired(err) {
		t.Errorf("Verify well past overlap: want key_retired, got %v", err)
	}
}

// spec: 10.3
// diagnosis: a token whose kid matched no key version was admitted. An
// unknown kid must be rejected with the "unknown_kid" reason.
func TestRotatingVerifierUnknownKIDRejected(t *testing.T) {
	t.Parallel()
	cur := NewHMACSigner("kid-1", []byte("secret-1"))
	v := NewRotatingVerifier(cur, DefaultOverlapWindow)

	// A token signed by a key the verifier has never held.
	stranger := NewHMACSigner("kid-stranger", []byte("secret-x"))
	tok, err := stranger.Sign(Claims{Subject: "mallory@acme.com", Expiry: rotatingFutureExpiry})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	_, err = v.Verify(tok)
	var ve *VerifyError
	if !errors.As(err, &ve) || ve.Reason != "unknown_kid" {
		t.Errorf("Verify unknown-kid token: want unknown_kid, got %v", err)
	}
}

// spec: 10.3
// diagnosis: a token carrying no kid header was admitted under a guessed
// key. A rotating verifier holds multiple keys and must reject an
// unkeyed token with the "missing_kid" reason rather than guess.
func TestRotatingVerifierMissingKIDRejected(t *testing.T) {
	t.Parallel()
	cur := NewHMACSigner("kid-1", []byte("secret-1"))
	v := NewRotatingVerifier(cur, DefaultOverlapWindow)

	// An empty-keyID signer omits the kid header (see HMACSigner.Sign).
	noKID := NewHMACSigner("", []byte("secret-1"))
	tok, err := noKID.Sign(Claims{Subject: "alice@acme.com", Expiry: rotatingFutureExpiry})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	_, err = v.Verify(tok)
	var ve *VerifyError
	if !errors.As(err, &ve) || ve.Reason != "missing_kid" {
		t.Errorf("Verify no-kid token: want missing_kid, got %v", err)
	}
}

// spec: 10.3
// diagnosis: a previous-key token with a valid kid but a bad signature
// was accepted. Key selection routes by kid; the selected per-key
// Verifier must still reject a tampered signature.
func TestRotatingVerifierTamperedSignatureRejected(t *testing.T) {
	t.Parallel()
	cur := NewHMACSigner("kid-1", []byte("secret-1"))
	v := NewRotatingVerifier(cur, DefaultOverlapWindow)

	tok, err := cur.Sign(Claims{Subject: "alice@acme.com", Expiry: rotatingFutureExpiry})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	tampered := tok[:len(tok)-1]
	if tok[len(tok)-1] == 'A' {
		tampered += "B"
	} else {
		tampered += "A"
	}
	_, err = v.Verify(tampered)
	var ve *VerifyError
	if !errors.As(err, &ve) || ve.Reason != "signature_mismatch" {
		t.Errorf("Verify tampered token: want signature_mismatch, got %v", err)
	}
}

// spec: 10.3
// diagnosis: a structurally malformed token panicked or was admitted.
// A non-JWS token must be rejected with the "malformed" reason before
// any key selection.
func TestRotatingVerifierMalformedRejected(t *testing.T) {
	t.Parallel()
	v := NewRotatingVerifier(NewHMACSigner("kid-1", []byte("secret-1")), DefaultOverlapWindow)
	cases := []string{
		"",
		"only.one.dot",
		"too.many.dots.here.now",
		"@@@.payload.signature",
	}
	for _, tok := range cases {
		_, err := v.Verify(tok)
		var ve *VerifyError
		if !errors.As(err, &ve) || ve.Reason != "malformed" {
			t.Errorf("Verify %q: want malformed, got %v", tok, err)
		}
	}
}

// spec: 10.3
// diagnosis: Rotate accepted a next key that reused a kid, making the
// retired and current versions indistinguishable. Rotate must reject an
// empty kid, a nil key, and a kid that collides with the current key.
func TestRotatingVerifierRotateRejectsBadNextKey(t *testing.T) {
	t.Parallel()
	cur := NewHMACSigner("kid-1", []byte("secret-1"))
	v := NewRotatingVerifier(cur, DefaultOverlapWindow)

	if err := v.Rotate(nil); err == nil {
		t.Error("Rotate accepted a nil next key")
	}
	if err := v.Rotate(NewHMACSigner("", []byte("secret-2"))); err == nil {
		t.Error("Rotate accepted an empty-kid next key")
	}
	if err := v.Rotate(NewHMACSigner("kid-1", []byte("secret-2"))); err == nil {
		t.Error("Rotate accepted a next key reusing the current kid")
	}
	// None of the rejected calls should have disturbed the key set.
	if v.CurrentKeyID() != "kid-1" {
		t.Errorf("CurrentKeyID after rejected rotations: got %q, want kid-1", v.CurrentKeyID())
	}
	if len(v.PreviousKeyIDs()) != 0 {
		t.Errorf("PreviousKeyIDs after rejected rotations: got %v, want []", v.PreviousKeyIDs())
	}
}

// spec: 10.3
// diagnosis: after two rotations the oldest key was still consulted
// past its window, and RetireExpired did not prune it. RetireExpired
// must drop window-closed previous keys; chained rotations must each
// land their predecessor in the previous set.
func TestRotatingVerifierChainedRotationAndRetireExpired(t *testing.T) {
	t.Parallel()
	k1 := NewHMACSigner("kid-1", []byte("secret-1"))
	k2 := NewHMACSigner("kid-2", []byte("secret-2"))
	k3 := NewHMACSigner("kid-3", []byte("secret-3"))

	clock := time.Date(2026, 5, 18, 0, 0, 0, 0, time.UTC)
	v := withClock(NewRotatingVerifier(k1, time.Hour), func() time.Time { return clock })

	if err := v.Rotate(k2); err != nil { // kid-1 retired at T+0
		t.Fatalf("Rotate k2: %v", err)
	}
	clock = clock.Add(30 * time.Minute)
	if err := v.Rotate(k3); err != nil { // kid-2 retired at T+30m
		t.Fatalf("Rotate k3: %v", err)
	}
	if got := v.PreviousKeyIDs(); len(got) != 2 {
		t.Fatalf("PreviousKeyIDs after two rotations: got %v, want two entries", got)
	}

	// At T+1h05m: kid-1's window (closed at T+1h) is shut; kid-2's
	// (closed at T+1h30m) is still open.
	clock = clock.Add(35 * time.Minute)
	removed := v.RetireExpired()
	if removed != 1 {
		t.Errorf("RetireExpired removed %d keys, want 1 (kid-1)", removed)
	}
	prev := v.PreviousKeyIDs()
	if len(prev) != 1 || prev[0] != "kid-2" {
		t.Errorf("PreviousKeyIDs after RetireExpired: got %v, want [kid-2]", prev)
	}

	// A kid-1 token now reports unknown_kid (the entry is gone), while a
	// kid-2 token still verifies inside its window.
	k1Tok, _ := k1.Sign(Claims{Subject: "alice@acme.com", Expiry: rotatingFutureExpiry})
	if _, err := v.Verify(k1Tok); !errors.As(err, new(*VerifyError)) {
		t.Errorf("kid-1 token after prune: want *VerifyError, got %v", err)
	} else {
		var ve *VerifyError
		errors.As(err, &ve)
		if ve.Reason != "unknown_kid" {
			t.Errorf("kid-1 token after prune: want unknown_kid, got %q", ve.Reason)
		}
	}
	k2Tok, _ := k2.Sign(Claims{Subject: "bob@acme.com", Expiry: rotatingFutureExpiry})
	if _, err := v.Verify(k2Tok); err != nil {
		t.Errorf("kid-2 token inside its window after prune: %v", err)
	}
}

// spec: 10.3
// diagnosis: NewRotatingVerifier accepted a key whose kid is empty,
// which a rotating verifier could never address. Construction must
// panic on a nil or empty-kid current key.
func TestNewRotatingVerifierRejectsBadCurrentKey(t *testing.T) {
	t.Parallel()
	assertPanics(t, "nil current key", func() {
		NewRotatingVerifier(nil, DefaultOverlapWindow)
	})
	assertPanics(t, "empty-kid current key", func() {
		NewRotatingVerifier(NewHMACSigner("", []byte("secret")), DefaultOverlapWindow)
	})
}

// spec: 10.3
// diagnosis: a non-positive overlap window was taken literally, closing
// the window instantly. NewRotatingVerifier must substitute the 24h
// DefaultOverlapWindow for a value <= 0.
func TestNewRotatingVerifierDefaultsOverlapWindow(t *testing.T) {
	t.Parallel()
	for _, w := range []time.Duration{0, -time.Hour} {
		v := NewRotatingVerifier(NewHMACSigner("kid-1", []byte("secret")), w)
		v.mu.RLock()
		got := v.overlapWindow
		v.mu.RUnlock()
		if got != DefaultOverlapWindow {
			t.Errorf("overlapWindow for input %v: got %v, want %v", w, got, DefaultOverlapWindow)
		}
	}
}

// spec: 10.3
// diagnosis: concurrent Verify calls against the verifier raced on the
// key set. The gateway verifies pod tokens concurrently, so Verify and
// Rotate must be race-free. Run under `go test -race`.
func TestRotatingVerifierConcurrentVerify(t *testing.T) {
	t.Parallel()
	k1 := NewHMACSigner("kid-1", []byte("secret-1"))
	k2 := NewHMACSigner("kid-2", []byte("secret-2"))
	v := NewRotatingVerifier(k1, DefaultOverlapWindow)

	tok1, _ := k1.Sign(Claims{Subject: "alice@acme.com", Expiry: rotatingFutureExpiry})
	tok2, _ := k2.Sign(Claims{Subject: "bob@acme.com", Expiry: rotatingFutureExpiry})

	var wg sync.WaitGroup
	// Readers: hammer Verify on both kids while a rotation lands.
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_, _ = v.Verify(tok1)
				_, _ = v.Verify(tok2)
				_ = v.CurrentKeyID()
				_ = v.PreviousKeyIDs()
			}
		}()
	}
	// A single writer rotates k1 out and k2 in partway through.
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = v.Rotate(k2)
		_ = v.RetireExpired()
	}()
	wg.Wait()

	// After the rotation settles, kid-2 is current and verifies.
	if _, err := v.Verify(tok2); err != nil {
		t.Errorf("Verify kid-2 token after concurrent rotation: %v", err)
	}
}

// recordingRotationObserver captures every RotationEvent it receives
// so a test can assert on the audit-shaped record without reaching
// into the verifier's private state.
type recordingRotationObserver struct {
	events []RotationEvent
}

func (r *recordingRotationObserver) OnRotation(ev RotationEvent) {
	r.events = append(r.events, ev)
}

// spec: 10.3
// diagnosis: a successful Rotate did not deliver a promote_current
// event to the installed RotationObserver, or delivered an event
// with the wrong from/to kid. The §10.3 audit pipeline depends on
// exactly one event per Rotate call carrying the outgoing and
// incoming kids.
func TestRotatingVerifierRotateEmitsPromoteCurrentEvent(t *testing.T) {
	t.Parallel()
	oldKey := NewHMACSigner("kid-1", []byte("secret-1"))
	newKey := NewHMACSigner("kid-2", []byte("secret-2"))
	clock := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	v := withClock(NewRotatingVerifier(oldKey, time.Hour), func() time.Time { return clock })
	obs := &recordingRotationObserver{}
	v.SetObserver(obs)

	if err := v.Rotate(newKey); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if len(obs.events) != 1 {
		t.Fatalf("observer received %d events, want 1", len(obs.events))
	}
	got := obs.events[0]
	if got.Transition != TransitionPromoteCurrent {
		t.Errorf("Transition = %q, want %q", got.Transition, TransitionPromoteCurrent)
	}
	if got.FromKeyID != "kid-1" {
		t.Errorf("FromKeyID = %q, want kid-1", got.FromKeyID)
	}
	if got.ToKeyID != "kid-2" {
		t.Errorf("ToKeyID = %q, want kid-2", got.ToKeyID)
	}
	if !got.At.Equal(clock) {
		t.Errorf("At = %v, want %v", got.At, clock)
	}
}

// spec: 10.3
// diagnosis: a failed Rotate (nil next, empty kid, kid collision)
// still emitted a rotation event. The observer must see events only
// for transitions that committed to the key set.
func TestRotatingVerifierRotateDoesNotEmitOnFailure(t *testing.T) {
	t.Parallel()
	cur := NewHMACSigner("kid-1", []byte("secret-1"))
	v := NewRotatingVerifier(cur, DefaultOverlapWindow)
	obs := &recordingRotationObserver{}
	v.SetObserver(obs)

	_ = v.Rotate(nil)
	_ = v.Rotate(NewHMACSigner("", []byte("x")))
	_ = v.Rotate(NewHMACSigner("kid-1", []byte("y")))

	if len(obs.events) != 0 {
		t.Errorf("observer received %d events on failed rotations, want 0", len(obs.events))
	}
}

// spec: 10.3
// diagnosis: RetireExpired pruned previous keys without emitting one
// retire_previous event per pruned key, or emitted events for keys
// whose overlap window had not yet closed. The audit pipeline must
// see exactly one event per actual retirement.
func TestRetireExpiredEmitsOnePerPrunedKey(t *testing.T) {
	t.Parallel()
	k1 := NewHMACSigner("kid-1", []byte("secret-1"))
	k2 := NewHMACSigner("kid-2", []byte("secret-2"))
	k3 := NewHMACSigner("kid-3", []byte("secret-3"))

	clock := time.Date(2026, 5, 18, 0, 0, 0, 0, time.UTC)
	v := withClock(NewRotatingVerifier(k1, time.Hour), func() time.Time { return clock })
	obs := &recordingRotationObserver{}
	v.SetObserver(obs)

	if err := v.Rotate(k2); err != nil { // kid-1 retired at T+0
		t.Fatalf("Rotate k2: %v", err)
	}
	clock = clock.Add(30 * time.Minute)
	if err := v.Rotate(k3); err != nil { // kid-2 retired at T+30m
		t.Fatalf("Rotate k3: %v", err)
	}

	// Two promote events plus zero retire events so far.
	if got := countByTransition(obs.events, TransitionPromoteCurrent); got != 2 {
		t.Errorf("promote_current count = %d, want 2", got)
	}
	if got := countByTransition(obs.events, TransitionRetirePrevious); got != 0 {
		t.Errorf("retire_previous count = %d, want 0", got)
	}

	// Step past kid-1's window but before kid-2's: only kid-1 retires.
	clock = clock.Add(35 * time.Minute) // T+1h05m
	if removed := v.RetireExpired(); removed != 1 {
		t.Errorf("RetireExpired removed %d, want 1", removed)
	}
	// One retire event has appeared, naming kid-1.
	retires := filterByTransition(obs.events, TransitionRetirePrevious)
	if len(retires) != 1 {
		t.Fatalf("retire_previous count after first sweep = %d, want 1", len(retires))
	}
	if retires[0].FromKeyID != "kid-1" {
		t.Errorf("first retire FromKeyID = %q, want kid-1", retires[0].FromKeyID)
	}

	// Step past kid-2's window: it retires too.
	clock = clock.Add(40 * time.Minute) // T+1h45m, past kid-2 close at T+1h30m
	if removed := v.RetireExpired(); removed != 1 {
		t.Errorf("RetireExpired second sweep removed %d, want 1", removed)
	}
	retires = filterByTransition(obs.events, TransitionRetirePrevious)
	if len(retires) != 2 {
		t.Fatalf("retire_previous total = %d, want 2", len(retires))
	}
	// The second retire names kid-2 and stamps the current clock.
	if retires[1].FromKeyID != "kid-2" {
		t.Errorf("second retire FromKeyID = %q, want kid-2", retires[1].FromKeyID)
	}
	if !retires[1].At.Equal(clock) {
		t.Errorf("second retire At = %v, want %v", retires[1].At, clock)
	}
}

// countByTransition counts events with the named transition.
func countByTransition(events []RotationEvent, want string) int {
	n := 0
	for _, e := range events {
		if e.Transition == want {
			n++
		}
	}
	return n
}

// filterByTransition returns events whose transition matches want,
// preserving order.
func filterByTransition(events []RotationEvent, want string) []RotationEvent {
	out := make([]RotationEvent, 0, len(events))
	for _, e := range events {
		if e.Transition == want {
			out = append(out, e)
		}
	}
	return out
}

// assertPanics fails the test if fn does not panic.
func assertPanics(t *testing.T, name string, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Errorf("%s: expected a panic, got none", name)
		}
	}()
	fn()
}
