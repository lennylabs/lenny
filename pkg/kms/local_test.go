// SPDX-License-Identifier: MIT

package kms_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/kms"
)

// freshDEK returns a random 32-byte data-encryption-key.
func freshDEK(t *testing.T) []byte {
	t.Helper()
	dek := make([]byte, kms.DEKSize)
	if _, err := rand.Read(dek); err != nil {
		t.Fatalf("rand DEK: %v", err)
	}
	return dek
}

// spec: 4
// diagnosis: the local KEK provider did not round-trip a DEK. WrapDEK
// then UnwrapDEK must recover the exact key bytes.
func TestLocalWrapUnwrapRoundTrip(t *testing.T) {
	t.Parallel()
	provider, err := kms.NewLocalRandom()
	if err != nil {
		t.Fatalf("NewLocalRandom: %v", err)
	}
	ctx := context.Background()
	dek := freshDEK(t)

	wrapped, err := provider.WrapDEK(ctx, "tenant:acme", dek)
	if err != nil {
		t.Fatalf("WrapDEK: %v", err)
	}
	if wrapped.KEKVersion != 1 {
		t.Errorf("first WrapDEK KEK version: got %d, want 1", wrapped.KEKVersion)
	}
	if bytes.Equal(wrapped.Ciphertext, dek) {
		t.Error("wrapped DEK equals the plaintext DEK; the DEK was not encrypted")
	}
	got, err := provider.UnwrapDEK(ctx, "tenant:acme", wrapped)
	if err != nil {
		t.Fatalf("UnwrapDEK: %v", err)
	}
	if !bytes.Equal(got, dek) {
		t.Errorf("round-trip mismatch:\n got %x\nwant %x", got, dek)
	}
}

// spec: 4
// diagnosis: a fixed-seed local provider was not deterministic. Two
// providers built from the same seed must unwrap each other's wrapped
// DEKs, which is what lets replicas share the same KEK.
func TestLocalDeterministicAcrossInstances(t *testing.T) {
	t.Parallel()
	seed := make([]byte, kms.DEKSize)
	for i := range seed {
		seed[i] = byte(i)
	}
	a, err := kms.NewLocal(seed)
	if err != nil {
		t.Fatalf("NewLocal a: %v", err)
	}
	b, err := kms.NewLocal(seed)
	if err != nil {
		t.Fatalf("NewLocal b: %v", err)
	}
	ctx := context.Background()
	dek := freshDEK(t)

	wrapped, err := a.WrapDEK(ctx, "tenant:acme", dek)
	if err != nil {
		t.Fatalf("a.WrapDEK: %v", err)
	}
	got, err := b.UnwrapDEK(ctx, "tenant:acme", wrapped)
	if err != nil {
		t.Fatalf("b.UnwrapDEK: %v", err)
	}
	if !bytes.Equal(got, dek) {
		t.Error("a fixed-seed provider did not unwrap another instance's wrapped DEK")
	}
}

// spec: 4
// diagnosis: a different-seed provider unwrapped a wrapped DEK. A
// wrapped DEK must not unwrap under an unrelated KEK seed.
func TestLocalWrongSeedFailsUnwrap(t *testing.T) {
	t.Parallel()
	seedA := bytes.Repeat([]byte{0xa1}, kms.DEKSize)
	seedB := bytes.Repeat([]byte{0xb2}, kms.DEKSize)
	a, err := kms.NewLocal(seedA)
	if err != nil {
		t.Fatalf("NewLocal a: %v", err)
	}
	b, err := kms.NewLocal(seedB)
	if err != nil {
		t.Fatalf("NewLocal b: %v", err)
	}
	ctx := context.Background()

	wrapped, err := a.WrapDEK(ctx, "tenant:acme", freshDEK(t))
	if err != nil {
		t.Fatalf("a.WrapDEK: %v", err)
	}
	if _, err := b.UnwrapDEK(ctx, "tenant:acme", wrapped); err == nil {
		t.Error("UnwrapDEK under a different KEK seed should fail")
	} else if !errors.Is(err, kms.ErrUnwrap) {
		t.Errorf("wrong-seed unwrap error: got %v, want ErrUnwrap", err)
	}
}

