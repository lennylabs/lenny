// SPDX-License-Identifier: MIT

package jwt

import (
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/lennylabs/lenny/pkg/kms"
)

// AlgES256 is the JOSE alg of every Lenny-issued JWT (RFC 7518 section
// 3.4). KeySet rejects every other value.
const AlgES256 = "ES256"

// KeySetEntry is one public key in a KeySet: the P-256 key the KMS
// returned for an alias and its RFC 7638 thumbprint, which is the kid
// of every token that key signs.
type KeySetEntry struct {
	KeyID     string
	PublicKey *ecdsa.PublicKey
}

// KeySet is the §10.2 verifier of Lenny-issued tokens: the public key of
// platform:jwt-signing and, when it is mapped, of platform:jwt-verify,
// fetched once at startup and indexed by kid. It is never refreshed,
// because a version-pinned public key is immutable, so verification
// never calls the KMS and an unknown kid costs no provider call.
//
// KeySet is immutable after LoadKeySet returns and is safe for
// concurrent use.
//
// spec: §10.2 (Authentication) "Verifier key set and rotation".
type KeySet struct {
	signing KeySetEntry
	keys    map[string]*ecdsa.PublicKey
	now     func() time.Time
}

// LoadKeySet fetches, with one PublicKey call per alias, the public key
// of platform:jwt-signing and, when aliasToKey maps platform:jwt-verify,
// of platform:jwt-verify, and indexes each by its RFC 7638 thumbprint.
// It returns any provider error, so a process that cannot load a mapped
// entry does not start. aliasToKey is the --kms-alias map; LoadKeySet
// reads only whether it carries a platform:jwt-verify entry, because the
// provider already resolves each alias to its key reference.
//
// LoadKeySet is the only caller of PublicKey on the platform:jwt-*
// aliases. The signer is built from Signing(), so the signer's kid and
// the verifier's kid come from one fetch.
//
// spec: §10.2 (Authentication) "Verifier key set and rotation".
func LoadKeySet(ctx context.Context, provider kms.Provider, aliasToKey map[string]string) (*KeySet, error) {
	if provider == nil {
		return nil, errors.New("jwt: load key set: nil kms provider")
	}
	signing, err := loadEntry(ctx, provider, kms.AliasJWTSigning)
	if err != nil {
		return nil, err
	}
	ks := &KeySet{
		signing: signing,
		keys:    map[string]*ecdsa.PublicKey{signing.KeyID: signing.PublicKey},
		now:     time.Now,
	}
	if _, mapped := aliasToKey[kms.AliasJWTVerify]; mapped {
		verify, err := loadEntry(ctx, provider, kms.AliasJWTVerify)
		if err != nil {
			return nil, err
		}
		ks.keys[verify.KeyID] = verify.PublicKey
	}
	return ks, nil
}

// loadEntry fetches alias's public key and computes its thumbprint.
func loadEntry(ctx context.Context, provider kms.Provider, alias string) (KeySetEntry, error) {
	pub, err := provider.PublicKey(ctx, alias)
	if err != nil {
		return KeySetEntry{}, fmt.Errorf("jwt: load key set: public key of %s: %w", alias, err)
	}
	jwk, err := ECPublicJWK(pub)
	if err != nil {
		return KeySetEntry{}, fmt.Errorf("jwt: load key set: %s: %w", alias, err)
	}
	kid, err := jwk.Thumbprint()
	if err != nil {
		return KeySetEntry{}, fmt.Errorf("jwt: load key set: %s: %w", alias, err)
	}
	return KeySetEntry{KeyID: kid, PublicKey: pub}, nil
}

// Signing returns the platform:jwt-signing entry, from which the
// process builds its RemoteSigner.
func (k *KeySet) Signing() KeySetEntry { return k.signing }

// Len reports how many keys the set holds: one, or two during a
// rotation that maps platform:jwt-verify to a different key.
func (k *KeySet) Len() int { return len(k.keys) }

// keySetHeader is the subset of the JOSE header KeySet reads.
type keySetHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
}

