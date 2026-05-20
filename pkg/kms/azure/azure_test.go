// SPDX-License-Identifier: MIT

package azure

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys"

	"github.com/lennylabs/lenny/pkg/kms"
)

// fakeKMS in-memory replacement for the Azure azkeys.Client. The
// envelope binds (key name, AAD, xor(plaintext, material)) so a
// wrong-AAD decrypt rejects authentically.
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

func (f *fakeKMS) Encrypt(_ context.Context, name, _ string, params azkeys.KeyOperationParameters, _ *azkeys.EncryptOptions) (azkeys.EncryptResponse, error) {
	if f.failEncrypt != nil {
		err := f.failEncrypt
		f.failEncrypt = nil
		return azkeys.EncryptResponse{}, err
	}
	f.lastAAD = params.AdditionalAuthenticatedData
	material, ok := f.keys[name]
	if !ok {
		return azkeys.EncryptResponse{}, &azcore.ResponseError{StatusCode: http.StatusNotFound, ErrorCode: "KeyNotFound"}
	}
	env := bytes.Buffer{}
	env.WriteString(name)
	env.WriteByte('|')
	env.Write(params.AdditionalAuthenticatedData)
	env.WriteByte('|')
	env.Write(xorBytes(params.Value, material))
	return azkeys.EncryptResponse{
		KeyOperationResult: azkeys.KeyOperationResult{Result: env.Bytes()},
	}, nil
}

func (f *fakeKMS) Decrypt(_ context.Context, name, _ string, params azkeys.KeyOperationParameters, _ *azkeys.DecryptOptions) (azkeys.DecryptResponse, error) {
	if f.failDecrypt != nil {
		err := f.failDecrypt
		f.failDecrypt = nil
		return azkeys.DecryptResponse{}, err
	}
	f.lastAAD = params.AdditionalAuthenticatedData
	material, ok := f.keys[name]
	if !ok {
		return azkeys.DecryptResponse{}, &azcore.ResponseError{StatusCode: http.StatusNotFound, ErrorCode: "KeyNotFound"}
	}
	parts := splitN(params.Value, '|', 3)
	if len(parts) != 3 || string(parts[0]) != name {
		return azkeys.DecryptResponse{}, &azcore.ResponseError{StatusCode: http.StatusBadRequest, ErrorCode: "Invalid"}
	}
	if !bytes.Equal(parts[1], params.AdditionalAuthenticatedData) {
		return azkeys.DecryptResponse{}, &azcore.ResponseError{StatusCode: http.StatusBadRequest, ErrorCode: "InvalidAAD"}
	}
	pt := xorBytes(parts[2], material)
	if len(pt) != kms.DEKSize {
		return azkeys.DecryptResponse{}, &azcore.ResponseError{StatusCode: http.StatusBadRequest, ErrorCode: "InvalidCiphertext"}
	}
	return azkeys.DecryptResponse{
		KeyOperationResult: azkeys.KeyOperationResult{Result: pt},
	}, nil
}

func (f *fakeKMS) GetKey(_ context.Context, name, _ string, _ *azkeys.GetKeyOptions) (azkeys.GetKeyResponse, error) {
	if _, ok := f.keys[name]; !ok {
		return azkeys.GetKeyResponse{}, &azcore.ResponseError{StatusCode: http.StatusNotFound, ErrorCode: "KeyNotFound"}
	}
	return azkeys.GetKeyResponse{}, nil
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
	f.seed("k-acme", []byte("0123456789abcdef0123456789abcdef"))
	f.seed("k-platform", []byte("ABCDEFGHIJKLMNOPQRSTUVWXYZ012345"))
	p := newWithClient(f, map[string]KeyRef{
		"tenant:acme":                    {Name: "k-acme"},
		"platform:token-service-signing": {Name: "k-platform"},
	})
	return p, f
}