// spec: 4
// diagnosis: a wrapped DEK unwrapped under the wrong alias. The alias
// is bound into the wrap, so an alias substitution must fail even
// though both alias KEKs derive from the same seed.
func TestLocalAliasBinding(t *testing.T) {
	t.Parallel()
	provider, err := kms.NewLocalRandom()
	if err != nil {
		t.Fatalf("NewLocalRandom: %v", err)
	}
	ctx := context.Background()

	wrapped, err := provider.WrapDEK(ctx, "tenant:acme", freshDEK(t))
	if err != nil {
		t.Fatalf("WrapDEK: %v", err)
	}
	if _, err := provider.UnwrapDEK(ctx, "tenant:globex", wrapped); err == nil {
		t.Error("UnwrapDEK under a different alias should fail")
	} else if !errors.Is(err, kms.ErrUnwrap) {
		t.Errorf("wrong-alias unwrap error: got %v, want ErrUnwrap", err)
	}
}

// spec: 4
// diagnosis: a tampered wrapped DEK decrypted cleanly. AES-GCM
// authenticates the wrapped DEK; any bit flip must fail the unwrap.
func TestLocalTamperedWrappedDEKFails(t *testing.T) {
	t.Parallel()
	provider, err := kms.NewLocalRandom()
	if err != nil {
		t.Fatalf("NewLocalRandom: %v", err)
	}
	ctx := context.Background()

	wrapped, err := provider.WrapDEK(ctx, "tenant:acme", freshDEK(t))
	if err != nil {
		t.Fatalf("WrapDEK: %v", err)
	}
	// Flip the last byte of the wrapped DEK ciphertext.
	tampered := kms.WrappedDEK{
		KEKVersion: wrapped.KEKVersion,
		Ciphertext: append([]byte(nil), wrapped.Ciphertext...),
	}
	tampered.Ciphertext[len(tampered.Ciphertext)-1] ^= 0x01
	if _, err := provider.UnwrapDEK(ctx, "tenant:acme", tampered); err == nil {
		t.Error("UnwrapDEK of a tampered wrapped DEK should fail")
	} else if !errors.Is(err, kms.ErrUnwrap) {
		t.Errorf("tampered unwrap error: got %v, want ErrUnwrap", err)
	}
}

// spec: 4.9.1
// diagnosis: KEK rotation broke decryption of a DEK wrapped under a
// prior version. After RotateKEK, WrapDEK must stamp the new version
// while UnwrapDEK still recovers a DEK wrapped under the old version.
func TestLocalKEKRotation(t *testing.T) {
	t.Parallel()
	provider, err := kms.NewLocalRandom()
	if err != nil {
		t.Fatalf("NewLocalRandom: %v", err)
	}
	ctx := context.Background()
	dek := freshDEK(t)

	v1Wrapped, err := provider.WrapDEK(ctx, "tenant:acme", dek)
	if err != nil {
		t.Fatalf("WrapDEK v1: %v", err)
	}
	if v1Wrapped.KEKVersion != 1 {
		t.Fatalf("pre-rotation version: got %d, want 1", v1Wrapped.KEKVersion)
	}

	newVersion := provider.RotateKEK("tenant:acme")
	if newVersion != 2 {
		t.Fatalf("RotateKEK returned %d, want 2", newVersion)
	}
	cur, err := provider.CurrentKEKVersion(ctx, "tenant:acme")
	if err != nil {
		t.Fatalf("CurrentKEKVersion: %v", err)
	}
	if cur != 2 {
		t.Errorf("CurrentKEKVersion after rotation: got %d, want 2", cur)
	}

	// A DEK wrapped after the rotation carries version 2.
	v2Wrapped, err := provider.WrapDEK(ctx, "tenant:acme", dek)
	if err != nil {
		t.Fatalf("WrapDEK v2: %v", err)
	}
	if v2Wrapped.KEKVersion != 2 {
		t.Errorf("post-rotation version: got %d, want 2", v2Wrapped.KEKVersion)
	}

	// The §4.9.1 property: a DEK wrapped under the superseded version
	// still unwraps after the rotation.
	gotV1, err := provider.UnwrapDEK(ctx, "tenant:acme", v1Wrapped)
	if err != nil {
		t.Fatalf("UnwrapDEK of a v1-wrapped DEK after rotation: %v", err)
	}
	if !bytes.Equal(gotV1, dek) {
		t.Error("prior-version unwrap returned the wrong DEK")
	}
	gotV2, err := provider.UnwrapDEK(ctx, "tenant:acme", v2Wrapped)
	if err != nil {
		t.Fatalf("UnwrapDEK of a v2-wrapped DEK: %v", err)
	}
	if !bytes.Equal(gotV2, dek) {
		t.Error("current-version unwrap returned the wrong DEK")
	}
}

