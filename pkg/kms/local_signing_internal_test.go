// SPDX-License-Identifier: MIT

package kms

import (
	"bytes"
	"testing"
)

// spec: 10.2 (Authentication), 17.4 (Local Development Mode (lenny-dev))
// The signing scalar must be independent of every KEK the same seed
// derives, including a KEK addressed by the signing alias or by the
// reference string itself.
func TestLocalSigningKeySeparatedFromKEKMaterial_spec_10_2(t *testing.T) {
	seed := bytes.Repeat([]byte{0x5a}, DEKSize)
	l, err := NewLocal(seed)
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	refs := []string{AliasJWTSigning, AliasJWTVerify, "local-key-1", "tenant:acme"}
	for _, ref := range refs {
		l.SetSigningReference(AliasJWTSigning, ref)
		key, err := l.signingKey(AliasJWTSigning)
		if err != nil {
			t.Fatalf("signingKey(%s): %v", ref, err)
		}
		scalar, err := key.Bytes()
		if err != nil {
			t.Fatalf("encode scalar: %v", err)
		}
		for _, alias := range refs {
			for v := 0; v <= 4; v++ {
				kek, err := l.kekMaterial(alias, v)
				if err != nil {
					t.Fatalf("kekMaterial: %v", err)
				}
				if bytes.Equal(kek, scalar) {
					t.Fatalf("signing scalar for %q equals KEK(%q, v%d)", ref, alias, v)
				}
			}
		}
	}
}

// spec: 10.2 (Authentication)
// The derivation is a pure function of (seed, reference); a regression
// to ecdsa.GenerateKey over the HKDF stream would make it vary.
func TestDeriveSigningKeyIsPureFunction_spec_10_2(t *testing.T) {
	seed := bytes.Repeat([]byte{0x01}, DEKSize)
	a, err := deriveSigningKey(seed, "r")
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	for i := 0; i < 8; i++ {
		b, err := deriveSigningKey(seed, "r")
		if err != nil {
			t.Fatalf("derive: %v", err)
		}
		if !a.Equal(b) {
			t.Fatal("deriveSigningKey returned different keys for one input")
		}
	}
}
