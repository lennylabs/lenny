// SPDX-License-Identifier: MIT

package deadletterredaction

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/kms"
)

// KMSReceiptSigner signs RedactionReceipts inside the KMS with the ES256
// key the platform:audit-signing alias names, through the same Sign
// operation the JWT signer uses. On a cloud provider the private key never
// leaves the KMS, so every replica signs alike and a receipt outlives the
// process that signed it. It satisfies Signer and is safe for concurrent
// use.
type KMSReceiptSigner struct {
	provider kms.Provider
	alias    string
	keyID    string
}

// NewKMSReceiptSigner returns a signer that signs under alias and stamps
// keyID, the immutable key-version reference alias maps to, on every
// receipt as its signature_kms_key_id.
func NewKMSReceiptSigner(provider kms.Provider, alias, keyID string) (*KMSReceiptSigner, error) {
	if provider == nil {
		return nil, errors.New("deadletterredaction: nil kms provider")
	}
	if alias == "" || keyID == "" {
		return nil, errors.New("deadletterredaction: receipt signer needs an alias and a key identifier")
	}
	return &KMSReceiptSigner{provider: provider, alias: alias, keyID: keyID}, nil
}

// Sign returns the 64-byte ES256 r||s signature of the SHA-256 digest of
// message under the receipt signing key, and the key identifier. A KMS
// failure, throttling included, is returned, so the erasure job's
// redaction step fails and is retried rather than writing an unsigned
// receipt.
//
// spec: §12.8 (Compliance Interfaces)
func (s *KMSReceiptSigner) Sign(ctx context.Context, message []byte) ([]byte, string, error) {
	digest := sha256.Sum256(message)
	sig, err := s.provider.Sign(ctx, s.alias, digest[:])
	if err != nil {
		return nil, "", fmt.Errorf("deadletterredaction: kms sign under %s: %w", s.alias, err)
	}
	return sig, s.keyID, nil
}

// KeyID returns the key identifier the signer stamps on receipts.
func (s *KMSReceiptSigner) KeyID() string { return s.keyID }

// receiptKeyRef returns the key reference alias maps to in aliasToKey, or
// the alias name itself when the map leaves it unmapped, which is the key
// the local provider derives for an unmapped alias. A cloud provider
// refuses an unmapped alias when the loader fetches its public key.
func receiptKeyRef(aliasToKey map[string]string, alias string) string {
	if ref := aliasToKey[alias]; ref != "" {
		return ref
	}
	return alias
}

// LoadReceiptSigning builds the receipt signer and the set of keys a chain
// verifier trusts: the key platform:audit-signing names, which signs, and
// the key the optional platform:audit-verify names, which verifies
// receipts signed before the last rotation. Each key is identified by the
// reference its alias maps to, which is the value a receipt records in
// signature_kms_key_id. The public keys are fetched once; a process that
// cannot load them does not start, as for the JWT key set.
//
// spec: §12.8 (Compliance Interfaces)
func LoadReceiptSigning(ctx context.Context, provider kms.Provider, aliasToKey map[string]string) (*audit.ReceiptKeySet, *KMSReceiptSigner, error) {
	if provider == nil {
		return nil, nil, errors.New("deadletterredaction: nil kms provider")
	}
	aliases := []string{kms.AliasAuditSigning}
	if aliasToKey[kms.AliasAuditVerify] != "" {
		aliases = append(aliases, kms.AliasAuditVerify)
	}
	keys := make([]audit.ReceiptKey, 0, len(aliases))
	for _, alias := range aliases {
		pub, err := provider.PublicKey(ctx, alias)
		if err != nil {
			return nil, nil, fmt.Errorf("deadletterredaction: load public key of %s: %w", alias, err)
		}
		keys = append(keys, audit.ReceiptKey{KeyID: receiptKeyRef(aliasToKey, alias), PublicKey: pub})
	}
	set, err := audit.NewReceiptKeySet(keys...)
	if err != nil {
		return nil, nil, err
	}
	signer, err := NewKMSReceiptSigner(provider, kms.AliasAuditSigning, receiptKeyRef(aliasToKey, kms.AliasAuditSigning))
	if err != nil {
		return nil, nil, err
	}
	return set, signer, nil
}
