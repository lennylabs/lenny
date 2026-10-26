// SPDX-License-Identifier: MIT

// Package kmstest holds the signing fixtures the kms adapter tests
// share: P-256 keys, DER signatures whose r half needs left-padding, a
// non-P-256 public key, and an r||s verifier.
package kmstest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"math/big"
	"testing"
)

// Digest returns the SHA-256 digest of msg.
func Digest(msg string) []byte {
	d := sha256.Sum256([]byte(msg))
	return d[:]
}

// NewP256Key returns a fresh P-256 private key.
func NewP256Key(t testing.TB) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate P-256 key: %v", err)
	}
	return key
}

// PKIX returns the DER SubjectPublicKeyInfo of pub.
func PKIX(t testing.TB, pub *ecdsa.PublicKey) []byte {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}
	return der
}

// P384PKIX returns the DER SubjectPublicKeyInfo of a fresh P-384 key,
// the non-P-256 case PublicKey must reject.
func P384PKIX(t testing.TB) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatalf("generate P-384 key: %v", err)
	}
	return PKIX(t, &key.PublicKey)
}

// ShortRSignature signs digest with key until the r half of the
// signature has a zero top byte, so its minimal DER encoding carries a
// 31-byte (or shorter) r, and returns that DER signature. A conversion
// that copies DER integers without left-padding produces a signature
// that does not verify. The expected number of attempts is 256.
func ShortRSignature(t testing.TB, key *ecdsa.PrivateKey, digest []byte) []byte {
	t.Helper()
	for i := 0; i < 1<<16; i++ {
		r, s, err := ecdsa.Sign(rand.Reader, key, digest)
		if err != nil {
			t.Fatalf("sign: %v", err)
		}
		if r.BitLen() > 248 {
			continue
		}
		der, err := asn1.Marshal(struct{ R, S *big.Int }{r, s})
		if err != nil {
			t.Fatalf("marshal DER signature: %v", err)
		}
		return der
	}
	t.Fatal("no signature with a short r in the attempt bound")
	return nil
}

// VerifyRS reports whether sig is a valid 64-byte r||s signature of
// digest under pub.
func VerifyRS(pub *ecdsa.PublicKey, digest, sig []byte) bool {
	if len(sig) != 64 {
		return false
	}
	r := new(big.Int).SetBytes(sig[:32])
	s := new(big.Int).SetBytes(sig[32:])
	return ecdsa.Verify(pub, digest, r, s)
}
