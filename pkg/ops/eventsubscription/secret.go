// SPDX-License-Identifier: MIT

package eventsubscription

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// secretBytes is the §25.5 line 2702 secret entropy: 256 bits from a
// CSPRNG.
const secretBytes = 32

// secretPrefix marks a Lenny webhook secret so a receiver can recognize
// it, mirroring the spec's "whsec_..." example. spec: §25.5 line 2706.
const secretPrefix = "whsec_"

// GenerateSecret returns a fresh 256-bit webhook signing secret encoded
// as the §25.5 "whsec_" + hex form. spec: §25.5 line 2702.
func GenerateSecret() (string, error) {
	buf := make([]byte, secretBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("eventsubscription: generate secret: %w", err)
	}
	return secretPrefix + hex.EncodeToString(buf), nil
}

// HashSecret returns the lowercase hex SHA-256 of the plaintext secret.
// This is the value stored at rest in ops_event_subscriptions.secret_hash;
// the plaintext is never persisted. spec: §25.5 line 2702.
func HashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// Fingerprint returns the first 8 hex characters of the secret's SHA-256
// hash, used in audit events so an operator can confirm which secret was
// in effect without learning it. spec: §25.5 lines 2718, 2733.
func Fingerprint(secret string) string {
	return HashSecret(secret)[:8]
}
