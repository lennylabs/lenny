// SPDX-License-Identifier: MIT

package deadletterredaction

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/kms/envelope"
)

// receiptSigningKeySize is the byte length of the HMAC-SHA256 audit
// signing key used to sign RedactionReceipts.
const receiptSigningKeySize = 32

// HMACReceiptSigner signs §12.8 RedactionReceipts with HMAC-SHA256 under a
// key held in memory (unwrapped from KMS at startup, mirroring the §13.3
// Token Service signer). It satisfies Signer.
type HMACReceiptSigner struct {
	keyID string
	key   []byte
}

// NewHMACReceiptSigner returns a signer over the supplied key. Used by
// tests and as the building block of the KMS-backed constructor.
func NewHMACReceiptSigner(keyID string, key []byte) *HMACReceiptSigner {
	return &HMACReceiptSigner{keyID: keyID, key: key}
}

// NewKMSReceiptSigner generates a fresh HMAC-SHA256 audit signing key,
// envelope-seals it under the KEK named by kekAlias, and returns a signer
// over the unwrapped key. The key never leaves the process in plaintext;
// it is the redaction-receipt analog of jwt.NewKMSSigner.
func NewKMSReceiptSigner(ctx context.Context, provider kms.Provider, kekAlias, keyID string) (*HMACReceiptSigner, error) {
	if provider == nil {
		return nil, errors.New("deadletterredaction: nil kms provider")
	}
	cipher, err := envelope.New(provider, kekAlias)
	if err != nil {
		return nil, fmt.Errorf("deadletterredaction: receipt signer envelope: %w", err)
	}
	key := make([]byte, receiptSigningKeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("deadletterredaction: generate receipt signing key: %w", err)
	}
	if _, err := cipher.Seal(ctx, key); err != nil {
		return nil, fmt.Errorf("deadletterredaction: seal receipt signing key: %w", err)
	}
	return &HMACReceiptSigner{keyID: keyID, key: key}, nil
}

// Sign returns the HMAC-SHA256 of message under the signing key and the
// key id stamped onto the receipt's signature_kms_key_id column.
func (s *HMACReceiptSigner) Sign(_ context.Context, message []byte) ([]byte, string, error) {
	if len(s.key) == 0 {
		return nil, "", errors.New("deadletterredaction: receipt signer has no key")
	}
	mac := hmac.New(sha256.New, s.key)
	mac.Write(message)
	return mac.Sum(nil), s.keyID, nil
}
