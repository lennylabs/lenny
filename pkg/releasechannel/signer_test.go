// SPDX-License-Identifier: MIT

package releasechannel_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/releasechannel"
)

// generateKey returns a fresh Ed25519 keypair tagged with id for tests.
func generateKey(t *testing.T, id string) releasechannel.Key {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate keypair %q: %v", id, err)
	}
	return releasechannel.Key{ID: id, Private: priv, Public: pub}
}

func TestSignerSignAndVerifyRoundTrip(t *testing.T) {
	k := generateKey(t, "key-1")
	signer, err := releasechannel.NewSigner(k, nil)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	body := []byte(`{"version":"1.5.0"}`)
	env, err := signer.Sign(body)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if env.KeyID != "key-1" {
		t.Errorf("KeyID = %q, want key-1", env.KeyID)
	}
	if len(env.Signature) != ed25519.SignatureSize {
		t.Errorf("signature length = %d, want %d", len(env.Signature), ed25519.SignatureSize)
	}

	verifier, err := releasechannel.NewVerifier(signer.PublicKeySet())
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	if err := verifier.Verify(env, body); err != nil {
		t.Errorf("verify round-trip: %v", err)
	}
}

func TestSignerRejectsTamperedBody(t *testing.T) {
	k := generateKey(t, "key-1")
	signer, _ := releasechannel.NewSigner(k, nil)
	verifier, _ := releasechannel.NewVerifier(signer.PublicKeySet())
	body := []byte(`{"version":"1.5.0"}`)
	env, _ := signer.Sign(body)

	// Mutate the body after signing: verification must fail.
	tampered := []byte(`{"version":"9.9.9"}`)
	err := verifier.Verify(env, tampered)
	if !errors.Is(err, releasechannel.ErrSignatureMismatch) {
		t.Errorf("Verify of tampered body returned %v, want ErrSignatureMismatch", err)
	}
}

func TestSignerRejectsUnknownKey(t *testing.T) {
	// Signer holds key-A; verifier trusts only key-B. Verification must
	// fail with ErrUnknownKey, distinguishing "trust bundle is stale"
	// from "signature is forged".
	keyA := generateKey(t, "key-A")
	keyB := generateKey(t, "key-B")
	signer, _ := releasechannel.NewSigner(keyA, nil)
	verifier, _ := releasechannel.NewVerifier([]releasechannel.Key{
		{ID: keyB.ID, Public: keyB.Public},
	})
	env, _ := signer.Sign([]byte(`{}`))
	err := verifier.Verify(env, []byte(`{}`))
	if !errors.Is(err, releasechannel.ErrUnknownKey) {
		t.Errorf("Verify under stale trust bundle = %v, want ErrUnknownKey", err)
	}
}

func TestRotationOverlapBothKeysVerify(t *testing.T) {
	// Step 1: signer signs under key-1.
	keyOld := generateKey(t, "key-old")
	signer, _ := releasechannel.NewSigner(keyOld, nil)
	body := []byte(`{"version":"1.0.0"}`)
	oldEnv, _ := signer.Sign(body)

	// Step 2: operator rotates to key-2. The previous key is preserved
	// for the overlap window.
	keyNew := generateKey(t, "key-new")
	if err := signer.Rotate(keyNew); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if signer.CurrentKeyID() != "key-new" {
		t.Errorf("CurrentKeyID after Rotate = %q, want key-new", signer.CurrentKeyID())
	}
	if signer.PreviousKeyID() != "key-old" {
		t.Errorf("PreviousKeyID after Rotate = %q, want key-old", signer.PreviousKeyID())
	}

	// Step 3: new manifests are signed under key-new.
	newEnv, _ := signer.Sign(body)
	if newEnv.KeyID != "key-new" {
		t.Errorf("post-rotation signature KeyID = %q, want key-new", newEnv.KeyID)
	}

	// Step 4: during the overlap window the verifier trusts both keys.
	overlapVerifier, _ := releasechannel.NewVerifier(signer.PublicKeySet())
	if !overlapVerifier.TrustsKey("key-new") {
		t.Errorf("verifier should trust key-new during overlap")
	}
	if !overlapVerifier.TrustsKey("key-old") {
		t.Errorf("verifier should still trust key-old during overlap")
	}
	if err := overlapVerifier.Verify(oldEnv, body); err != nil {
		t.Errorf("during overlap, pre-rotation signature should still verify: %v", err)
	}
	if err := overlapVerifier.Verify(newEnv, body); err != nil {
		t.Errorf("during overlap, post-rotation signature should verify: %v", err)
	}
}

