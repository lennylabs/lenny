// SPDX-License-Identifier: MIT

package jwt

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"fmt"
	"math/big"

	// Register the SHA-384 and SHA-512 hashes crypto.Hash.New uses.
	_ "crypto/sha512"
)

// oidcAlg describes one JOSE signature algorithm an IdP may sign with.
type oidcAlg struct {
	hash crypto.Hash
	// kind is "rsa-pkcs1", "rsa-pss", or "ecdsa".
	kind string
	// curve is the ECDSA curve the algorithm fixes; nil for RSA.
	curve elliptic.Curve
}

// oidcAlgorithms is the closed set of RFC 7518 section 3.1 asymmetric
// algorithms the OIDC verifier accepts. `none` and the HMAC algorithms
// are absent, so a token signed with an IdP's public key as an HMAC
// secret is refused before any key lookup.
var oidcAlgorithms = map[string]oidcAlg{
	"RS256": {hash: crypto.SHA256, kind: "rsa-pkcs1"},
	"RS384": {hash: crypto.SHA384, kind: "rsa-pkcs1"},
	"RS512": {hash: crypto.SHA512, kind: "rsa-pkcs1"},
	"PS256": {hash: crypto.SHA256, kind: "rsa-pss"},
	"PS384": {hash: crypto.SHA384, kind: "rsa-pss"},
	"PS512": {hash: crypto.SHA512, kind: "rsa-pss"},
	"ES256": {hash: crypto.SHA256, kind: "ecdsa", curve: elliptic.P256()},
	"ES384": {hash: crypto.SHA384, kind: "ecdsa", curve: elliptic.P384()},
	"ES512": {hash: crypto.SHA512, kind: "ecdsa", curve: elliptic.P521()},
}

// keyMatchesAlg reports whether key can verify a signature made with alg.
func keyMatchesAlg(key crypto.PublicKey, alg string) bool {
	a, ok := oidcAlgorithms[alg]
	if !ok {
		return false
	}
	switch k := key.(type) {
	case *rsa.PublicKey:
		return a.kind == "rsa-pkcs1" || a.kind == "rsa-pss"
	case *ecdsa.PublicKey:
		return a.kind == "ecdsa" && k.Curve == a.curve
	default:
		return false
	}
}

// verifyOIDCSignature checks the base64url JWS signature over
// signingInput with key under alg. The caller has already checked that
// key matches alg.
func verifyOIDCSignature(alg string, key crypto.PublicKey, signingInput, sigB64 string) error {
	a := oidcAlgorithms[alg]
	sig, err := decode(sigB64)
	if err != nil {
		return &VerifyError{Reason: "malformed", Detail: "signature is not base64url"}
	}
	h := a.hash.New()
	h.Write([]byte(signingInput))
	digest := h.Sum(nil)
	var ok bool
	switch k := key.(type) {
	case *rsa.PublicKey:
		if a.kind == "rsa-pss" {
			ok = rsa.VerifyPSS(k, a.hash, digest, sig, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash}) == nil
		} else {
			ok = rsa.VerifyPKCS1v15(k, a.hash, digest, sig) == nil
		}
	case *ecdsa.PublicKey:
		ok = verifyJWSECDSA(k, digest, sig)
	default:
		return &VerifyError{Reason: "unsupported_alg", Detail: fmt.Sprintf("key type %T", key)}
	}
	if !ok {
		return &VerifyError{Reason: "signature_mismatch"}
	}
	return nil
}

// verifyJWSECDSA checks an RFC 7518 section 3.4 r||s signature, whose
// halves are each the curve's byte size.
func verifyJWSECDSA(pub *ecdsa.PublicKey, digest, sig []byte) bool {
	size := (pub.Curve.Params().BitSize + 7) / 8
	if len(sig) != 2*size {
		return false
	}
	r := new(big.Int).SetBytes(sig[:size])
	s := new(big.Int).SetBytes(sig[size:])
	return ecdsa.Verify(pub, digest, r, s)
}
