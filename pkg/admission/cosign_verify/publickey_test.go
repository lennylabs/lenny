// SPDX-License-Identifier: MIT

package cosign_verify

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"strings"
	"testing"
)

// signingKey is a generated ECDSA-P256 key pair plus the PEM encoding
// of its public half, used to exercise the keyed verifier end to end.
type signingKey struct {
	priv      *ecdsa.PrivateKey
	publicPEM []byte
}

// newSigningKey generates an ECDSA-P256 key and PEM-encodes its public
// key, matching the key material cosign keyed signing uses by default.
func newSigningKey(t *testing.T) signingKey {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	der, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}
	return signingKey{
		priv:      priv,
		publicPEM: pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}),
	}
}

// digestOf returns a deterministic sha256 "algo:hex" digest for a
// label, standing in for an image manifest digest.
func digestOf(label string) string {
	sum := sha256.Sum256([]byte(label))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// sign produces the detached cosign-style signature over the digest
// bytes of digest, base64-standard-encoded.
func (k signingKey) sign(t *testing.T, digest string) string {
	t.Helper()
	raw, err := decodeSHA256Digest(digest)
	if err != nil {
		t.Fatalf("decode digest under test: %v", err)
	}
	sig, err := ecdsa.SignASN1(rand.Reader, k.priv, raw)
	if err != nil {
		t.Fatalf("sign digest: %v", err)
	}
	return base64.StdEncoding.EncodeToString(sig)
}

func TestPublicKeyVerifierAdmitsValidSignature(t *testing.T) {
	key := newSigningKey(t)
	const ref = "ghcr.io/lennylabs/runtime@sha256:aaa"
	digest := digestOf(ref)

	resolver := NewStaticResolver(map[string]SignedDigest{
		ref: {Digest: digest, Signature: key.sign(t, digest)},
	})
	v, err := NewPublicKeyVerifier(key.publicPEM, resolver)
	if err != nil {
		t.Fatalf("build verifier: %v", err)
	}
	if err := v.Verify(context.Background(), ref); err != nil {
		t.Errorf("valid signature should verify, got %v", err)
	}
}

func TestPublicKeyVerifierRejectsWrongKey(t *testing.T) {
	signer := newSigningKey(t)
	trusted := newSigningKey(t) // a different key than the one that signed
	const ref = "ghcr.io/lennylabs/runtime@sha256:bbb"
	digest := digestOf(ref)

	resolver := NewStaticResolver(map[string]SignedDigest{
		ref: {Digest: digest, Signature: signer.sign(t, digest)},
	})
	v, err := NewPublicKeyVerifier(trusted.publicPEM, resolver)
	if err != nil {
		t.Fatalf("build verifier: %v", err)
	}
	err = v.Verify(context.Background(), ref)
	if err == nil {
		t.Fatalf("signature from an untrusted key must be rejected")
	}
	if !strings.Contains(err.Error(), "does not verify") {
		t.Errorf("error should explain the verification failure, got %v", err)
	}
}

func TestPublicKeyVerifierRejectsTamperedDigest(t *testing.T) {
	key := newSigningKey(t)
	const ref = "ghcr.io/lennylabs/runtime@sha256:ccc"
	signedDigest := digestOf(ref)
	signature := key.sign(t, signedDigest)

	// The resolver returns a different digest than the one that was
	// signed: an attacker swapped the image content after signing.
	resolver := NewStaticResolver(map[string]SignedDigest{
		ref: {Digest: digestOf("tampered-content"), Signature: signature},
	})
	v, err := NewPublicKeyVerifier(key.publicPEM, resolver)
	if err != nil {
		t.Fatalf("build verifier: %v", err)
	}
	if err := v.Verify(context.Background(), ref); err == nil {
		t.Fatalf("a signature over a different digest must be rejected")
	}
}

func TestPublicKeyVerifierRejectsMissingSignature(t *testing.T) {
	key := newSigningKey(t)
	// The resolver has no entry for the image: an unsigned image.
	resolver := NewStaticResolver(map[string]SignedDigest{})
	v, err := NewPublicKeyVerifier(key.publicPEM, resolver)
	if err != nil {
		t.Fatalf("build verifier: %v", err)
	}
	err = v.Verify(context.Background(), "ghcr.io/lennylabs/runtime@sha256:ddd")
	if err == nil {
		t.Fatalf("an image with no recorded signature must be rejected")
	}
	if !strings.Contains(err.Error(), "no signature recorded") {
		t.Errorf("error should explain the absent signature, got %v", err)
	}
}

func TestPublicKeyVerifierAcceptsRotatedKey(t *testing.T) {
	// Two trusted keys are configured during a rotation overlap window;
	// an image signed by the second key still verifies.
	oldKey := newSigningKey(t)
	newKey := newSigningKey(t)
	const ref = "ghcr.io/lennylabs/runtime@sha256:eee"
	digest := digestOf(ref)

	resolver := NewStaticResolver(map[string]SignedDigest{
		ref: {Digest: digest, Signature: newKey.sign(t, digest)},
	})
	bundle := append(append([]byte{}, oldKey.publicPEM...), newKey.publicPEM...)
	v, err := NewPublicKeyVerifier(bundle, resolver)
	if err != nil {
		t.Fatalf("build verifier: %v", err)
	}
	if err := v.Verify(context.Background(), ref); err != nil {
		t.Errorf("signature from the rotated-in key should verify, got %v", err)
	}
}

func TestNewPublicKeyVerifierRejectsEmptyKeyMaterial(t *testing.T) {
	_, err := NewPublicKeyVerifier([]byte("not a pem block"), NewStaticResolver(nil))
	if err == nil {
		t.Fatalf("a verifier with no trust anchor must not be constructible")
	}
}

func TestPublicKeyVerifierRejectsMalformedDigest(t *testing.T) {
	key := newSigningKey(t)
	const ref = "ghcr.io/lennylabs/runtime@sha256:fff"
	resolver := NewStaticResolver(map[string]SignedDigest{
		ref: {Digest: "md5:abcdef", Signature: "AA=="},
	})
	v, err := NewPublicKeyVerifier(key.publicPEM, resolver)
	if err != nil {
		t.Fatalf("build verifier: %v", err)
	}
	if err := v.Verify(context.Background(), ref); err == nil {
		t.Fatalf("a non-sha256 digest must be rejected")
	}
}
