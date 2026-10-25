// SPDX-License-Identifier: MIT

package kms_test

import (
	"encoding/asn1"
	"errors"
	"math/big"
	"testing"

	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/kms/kmstest"
)

// spec: 10.2 (Authentication)
// A DER signature whose r is shorter than 32 bytes must be left-padded,
// or the r||s signature does not verify.
func TestSignatureFromASN1LeftPadsShortR_spec_10_2(t *testing.T) {
	t.Parallel()
	key := kmstest.NewP256Key(t)
	digest := kmstest.Digest("m")
	der := kmstest.ShortRSignature(t, key, digest)
	sig, err := kms.SignatureFromASN1(der)
	if err != nil {
		t.Fatalf("SignatureFromASN1: %v", err)
	}
	if len(sig) != kms.SignatureSize || sig[0] != 0 {
		t.Fatalf("got %d bytes with first byte %#x, want 64 bytes with a zero pad byte", len(sig), sig[0])
	}
	if !kmstest.VerifyRS(&key.PublicKey, digest, sig) {
		t.Fatal("left-padded signature does not verify")
	}
}

// spec: 10.2 (Authentication)
func TestSignatureFromASN1RejectsMalformed_spec_10_2(t *testing.T) {
	t.Parallel()
	valid, _ := asn1.Marshal(struct{ R, S *big.Int }{big.NewInt(1), big.NewInt(2)})
	zero, _ := asn1.Marshal(struct{ R, S *big.Int }{big.NewInt(0), big.NewInt(2)})
	wide, _ := asn1.Marshal(struct{ R, S *big.Int }{new(big.Int).Lsh(big.NewInt(1), 300), big.NewInt(2)})
	cases := map[string][]byte{
		"garbage":       {0x01, 0x02},
		"trailing data": append(append([]byte{}, valid...), 0x00),
		"zero r":        zero,
		"oversized r":   wide,
	}
	for name, der := range cases {
		if _, err := kms.SignatureFromASN1(der); !errors.Is(err, kms.ErrNotP256) {
			t.Errorf("%s: err=%v, want ErrNotP256", name, err)
		}
	}
	if err := kms.CheckRawSignature(make([]byte, 63)); !errors.Is(err, kms.ErrNotP256) {
		t.Errorf("CheckRawSignature(63) err=%v, want ErrNotP256", err)
	}
}

// spec: 10.2 (Authentication)
func TestPublicKeyFromPKIXRejectsNonP256_spec_10_2(t *testing.T) {
	t.Parallel()
	if _, err := kms.PublicKeyFromPKIX(kmstest.P384PKIX(t)); !errors.Is(err, kms.ErrNotP256) {
		t.Fatalf("P-384 key err=%v, want ErrNotP256", err)
	}
	key := kmstest.NewP256Key(t)
	pub, err := kms.PublicKeyFromPKIX(kmstest.PKIX(t, &key.PublicKey))
	if err != nil || !pub.Equal(&key.PublicKey) {
		t.Fatalf("P-256 key: pub=%v err=%v", pub, err)
	}
	if _, err := kms.PublicKeyFromPKIX([]byte{0x30}); err == nil {
		t.Fatal("malformed DER accepted")
	}
}
