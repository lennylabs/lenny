// SPDX-License-Identifier: MIT

// Package connectoroauth implements the §9.3 connector OAuth 2.1
// authorization-code flow primitives the gateway runs when a
// ConnectorDefinition declares `auth.type: oauth2`.
//
// The package is pure: no Redis, no Postgres, no HTTP. It provides
// three testable pieces the gateway's connector OAuth handlers compose:
//
//   - PKCE (this file) — RFC 7636 S256 code_verifier / code_challenge
//     generation. §9.3 mandates PKCE (S256) for public clients and
//     recommends it for confidential clients, so every authorization
//     request the gateway builds carries a code_challenge.
//   - StateSigner (state.go) — the §9.3 anti-CSRF `state` parameter.
//     state is cryptographically random (>=128 bits, base64url) and
//     HMAC-SHA256 signed so a forged callback whose state was not
//     minted by this gateway is rejected before any code exchange.
//   - StateStore (state.go) — the per-flow context (code_verifier,
//     connector id, session id, user id, redirect URI) keyed by the
//     state value with a 10-minute TTL per §9.3, and single-use
//     consumption so a replayed callback is rejected.
//
// OAuth 2.1 framing. OAuth 2.1 removes the implicit grant; this
// package supports only the authorization-code grant with PKCE. There
// is no implicit-grant code path.
package connectoroauth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
)

// pkceVerifierBytes is the number of random bytes drawn for a PKCE
// code_verifier. RFC 7636 section 4.1 requires a verifier of 43–128
// characters from the unreserved alphabet. base64url without padding
// expands n bytes to ceil(4n/3) characters; 32 bytes yields a
// 43-character verifier, the RFC minimum, with 256 bits of entropy.
const pkceVerifierBytes = 32

// PKCEMethodS256 is the RFC 7636 `code_challenge_method` value for the
// SHA-256 transform. §9.3 mandates S256; the `plain` method is never
// used.
const PKCEMethodS256 = "S256"

// ErrInvalidVerifier is returned by CodeChallengeS256 when the supplied
// verifier is empty or shorter than the RFC 7636 minimum length.
var ErrInvalidVerifier = errors.New("connectoroauth: code_verifier is empty or too short")

// PKCE is a generated RFC 7636 verifier/challenge pair. The gateway
// sends Challenge (with ChallengeMethod) in the authorization request
// and stores Verifier in the StateStore for the token exchange.
type PKCE struct {
	// Verifier is the high-entropy code_verifier. It is stored in the
	// StateStore and submitted at the token endpoint; it never appears
	// in the authorization request or any URL.
	Verifier string

	// Challenge is base64url(SHA-256(Verifier)) without padding. It is
	// sent in the authorization request as `code_challenge`.
	Challenge string

	// ChallengeMethod is always PKCEMethodS256.
	ChallengeMethod string
}

// GeneratePKCE draws a fresh code_verifier from crypto/rand and derives
// its S256 code_challenge. It returns an error only when the system
// random source fails.
func GeneratePKCE() (PKCE, error) {
	verifier, err := GenerateCodeVerifier()
	if err != nil {
		return PKCE{}, err
	}
	challenge, err := CodeChallengeS256(verifier)
	if err != nil {
		return PKCE{}, err
	}
	return PKCE{
		Verifier:        verifier,
		Challenge:       challenge,
		ChallengeMethod: PKCEMethodS256,
	}, nil
}

// GenerateCodeVerifier returns a fresh RFC 7636 code_verifier: 256 bits
// of crypto/rand entropy encoded with the base64url unreserved
// alphabet, 43 characters long. It returns an error only when the
// system random source fails.
func GenerateCodeVerifier() (string, error) {
	buf := make([]byte, pkceVerifierBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("connectoroauth: read random for code_verifier: %w", err)
	}
	// base64url without padding keeps every character inside the RFC
	// 7636 unreserved set (A-Z a-z 0-9 - _), so the verifier needs no
	// further escaping in the token-exchange form body.
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// CodeChallengeS256 derives the RFC 7636 S256 code_challenge from a
// verifier: base64url(SHA-256(ASCII(verifier))) without padding. It
// returns ErrInvalidVerifier when verifier is empty or shorter than the
// 43-character RFC minimum.
func CodeChallengeS256(verifier string) (string, error) {
	if len(verifier) < 43 {
		return "", ErrInvalidVerifier
	}
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:]), nil
}
