// SPDX-License-Identifier: MIT

package kms_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"testing"

	pkgkms "github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/kms/envelope"
	"github.com/lennylabs/lenny/tests/testinfra/stubs/kms"
)

// stubDEK returns a random 32-byte data-encryption-key.
func stubDEK(t *testing.T) []byte {
	t.Helper()
	dek := make([]byte, pkgkms.DEKSize)
	if _, err := rand.Read(dek); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return dek
}

// spec: 4 (the stub satisfies pkg/kms.Provider)
// diagnosis: the stub's Provider adapter did not round-trip a DEK
// through WrapDEK/UnwrapDEK, so the §4 envelope path could not be
// driven against the stub.
func TestProviderWrapUnwrapRoundTrip(t *testing.T) {
	t.Parallel()
	p := kms.New(t).AsProvider()
	ctx := context.Background()
	dek := stubDEK(t)

	wrapped, err := p.WrapDEK(ctx, "tenant:acme", dek)
	if err != nil {
		t.Fatalf("WrapDEK: %v", err)
	}
	if wrapped.KEKVersion != 1 {
		t.Errorf("first WrapDEK version: got %d, want 1", wrapped.KEKVersion)
	}
	if bytes.Equal(wrapped.Ciphertext, dek) {
		t.Error("wrapped DEK equals the plaintext DEK")
	}
	got, err := p.UnwrapDEK(ctx, "tenant:acme", wrapped)
	if err != nil {
		t.Fatalf("UnwrapDEK: %v", err)
	}
	if !bytes.Equal(got, dek) {
		t.Errorf("round-trip mismatch:\n got %x\nwant %x", got, dek)
	}
}

// spec: 4 (fault injection reaches the envelope helper)
// diagnosis: a KMS fault injected on the stub did not surface through
// the Provider adapter, so an envelope test could not exercise the
// fail-closed path.
func TestProviderSurfacesStubFault(t *testing.T) {
	t.Parallel()
	stub := kms.New(t)
	p := stub.AsProvider()
	ctx := context.Background()

	stub.SetUnavailable(true)
	if _, err := p.WrapDEK(ctx, "tenant:acme", stubDEK(t)); !errors.Is(err, kms.ErrUnavailable) {
		t.Errorf("WrapDEK under an unavailable KMS: got %v, want ErrUnavailable", err)
	}
	stub.SetUnavailable(false)
	if _, err := p.WrapDEK(ctx, "tenant:acme", stubDEK(t)); err != nil {
		t.Errorf("WrapDEK after recovery: %v", err)
	}
}

// spec: 4 (the stub Provider drives the envelope helper end to end)
// diagnosis: the envelope helper did not Seal/Open over the stub
// Provider, so component tests could not envelope-encrypt against the
// stub the rest of the credential suite uses.
func TestProviderDrivesEnvelope(t *testing.T) {
	t.Parallel()
	p := kms.New(t).AsProvider()
	cipher, err := envelope.New(p, "tenant:acme")
	if err != nil {
		t.Fatalf("envelope.New: %v", err)
	}
	ctx := context.Background()

	sealed, err := cipher.Seal(ctx, []byte("an-upstream-key"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if bytes.Contains(sealed.Ciphertext, []byte("an-upstream-key")) {
		t.Error("sealed ciphertext contains the plaintext")
	}
	got, err := cipher.Open(ctx, sealed)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if string(got) != "an-upstream-key" {
		t.Errorf("round-trip: got %q, want an-upstream-key", got)
	}
}

// spec: 4.9.1 (KEK rotation through the stub Provider)
// diagnosis: a KEK rotated on the stub did not advance the version the
// Provider reports, so the §4.9.1 re-encryption path could not be
// rehearsed against the stub.
func TestProviderRotationAdvancesVersion(t *testing.T) {
	t.Parallel()
	stub := kms.New(t)
	p := stub.AsProvider()
	ctx := context.Background()

	if _, err := p.WrapDEK(ctx, "tenant:acme", stubDEK(t)); err != nil {
		t.Fatalf("WrapDEK: %v", err)
	}
	v, err := p.CurrentKEKVersion(ctx, "tenant:acme")
	if err != nil {
		t.Fatalf("CurrentKEKVersion: %v", err)
	}
	if v != 1 {
		t.Fatalf("version before rotation: got %d, want 1", v)
	}
	if _, err := stub.RotateKey("tenant:acme", kms.TriggerManual); err != nil {
		t.Fatalf("RotateKey: %v", err)
	}
	v, err = p.CurrentKEKVersion(ctx, "tenant:acme")
	if err != nil {
		t.Fatalf("CurrentKEKVersion after rotation: %v", err)
	}
	if v != 2 {
		t.Errorf("version after rotation: got %d, want 2", v)
	}
}
