// SPDX-License-Identifier: MIT

package connectoroauth

import (
	"strings"
	"testing"
	"time"
)

func newTestSigner(t *testing.T) *StateSigner {
	t.Helper()
	s, err := NewStateSigner(SigningKey{KeyID: "k1", Secret: []byte("test-state-signing-key-0123456789")})
	if err != nil {
		t.Fatalf("NewStateSigner: %v", err)
	}
	return s
}

func TestNewStateSignerRejectsEmptySecret(t *testing.T) {
	if _, err := NewStateSigner(SigningKey{KeyID: "k1"}); err == nil {
		t.Fatalf("NewStateSigner with empty secret: want error")
	}
}

// TestStateSignRoundTrip is the sign/verify round-trip: a minted state
// verifies, and two mints are distinct (the nonce is random).
func TestStateSignRoundTrip(t *testing.T) {
	s := newTestSigner(t)
	a, err := s.Mint()
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if err := s.Verify(a); err != nil {
		t.Fatalf("Verify of a freshly minted state: %v", err)
	}
	b, err := s.Mint()
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if a == b {
		t.Fatalf("two Mint calls returned an identical state; the nonce is not random")
	}
}

// TestStateTamperedSignatureRejected covers a forged callback: a state
// whose signature bytes were altered must fail verification, so an
// attacker who fabricates a `state` cannot drive a code exchange.
func TestStateTamperedSignatureRejected(t *testing.T) {
	s := newTestSigner(t)
	state, err := s.Mint()
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	nonce, sig, _ := strings.Cut(state, ".")

	// Flip the FIRST character of the signature segment. The last
	// character of a base64.RawURLEncoding string carries fewer bits
	// than a middle character (its low bits are padding-zero), so
	// flipping the last char can decode to the same bytes; the first
	// character is always meaningful and a flip always changes the
	// decoded signature.
	sigRunes := []rune(sig)
	if sigRunes[0] == 'A' {
		sigRunes[0] = 'B'
	} else {
		sigRunes[0] = 'A'
	}
	tampered := nonce + "." + string(sigRunes)
	if err := s.Verify(tampered); err != ErrStateSignatureInvalid {
		t.Fatalf("Verify of a tampered signature: got %v, want ErrStateSignatureInvalid", err)
	}

	// Tampering with the nonce must also fail: the signature no longer
	// covers the altered nonce.
	nonceRunes := []rune(nonce)
	if nonceRunes[0] == 'A' {
		nonceRunes[0] = 'B'
	} else {
		nonceRunes[0] = 'A'
	}
	tamperedNonce := string(nonceRunes) + "." + sig
	if err := s.Verify(tamperedNonce); err != ErrStateSignatureInvalid {
		t.Fatalf("Verify of a tampered nonce: got %v, want ErrStateSignatureInvalid", err)
	}
}

// TestStateForgedUnderWrongKeyRejected covers a state minted by a
// signer with a different key: it must not verify under this signer.
func TestStateForgedUnderWrongKeyRejected(t *testing.T) {
	mine := newTestSigner(t)
	attacker, err := NewStateSigner(SigningKey{KeyID: "evil", Secret: []byte("a-different-attacker-controlled-key")})
	if err != nil {
		t.Fatalf("NewStateSigner: %v", err)
	}
	forged, err := attacker.Mint()
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if err := mine.Verify(forged); err != ErrStateSignatureInvalid {
		t.Fatalf("Verify of a state minted under a foreign key: got %v, want ErrStateSignatureInvalid", err)
	}
}

func TestStateMalformedRejected(t *testing.T) {
	s := newTestSigner(t)
	for _, bad := range []string{
		"",
		"no-separator",
		".",
		"nonce.",
		".sig",
		"not!base64.also!bad",
	} {
		if err := s.Verify(bad); err != ErrStateMalformed && err != ErrStateSignatureInvalid {
			t.Fatalf("Verify(%q) = %v, want ErrStateMalformed or ErrStateSignatureInvalid", bad, err)
		}
	}
}

