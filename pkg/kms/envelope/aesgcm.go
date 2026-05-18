// SPDX-License-Identifier: MIT

package envelope

import (
	"crypto/aes"
	"crypto/cipher"
	"fmt"

	"github.com/lennylabs/lenny/pkg/kms"
)

// newGCM builds an AES-256-GCM AEAD from a 32-byte data-encryption-key.
// It rejects a key whose length is not kms.DEKSize so a short DEK
// surfaces as an error rather than a silently weaker cipher.
func newGCM(dek []byte) (cipher.AEAD, error) {
	if len(dek) != kms.DEKSize {
		return nil, fmt.Errorf("envelope: DEK must be %d bytes, got %d", kms.DEKSize, len(dek))
	}
	block, err := aes.NewCipher(dek)
	if err != nil {
		return nil, fmt.Errorf("envelope: aes cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("envelope: gcm: %w", err)
	}
	return gcm, nil
}
