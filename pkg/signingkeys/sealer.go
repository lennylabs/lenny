// SPDX-License-Identifier: MIT

package signingkeys

import (
	"context"
	"fmt"

	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/kms/envelope"
)

// Sealer wraps a secret for storage and unwraps a stored one. The wrapped
// form is the only form that leaves process memory.
type Sealer interface {
	Seal(ctx context.Context, plaintext []byte) ([]byte, error)
	Open(ctx context.Context, wrapped []byte) ([]byte, error)
}

// envelopeSealer is the Sealer over pkg/kms/envelope: each secret is
// encrypted under its own data key, and the data key is wrapped by the KEK
// that a platform KMS alias names.
type envelopeSealer struct {
	cipher *envelope.Cipher
}

// NewEnvelopeSealer returns a Sealer that envelope-encrypts under the KEK
// alias names in provider.
//
// spec: §12.3 (Postgres HA Requirements), §12.9 (Data Classification)
func NewEnvelopeSealer(provider kms.Provider, alias string) (Sealer, error) {
	cipher, err := envelope.New(provider, alias)
	if err != nil {
		return nil, fmt.Errorf("signingkeys: envelope cipher for %q: %w", alias, err)
	}
	return &envelopeSealer{cipher: cipher}, nil
}

// Seal implements Sealer.
func (s *envelopeSealer) Seal(ctx context.Context, plaintext []byte) ([]byte, error) {
	sealed, err := s.cipher.Seal(ctx, plaintext)
	if err != nil {
		return nil, fmt.Errorf("signingkeys: seal: %w", err)
	}
	buf, err := envelope.Encode(sealed)
	if err != nil {
		return nil, fmt.Errorf("signingkeys: encode sealed secret: %w", err)
	}
	return buf, nil
}

// Open implements Sealer.
func (s *envelopeSealer) Open(ctx context.Context, wrapped []byte) ([]byte, error) {
	sealed, err := envelope.Decode(wrapped)
	if err != nil {
		return nil, fmt.Errorf("signingkeys: decode sealed secret: %w", err)
	}
	plain, err := s.cipher.Open(ctx, sealed)
	if err != nil {
		return nil, fmt.Errorf("signingkeys: open: %w", err)
	}
	return plain, nil
}
