// SPDX-License-Identifier: MIT

package cosign_verify

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
)

// PublicKeyVerifier is the default Verifier backend: keyed cosign
// verification against a deployer-configured trusted public key. It
// checks that an image's manifest digest carries a detached signature
// the configured key accepts, using only the Go standard library.
//
// Why keyed verification is the default. The full cosign/Sigstore
// keyless path (Fulcio certificate issuance, Rekor transparency-log
// inclusion proofs, TUF root-of-trust refresh) requires the
// github.com/sigstore/cosign and github.com/sigstore/sigstore modules,
// whose transitive trees pull a headless-browser automation library, a
// full Docker client, an ACME CA implementation, and the TUF client —
// dozens of modules unrelated to admission control. §5.2 mandates a
// fail-closed signature gate; it does not mandate the keyless backend.
// The Verifier interface keeps the keyless backend addable later as a
// drop-in replacement without touching the decision logic or the
// webhook transport.
//
// The signature material. cosign keyed signing produces, for an image
// pushed by digest, an ECDSA (or RSA, or Ed25519) signature over the
// SHA-256 manifest digest of the image. PublicKeyVerifier holds the
// trusted public key and an ImageDigestResolver that maps an image
// reference to (digest, signature). The deployer supplies the signature
// store; the resolver abstracts where it lives (an OCI .sig artifact
// fetched by a registry client the deployer wires in, a sidecar policy
// bundle, or a config map). PublicKeyVerifier performs the cryptographic
// check itself. Splitting resolution from verification keeps this type
// free of any registry-client dependency while still performing a
// genuine cryptographic verification rather than a stub allow.
type PublicKeyVerifier struct {
	// keys are the trusted public keys parsed from the deployer's
	// configured PEM material. A signature that verifies under any one
	// key is accepted, which supports key rotation with an overlap
	// window: the new key is added before the old key is retired.
	keys []any

	// resolve maps an image reference to its signed digest and the
	// detached signature over that digest. A nil resolver makes every
	// Verify call fail closed, because the verifier then has no
	// signature to check.
	resolve ImageDigestResolver
}

// SignedDigest is the signing material for one image: the digest that
// was signed and the detached signature over it.
type SignedDigest struct {
	// Digest is the image manifest digest in "algo:hex" form, for
	// example "sha256:abc123...". Only sha256 is supported, matching
	// the digest algorithm Lenny pins images by (§5.2 digest pinning).
	Digest string

	// Signature is the detached signature over the digest bytes,
	// base64-standard-encoded as cosign stores it.
	Signature string
}

// ImageDigestResolver maps an image reference to the digest that was
// signed and the detached signature over it. The deployer wires in the
// concrete resolver; PublicKeyVerifier performs the cryptographic check
// on the result. A resolver that cannot find a signature for the image
// returns an error, which PublicKeyVerifier surfaces as a fail-closed
// rejection.
type ImageDigestResolver interface {
	// Resolve returns the signed digest and signature for imageRef.
	Resolve(ctx context.Context, imageRef string) (SignedDigest, error)
}

// NewPublicKeyVerifier builds a PublicKeyVerifier from PEM-encoded
// trusted public keys and a digest resolver. pemKeys is the deployer's
// configured cosign public-key material; it may carry several PEM
// blocks to support key rotation. resolve maps an image reference to
// its signing material.
//
// It returns an error when pemKeys carries no parseable public key, so
// a misconfigured key cannot silently produce a verifier that rejects
// every image (which would itself be a fail-closed denial-of-service)
// or, worse, a verifier with no trust anchor.
func NewPublicKeyVerifier(pemKeys []byte, resolve ImageDigestResolver) (*PublicKeyVerifier, error) {
	keys, err := parsePublicKeys(pemKeys)
	if err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return nil, errors.New("cosign_verify: no trusted public key found in configured PEM material")
	}
	return &PublicKeyVerifier{keys: keys, resolve: resolve}, nil
}

// Verify implements Verifier. It resolves the image's signed digest and
// detached signature, then checks the signature against every trusted
// public key, accepting the image when any key verifies it. Every
// failure path returns a non-nil error so Decide treats the image as
// fail-closed.
func (v *PublicKeyVerifier) Verify(ctx context.Context, imageRef string) error {
	if v.resolve == nil {
		return errors.New("no image-digest resolver configured; cannot obtain a signature to verify")
	}
	signed, err := v.resolve.Resolve(ctx, imageRef)
	if err != nil {
		return fmt.Errorf("resolve signature for %s: %w", imageRef, err)
	}

	digestBytes, err := decodeSHA256Digest(signed.Digest)
	if err != nil {
		return err
	}
	sig, err := base64.StdEncoding.DecodeString(signed.Signature)
	if err != nil {
		return fmt.Errorf("decode signature: %w", err)
	}
	if len(sig) == 0 {
		return errors.New("image carries an empty signature")
	}

	for _, key := range v.keys {
		if verifyDigestSignature(key, digestBytes, sig) {
			return nil
		}
	}
	return errors.New("signature does not verify against any trusted public key")
}

// decodeSHA256Digest parses an "algo:hex" image digest and returns the
// raw 32-byte hash. It rejects any algorithm other than sha256 and any
// hex string that is not a 32-byte digest, so a malformed digest fails
// closed rather than verifying against truncated bytes.
func decodeSHA256Digest(digest string) ([]byte, error) {
	algo, hexPart, ok := strings.Cut(digest, ":")
	if !ok {
		return nil, fmt.Errorf("malformed image digest %q: want \"algo:hex\"", digest)
	}
	if algo != "sha256" {
		return nil, fmt.Errorf("unsupported digest algorithm %q: only sha256 is verified", algo)
	}
	raw, err := hex.DecodeString(hexPart)
	if err != nil {
		return nil, fmt.Errorf("decode digest hex: %w", err)
	}
	if len(raw) != sha256.Size {
		return nil, fmt.Errorf("digest is %d bytes, want %d for sha256", len(raw), sha256.Size)
	}
	return raw, nil
}

// verifyDigestSignature checks sig against the precomputed image digest
// for one trusted public key. The digest is itself a SHA-256 hash, so
// the signature is the key's signature over those 32 bytes. ECDSA, RSA
// PKCS#1 v1.5, and Ed25519 keys are supported; an unrecognized key type
// reports failure rather than panicking.
func verifyDigestSignature(key any, digest, sig []byte) bool {
	switch k := key.(type) {
	case *ecdsa.PublicKey:
		return ecdsa.VerifyASN1(k, digest, sig)
	case *rsa.PublicKey:
		return rsa.VerifyPKCS1v15(k, crypto.SHA256, digest, sig) == nil
	case ed25519.PublicKey:
		// Ed25519 signs the message directly; cosign's Ed25519 mode
		// signs the digest bytes.
		return ed25519.Verify(k, digest, sig)
	default:
		return false
	}
}

// parsePublicKeys decodes every PEM block in raw into a public key. It
// accepts PKIX public keys and X.509 certificates (the verifier uses
// the certificate's public key). Blocks that do not parse as either are
// skipped; an input with no usable key yields an empty slice, which
// NewPublicKeyVerifier rejects.
func parsePublicKeys(raw []byte) ([]any, error) {
	var keys []any
	rest := raw
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if key, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
			keys = append(keys, key)
			continue
		}
		if cert, err := x509.ParseCertificate(block.Bytes); err == nil {
			keys = append(keys, cert.PublicKey)
			continue
		}
	}
	return keys, nil
}