func TestRetirePreviousClosesOverlapWindow(t *testing.T) {
	keyOld := generateKey(t, "key-old")
	keyNew := generateKey(t, "key-new")
	signer, _ := releasechannel.NewSigner(keyOld, nil)
	body := []byte(`{}`)
	staleEnv, _ := signer.Sign(body)

	if err := signer.Rotate(keyNew); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	signer.RetirePrevious()
	if signer.PreviousKeyID() != "" {
		t.Errorf("PreviousKeyID after RetirePrevious = %q, want empty", signer.PreviousKeyID())
	}

	// A verifier built from the post-retirement trust bundle no longer
	// accepts the pre-rotation signature.
	v, _ := releasechannel.NewVerifier(signer.PublicKeySet())
	if v.TrustsKey("key-old") {
		t.Errorf("verifier should no longer trust key-old after retirement")
	}
	if err := v.Verify(staleEnv, body); !errors.Is(err, releasechannel.ErrUnknownKey) {
		t.Errorf("stale signature should fail with ErrUnknownKey post-retirement, got %v", err)
	}
}

func TestSignerErrorsOnMissingCurrentKey(t *testing.T) {
	// Test that a signer constructed with NewSigner cannot be
	// instantiated without a current key. The zero-value Signer
	// (constructed accidentally) returns ErrNoSigningKey on Sign.
	var s releasechannel.Signer
	if _, err := s.Sign([]byte("body")); !errors.Is(err, releasechannel.ErrNoSigningKey) {
		t.Errorf("zero-value Signer should return ErrNoSigningKey, got %v", err)
	}
}

func TestSignerRotateBootstrapsWithoutPreviousKey(t *testing.T) {
	// Rotate is the operator path for the first key on a fresh
	// install: there is no previous key to preserve.
	k := generateKey(t, "key-1")
	signer, _ := releasechannel.NewSigner(k, nil)
	if signer.PreviousKeyID() != "" {
		t.Errorf("fresh signer should have no previous key")
	}
}

func TestSignatureEnvelopeMarshalRoundTrip(t *testing.T) {
	k := generateKey(t, "abc-123")
	signer, _ := releasechannel.NewSigner(k, nil)
	env, _ := signer.Sign([]byte("hello"))
	wire := env.Marshal()
	parsed, err := releasechannel.ParseSignature(wire)
	if err != nil {
		t.Fatalf("ParseSignature(%q): %v", wire, err)
	}
	if parsed.KeyID != env.KeyID {
		t.Errorf("KeyID round-trip: got %q, want %q", parsed.KeyID, env.KeyID)
	}
	if len(parsed.Signature) != len(env.Signature) {
		t.Errorf("Signature round-trip: got %d bytes, want %d", len(parsed.Signature), len(env.Signature))
	}
	for i := range env.Signature {
		if parsed.Signature[i] != env.Signature[i] {
			t.Errorf("Signature byte %d differs after round-trip", i)
			break
		}
	}
}

func TestParseSignatureRejectsBadEnvelopes(t *testing.T) {
	cases := []struct {
		name    string
		wire    string
		wantSub string
	}{
		{"empty", "", "empty"},
		{"wrong-version", "v2;keyId=k;alg=ed25519;sig=AAAA", "v1"},
		{"missing-keyId", "v1;alg=ed25519;sig=AAAA", "missing keyId"},
		{"missing-sig", "v1;keyId=k;alg=ed25519", "missing sig"},
		{"bad-alg", "v1;keyId=k;alg=hmac;sig=AAAA", "unsupported signing algorithm"},
		{"bad-base64", "v1;keyId=k;alg=ed25519;sig=@@@@", "decode signature"},
		{"malformed-token", "v1;keyId-no-equals;alg=ed25519;sig=AAAA", "malformed envelope token"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := releasechannel.ParseSignature(c.wire)
			if err == nil {
				t.Fatalf("ParseSignature(%q) succeeded, want error containing %q", c.wire, c.wantSub)
			}
			if !strings.Contains(err.Error(), c.wantSub) {
				t.Errorf("ParseSignature(%q) error = %v, want substring %q", c.wire, err, c.wantSub)
			}
		})
	}
}

func TestNewSignerValidatesKeys(t *testing.T) {
	t.Run("missing-id", func(t *testing.T) {
		k := generateKey(t, "")
		if _, err := releasechannel.NewSigner(k, nil); err == nil {
			t.Errorf("NewSigner should reject a key with no ID")
		}
	})
	t.Run("short-private-key", func(t *testing.T) {
		k := releasechannel.Key{ID: "k", Private: []byte("too short")}
		if _, err := releasechannel.NewSigner(k, nil); err == nil {
			t.Errorf("NewSigner should reject a private key of the wrong length")
		}
	})
	t.Run("previous-key-validated", func(t *testing.T) {
		cur := generateKey(t, "cur")
		bad := releasechannel.Key{ID: "prev"} // no Public, no Private
		if _, err := releasechannel.NewSigner(cur, &bad); err == nil {
			t.Errorf("NewSigner should reject a malformed previous key")
		}
	})
}
