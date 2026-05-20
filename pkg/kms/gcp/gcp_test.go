// SPDX-License-Identifier: MIT

package gcp

import (
	"context"
	"errors"
	"testing"

	"cloud.google.com/go/kms/apiv1/kmspb"
	"google.golang.org/api/option"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/kms"
)

// fakeKMS mirrors the Cloud KMS Encrypt / Decrypt invariants without
// requiring a real API endpoint. The fake's "ciphertext" carries the
// (key name, AAD, xor(plaintext, key material)) tuple so a tampered
// or cross-AAD decrypt fails authentically.
type fakeKMS struct {
	keys        map[string][]byte
	failEncrypt error
	failDecrypt error
	lastAAD     []byte
}

func newFakeKMS() *fakeKMS {
	return &fakeKMS{keys: map[string][]byte{}}
}

func (f *fakeKMS) seed(name string, material []byte) {
	f.keys[name] = material
}

func (f *fakeKMS) Encrypt(_ context.Context, req *kmspb.EncryptRequest, _ ...option.ClientOption) (*kmspb.EncryptResponse, error) {
	if f.failEncrypt != nil {
		err := f.failEncrypt
		f.failEncrypt = nil
		return nil, err
	}
	f.lastAAD = req.AdditionalAuthenticatedData
	material, ok := f.keys[req.Name]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "key %s not found", req.Name)
	}
	envelope := []byte(req.Name + "|")
	envelope = append(envelope, req.AdditionalAuthenticatedData...)
	envelope = append(envelope, '|')
	envelope = append(envelope, xorBytes(req.Plaintext, material)...)
	return &kmspb.EncryptResponse{Ciphertext: envelope, Name: req.Name}, nil
}

func (f *fakeKMS) Decrypt(_ context.Context, req *kmspb.DecryptRequest, _ ...option.ClientOption) (*kmspb.DecryptResponse, error) {
	if f.failDecrypt != nil {
		err := f.failDecrypt
		f.failDecrypt = nil
		return nil, err
	}
	f.lastAAD = req.AdditionalAuthenticatedData
	material, ok := f.keys[req.Name]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "key %s not found", req.Name)
	}
	parts := splitN(req.Ciphertext, '|', 3)
	if len(parts) != 3 {
		return nil, status.Errorf(codes.InvalidArgument, "malformed envelope")
	}
	if string(parts[0]) != req.Name {
		return nil, status.Errorf(codes.InvalidArgument, "key name mismatch")
	}
	if string(parts[1]) != string(req.AdditionalAuthenticatedData) {
		return nil, status.Errorf(codes.InvalidArgument, "AAD mismatch")
	}
	pt := xorBytes(parts[2], material)
	if len(pt) != kms.DEKSize {
		return nil, status.Errorf(codes.InvalidArgument, "invalid ciphertext")
	}
	return &kmspb.DecryptResponse{Plaintext: pt}, nil
}

func (f *fakeKMS) GetCryptoKey(_ context.Context, req *kmspb.GetCryptoKeyRequest, _ ...option.ClientOption) (*kmspb.CryptoKey, error) {
	if _, ok := f.keys[req.Name]; !ok {
		return nil, status.Errorf(codes.NotFound, "key %s not found", req.Name)
	}
	return &kmspb.CryptoKey{Name: req.Name}, nil
}

func xorBytes(data, material []byte) []byte {
	out := make([]byte, len(data))
	for i := range data {
		out[i] = data[i] ^ material[i%len(material)]
	}
	return out
}

func splitN(s []byte, sep byte, n int) [][]byte {
	out := [][]byte{}
	cur := s
	for i := 0; i < n-1; i++ {
		idx := -1
		for j, b := range cur {
			if b == sep {
				idx = j
				break
			}
		}
		if idx < 0 {
			out = append(out, cur)
			return out
		}
		out = append(out, cur[:idx])
		cur = cur[idx+1:]
	}
	out = append(out, cur)
	return out
}

func newTestProvider(t *testing.T) (*Provider, *fakeKMS) {
	t.Helper()
	f := newFakeKMS()
	f.seed("projects/p/locations/us/keyRings/r/cryptoKeys/k-acme", []byte("0123456789abcdef0123456789abcdef"))
	f.seed("projects/p/locations/us/keyRings/r/cryptoKeys/k-platform", []byte("ABCDEFGHIJKLMNOPQRSTUVWXYZ012345"))
	p := newWithClient(f, map[string]string{
		"tenant:acme":                    "projects/p/locations/us/keyRings/r/cryptoKeys/k-acme",
		"platform:token-service-signing": "projects/p/locations/us/keyRings/r/cryptoKeys/k-platform",
	})
	return p, f
}

