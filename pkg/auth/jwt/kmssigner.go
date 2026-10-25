// SPDX-License-Identifier: MIT

package jwt

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/kms/envelope"
)

// KMSSigner is the §4 / §13.3 KMS-envelope-backed JWT Signer and
// Verifier. It replaces the per-process dev HMAC secret with signing
// material whose durable form is KMS-envelope-encrypted: the HMAC-
// SHA256 signing key is a data-encryption-key-class secret, sealed
// under a KMS key-encryption-key (KEK) through pkg/kms/envelope, and
// no plaintext signing key is ever persisted.
//
// §4 specifies that the Token Service signs tokens with KMS-backed
// material rather than a dev HMAC secret minted fresh per process.
// KMSSigner satisfies that: the configured form of the signing key is
// the envelope.Sealed blob (SealedKey), the KEK that wraps it lives in
// KMS, and the §4.9.1 rotation procedure rotates the KEK behind the
// alias. At construction KMSSigner asks the KMS to unwrap the signing
// key once and holds the plaintext in process memory for the signing
// hot path, exactly as a KMS-integrated signer does — KMS wraps the
// key at rest, it does not sit on the per-Sign code path.
//
// KMSSigner delegates the JOSE encode, HMAC-SHA256, and Verify logic
// to an internal HMACSigner built from the unwrapped key, so the
// on-the-wire token format is identical to the dev signer's.
//
// KMSSigner is safe for concurrent use; the unwrapped key is read-only
// after NewKMSSigner returns.
type KMSSigner struct {
	inner     *HMACSigner
	sealedKey envelope.Sealed
	kekAlias  string
}

// signingKeySize is the byte length of the Token Service's HMAC-SHA256
// signing key. A 256-bit key matches the HS256 hash width.
const signingKeySize = 32

// TokenServiceKEKAlias is the §4 KEK alias under which the Token
// Service's signing material is envelope-encrypted. It is a platform-
// scoped alias (distinct from the per-tenant "tenant:{id}" aliases
// that wrap credential secrets) because the signing key is a
// platform-wide secret, not tenant data.
const TokenServiceKEKAlias = "platform:token-service-signing"

// NewKMSSigner generates a fresh HMAC-SHA256 signing key, envelope-
// seals it under the KEK named by kekAlias through provider, and
// returns a Signer/Verifier over the unwrapped key. The returned
// signer's SealedKey is the durable, KMS-wrapped form of the signing
// material; a deployment persists SealedKey and reconstructs the
// signer with KMSSignerFromSealed across restarts and replicas so
// every replica signs with the same key.
//
// keyID is stamped on the JOSE header of every signed token, exactly
// as for the dev HMAC signer.
func NewKMSSigner(ctx context.Context, provider kms.Provider, kekAlias, keyID string) (*KMSSigner, error) {
	if provider == nil {
		return nil, errors.New("jwt: nil kms provider")
	}
	cipher, err := envelope.New(provider, kekAlias)
	if err != nil {
		return nil, fmt.Errorf("jwt: kms signer envelope: %w", err)
	}
	key := make([]byte, signingKeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("jwt: generate signing key: %w", err)
	}
	defer zeroBytes(key)

	sealed, err := cipher.Seal(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("jwt: seal signing key: %w", err)
	}
	return &KMSSigner{
		inner:     NewHMACSigner(keyID, key),
		sealedKey: sealed,
		kekAlias:  kekAlias,
	}, nil
}

// KMSSignerFromSealed reconstructs a KMSSigner from a previously
// produced envelope.Sealed signing key. It asks provider to unwrap the
// signing key under kekAlias and returns a signer over the recovered
// key. A deployment uses this to bring up a Token Service replica with
// the same signing key the durable SealedKey carries, so tokens minted
// by one replica verify on another.
//
// KMSSignerFromSealed unwraps the DEK at whatever KEK version the
// Sealed value records, so a signing key wrapped under a superseded
// KEK version still loads after a §4.9.1 KEK rotation.
func KMSSignerFromSealed(ctx context.Context, provider kms.Provider, kekAlias, keyID string, sealed envelope.Sealed) (*KMSSigner, error) {
	if provider == nil {
		return nil, errors.New("jwt: nil kms provider")
	}
	cipher, err := envelope.New(provider, kekAlias)
	if err != nil {
		return nil, fmt.Errorf("jwt: kms signer envelope: %w", err)
	}
	key, err := cipher.Open(ctx, sealed)
	if err != nil {
		return nil, fmt.Errorf("jwt: unwrap signing key: %w", err)
	}
	defer zeroBytes(key)
	if len(key) != signingKeySize {
		return nil, fmt.Errorf("jwt: unwrapped signing key is %d bytes, want %d", len(key), signingKeySize)
	}
	return &KMSSigner{
		inner:     NewHMACSigner(keyID, key),
		sealedKey: sealed,
		kekAlias:  kekAlias,
	}, nil
}

