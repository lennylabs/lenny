// SPDX-License-Identifier: MIT

package playground

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
)

// stateSecret is the process-local HMAC key the gateway signs the
// §27.3.1 OIDC state cookie with. It is drawn once at package init
// from crypto/rand: the state cookie lives at most 10 min and is
// single-process-scoped, so a per-process key is sufficient and
// rotating it on restart only invalidates in-flight logins.
var stateSecret = mustRandom(32)

// mustRandom returns n bytes of crypto/rand entropy. It panics only
// when the system random source fails, which is unrecoverable.
func mustRandom(n int) []byte {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		panic("playground: crypto/rand unavailable: " + err.Error())
	}
	return buf
}

// sealState serializes cv and returns a signed, base64url-encoded
// cookie value. The value is "<payload>.<hmac>" where the HMAC-SHA256
// covers the payload under the process state key, so a tampered
// cookie fails openState.
func (h *Handler) sealState(cv stateCookieValue) (string, error) {
	payload, err := json.Marshal(cv)
	if err != nil {
		return "", err
	}
	payloadB64 := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, stateSecret)
	mac.Write([]byte(payloadB64))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return payloadB64 + "." + sig, nil
}

// openState verifies and decodes a state cookie value sealed by
// sealState. It returns an error when the value is malformed or the
// HMAC does not verify.
func (h *Handler) openState(value string) (stateCookieValue, error) {
	payloadB64, sigB64, ok := strings.Cut(value, ".")
	if !ok || payloadB64 == "" || sigB64 == "" {
		return stateCookieValue{}, errors.New("playground: state cookie is malformed")
	}
	sig, err := base64.RawURLEncoding.DecodeString(sigB64)
	if err != nil {
		return stateCookieValue{}, errors.New("playground: state cookie signature is not base64url")
	}
	mac := hmac.New(sha256.New, stateSecret)
	mac.Write([]byte(payloadB64))
	if !hmac.Equal(mac.Sum(nil), sig) {
		return stateCookieValue{}, errors.New("playground: state cookie signature is invalid")
	}
	payload, err := base64.RawURLEncoding.DecodeString(payloadB64)
	if err != nil {
		return stateCookieValue{}, errors.New("playground: state cookie payload is not base64url")
	}
	var cv stateCookieValue
	if err := json.Unmarshal(payload, &cv); err != nil {
		return stateCookieValue{}, errors.New("playground: state cookie payload is not JSON")
	}
	return cv, nil
}

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
