// SPDX-License-Identifier: MIT

package playground

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

// generateCodeVerifier returns an RFC 7636 PKCE code_verifier: 256
// bits of crypto/rand entropy in the base64url unreserved alphabet,
// 43 characters long.
func generateCodeVerifier() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// codeChallengeS256 derives the RFC 7636 S256 code_challenge from a
// verifier: base64url(SHA-256(ASCII(verifier))) without padding.
func codeChallengeS256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