// spec: §4 / §4.9 — Wrap/Unwrap round-trip.
// diagnosis: Encrypt + Decrypt recovers the DEK byte-for-byte.
func TestWrapUnwrapRoundTrip(t *testing.T) {
	p, _ := newTestProvider(t)
	dek := []byte("0123456789abcdef0123456789abcdef")
	wrapped, err := p.WrapDEK(context.Background(), "tenant:acme", dek)
	if err != nil {
		t.Fatalf("WrapDEK: %v", err)
	}
	got, err := p.UnwrapDEK(context.Background(), "tenant:acme", wrapped)
	if err != nil {
		t.Fatalf("UnwrapDEK: %v", err)
	}
	if !bytes.Equal(got, dek) {
		t.Errorf("round-trip mismatch: got %q, want %q", got, dek)
	}
}

// spec: §4.9 — unmapped alias surfaces ErrUnknownKEK.
// diagnosis: fail-fast local rejection.
func TestWrapDEKUnknownAlias(t *testing.T) {
	p, _ := newTestProvider(t)
	_, err := p.WrapDEK(context.Background(), "tenant:bob", []byte("0123456789abcdef0123456789abcdef"))
	if !errors.Is(err, kms.ErrUnknownKEK) {
		t.Errorf("WrapDEK unmapped: %v, want ErrUnknownKEK", err)
	}
}

// spec: §4.9.1 — cross-alias unwrap rejected via AAD.
// diagnosis: a tenant:acme wrapped DEK does not unwrap under platform alias.
func TestUnwrapDEKRejectsCrossAlias(t *testing.T) {
	p, _ := newTestProvider(t)
	wrapped, err := p.WrapDEK(context.Background(), "tenant:acme", []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("WrapDEK: %v", err)
	}
	if _, err := p.UnwrapDEK(context.Background(), "platform:token-service-signing", wrapped); err == nil {
		t.Error("UnwrapDEK under a different alias should fail")
	}
}

// spec: §4 — 404 ResponseError maps to ErrUnknownKEK.
// diagnosis: a deleted Key Vault key surfaces as missing alias.
func TestDecryptMapsNotFound(t *testing.T) {
	p, f := newTestProvider(t)
	f.failDecrypt = &azcore.ResponseError{StatusCode: http.StatusNotFound, ErrorCode: "KeyNotFound"}
	_, err := p.UnwrapDEK(context.Background(), "tenant:acme", kms.WrappedDEK{KEKVersion: 1, Ciphertext: []byte{0x00}})
	if !errors.Is(err, kms.ErrUnknownKEK) {
		t.Errorf("404: %v, want ErrUnknownKEK", err)
	}
}

// spec: §4 — 400 ResponseError maps to ErrUnwrap.
// diagnosis: tampered ciphertext or AAD mismatch surfaces as Unwrap.
func TestDecryptMapsInvalidArgument(t *testing.T) {
	p, f := newTestProvider(t)
	f.failDecrypt = &azcore.ResponseError{StatusCode: http.StatusBadRequest, ErrorCode: "InvalidCiphertext"}
	_, err := p.UnwrapDEK(context.Background(), "tenant:acme", kms.WrappedDEK{KEKVersion: 1, Ciphertext: []byte{0x00}})
	if !errors.Is(err, kms.ErrUnwrap) {
		t.Errorf("400: %v, want ErrUnwrap", err)
	}
}

// spec: §4 — CurrentKEKVersion.
// diagnosis: known alias returns 1; unknown surfaces ErrUnknownKEK.
func TestCurrentKEKVersion(t *testing.T) {
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

// spec: §4.9 — Encrypt binds alias into AAD.
// diagnosis: AAD records the Lenny alias for cryptographic binding.
func TestEncryptBindsAliasIntoAAD(t *testing.T) {
	p, f := newTestProvider(t)
	if _, err := p.WrapDEK(context.Background(), "tenant:acme", []byte("0123456789abcdef0123456789abcdef")); err != nil {
		t.Fatalf("WrapDEK: %v", err)
	}
	if string(f.lastAAD) != "tenant:acme" {
		t.Errorf("AAD: got %q, want tenant:acme", f.lastAAD)
	}
}
