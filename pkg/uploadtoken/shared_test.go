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

// sharedReplica is one gateway replica's upload-token surface over a
// shared signing-key store.
type sharedReplica struct {
	ring     *signingkeys.Ring
	issuer   *uploadtoken.Issuer
	verifier *uploadtoken.Verifier
	redactor *uploadtoken.Redactor
}

func newSharedReplica(t *testing.T, store signingkeys.Store, provider kms.Provider, clock func() time.Time) sharedReplica {
	t.Helper()
	sealer, err := signingkeys.NewEnvelopeSealer(provider, signingkeys.AliasUploadTokenSigning)
	if err != nil {
		t.Fatal(err)
	}
	ring, err := signingkeys.Open(context.Background(),
		signingkeys.UploadTokenPolicy(uploadtoken.DefaultRotationInterval, uploadtoken.DefaultOverlapWindow),
		store, sealer, signingkeys.Options{StaleAfter: uploadtoken.DefaultOverlapWindow, Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	keys := uploadtoken.NewSharedKeys(ring)
	return sharedReplica{
		ring:     ring,
		issuer:   uploadtoken.NewIssuer(keys, clock),
		verifier: uploadtoken.NewVerifier(keys, nil, clock),
		redactor: uploadtoken.NewRedactor(keys),
	}
}

// TestATokenMintedOnOneReplicaVerifiesOnAnother builds two replicas over
// one signing-key store and one KMS master key, as two gateway processes
// over one Postgres and one KMS. A token either replica mints verifies on
// the other, and a stored response one replica redacted is reminted by the
// other with the original token.
//
// spec: §7.1 (Normal Flow), §11.5 (Idempotency)
func TestATokenMintedOnOneReplicaVerifiesOnAnother(t *testing.T) {
	now := time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	master := bytes.Repeat([]byte{3}, 32)
	pa, _ := kms.NewLocal(master)
	pb, _ := kms.NewLocal(master)
	store := signingkeys.NewMemoryStore()
	a := newSharedReplica(t, store, pa, clock)
	b := newSharedReplica(t, store, pb, clock)

	tok, err := a.issuer.Issue("sess-1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.verifier.Verify(tok, "sess-1"); err != nil {
		t.Fatalf("replica b refused replica a's token: %v", err)
	}
	tokB, _ := b.issuer.Issue("sess-2", time.Minute)
	if _, err := a.verifier.Verify(tokB, "sess-2"); err != nil {
		t.Fatalf("replica a refused replica b's token: %v", err)
	}
	body := []byte(`{"uploadToken":"` + tok + `"}`)
	stored := a.redactor.Strip(body)
	if bytes.Contains(stored, []byte(tok)) {
		t.Fatal("the stored response carries the token")
	}
	if got := b.redactor.Remint(stored); !bytes.Equal(got, body) {
		t.Errorf("replica b reminted %s, want %s", got, body)
	}
}

// TestATokenSignedWithAKeyThatLeftTheRingIsRefused checks that a token
// whose key is in no replica's ring, here one signed by a ring over
// another store, is refused as invalid, and that a replica whose ring has
// not been refreshed within the overlap window refuses to mint with
// ErrSigningUnavailable while it keeps verifying, and mints again after a
// successful refresh.
//
// spec: §7.1 (Normal Flow)
func TestATokenSignedWithAKeyThatLeftTheRingIsRefused(t *testing.T) {
	now := time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)
	current := now
	clock := func() time.Time { return current }
	provider, _ := kms.NewLocal(bytes.Repeat([]byte{5}, 32))
	a := newSharedReplica(t, signingkeys.NewMemoryStore(), provider, clock)
	foreign := newSharedReplica(t, signingkeys.NewMemoryStore(), provider, clock)
	tok, _ := foreign.issuer.Issue("sess-1", time.Minute)
	if _, err := a.verifier.Verify(tok, "sess-1"); !errors.Is(err, uploadtoken.ErrInvalid) {
		t.Errorf("token of a key outside the ring: %v, want ErrInvalid", err)
	}

	valid, _ := a.issuer.Issue("sess-2", time.Hour)
	current = now.Add(uploadtoken.DefaultOverlapWindow + time.Second)
	_, err := a.issuer.Issue("sess-3", time.Minute)
	if !errors.Is(err, uploadtoken.ErrSigningUnavailable) || !errors.Is(err, signingkeys.ErrSigningUnavailable) {
		t.Errorf("mint on a stale ring: %v, want ErrSigningUnavailable", err)
	}
	if _, err := a.verifier.Verify(valid, "sess-2"); err != nil {
		t.Errorf("verification on a stale ring: %v, want nil", err)
	}
	if err := a.ring.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := a.issuer.Issue("sess-3", time.Minute); err != nil {
		t.Errorf("mint after a successful refresh: %v", err)
	}
}