func TestStateSignerRotationAcceptsBothKeys(t *testing.T) {
	s := newTestSigner(t)
	old, err := s.Mint()
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if err := s.Rotate(SigningKey{KeyID: "k2", Secret: []byte("the-rotated-state-signing-key-xyz")}); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	// A state minted before the rotation still verifies (overlap key).
	if err := s.Verify(old); err != nil {
		t.Fatalf("Verify of a pre-rotation state: %v", err)
	}
	// A state minted after the rotation verifies too.
	fresh, err := s.Mint()
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if err := s.Verify(fresh); err != nil {
		t.Fatalf("Verify of a post-rotation state: %v", err)
	}
}

func sampleFlow(now time.Time) FlowContext {
	return FlowContext{
		ConnectorID:  "github",
		TenantID:     "acme",
		UserID:       "alice@acme.com",
		SessionID:    "sess_abc",
		CodeVerifier: "verifier-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		RedirectURI:  "https://gw.acme.com/v1/admin/connectors/oauth/callback",
		Scopes:       []string{"repo"},
		CreatedAt:    now,
	}
}

// TestStateStorePutConsumeRoundTrip stores a flow and consumes it once.
func TestStateStorePutConsumeRoundTrip(t *testing.T) {
	store := NewMemoryStateStore()
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	flow := sampleFlow(now)
	if err := store.Put("state-1", flow, DefaultStateTTL); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := store.Consume("state-1", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if got.ConnectorID != "github" || got.UserID != "alice@acme.com" || got.CodeVerifier != flow.CodeVerifier {
		t.Fatalf("Consume returned the wrong flow context: %+v", got)
	}
}

func TestStateStoreUnknownStateRejected(t *testing.T) {
	store := NewMemoryStateStore()
	if _, err := store.Consume("never-issued", time.Now()); err != ErrStateUnknown {
		t.Fatalf("Consume of an unknown state: got %v, want ErrStateUnknown", err)
	}
}

// TestStateStoreExpiredStateRejected covers the §9.3 10-minute TTL: a
// callback that arrives after the TTL must be rejected.
func TestStateStoreExpiredStateRejected(t *testing.T) {
	store := NewMemoryStateStore()
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	if err := store.Put("state-1", sampleFlow(now), DefaultStateTTL); err != nil {
		t.Fatalf("Put: %v", err)
	}
	// One second past the 10-minute TTL.
	late := now.Add(DefaultStateTTL + time.Second)
	if _, err := store.Consume("state-1", late); err != ErrStateExpired {
		t.Fatalf("Consume past the TTL: got %v, want ErrStateExpired", err)
	}
	// An expired state cannot then be redeemed even at an earlier
	// clock: it was marked consumed.
	if _, err := store.Consume("state-1", now.Add(time.Minute)); err != ErrStateConsumed {
		t.Fatalf("Consume after an expiry: got %v, want ErrStateConsumed", err)
	}
}

// TestStateStoreReplayedStateRejected covers a replayed callback: a
// state consumed once must fail a second Consume, so a captured
// callback cannot be replayed into a second token exchange.
func TestStateStoreReplayedStateRejected(t *testing.T) {
	store := NewMemoryStateStore()
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	if err := store.Put("state-1", sampleFlow(now), DefaultStateTTL); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, err := store.Consume("state-1", now.Add(time.Minute)); err != nil {
		t.Fatalf("first Consume: %v", err)
	}
	if _, err := store.Consume("state-1", now.Add(2*time.Minute)); err != ErrStateConsumed {
		t.Fatalf("replayed Consume: got %v, want ErrStateConsumed", err)
	}
}

func TestStateStoreSweep(t *testing.T) {
	store := NewMemoryStateStore()
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	_ = store.Put("live", sampleFlow(now), DefaultStateTTL)
	_ = store.Put("stale", sampleFlow(now), DefaultStateTTL)
	_ = store.Put("used", sampleFlow(now), DefaultStateTTL)
	if _, err := store.Consume("used", now.Add(time.Minute)); err != nil {
		t.Fatalf("Consume: %v", err)
	}
	// Sweep at a time past the TTL: "stale" and "used" go, "live"
	// stays only if still inside its TTL — here it is not, so all are
	// reclaimed. Sweep just past one minute keeps "live".
	dropped := store.Sweep(now.Add(2 * time.Minute))
	if dropped != 1 {
		t.Fatalf("Sweep at now+2m dropped %d, want 1 (the consumed entry)", dropped)
	}
	if _, err := store.Consume("live", now.Add(3*time.Minute)); err != nil {
		t.Fatalf("live entry was swept prematurely: %v", err)
	}
}