// spec: 4
// diagnosis: UnwrapDEK accepted a KEK version the provider never
// minted. A future version must fail rather than derive a key.
func TestLocalUnknownKEKVersionFails(t *testing.T) {
	t.Parallel()
	provider, err := kms.NewLocalRandom()
	if err != nil {
		t.Fatalf("NewLocalRandom: %v", err)
	}
	ctx := context.Background()

	wrapped, err := provider.WrapDEK(ctx, "tenant:acme", freshDEK(t))
	if err != nil {
		t.Fatalf("WrapDEK: %v", err)
	}
	wrapped.KEKVersion = 99 // a version no rotation has minted
	if _, err := provider.UnwrapDEK(ctx, "tenant:acme", wrapped); !errors.Is(err, kms.ErrUnknownKEKVersion) {
		t.Errorf("future-version unwrap: got %v, want ErrUnknownKEKVersion", err)
	}
	wrapped.KEKVersion = 0
	if _, err := provider.UnwrapDEK(ctx, "tenant:acme", wrapped); !errors.Is(err, kms.ErrUnknownKEKVersion) {
		t.Errorf("zero-version unwrap: got %v, want ErrUnknownKEKVersion", err)
	}
}

// spec: 4
// diagnosis: WrapDEK accepted a DEK whose length was not 32 bytes,
// which would silently weaken the envelope cipher.
func TestLocalWrapRejectsWrongDEKSize(t *testing.T) {
	t.Parallel()
	provider, err := kms.NewLocalRandom()
	if err != nil {
		t.Fatalf("NewLocalRandom: %v", err)
	}
	ctx := context.Background()
	for _, size := range []int{0, 16, 31, 33, 64} {
		if _, err := provider.WrapDEK(ctx, "tenant:acme", make([]byte, size)); err == nil {
			t.Errorf("WrapDEK accepted a %d-byte DEK; want a rejection", size)
		}
	}
}

// spec: 4
// diagnosis: NewLocal accepted a seed shorter than 32 bytes, which
// would weaken every derived KEK.
func TestLocalRejectsShortSeed(t *testing.T) {
	t.Parallel()
	if _, err := kms.NewLocal(make([]byte, 16)); err == nil {
		t.Error("NewLocal accepted a 16-byte seed; want a rejection")
	}
	if _, err := kms.NewLocal(make([]byte, kms.DEKSize)); err != nil {
		t.Errorf("NewLocal rejected a 32-byte seed: %v", err)
	}
}

// spec: 4
// diagnosis: UnwrapDEK accepted a structurally invalid wrapped DEK
// instead of reporting ErrMalformedWrappedDEK.
func TestLocalMalformedWrappedDEK(t *testing.T) {
	t.Parallel()
	provider, err := kms.NewLocalRandom()
	if err != nil {
		t.Fatalf("NewLocalRandom: %v", err)
	}
	ctx := context.Background()
	// Provision the alias so the version check passes and the length
	// check is what fails.
	if _, err := provider.WrapDEK(ctx, "tenant:acme", freshDEK(t)); err != nil {
		t.Fatalf("WrapDEK: %v", err)
	}
	short := kms.WrappedDEK{KEKVersion: 1, Ciphertext: []byte{0x00, 0x01}}
	if _, err := provider.UnwrapDEK(ctx, "tenant:acme", short); !errors.Is(err, kms.ErrMalformedWrappedDEK) {
		t.Errorf("short wrapped DEK: got %v, want ErrMalformedWrappedDEK", err)
	}
}
