// SPDX-License-Identifier: MIT

//go:build security

// security-tier probes of the shared upload-token signing keys: the stored
// form of a key never carries its secret, a token signed with a key that has
// left every replica's ring is refused, and a token signed under a key no
// replica holds is refused, however well-formed it is.
package security_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/signingkeys"
	"github.com/lennylabs/lenny/pkg/uploadtoken"
)

// keysClock is a settable clock.
type keysClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *keysClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *keysClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// spec: §7.1 (Normal Flow), §12.9 (Data Classification)
// diagnosis: The shared upload-token signing keys leak or outlive their
// window. Either a stored key row carries the HMAC secret in plaintext
// (a database dump would let an attacker forge tokens for any session), a
// token signed with a key past its verification time still verifies, or a
// token forged under a key no replica holds verifies. See pkg/signingkeys
// and pkg/uploadtoken.
func TestUploadTokenKeysNeverLeakAndRetiredKeysStopVerifying(t *testing.T) {
	ctx := context.Background()
	store := signingkeys.NewMemoryStore()
	provider, err := kms.NewLocal(bytes.Repeat([]byte{0x51}, 32))
	if err != nil {
		t.Fatal(err)
	}
	sealer, err := signingkeys.NewEnvelopeSealer(provider, signingkeys.AliasUploadTokenSigning)
	if err != nil {
		t.Fatal(err)
	}
	clock := &keysClock{now: time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)}
	ring, err := signingkeys.Open(ctx,
		signingkeys.UploadTokenPolicy(uploadtoken.DefaultRotationInterval, uploadtoken.DefaultOverlapWindow, uploadtoken.DefaultTTL),
		store, sealer, signingkeys.Options{StaleAfter: uploadtoken.DefaultOverlapWindow, Clock: clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	keys := uploadtoken.NewSharedKeys(ring)
	issuer := uploadtoken.NewIssuer(keys, clock.Now)
	verifier := uploadtoken.NewVerifier(keys, nil, clock.Now)

	first, err := ring.SigningKey()
	if err != nil {
		t.Fatal(err)
	}
	recs, _ := store.List(ctx, signingkeys.PurposeUploadToken, clock.Now())
	for _, rec := range recs {
		if bytes.Contains(rec.WrappedSecret, first.Secret) || bytes.Contains(rec.WrappedSecret, []byte(hex.EncodeToString(first.Secret))) {
			t.Fatalf("stored key %s carries its secret", rec.KeyID)
		}
	}

	// A token signed under a key no replica holds is refused.
	forged := forgeToken([]byte("attacker-key"), "sess-1", clock.Now().Add(time.Hour))
	if _, err := verifier.Verify(forged, "sess-1"); !errors.Is(err, uploadtoken.ErrInvalid) {
		t.Errorf("forged token: %v, want ErrInvalid", err)
	}

	// A token whose own expiry is far away stops verifying once its key has
	// left the ring: rotation, then the replaced key's verification time
	// after the next key becomes active.
	old, err := issuer.Issue("sess-2", 48*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(uploadtoken.DefaultRotationInterval)
	if err := ring.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	clock.Advance(signingkeys.DefaultPublishLead + signingkeys.UploadTokenVerificationTime(uploadtoken.DefaultOverlapWindow, uploadtoken.DefaultTTL))
	if err := ring.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.Verify(old, "sess-2"); !errors.Is(err, uploadtoken.ErrInvalid) {
		t.Errorf("token of a key past its verification time: %v, want ErrInvalid", err)
	}
	fresh, err := issuer.Issue("sess-3", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.Verify(fresh, "sess-3"); err != nil {
		t.Errorf("token of the current key: %v, want nil", err)
	}
}

// forgeToken signs a well-formed upload token for sessionID under key.
func forgeToken(key []byte, sessionID string, expiry time.Time) string {
	exp := strconv.FormatInt(expiry.Unix(), 10)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(sessionID + "." + exp))
	return sessionID + "." + exp + "." + hex.EncodeToString(mac.Sum(nil))
}
