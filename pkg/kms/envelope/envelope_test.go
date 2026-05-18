// SPDX-License-Identifier: MIT

package envelope_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/kms/envelope"
)

// newCipher builds an envelope.Cipher over a fixed-seed local KEK
// provider so a test can reconstruct an independent provider with the
// same seed to model a wrong-KEK or cross-process case.
func newCipher(t *testing.T, seedByte byte, alias string) (*envelope.Cipher, *kms.Local) {
	t.Helper()
	seed := bytes.Repeat([]byte{seedByte}, kms.DEKSize)
	provider, err := kms.NewLocal(seed)
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	c, err := envelope.New(provider, alias)
	if err != nil {
		t.Fatalf("envelope.New: %v", err)
	}
	return c, provider
}

// spec: 4
// diagnosis: envelope Seal/Open did not round-trip a record. Open of a
// Seal result must recover the exact plaintext.
func TestSealOpenRoundTrip(t *testing.T) {
	t.Parallel()
	c, _ := newCipher(t, 0x11, "tenant:acme")
	ctx := context.Background()

	for _, plaintext := range [][]byte{
		[]byte("sk-an-upstream-api-key"),
		[]byte(""),                  // empty secret must round-trip
		bytes.Repeat([]byte{0}, 64), // all-zero bytes
		[]byte("multi\nline\x00binary"),
	} {
		sealed, err := c.Seal(ctx, plaintext)
		if err != nil {
			t.Fatalf("Seal(%q): %v", plaintext, err)
		}
		if bytes.Contains(sealed.Ciphertext, plaintext) && len(plaintext) > 0 {
			t.Errorf("Seal(%q) ciphertext contains the plaintext", plaintext)
		}
		if sealed.KEKVersion != 1 {
			t.Errorf("Seal KEK version: got %d, want 1", sealed.KEKVersion)
		}
		if len(sealed.WrappedDEK) == 0 {
			t.Error("Seal produced an empty wrapped DEK")
		}
		got, err := c.Open(ctx, sealed)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		if !bytes.Equal(got, plaintext) {
			t.Errorf("round-trip mismatch:\n got %q\nwant %q", got, plaintext)
		}
	}
}

// spec: 4
// diagnosis: two Seal calls on the same plaintext produced the same
// ciphertext. Each Seal mints a fresh DEK and nonce, so the outputs
// must differ.
func TestSealIsNonDeterministic(t *testing.T) {
	t.Parallel()
	c, _ := newCipher(t, 0x22, "tenant:acme")
	ctx := context.Background()

	a, err := c.Seal(ctx, []byte("same-plaintext"))
	if err != nil {
		t.Fatalf("Seal a: %v", err)
	}
	b, err := c.Seal(ctx, []byte("same-plaintext"))
	if err != nil {
		t.Fatalf("Seal b: %v", err)
	}
	if bytes.Equal(a.Ciphertext, b.Ciphertext) {
		t.Error("two Seal calls produced identical ciphertext")
	}
	if bytes.Equal(a.WrappedDEK, b.WrappedDEK) {
		t.Error("two Seal calls produced identical wrapped DEKs")
	}
	if bytes.Equal(a.Nonce, b.Nonce) {
		t.Error("two Seal calls produced identical nonces")
	}
}

