// SPDX-License-Identifier: MIT

// Package jwt is the dev-mode HMAC-SHA256 JWT signer / verifier. It
// is the §10.2 LENNY_DEV_MODE=true backend; production deploys swap
// in a KMS-backed signer behind the same Signer / Verifier interface.
// This implementation handles the §10.2 claim shape (typ, tenant_id,
// sub, exp, iat) and the RFC 7519 whole-second exp requirement.
//
// The package intentionally does not depend on any third-party JWT
// library — the dev mode wants minimal dependencies. Production
// callers using a KMS signer wrap a real library behind the Signer
// interface from this package.
package jwt

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
)

// Claims are the §10.2 JWT payload claims this package recognises.
// Unknown claims are preserved through Marshal so callers can carry
// additional values.
type Claims struct {
	// RFC 7519 standard claims.
	Issuer    string   `json:"iss,omitempty"`
	Subject   string   `json:"sub,omitempty"`
	Audience  []string `json:"aud,omitempty"`
	Expiry    int64    `json:"exp,omitempty"`
	NotBefore int64    `json:"nbf,omitempty"`
	IssuedAt  int64    `json:"iat,omitempty"`
	JWTID     string   `json:"jti,omitempty"`

	// §10.2 Lenny extensions.
	TenantID        string         `json:"tenant_id,omitempty"`
	SessionID       string         `json:"session_id,omitempty"`
	CallerType      string         `json:"caller_type,omitempty"`
	DelegationDepth int            `json:"delegation_depth,omitempty"`
	Scope           string         `json:"scope,omitempty"`
	Typ             auth.TokenType `json:"typ,omitempty"`
}

// ExpiryTime returns the Claims expiry as a time.Time. Zero when
// Expiry is unset.
func (c Claims) ExpiryTime() time.Time {
	if c.Expiry == 0 {
		return time.Time{}
	}
	return time.Unix(c.Expiry, 0).UTC()
}

// Signer signs Claims into a compact JWT string.
type Signer interface {
	Sign(c Claims) (string, error)
	KeyID() string
}

// Verifier validates and decodes a compact JWT string into Claims.
type Verifier interface {
	Verify(token string) (Claims, error)
}

// HMACSigner is the dev-mode HMAC-SHA256 Signer / Verifier. It is
// safe for concurrent use; the secret is read-only after construction.
type HMACSigner struct {
	secret []byte
	keyID  string
}

// NewHMACSigner returns a Signer + Verifier backed by secret. The
// keyID is stamped on every signed token's JOSE header so the
// verifier can rotate keys later.
func NewHMACSigner(keyID string, secret []byte) *HMACSigner {
	return &HMACSigner{secret: append([]byte{}, secret...), keyID: keyID}
}

// KeyID returns the kid header value the signer stamps on tokens.
func (s *HMACSigner) KeyID() string { return s.keyID }

// Sign produces a compact JWT string for the supplied Claims.
func (s *HMACSigner) Sign(c Claims) (string, error) {
	header := map[string]any{"alg": "HS256", "typ": "JWT"}
	if s.keyID != "" {
		header["kid"] = s.keyID
	}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	payloadJSON, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	headerB64 := encode(headerJSON)
	payloadB64 := encode(payloadJSON)
	signingInput := headerB64 + "." + payloadB64
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(signingInput))
	sig := mac.Sum(nil)
	return signingInput + "." + encode(sig), nil
}

// Verify validates the signature on token and decodes its Claims.
// Returns *VerifyError on every failure: signature mismatch, expired
// token, malformed structure, etc.
func (s *HMACSigner) Verify(token string) (Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return Claims{}, &VerifyError{Reason: "malformed", Detail: fmt.Sprintf("token has %d segments, want 3", len(parts))}
	}
	signingInput := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(signingInput))
	expected := mac.Sum(nil)
	got, err := decode(parts[2])
	if err != nil {
		return Claims{}, &VerifyError{Reason: "malformed", Detail: "signature is not base64url"}
	}
	if !hmac.Equal(expected, got) {
		return Claims{}, &VerifyError{Reason: "signature_mismatch"}
	}
	payload, err := decode(parts[1])
	if err != nil {
		return Claims{}, &VerifyError{Reason: "malformed", Detail: "payload is not base64url"}
	}
	var claims Claims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return Claims{}, &VerifyError{Reason: "malformed", Detail: fmt.Sprintf("payload not JSON: %v", err)}
	}
	if claims.Expiry > 0 {
		now := time.Now().Unix()
		// §13.3 ±1s skew allowance.
		if claims.Expiry+1 < now {
			return Claims{}, &VerifyError{Reason: "expired"}
		}
	}
	return claims, nil
}

// VerifyError captures every JWT verification failure.
type VerifyError struct {
	Reason string
	Detail string
}

func (e *VerifyError) Error() string {
	if e.Detail != "" {
		return fmt.Sprintf("jwt: %s: %s", e.Reason, e.Detail)
	}
	return "jwt: " + e.Reason
}

// IsExpired reports whether err is a *VerifyError with Reason expired.
func IsExpired(err error) bool {
	var ve *VerifyError
	if errors.As(err, &ve) {
		return ve.Reason == "expired"
	}
	return false
}

func encode(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
func decode(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}
