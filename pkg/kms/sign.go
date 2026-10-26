// SPDX-License-Identifier: MIT

package kms

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"fmt"
	"math/big"
)

// The reserved aliases that name the §10.2 JWT signing keys. The
// deployment maps each to a provider key reference through --kms-alias.
const (
	// AliasJWTSigning names the key that signs every Lenny-issued JWT.
	AliasJWTSigning = "platform:jwt-signing"

	// AliasJWTVerify names the optional second key that verifies and
	// never signs, used during the §10.2 three-rollout rotation.
	AliasJWTVerify = "platform:jwt-verify"
)

// DigestSize is the byte length of the SHA-256 digest Sign accepts.
const DigestSize = sha256.Size

// SignatureSize is the byte length of an ES256 signature in the JWS
// r||s encoding (RFC 7518 section 3.4): two 32-byte big-endian integers.
const SignatureSize = 64

// p256CoordSize is the byte length of one P-256 scalar or coordinate.
const p256CoordSize = SignatureSize / 2

// IsJWTAlias reports whether alias is one of the reserved §10.2 JWT
// signing aliases.
func IsJWTAlias(alias string) bool {
	return alias == AliasJWTSigning || alias == AliasJWTVerify
}

// CheckDigest returns ErrInvalidDigest unless digest is 32 bytes long.
// Every Provider.Sign implementation calls it before reaching its key.
func CheckDigest(digest []byte) error {
	if len(digest) != DigestSize {
		return fmt.Errorf("%w: got %d bytes", ErrInvalidDigest, len(digest))
	}
	return nil
}

// ecdsaSignature is the ASN.1 DER structure AWS KMS, GCP Cloud KMS, and
// crypto/ecdsa.SignASN1 return: SEQUENCE { r INTEGER, s INTEGER }.
type ecdsaSignature struct {
	R, S *big.Int
}

// SignatureFromASN1 converts an ASN.1 DER ECDSA P-256 signature to the
// 64-byte JWS r||s encoding. DER encodes each integer in its minimal
// length, so an r or s with leading zero bytes arrives shorter than 32
// bytes; each half is left-padded to 32 bytes here. A signature with
// trailing data, a non-positive integer, or an integer wider than the
// P-256 order is rejected.
//
// spec: §10.2 (Authentication): ES256 signatures in the JWS encoding.
func SignatureFromASN1(der []byte) ([]byte, error) {
	var sig ecdsaSignature
	rest, err := asn1.Unmarshal(der, &sig)
	if err != nil {
		return nil, fmt.Errorf("%w: parse DER signature: %v", ErrNotP256, err)
	}
	if len(rest) != 0 {
		return nil, fmt.Errorf("%w: %d trailing bytes after DER signature", ErrNotP256, len(rest))
	}
	order := elliptic.P256().Params().N
	for _, v := range []*big.Int{sig.R, sig.S} {
		if v == nil || v.Sign() <= 0 || v.Cmp(order) >= 0 {
			return nil, fmt.Errorf("%w: signature integer outside the P-256 order", ErrNotP256)
		}
	}
	out := make([]byte, SignatureSize)
	sig.R.FillBytes(out[:p256CoordSize])
	sig.S.FillBytes(out[p256CoordSize:])
	return out, nil
}

// CheckRawSignature returns ErrNotP256 unless sig is a 64-byte r||s
// signature. Azure Key Vault returns this encoding directly.
func CheckRawSignature(sig []byte) error {
	if len(sig) != SignatureSize {
		return fmt.Errorf("%w: r||s signature is %d bytes, want %d", ErrNotP256, len(sig), SignatureSize)
	}
	return nil
}

// PublicKeyFromPKIX parses a DER SubjectPublicKeyInfo and returns it
// when it is an ECDSA P-256 key. AWS KMS returns this encoding, and GCP
// Cloud KMS returns it PEM-wrapped.
func PublicKeyFromPKIX(der []byte) (*ecdsa.PublicKey, error) {
	pub, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, fmt.Errorf("kms: parse public key: %w", err)
	}
	ec, ok := pub.(*ecdsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("%w: public key type %T", ErrNotP256, pub)
	}
	if ec.Curve != elliptic.P256() {
		return nil, fmt.Errorf("%w: curve %s", ErrNotP256, ec.Curve.Params().Name)
	}
	return ec, nil
}
