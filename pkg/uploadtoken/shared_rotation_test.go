// SPDX-License-Identifier: MIT

package uploadtoken_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/signingkeys"
	"github.com/lennylabs/lenny/pkg/uploadtoken"
)

// settableClock is a clock a test moves; the replicas of one test share it.
type settableClock struct{ now time.Time }

func (c *settableClock) Now() time.Time          { return c.now }
func (c *settableClock) Set(t time.Time)         { c.now = t }
func (c *settableClock) Advance(d time.Duration) { c.now = c.now.Add(d) }

// rotateOnPeer moves clock to the instant the first key of replicas opened
// at start falls due, refreshes peer there so it publishes the next key one
// publish lead ahead, and returns the next key's activation instant. The
// first key is backdated by the publish lead, so it falls due one lead
// before the rotation interval.
func rotateOnPeer(t *testing.T, clock *settableClock, start time.Time, peer sharedReplica) time.Time {
	t.Helper()
	clock.Set(start.Add(uploadtoken.DefaultRotationInterval - signingkeys.DefaultPublishLead))
	if err := peer.ring.Refresh(context.Background()); err != nil {
		t.Fatalf("peer refresh at the rotation: %v", err)
	}
	keys := peer.ring.VerificationKeys()
	if len(keys) != 2 {
		t.Fatalf("peer holds %d keys after the rotation, want the next key and the replaced key", len(keys))
	}
	return keys[0].ActiveFrom
}

// TestATokenMintedAtTheStaleBoundVerifiesOnAPeerUntilItExpires builds a
// replica whose last reload came one second before a peer published the
// next key. The stale replica does not learn of the next key and keeps
// signing with the replaced key until its last reload is as old as the
// stale bound. The token it mints then, valid for the whole token
// lifetime, verifies on the peer until it expires, after the peer has swept
// its retired keys, and is refused as expired afterwards.
//
// spec: §7.1 (Normal Flow)
func TestATokenMintedAtTheStaleBoundVerifiesOnAPeerUntilItExpires(t *testing.T) {
	start := time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)
	clock := &settableClock{now: start}
	provider, _ := kms.NewLocal(bytes.Repeat([]byte{7}, 32))
	store := signingkeys.NewMemoryStore()
	stale := newSharedReplica(t, store, provider, clock.Now)
	peer := newSharedReplica(t, store, provider, clock.Now)
	replaced, err := stale.ring.SigningKey()
	if err != nil {
		t.Fatal(err)
	}

	// The stale replica's last successful reload is one second before the
	// rotation falls due; after it, the stale replica never reloads.
	clock.Set(start.Add(uploadtoken.DefaultRotationInterval - signingkeys.DefaultPublishLead - time.Second))
	if err := stale.ring.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	lastReload := clock.Now()
	activeFrom := rotateOnPeer(t, clock, start, peer)

	// At its stale bound the stale replica still signs, with the replaced
	// key, while the peer already signs with the next key.
	clock.Set(lastReload.Add(uploadtoken.DefaultOverlapWindow))
	if !clock.Now().After(activeFrom) {
		t.Fatalf("the stale bound %s is not after the next key's activation %s", clock.Now(), activeFrom)
	}
	tok, parsed, err := stale.issuer.IssueDetailed("sess-stale", uploadtoken.DefaultTTL)
	if err != nil {
		t.Fatalf("mint at the stale bound: %v", err)
	}
	if parsed.KeyID != replaced.KeyID {
		t.Fatalf("the stale replica signed with %q, want the replaced key %q", parsed.KeyID, replaced.KeyID)
	}
	if k, _ := peer.ring.SigningKey(); k.KeyID == replaced.KeyID {
		t.Fatal("the peer still signs with the replaced key")
	}

	// The peer sweeps, then verifies at the token's expiry.
	clock.Set(parsed.Expiry)
	if err := peer.ring.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := peer.verifier.Verify(tok, "sess-stale"); err != nil {
		t.Fatalf("the peer refused an unexpired token the stale replica minted: %v", err)
	}
	clock.Advance(time.Second)
	if _, err := peer.verifier.Verify(tok, "sess-stale"); !errors.Is(err, uploadtoken.ErrExpired) {
		t.Errorf("verification after expiry = %v, want ErrExpired", err)
	}
}

// TestATokenLifetimeLongerThanTheOverlapWindowVerifiesAcrossARotation
// configures a token lifetime of 15 minutes, three times the overlap
// window. A token minted one second before the next key becomes active,
// with the replaced key, verifies on another replica until it expires, with
// the sweep run at the expiry. Once the replaced key's verification time
// has passed, the token is refused as invalid.
//
// spec: §7.1 (Normal Flow)
func TestATokenLifetimeLongerThanTheOverlapWindowVerifiesAcrossARotation(t *testing.T) {
	const lifetime = 15 * time.Minute
	start := time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)
	clock := &settableClock{now: start}
	provider, _ := kms.NewLocal(bytes.Repeat([]byte{9}, 32))
	store := signingkeys.NewMemoryStore()
	a := newSharedReplicaWithLifetime(t, store, provider, clock.Now, lifetime)
	b := newSharedReplicaWithLifetime(t, store, provider, clock.Now, lifetime)
	activeFrom := rotateOnPeer(t, clock, start, b)
	if err := a.ring.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}

	clock.Set(activeFrom.Add(-time.Second))
	tok, parsed, err := a.issuer.IssueDetailed("sess-long", lifetime)
	if err != nil {
		t.Fatal(err)
	}
	clock.Set(activeFrom)
	if k, _ := a.ring.SigningKey(); k.KeyID == parsed.KeyID {
		t.Fatal("the token was not signed with the replaced key")
	}

	clock.Set(parsed.Expiry)
	for _, r := range []sharedReplica{a, b} {
		if err := r.ring.Refresh(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.verifier.Verify(tok, "sess-long"); err != nil {
		t.Fatalf("a token within its %s lifetime was refused after the rotation: %v", lifetime, err)
	}
	clock.Set(activeFrom.Add(signingkeys.UploadTokenVerificationTime(uploadtoken.DefaultOverlapWindow, lifetime)))
	if err := b.ring.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := b.verifier.Verify(tok, "sess-long"); !errors.Is(err, uploadtoken.ErrInvalid) {
		t.Errorf("verification once the replaced key's verification time has passed = %v, want ErrInvalid", err)
	}
}
