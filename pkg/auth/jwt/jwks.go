// SPDX-License-Identifier: MIT

package jwt

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
)

// JWK is the RFC 7517 JSON Web Key representation of one signing key.
// Only the closed set of fields Lenny reads or derives is modelled here.
// For an EC key, Crv, X, and Y describe the public point, and Thumbprint
// derives the token kid from them. A symmetric key never carries its
// secret in a JWK.
type JWK struct {
	// Kty is the JOSE key type: "oct" for symmetric HMAC keys, "RSA"
	// for RSA, "EC" for elliptic curve. RFC 7517 §4.1.
	Kty string `json:"kty"`

	// Use is the key purpose. Lenny only signs JWTs, so this is
	// always "sig". RFC 7517 §4.2.
	Use string `json:"use,omitempty"`

	// Alg is the JOSE algorithm the key signs with. RFC 7517 §4.4.
	Alg string `json:"alg,omitempty"`

	// Kid is the JOSE key id stamped on every token signed by this
	// key; a verifier routes a token to its key by Kid. RFC 7517 §4.5.
	Kid string `json:"kid"`

	// N is the base64url-encoded RSA modulus. Present only for RSA.
	N string `json:"n,omitempty"`

	// E is the base64url-encoded RSA exponent. Present only for RSA.
	E string `json:"e,omitempty"`

	// Crv is the EC curve name (e.g., "P-256"). Present only for EC.
	Crv string `json:"crv,omitempty"`

	// X is the base64url-encoded EC point x-coordinate. EC only.
	X string `json:"x,omitempty"`

	// Y is the base64url-encoded EC point y-coordinate. EC only.
	Y string `json:"y,omitempty"`
}

// curveP256 is the RFC 7518 section 6.2.1.1 crv value of a P-256 key.
const curveP256 = "P-256"

// p256CoordSize is the byte length of a P-256 coordinate.
const p256CoordSize = 32

// ECPublicJWK returns the RFC 7518 section 6.2 EC JWK of a P-256 public
// key: kty EC, crv P-256, and the x and y coordinates base64url-encoded
// at their full 32-byte width, as RFC 7518 section 6.2.1.2 requires.
//
// spec: §10.2 (Authentication).
func ECPublicJWK(pub *ecdsa.PublicKey) (JWK, error) {
	if pub == nil || pub.Curve != elliptic.P256() {
		return JWK{}, errors.New("jwt: EC JWK needs a P-256 public key")
	}
	raw, err := pub.Bytes()
	if err != nil {
		return JWK{}, fmt.Errorf("jwt: encode public key: %w", err)
	}
	// raw is the uncompressed point 0x04 || X || Y.
	return JWK{
		Kty: "EC",
		Use: "sig",
		Alg: AlgES256,
		Crv: curveP256,
		X:   encode(raw[1 : 1+p256CoordSize]),
		Y:   encode(raw[1+p256CoordSize:]),
	}, nil
}

// ecThumbprintInput is the RFC 7638 section 3.2 thumbprint input of an
// EC key: only the required members, in lexicographic order, which
// encoding/json emits for struct fields declared in that order.
type ecThumbprintInput struct {
	Crv string `json:"crv"`
	Kty string `json:"kty"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

// Thumbprint returns the RFC 7638 JWK thumbprint of an EC key: the
// base64url SHA-256 of its required members serialized in lexicographic
// order with no whitespace. It is the kid of every Lenny-issued token.
//
// spec: §10.2 (Authentication).
func (j JWK) Thumbprint() (string, error) {
	if j.Kty != "EC" || j.Crv == "" || j.X == "" || j.Y == "" {
		return "", fmt.Errorf("jwt: thumbprint needs an EC JWK with crv, x, and y, got kty %q", j.Kty)
	}
	canonical, err := json.Marshal(ecThumbprintInput{Crv: j.Crv, Kty: j.Kty, X: j.X, Y: j.Y})
	if err != nil {
		return "", fmt.Errorf("jwt: encode thumbprint input: %w", err)
	}
	sum := sha256.Sum256(canonical)
	return encode(sum[:]), nil
}
