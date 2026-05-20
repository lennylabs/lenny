// SPDX-License-Identifier: MIT

package aws

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	awskms "github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/kms/types"

	"github.com/lennylabs/lenny/pkg/kms"
)

// fakeKMS is an in-process AWS KMS stand-in for the unit tests. It
// pairs an in-memory key id <-> deterministic 32-byte key material
// table with Encrypt / Decrypt behaviors that match the real
// service's failure cases (NotFoundException, InvalidCiphertextException).
type fakeKMS struct {
	keys map[string][]byte
	// failEncrypt forces the next Encrypt to return err.
	failEncrypt error
	// failDecrypt forces the next Decrypt to return err.
	failDecrypt error
	// lastContext records the EncryptionContext from the most recent
	// Encrypt / Decrypt call so a test can assert it.
	lastContext map[string]string
}

func newFakeKMS() *fakeKMS {
	return &fakeKMS{keys: map[string][]byte{}}
}

func (f *fakeKMS) seed(keyID string, material []byte) {
	f.keys[keyID] = material
}

// envelope mirrors the (key id, encryption context, ciphertext)
// triple AWS KMS validates internally. The fake serialises it into a
// pipe-separated blob: "<keyId>|<alias>|<xor>".
const envelopeSep = "|"

func (f *fakeKMS) Encrypt(_ context.Context, in *awskms.EncryptInput, _ ...func(*awskms.Options)) (*awskms.EncryptOutput, error) {
	if f.failEncrypt != nil {
		err := f.failEncrypt
		f.failEncrypt = nil
		return nil, err
	}
	f.lastContext = in.EncryptionContext
	keyID := awssdk.ToString(in.KeyId)
	material, ok := f.keys[keyID]
	if !ok {
		return nil, &types.NotFoundException{Message: awssdk.String("key not found")}
	}
	alias := in.EncryptionContext["lenny.alias"]
	ct := xorKey(in.Plaintext, material, []byte(alias))
	envelope := append([]byte(keyID+envelopeSep+alias+envelopeSep), ct...)
	return &awskms.EncryptOutput{
		CiphertextBlob: envelope,
		KeyId:          in.KeyId,
	}, nil
}

func (f *fakeKMS) Decrypt(_ context.Context, in *awskms.DecryptInput, _ ...func(*awskms.Options)) (*awskms.DecryptOutput, error) {
	if f.failDecrypt != nil {
		err := f.failDecrypt
		f.failDecrypt = nil
		return nil, err
	}
	f.lastContext = in.EncryptionContext
	keyID := awssdk.ToString(in.KeyId)
	material, ok := f.keys[keyID]
	if !ok {
		return nil, &types.NotFoundException{Message: awssdk.String("key not found")}
	}
	// Split the envelope and verify the key id + alias match the
	// caller's. A mismatch is the real-KMS InvalidCiphertextException.
	parts := splitN(in.CiphertextBlob, envelopeSep, 3)
	if len(parts) != 3 {
		return nil, &types.InvalidCiphertextException{Message: awssdk.String("malformed envelope")}
	}
	if string(parts[0]) != keyID {
		return nil, &types.InvalidCiphertextException{Message: awssdk.String("key id mismatch")}
	}
	expectedAlias := in.EncryptionContext["lenny.alias"]
	if string(parts[1]) != expectedAlias {
		return nil, &types.InvalidCiphertextException{Message: awssdk.String("encryption context mismatch")}
	}
	pt := xorKey(parts[2], material, []byte(expectedAlias))
	if len(pt) != kms.DEKSize {
		return nil, &types.InvalidCiphertextException{Message: awssdk.String("invalid ciphertext")}
	}
	return &awskms.DecryptOutput{
		Plaintext: pt,
		KeyId:     in.KeyId,
	}, nil
}

