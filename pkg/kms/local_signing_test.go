// SPDX-License-Identifier: MIT

package kms_test

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/kms/kmstest"
)

// testSeed is a fixed 32-byte master key shared by the providers that
// must derive one signing key.
var testSeed = bytes.Repeat([]byte{0x5a}, kms.DEKSize)

func newSeededLocal(t *testing.T, seed []byte) *kms.Local {
	t.Helper()
	l, err := kms.NewLocal(seed)
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	return l
}

func publicKeyBytes(t *testing.T, p kms.Provider, alias string) []byte {
	t.Helper()
	pub, err := p.PublicKey(context.Background(), alias)
	if err != nil {
		t.Fatalf("PublicKey(%s): %v", alias, err)
	}
	b, err := pub.Bytes()
	if err != nil {
		t.Fatalf("encode public key: %v", err)
	}
	return b
}

// spec: 10.2 (Authentication), 17.4 (Local Development Mode (lenny-dev))
// Two processes that mount one master key must derive one signing key,
// or a token one process signs fails on the other.
func TestLocalSigningKeyDeterministicAcrossProvidersSharingSeed_spec_10_2(t *testing.T) {
	t.Parallel()
	a := newSeededLocal(t, testSeed)
	b := newSeededLocal(t, testSeed)
	a.SetSigningReference(kms.AliasJWTSigning, "local-key-1")
	b.SetSigningReference(kms.AliasJWTSigning, "local-key-1")
	if !bytes.Equal(publicKeyBytes(t, a, kms.AliasJWTSigning), publicKeyBytes(t, b, kms.AliasJWTSigning)) {
		t.Fatal("two providers with one seed and one reference derived different signing keys")
	}
	digest := kmstest.Digest("header.payload")
	sig, err := a.Sign(context.Background(), kms.AliasJWTSigning, digest)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	pub, err := b.PublicKey(context.Background(), kms.AliasJWTSigning)
	if err != nil {
		t.Fatalf("PublicKey: %v", err)
	}
	if !kmstest.VerifyRS(pub, digest, sig) {
		t.Fatal("a signature from one provider does not verify under the other's public key")
	}
}

// spec: 10.2 (Authentication)
// Rotation under the local provider remaps the alias to a new reference,
// which must yield a new key; an unmapped alias derives from its name.
func TestLocalSigningKeyDistinctPerReference_spec_10_2(t *testing.T) {
	t.Parallel()
	l := newSeededLocal(t, testSeed)
	unmapped := publicKeyBytes(t, l, kms.AliasJWTSigning)

	l.SetSigningReference(kms.AliasJWTSigning, "local-key-1")
	l.SetSigningReference(kms.AliasJWTVerify, "local-key-2")
	k1 := publicKeyBytes(t, l, kms.AliasJWTSigning)
	k2 := publicKeyBytes(t, l, kms.AliasJWTVerify)
	if bytes.Equal(k1, k2) {
		t.Fatal("two different references derived the same key")
	}
	if bytes.Equal(unmapped, k1) {
		t.Fatal("remapping the alias did not change its key")
	}

	// The step-2 swap: jwt-verify now names the old reference and must
	// reproduce the old key.
	l.SetSigningReference(kms.AliasJWTVerify, "local-key-1")
	if !bytes.Equal(publicKeyBytes(t, l, kms.AliasJWTVerify), k1) {
		t.Fatal("mapping jwt-verify to the old reference did not reproduce the old key")
	}

	// An unmapped alias derives from the alias name: a fresh provider's
	// unmapped jwt-signing equals a provider mapping it to its own name.
	named := newSeededLocal(t, testSeed)
	named.SetSigningReference(kms.AliasJWTSigning, kms.AliasJWTSigning)
	if !bytes.Equal(publicKeyBytes(t, named, kms.AliasJWTSigning), unmapped) {
		t.Fatal("an unmapped alias did not derive from the alias name")
	}

	other := newSeededLocal(t, bytes.Repeat([]byte{0x11}, kms.DEKSize))
	if bytes.Equal(publicKeyBytes(t, other, kms.AliasJWTSigning), unmapped) {
		t.Fatal("two different seeds derived the same signing key")
	}
}

// spec: 10.2 (Authentication)
func TestLocalSignProducesVerifiable64ByteSignature_spec_10_2(t *testing.T) {
	t.Parallel()
	l := newSeededLocal(t, testSeed)
	ctx := context.Background()
	pub, err := l.PublicKey(ctx, kms.AliasJWTSigning)
	if err != nil {
		t.Fatalf("PublicKey: %v", err)
	}
	for i := 0; i < 64; i++ {
		digest := kmstest.Digest(string(rune('a' + i%26)))
		sig, err := l.Sign(ctx, kms.AliasJWTSigning, digest)
		if err != nil {
			t.Fatalf("Sign: %v", err)
		}
		if len(sig) != kms.SignatureSize {
			t.Fatalf("signature is %d bytes, want %d", len(sig), kms.SignatureSize)
		}
		if !kmstest.VerifyRS(pub, digest, sig) {
			t.Fatalf("signature %d does not verify under PublicKey", i)
		}
	}
}

// spec: 10.2 (Authentication)
func TestLocalSignRejectsNon32ByteDigestAndCancelledContext_spec_10_2(t *testing.T) {
	t.Parallel()
	l := newSeededLocal(t, testSeed)
	for _, n := range []int{0, 31, 33, 64} {
		if _, err := l.Sign(context.Background(), kms.AliasJWTSigning, make([]byte, n)); !errors.Is(err, kms.ErrInvalidDigest) {
			t.Errorf("Sign(%d-byte digest) err=%v, want ErrInvalidDigest", n, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := l.Sign(ctx, kms.AliasJWTSigning, kmstest.Digest("x")); !errors.Is(err, context.Canceled) {
		t.Errorf("Sign(cancelled) err=%v, want context.Canceled", err)
	}
	if _, err := l.PublicKey(ctx, kms.AliasJWTSigning); !errors.Is(err, context.Canceled) {
		t.Errorf("PublicKey(cancelled) err=%v, want context.Canceled", err)
	}
}

// spec: 10.2 (Authentication)
// Concurrent first use of one reference must settle on one key.
func TestLocalSigningKeyConcurrentFirstUseAgrees_spec_10_2(t *testing.T) {
	t.Parallel()
	l := newSeededLocal(t, testSeed)
	const n = 32
	keys := make([][]byte, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			pub, err := l.PublicKey(context.Background(), kms.AliasJWTSigning)
			if err != nil {
				t.Errorf("PublicKey: %v", err)
				return
			}
			keys[i], _ = pub.Bytes()
		}(i)
	}
	wg.Wait()
	for i := 1; i < n; i++ {
		if !bytes.Equal(keys[0], keys[i]) {
			t.Fatal("concurrent first use derived different keys")
		}
	}
}