// SealedKey returns the KMS-envelope-encrypted form of the signing
// key. It is safe to persist: the signing key appears only wrapped
// under the KEK, and no plaintext key material is exposed. A
// deployment stores SealedKey and reloads it with KMSSignerFromSealed.
func (s *KMSSigner) SealedKey() envelope.Sealed { return s.sealedKey }

// KEKAlias reports the KMS KEK alias the signing key is wrapped under.
func (s *KMSSigner) KEKAlias() string { return s.kekAlias }

// Sign produces a compact JWT for the supplied Claims, signed with the
// KMS-wrapped HMAC key. The key is in process memory, so ctx bounds no
// remote call.
func (s *KMSSigner) Sign(_ context.Context, c Claims) (string, error) { return s.inner.Sign(c) }

// Verify validates the signature on token and decodes its Claims.
func (s *KMSSigner) Verify(token string) (Claims, error) { return s.inner.Verify(token) }

// KeyID returns the kid header value stamped on signed tokens.
func (s *KMSSigner) KeyID() string { return s.inner.KeyID() }

// Compile-time checks: KMSSigner satisfies both halves of the §13.3
// Token Service signer contract.
var (
	_ Signer   = (*KMSSigner)(nil)
	_ Verifier = (*KMSSigner)(nil)
)

// RemoteSigner is the §10.2 ES256 signer of every Lenny-issued JWT. It
// builds the JOSE header {"alg":"ES256","typ":"JWT","kid":<thumbprint>},
// hashes the signing input with SHA-256, and sends the digest to the KMS
// Sign operation under the platform:jwt-signing alias, so on a cloud
// provider the private key never exists in process memory. The kid is
// the RFC 7638 thumbprint the key set computed from its one startup
// PublicKey fetch, which keeps the signer's kid and the verifier's kid
// a single value. RemoteSigner implements no Verifier: verification of
// Lenny-issued tokens goes through KeySet only.
//
// RemoteSigner is safe for concurrent use.
//
// spec: §10.2 (Authentication).
type RemoteSigner struct {
	provider kms.Provider
	kid      string
	header   string
}

// NewRemoteSigner returns a signer over provider for the key set's
// signing entry. It fetches nothing: the entry already carries the kid
// that LoadKeySet derived from the signing key's public half.
func NewRemoteSigner(provider kms.Provider, signing KeySetEntry) (*RemoteSigner, error) {
	if provider == nil {
		return nil, errors.New("jwt: nil kms provider")
	}
	if signing.KeyID == "" || signing.PublicKey == nil {
		return nil, errors.New("jwt: remote signer needs the key set's signing entry")
	}
	headerJSON, err := json.Marshal(joseES256Header{Alg: AlgES256, Typ: "JWT", Kid: signing.KeyID})
	if err != nil {
		return nil, fmt.Errorf("jwt: encode JOSE header: %w", err)
	}
	return &RemoteSigner{provider: provider, kid: signing.KeyID, header: encode(headerJSON)}, nil
}

// joseES256Header is the JOSE header RemoteSigner stamps on every token.
type joseES256Header struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
	Kid string `json:"kid"`
}

// Sign produces a compact ES256 JWT for c. The KMS call runs under ctx,
// so the caller's request deadline bounds it.
func (s *RemoteSigner) Sign(ctx context.Context, c Claims) (string, error) {
	payloadJSON, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("jwt: encode claims: %w", err)
	}
	signingInput := s.header + "." + encode(payloadJSON)
	digest := sha256.Sum256([]byte(signingInput))
	sig, err := s.provider.Sign(ctx, kms.AliasJWTSigning, digest[:])
	if err != nil {
		return "", fmt.Errorf("jwt: kms sign: %w", err)
	}
	if len(sig) != kms.SignatureSize {
		return "", fmt.Errorf("jwt: kms returned a %d-byte signature, want %d", len(sig), kms.SignatureSize)
	}
	return signingInput + "." + encode(sig), nil
}

// KeyID returns the kid stamped on every token: the RFC 7638 thumbprint
// of the signing public key.
func (s *RemoteSigner) KeyID() string { return s.kid }

// Compile-time check: RemoteSigner is a Signer.
var _ Signer = (*RemoteSigner)(nil)

// zeroBytes overwrites b with zeros. It is best-effort defense in
// depth for plaintext signing-key material.
func zeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
