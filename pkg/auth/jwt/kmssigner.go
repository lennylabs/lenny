// SPDX-License-Identifier: MIT

package jwt

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/lennylabs/lenny/pkg/kms"
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
