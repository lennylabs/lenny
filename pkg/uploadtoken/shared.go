// SPDX-License-Identifier: MIT

package uploadtoken

import (
	"fmt"

	"github.com/lennylabs/lenny/pkg/signingkeys"
)

// sharedKeys adapts the shared signing-key ring of the upload-token purpose
// to Keys.
type sharedKeys struct {
	ring *signingkeys.Ring
}

// NewSharedKeys returns the Keys over ring, the gateway's ring of the
// upload-token purpose. Every replica holds the same keys, so a token any
// replica minted verifies on every replica.
//
// spec: §7.1 (Normal Flow)
func NewSharedKeys(ring *signingkeys.Ring) Keys {
	return sharedKeys{ring: ring}
}

// SigningKey implements Keys. A ring that refuses to sign, because it has
// not been refreshed within the overlap window or holds no active key,
// yields ErrSigningUnavailable, so the replica stops minting while it
// keeps verifying.
//
// spec: §7.1 (Normal Flow)
func (s sharedKeys) SigningKey() (SigningKey, error) {
	k, err := s.ring.SigningKey()
	if err != nil {
		return SigningKey{}, fmt.Errorf("%w: %w", ErrSigningUnavailable, err)
	}
	return SigningKey{KeyID: k.KeyID, Secret: k.Secret}, nil
}

// VerificationKeys implements Keys.
func (s sharedKeys) VerificationKeys() []SigningKey {
	keys := s.ring.VerificationKeys()
	out := make([]SigningKey, len(keys))
	for i, k := range keys {
		out[i] = SigningKey{KeyID: k.KeyID, Secret: k.Secret}
	}
	return out
}