// spec: 4
// diagnosis: a tampered ciphertext decrypted cleanly. AES-GCM
// authenticates the record; any bit flip in the ciphertext, the
// nonce, or the wrapped DEK must fail Open with ErrCiphertextTampered
// (or a wrapped kms error for the wrapped-DEK case).
func TestOpenDetectsTamper(t *testing.T) {
	t.Parallel()
	c, _ := newCipher(t, 0x33, "tenant:acme")
	ctx := context.Background()
	sealed, err := c.Seal(ctx, []byte("the-secret"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	t.Run("ciphertext bit flip", func(t *testing.T) {
		bad := sealed
		bad.Ciphertext = append([]byte(nil), sealed.Ciphertext...)
		bad.Ciphertext[0] ^= 0x01
		if _, err := c.Open(ctx, bad); !errors.Is(err, envelope.ErrCiphertextTampered) {
			t.Errorf("tampered ciphertext: got %v, want ErrCiphertextTampered", err)
		}
	})

	t.Run("nonce bit flip", func(t *testing.T) {
		bad := sealed
		bad.Nonce = append([]byte(nil), sealed.Nonce...)
		bad.Nonce[0] ^= 0x01
		if _, err := c.Open(ctx, bad); !errors.Is(err, envelope.ErrCiphertextTampered) {
			t.Errorf("tampered nonce: got %v, want ErrCiphertextTampered", err)
		}
	})

	t.Run("wrapped DEK bit flip", func(t *testing.T) {
		bad := sealed
		bad.WrappedDEK = append([]byte(nil), sealed.WrappedDEK...)
		bad.WrappedDEK[len(bad.WrappedDEK)-1] ^= 0x01
		// A tampered wrapped DEK fails at the KMS unwrap step.
		if _, err := c.Open(ctx, bad); err == nil {
			t.Error("tampered wrapped DEK: Open should fail")
		} else if !errors.Is(err, kms.ErrUnwrap) {
			t.Errorf("tampered wrapped DEK: got %v, want a wrapped kms.ErrUnwrap", err)
		}
	})
}

// spec: 4
// diagnosis: a Sealed value opened under a Cipher built from a
// different KEK. A wrong KEK must fail Open.
func TestOpenWrongKEKFails(t *testing.T) {
	t.Parallel()
	good, _ := newCipher(t, 0x44, "tenant:acme")
	wrong, _ := newCipher(t, 0x55, "tenant:acme") // different seed → different KEK
	ctx := context.Background()

	sealed, err := good.Seal(ctx, []byte("the-secret"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if _, err := wrong.Open(ctx, sealed); err == nil {
		t.Error("Open under a wrong-KEK Cipher should fail")
	}
}

// spec: 4
// diagnosis: a Sealed value opened under a Cipher for a different KEK
// alias. The alias is bound into the record, so an alias mismatch
// must fail Open even when both alias KEKs derive from the same seed.
func TestOpenWrongAliasFails(t *testing.T) {
	t.Parallel()
	seed := bytes.Repeat([]byte{0x66}, kms.DEKSize)
	provider, err := kms.NewLocal(seed)
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	acme, err := envelope.New(provider, "tenant:acme")
	if err != nil {
		t.Fatalf("envelope.New acme: %v", err)
	}
	globex, err := envelope.New(provider, "tenant:globex")
	if err != nil {
		t.Fatalf("envelope.New globex: %v", err)
	}
	ctx := context.Background()

	sealed, err := acme.Seal(ctx, []byte("acme-secret"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if _, err := globex.Open(ctx, sealed); err == nil {
		t.Error("Open under a different alias should fail")
	}
}

// spec: 4
// diagnosis: Encode/Decode did not round-trip a Sealed value, so a
// single-column stored form could not be recovered.
func TestEncodeDecodeRoundTrip(t *testing.T) {
	t.Parallel()
	c, _ := newCipher(t, 0x77, "tenant:acme")
	ctx := context.Background()
	sealed, err := c.Seal(ctx, []byte("encode-me"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	blob, err := envelope.Encode(sealed)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	decoded, err := envelope.Decode(blob)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if decoded.KEKVersion != sealed.KEKVersion ||
		!bytes.Equal(decoded.WrappedDEK, sealed.WrappedDEK) ||
		!bytes.Equal(decoded.Nonce, sealed.Nonce) ||
		!bytes.Equal(decoded.Ciphertext, sealed.Ciphertext) {
		t.Errorf("Encode/Decode mismatch:\n got %+v\nwant %+v", decoded, sealed)
	}
	// The decoded value still opens.
	got, err := c.Open(ctx, decoded)
	if err != nil {
		t.Fatalf("Open decoded: %v", err)
	}
	if string(got) != "encode-me" {
		t.Errorf("opened decoded value: got %q, want encode-me", got)
	}
}

// spec: 4
// diagnosis: Decode accepted a structurally invalid blob instead of
// returning ErrMalformed.
func TestDecodeRejectsMalformed(t *testing.T) {
	t.Parallel()
	for name, blob := range map[string][]byte{
		"empty":          {},
		"header only":    {0x01, 0, 0, 0, 1, 0, 0, 0, 4},
		"bad magic":      append([]byte{0xff, 0, 0, 0, 1, 0, 0, 0, 1}, bytes.Repeat([]byte{0}, 20)...),
		"truncated body": {0x01, 0, 0, 0, 1, 0, 0, 0, 64, 0x01},
	} {
		if _, err := envelope.Decode(blob); !errors.Is(err, envelope.ErrMalformed) {
			t.Errorf("Decode(%s): got %v, want ErrMalformed", name, err)
		}
	}
}

// spec: 4
// diagnosis: Open accepted a structurally invalid Sealed value
// instead of returning ErrMalformed.
func TestOpenRejectsMalformedSealed(t *testing.T) {
	t.Parallel()
	c, _ := newCipher(t, 0x88, "tenant:acme")
	ctx := context.Background()
	for name, s := range map[string]envelope.Sealed{
		"zero KEK version":  {KEKVersion: 0, WrappedDEK: []byte{1}, Nonce: bytes.Repeat([]byte{0}, 12)},
		"empty wrapped DEK": {KEKVersion: 1, WrappedDEK: nil, Nonce: bytes.Repeat([]byte{0}, 12)},
		"short nonce":       {KEKVersion: 1, WrappedDEK: []byte{1}, Nonce: []byte{0, 1}},
	} {
		if _, err := c.Open(ctx, s); !errors.Is(err, envelope.ErrMalformed) {
			t.Errorf("Open(%s): got %v, want ErrMalformed", name, err)
		}
	}
}

// spec: 4.9.1
// diagnosis: Reseal did not re-wrap a Sealed value's DEK under the
// rotated KEK version. Reseal is the per-row step of the §4.9.1
// re-encryption job: after a KEK rotation it must lift a row from a
// superseded KEK version to the current one, leaving the plaintext
// unchanged.
func TestResealAfterKEKRotation(t *testing.T) {
	t.Parallel()
	seed := bytes.Repeat([]byte{0x99}, kms.DEKSize)
	provider, err := kms.NewLocal(seed)
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	c, err := envelope.New(provider, "tenant:acme")
	if err != nil {
		t.Fatalf("envelope.New: %v", err)
	}
	ctx := context.Background()

	sealed, err := c.Seal(ctx, []byte("rotate-me"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if sealed.KEKVersion != 1 {
		t.Fatalf("pre-rotation KEK version: got %d, want 1", sealed.KEKVersion)
	}

	// Reseal before any rotation is a no-op: the value is already at
	// the current KEK version, which keeps the §4.9.1 job idempotent.
	same, err := c.Reseal(ctx, sealed)
	if err != nil {
		t.Fatalf("Reseal (no rotation): %v", err)
	}
	if same.KEKVersion != 1 || !bytes.Equal(same.WrappedDEK, sealed.WrappedDEK) {
		t.Error("Reseal of an already-current value should be a no-op")
	}

	provider.RotateKEK("tenant:acme")

	resealed, err := c.Reseal(ctx, sealed)
	if err != nil {
		t.Fatalf("Reseal after rotation: %v", err)
	}
	if resealed.KEKVersion != 2 {
		t.Errorf("resealed KEK version: got %d, want 2", resealed.KEKVersion)
	}
	if bytes.Equal(resealed.WrappedDEK, sealed.WrappedDEK) {
		t.Error("Reseal did not re-wrap the DEK under the new KEK version")
	}
	// The record ciphertext is unchanged: Reseal only re-wraps the DEK.
	if !bytes.Equal(resealed.Ciphertext, sealed.Ciphertext) || !bytes.Equal(resealed.Nonce, sealed.Nonce) {
		t.Error("Reseal changed the record ciphertext; it should only re-wrap the DEK")
	}
	// The resealed value still opens to the original plaintext.
	got, err := c.Open(ctx, resealed)
	if err != nil {
		t.Fatalf("Open resealed: %v", err)
	}
	if string(got) != "rotate-me" {
		t.Errorf("opened resealed value: got %q, want rotate-me", got)
	}
	// The pre-rotation value still opens too: the §4.9.1 job runs
	// while both KEK versions are live.
	stillGood, err := c.Open(ctx, sealed)
	if err != nil {
		t.Fatalf("Open pre-rotation value after rotation: %v", err)
	}
	if string(stillGood) != "rotate-me" {
		t.Errorf("opened pre-rotation value: got %q, want rotate-me", stillGood)
	}
}

// spec: 4
// diagnosis: envelope.New accepted a nil provider or an empty alias.
func TestNewRejectsBadArgs(t *testing.T) {
	t.Parallel()
	if _, err := envelope.New(nil, "tenant:acme"); err == nil {
		t.Error("envelope.New accepted a nil provider")
	}
	provider, err := kms.NewLocalRandom()
	if err != nil {
		t.Fatalf("NewLocalRandom: %v", err)
	}
	if _, err := envelope.New(provider, ""); err == nil {
		t.Error("envelope.New accepted an empty alias")
	}
}