// spec: §4 / §4.9 — Wrap/Unwrap round-trip on a known alias.
// diagnosis: Encrypt followed by Decrypt under the same alias must recover the original DEK byte-for-byte.
func TestWrapUnwrapRoundTrip(t *testing.T) {
	p, _ := newTestProvider(t)
	dek := []byte("0123456789abcdef0123456789abcdef")
	ctx := context.Background()
	wrapped, err := p.WrapDEK(ctx, "tenant:acme", dek)
	if err != nil {
		t.Fatalf("WrapDEK: %v", err)
	}
	got, err := p.UnwrapDEK(ctx, "tenant:acme", wrapped)
	if err != nil {
		t.Fatalf("UnwrapDEK: %v", err)
	}
	if string(got) != string(dek) {
		t.Errorf("round-trip mismatch: got %q, want %q", got, dek)
	}
}

// spec: §4.9 — WrapDEK on an unmapped alias surfaces ErrUnknownKEK.
// diagnosis: the Provider rejects unmapped aliases before contacting Cloud KMS so misconfiguration fails fast.
func TestWrapDEKUnknownAlias(t *testing.T) {
	p, _ := newTestProvider(t)
	_, err := p.WrapDEK(context.Background(), "tenant:bob", []byte("0123456789abcdef0123456789abcdef"))
	if !errors.Is(err, kms.ErrUnknownKEK) {
		t.Errorf("WrapDEK unmapped: %v, want ErrUnknownKEK", err)
	}
}

// spec: §4.9.1 — cross-alias unwrap rejected via AAD binding.
// diagnosis: a wrapped DEK from one tenant alias must not unwrap under another; the AAD ("lenny.alias=...") is the cryptographic binding.
func TestUnwrapDEKRejectsCrossAlias(t *testing.T) {
	p, _ := newTestProvider(t)
	dek := []byte("0123456789abcdef0123456789abcdef")
	wrapped, err := p.WrapDEK(context.Background(), "tenant:acme", dek)
	if err != nil {
		t.Fatalf("WrapDEK: %v", err)
	}
	_, err = p.UnwrapDEK(context.Background(), "platform:token-service-signing", wrapped)
	if err == nil {
		t.Error("UnwrapDEK under a different alias should fail")
	}
}

// spec: §4 — gRPC error codes map to the kms package's sentinel errors.
// diagnosis: codes.NotFound from Decrypt maps to ErrUnknownKEK; codes.InvalidArgument maps to ErrUnwrap (the AAD-binding rejection path).
func TestUnwrapDEKMapsNotFound(t *testing.T) {
	p, f := newTestProvider(t)
	f.failDecrypt = status.Errorf(codes.NotFound, "missing")
	_, err := p.UnwrapDEK(context.Background(), "tenant:acme", kms.WrappedDEK{KEKVersion: 1, Ciphertext: []byte{0x00}})
	if !errors.Is(err, kms.ErrUnknownKEK) {
		t.Errorf("NotFound: %v, want ErrUnknownKEK", err)
	}
}

func TestUnwrapDEKMapsInvalidArgument(t *testing.T) {
	p, f := newTestProvider(t)
	f.failDecrypt = status.Errorf(codes.InvalidArgument, "AAD mismatch")
	_, err := p.UnwrapDEK(context.Background(), "tenant:acme", kms.WrappedDEK{KEKVersion: 1, Ciphertext: []byte{0x00}})
	if !errors.Is(err, kms.ErrUnwrap) {
		t.Errorf("InvalidArgument: %v, want ErrUnwrap", err)
	}
}

// spec: §4 — CurrentKEKVersion confirms alias addressability.
// diagnosis: a known alias returns version 1; an unknown alias surfaces ErrUnknownKEK.
func TestCurrentKEKVersionKnownAndUnknown(t *testing.T) {
	p, _ := newTestProvider(t)
	v, err := p.CurrentKEKVersion(context.Background(), "tenant:acme")
	if err != nil {
		t.Fatalf("known: %v", err)
	}
	if v != 1 {
		t.Errorf("version: got %d, want 1", v)
	}
	if _, err := p.CurrentKEKVersion(context.Background(), "tenant:bob"); !errors.Is(err, kms.ErrUnknownKEK) {
		t.Errorf("unknown: %v, want ErrUnknownKEK", err)
	}
}

// spec: §4.9 — Encrypt binds the alias into the AAD field.
// diagnosis: the AAD records the Lenny alias; tampering with it on
// disk rejects the decrypt.
func TestEncryptBindsAliasIntoAAD(t *testing.T) {
	p, f := newTestProvider(t)
	if _, err := p.WrapDEK(context.Background(), "tenant:acme", []byte("0123456789abcdef0123456789abcdef")); err != nil {
		t.Fatalf("WrapDEK: %v", err)
	}
	if string(f.lastAAD) != "lenny.alias=tenant:acme" {
		t.Errorf("AAD: got %q, want lenny.alias=tenant:acme", f.lastAAD)
	}
}