func splitN(s []byte, sep string, n int) [][]byte {
	out := [][]byte{}
	cur := s
	for i := 0; i < n-1; i++ {
		idx := indexByte(cur, sep[0])
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

func indexByte(s []byte, c byte) int {
	for i, b := range s {
		if b == c {
			return i
		}
	}
	return -1
}

func (f *fakeKMS) DescribeKey(_ context.Context, in *awskms.DescribeKeyInput, _ ...func(*awskms.Options)) (*awskms.DescribeKeyOutput, error) {
	if _, ok := f.keys[awssdk.ToString(in.KeyId)]; !ok {
		return nil, &types.NotFoundException{Message: awssdk.String("key not found")}
	}
	return &awskms.DescribeKeyOutput{
		KeyMetadata: &types.KeyMetadata{KeyId: in.KeyId},
	}, nil
}

func xorKey(data, material, alias []byte) []byte {
	out := make([]byte, len(data))
	salt := append([]byte(nil), material...)
	salt = append(salt, alias...)
	for i := range data {
		out[i] = data[i] ^ salt[i%len(salt)]
	}
	return out
}

func newTestProvider(t *testing.T) (*Provider, *fakeKMS) {
	t.Helper()
	f := newFakeKMS()
	f.seed("arn:aws:kms:us-east-1:000000000000:key/k-acme", []byte("0123456789abcdef0123456789abcdef"))
	f.seed("arn:aws:kms:us-east-1:000000000000:key/k-platform", []byte("ABCDEFGHIJKLMNOPQRSTUVWXYZ012345"))
	p := newWithClient(f, map[string]string{
		"tenant:acme":                    "arn:aws:kms:us-east-1:000000000000:key/k-acme",
		"platform:token-service-signing": "arn:aws:kms:us-east-1:000000000000:key/k-platform",
	})
	return p, f
}

// spec: §4 / §4.9 — Wrap/Unwrap round-trip on a known alias.
// diagnosis: a successful Encrypt followed by a Decrypt under the same alias must recover the original DEK byte-for-byte.
func TestWrapUnwrapRoundTrip(t *testing.T) {
	p, _ := newTestProvider(t)
	dek := []byte("0123456789abcdef0123456789abcdef") // 32 bytes
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
		t.Errorf("round-trip plaintext mismatch: got %q, want %q", got, dek)
	}
}

// spec: §4.9 — WrapDEK on an unmapped alias surfaces ErrUnknownKEK.
// diagnosis: the Provider rejects unmapped aliases without contacting AWS so the call site does not pay a round-trip on a misconfiguration.
func TestWrapDEKUnknownAlias(t *testing.T) {
	p, _ := newTestProvider(t)
	_, err := p.WrapDEK(context.Background(), "tenant:bob", []byte("0123456789abcdef0123456789abcdef"))
	if !errors.Is(err, kms.ErrUnknownKEK) {
		t.Errorf("WrapDEK unmapped alias: %v, want ErrUnknownKEK", err)
	}
}

// spec: §4 — WrapDEK validates the DEK length before contacting KMS.
// diagnosis: a non-32-byte DEK is a programming error; surface it locally rather than producing an opaque AWS validation failure.
func TestWrapDEKWrongLength(t *testing.T) {
	p, _ := newTestProvider(t)
	_, err := p.WrapDEK(context.Background(), "tenant:acme", []byte("short"))
	if err == nil {
		t.Error("WrapDEK with a short DEK should fail")
	}
}

// spec: §4.9.1 — a wrapped DEK does not unwrap under a different alias.
// diagnosis: encryption-context tampering at rest would let an attacker move a wrapped DEK between aliases; the bound EncryptionContext defends against this.
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

// spec: §4 — Decrypt errors map to the kms package's sentinel errors.
// diagnosis: AWS KMS InvalidCiphertextException maps to kms.ErrUnwrap; NotFoundException maps to kms.ErrUnknownKEK.
func TestUnwrapDEKMapsInvalidCiphertext(t *testing.T) {
	p, f := newTestProvider(t)
	f.failDecrypt = &types.InvalidCiphertextException{Message: awssdk.String("tampered")}
	_, err := p.UnwrapDEK(context.Background(), "tenant:acme", kms.WrappedDEK{
		KEKVersion: 1,
		Ciphertext: []byte{0x00},
	})
	if !errors.Is(err, kms.ErrUnwrap) {
		t.Errorf("Decrypt tampered: %v, want ErrUnwrap", err)
	}
}

func TestUnwrapDEKMapsNotFound(t *testing.T) {
	p, f := newTestProvider(t)
	f.failDecrypt = &types.NotFoundException{Message: awssdk.String("missing")}
	_, err := p.UnwrapDEK(context.Background(), "tenant:acme", kms.WrappedDEK{
		KEKVersion: 1,
		Ciphertext: []byte{0x00},
	})
	if !errors.Is(err, kms.ErrUnknownKEK) {
		t.Errorf("Decrypt missing: %v, want ErrUnknownKEK", err)
	}
}

// spec: §4 — UnwrapDEK rejects version 0 before any RPC.
// diagnosis: an absent version is a malformed record; surface it as ErrUnknownKEKVersion locally.
func TestUnwrapDEKRejectsZeroVersion(t *testing.T) {
	p, _ := newTestProvider(t)
	_, err := p.UnwrapDEK(context.Background(), "tenant:acme", kms.WrappedDEK{KEKVersion: 0})
	if !errors.Is(err, kms.ErrUnknownKEKVersion) {
		t.Errorf("version 0: %v, want ErrUnknownKEKVersion", err)
	}
}

// spec: §4 — CurrentKEKVersion drives the §4.9.1 re-encryption job's check that an alias is still active.
// diagnosis: a known alias returns version 1; an unknown alias maps to ErrUnknownKEK so the job does not stall on a non-existent key.
func TestCurrentKEKVersionKnownAndUnknown(t *testing.T) {
	p, _ := newTestProvider(t)
	v, err := p.CurrentKEKVersion(context.Background(), "tenant:acme")
	if err != nil {
		t.Fatalf("CurrentKEKVersion known: %v", err)
	}
	if v != 1 {
		t.Errorf("version: got %d, want 1", v)
	}
	if _, err := p.CurrentKEKVersion(context.Background(), "tenant:unknown"); !errors.Is(err, kms.ErrUnknownKEK) {
		t.Errorf("CurrentKEKVersion unknown: %v, want ErrUnknownKEK", err)
	}
}

// spec: §4.9 — Encrypt binds the alias into the EncryptionContext.
// diagnosis: AWS KMS's EncryptionContext is the cryptographic binding that prevents wrapped-DEK reuse across aliases; verifying the bound key is the value the operator can audit in CloudTrail.
func TestEncryptBindsAliasIntoEncryptionContext(t *testing.T) {
	p, f := newTestProvider(t)
	if _, err := p.WrapDEK(context.Background(), "tenant:acme", []byte("0123456789abcdef0123456789abcdef")); err != nil {
		t.Fatalf("WrapDEK: %v", err)
	}
	if f.lastContext["lenny.alias"] != "tenant:acme" {
		t.Errorf("EncryptionContext lenny.alias: got %q, want tenant:acme", f.lastContext["lenny.alias"])
	}
}

// spec: §17.6 — MarshalCiphertext encodes the wrapped DEK as the
// AWS KMS-native base64 envelope.
// diagnosis: the backup pipeline persists wrapped DEKs as base64 so a downstream operator can round-trip them through a generic JSON tool.
func TestMarshalCiphertext(t *testing.T) {
	got := MarshalCiphertext(kms.WrappedDEK{Ciphertext: []byte{0xde, 0xad, 0xbe, 0xef}})
	want := base64.StdEncoding.EncodeToString([]byte{0xde, 0xad, 0xbe, 0xef})
	if got != want {
		t.Errorf("MarshalCiphertext: got %q, want %q", got, want)
	}
}

// spec: §4 — SetAlias is the runtime mutator the tenant-deletion
// controller uses to drop a tenant's alias mapping.
// diagnosis: SetAlias updates the in-process map; a freshly-set alias resolves on the next call without restart.
func TestSetAlias(t *testing.T) {
	f := newFakeKMS()
	f.seed("k-new", []byte("0123456789abcdef0123456789abcdef"))
	p := newWithClient(f, map[string]string{})
	if _, err := p.WrapDEK(context.Background(), "tenant:new", []byte("0123456789abcdef0123456789abcdef")); !errors.Is(err, kms.ErrUnknownKEK) {
		t.Fatalf("WrapDEK pre-SetAlias: %v, want ErrUnknownKEK", err)
	}
	p.SetAlias("tenant:new", "k-new")
	if _, err := p.WrapDEK(context.Background(), "tenant:new", []byte("0123456789abcdef0123456789abcdef")); err != nil {
		t.Errorf("WrapDEK after SetAlias: %v", err)
	}
}
