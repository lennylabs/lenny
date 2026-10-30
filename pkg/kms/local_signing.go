// SPDX-License-Identifier: MIT

package kms

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"strconv"
)

// localSigningSalt is the HKDF salt of the ES256 signing-key derivation.
// kekMaterial's salt is always `alias\x00version` and so always contains
// a NUL byte; this salt contains none, so no alias and version can make
// the two derivations share a salt. The distinct info prefix separates
// them a second time.
const localSigningSalt = "lenny/kms/local/es256-salt/v1"

// localSigningInfo prefixes the HKDF info of the signing-key derivation.
// The info carries the key reference and the rejection-sampling counter.
const localSigningInfo = "lenny/kms/local/es256/v1"

// maxScalarAttempts bounds the rejection sampling in deriveSigningKey. A
// uniformly random 32-byte value falls at or above the P-256 order with
// probability below 2^-32, so a second attempt is already rare and 16
// attempts failing indicates a defect rather than chance.
const maxScalarAttempts = 16

// SetSigningReference records the key reference a §10.2 JWT alias maps
// to. Sign and PublicKey derive the alias's ES256 key from the seed and
// this reference, so the three-rollout rotation works under the local
// provider: remapping platform:jwt-signing to a new reference yields a
// new key, and mapping platform:jwt-verify to the old reference keeps
// the old key verifiable. providerflags passes the platform:jwt-* entries
// of --kms-alias here.
//
// spec: §10.2 (Authentication) "Development", §17.4.
func (l *Local) SetSigningReference(alias, reference string) {
	l.signingMu.Lock()
	defer l.signingMu.Unlock()
	l.signingRefs[alias] = reference
}

// Sign returns the ES256 r||s signature of digest under the key derived
// for alias. The private key exists in process memory, which is why the
// local provider is refused in production.
//
// spec: §10.2 (Authentication) "Development".
func (l *Local) Sign(ctx context.Context, alias string, digest []byte) ([]byte, error) {
	if err := CheckDigest(digest); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("kms: local sign: %w", err)
	}
	key, err := l.signingKey(alias)
	if err != nil {
		return nil, err
	}
	der, err := ecdsa.SignASN1(rand.Reader, key, digest)
	if err != nil {
		return nil, fmt.Errorf("kms: local sign: %w", err)
	}
	return SignatureFromASN1(der)
}

// PublicKey returns the P-256 public key derived for alias.
func (l *Local) PublicKey(ctx context.Context, alias string) (*ecdsa.PublicKey, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("kms: local public key: %w", err)
	}
	key, err := l.signingKey(alias)
	if err != nil {
		return nil, err
	}
	return &key.PublicKey, nil
}

// signingReference returns the reference alias maps to, or the alias
// name itself when the deployment left it unmapped.
func (l *Local) signingReference(alias string) string {
	l.signingMu.RLock()
	defer l.signingMu.RUnlock()
	if ref, ok := l.signingRefs[alias]; ok {
		return ref
	}
	return alias
}

// signingKey returns the cached key for alias's reference, deriving it
// on first use.
func (l *Local) signingKey(alias string) (*ecdsa.PrivateKey, error) {
	ref := l.signingReference(alias)
	l.signingMu.RLock()
	key, ok := l.signingKeys[ref]
	l.signingMu.RUnlock()
	if ok {
		return key, nil
	}
	key, err := deriveSigningKey(l.seed, ref)
	if err != nil {
		return nil, err
	}
	l.signingMu.Lock()
	defer l.signingMu.Unlock()
	if cached, ok := l.signingKeys[ref]; ok {
		return cached, nil
	}
	l.signingKeys[ref] = key
	return key, nil
}

// deriveSigningKey derives a P-256 private key deterministically from
// seed and reference. Each attempt expands 32 bytes with HKDF-SHA256 and
// accepts them as the scalar when they encode a value in [1, N-1];
// otherwise the counter in the info advances and the next attempt runs.
// The key is built from the scalar with ecdsa.ParseRawPrivateKey.
// ecdsa.GenerateKey is deliberately not used: it does not produce a
// deterministic key for a fixed reader, so two processes sharing one
// seed would derive different keys.
//
// spec: §10.2 (Authentication) "Development": processes that share one
// master key share one signing key.
func deriveSigningKey(seed []byte, reference string) (*ecdsa.PrivateKey, error) {
	for attempt := 0; attempt < maxScalarAttempts; attempt++ {
		info := localSigningInfo + "\x00" + reference + "\x00" + strconv.Itoa(attempt)
		scalar, err := hkdf.Key(sha256.New, seed, []byte(localSigningSalt), info, p256CoordSize)
		if err != nil {
			return nil, fmt.Errorf("kms: derive local signing key for %q: %w", reference, err)
		}
		key, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), scalar)
		if err == nil {
			return key, nil
		}
	}
	return nil, errors.New("kms: derive local signing key: no valid P-256 scalar in the attempt bound")
}