// Verify validates a Lenny-issued token and decodes its claims. It
// rejects a token whose alg is anything other than ES256 (HS256 and none
// included), rejects a token with no kid as missing_kid and a kid
// outside the set as unknown_kid without any provider call, then checks
// the ECDSA P-256 signature over the SHA-256 of the signing input and
// the exp and nbf claims. Every failure is a *VerifyError.
//
// spec: §10.2 (Authentication) "Session capability context",
// "Verifier key set and rotation".
func (k *KeySet) Verify(token string) (Claims, error) {
	parts, err := k.checkSignature(token)
	if err != nil {
		return Claims{}, err
	}
	return decodeClaims(parts[1], k.now())
}

// Signed reports whether a key in the set signed token: the header
// carries alg ES256 and a kid in the set, and the signature verifies
// under that key. It reads no claim and no clock, so its answer for a
// token never changes over time. A caller that composes the key set with
// another verifier uses it to tell which member accepted a token after
// the composition verified it; a token another member accepted can never
// satisfy it, because only the set's private keys produce such a
// signature.
//
// spec: §10.2 (Authentication) "Verifier key set and rotation"; §25.4
// (The lenny-ops Service: Authentication).
func (k *KeySet) Signed(token string) bool {
	_, err := k.checkSignature(token)
	return err == nil
}

// checkSignature splits token, applies the alg and kid rules, and checks
// the ES256 signature, returning the three segments.
func (k *KeySet) checkSignature(token string) ([]string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, &VerifyError{Reason: "malformed", Detail: fmt.Sprintf("token has %d segments, want 3", len(parts))}
	}
	pub, err := k.selectKey(parts[0])
	if err != nil {
		return nil, err
	}
	if err := verifyES256(pub, parts[0]+"."+parts[1], parts[2]); err != nil {
		return nil, err
	}
	return parts, nil
}

// selectKey applies the alg rule and returns the key the header's kid
// names.
func (k *KeySet) selectKey(headerB64 string) (*ecdsa.PublicKey, error) {
	headerJSON, err := decode(headerB64)
	if err != nil {
		return nil, &VerifyError{Reason: "malformed", Detail: "header is not base64url"}
	}
	var h keySetHeader
	if err := json.Unmarshal(headerJSON, &h); err != nil {
		return nil, &VerifyError{Reason: "malformed", Detail: fmt.Sprintf("header not JSON: %v", err)}
	}
	if h.Alg != AlgES256 {
		return nil, &VerifyError{Reason: "unsupported_alg", Detail: fmt.Sprintf("alg %q, want %s", h.Alg, AlgES256)}
	}
	if h.Kid == "" {
		return nil, &VerifyError{Reason: "missing_kid", Detail: "JOSE header carries no kid"}
	}
	pub, ok := k.keys[h.Kid]
	if !ok {
		return nil, &VerifyError{Reason: "unknown_kid", Detail: fmt.Sprintf("kid %q is not in the key set", h.Kid)}
	}
	return pub, nil
}

// verifyES256 checks a base64url JWS r||s signature over signingInput.
func verifyES256(pub *ecdsa.PublicKey, signingInput, sigB64 string) error {
	sig, err := decode(sigB64)
	if err != nil {
		return &VerifyError{Reason: "malformed", Detail: "signature is not base64url"}
	}
	if len(sig) != kms.SignatureSize {
		return &VerifyError{Reason: "signature_mismatch", Detail: fmt.Sprintf("signature is %d bytes, want %d", len(sig), kms.SignatureSize)}
	}
	digest := sha256.Sum256([]byte(signingInput))
	r := new(big.Int).SetBytes(sig[:kms.SignatureSize/2])
	s := new(big.Int).SetBytes(sig[kms.SignatureSize/2:])
	if !ecdsa.Verify(pub, digest[:], r, s) {
		return &VerifyError{Reason: "signature_mismatch"}
	}
	return nil
}

// Compile-time check: KeySet is a Verifier.
var _ Verifier = (*KeySet)(nil)
