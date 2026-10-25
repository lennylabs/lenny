// SPDX-License-Identifier: MIT

// Package jwt signs and verifies the §10.2 JWTs Lenny issues. Every
// Lenny-issued token is ES256-signed through the KMS provider by
// RemoteSigner and verified by the static KeySet loaded at startup. The
// package also holds the HMAC signer behind the dev-only embedded OIDC
// trust hook, the claim checker, and the multi-member verifier. It
// handles the §10.2 claim structure (typ, tenant_id, sub, exp, iat) and
// the RFC 7519 whole-second exp requirement, and it depends on no
// third-party JWT library.
package jwt

import (
	"context"
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

// §13.3 timing tolerances. ClockDriftTolerance is the maximum
// pairwise NTP drift the spec permits between gateway replicas;
// the §16.1 lenny_time_drift_seconds metric alerts when observed
// drift exceeds this value. JWTSkewAllowance is the symmetric
// ±N-second fudge Verify applies when comparing exp against the
// local clock; it must be larger than ClockDriftTolerance so a
// token whose exp lands on a clock boundary is never observed
// inconsistently across replicas.
const (
	ClockDriftTolerance = 500 * time.Millisecond
	JWTSkewAllowance    = 1 * time.Second
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

	// Roles carries the §10.2 RBAC role claim. Values are the closed
	// auth.Role enum (`platform-admin`, `tenant-admin`, `tenant-viewer`,
	// `billing-viewer`, `user`). The role claim is admission-time only;
	// the gateway re-evaluates per-endpoint role requirements every
	// request and does not persist roles in the session row.
	Roles []auth.Role `json:"roles,omitempty"`

	// Groups carries the §10.6 OIDC group claim. Environment membership
	// keys on group identity: an environment member of type oidc-group
	// matches a caller whose token carries that group. Like the role
	// claim, the group claim is admission-time only.
	Groups []string `json:"groups,omitempty"`

	// AuthorizedTools carries the §13.3 narrowed tool allowlist
	// for operability-scope tokens (§25.1). A token exchange preserves or
	// further narrows it (broadening is rejected by the §13.3 validator),
	// so the claim survives the exchange path rather than being dropped.
	AuthorizedTools []string `json:"authorized_tools,omitempty"`

	// Origin carries the §27.3 token-origin claim. It is set to
	// "playground" on every session-capability JWT minted for a
	// /playground/* request, in all three playground auth modes. The
	// claim — not the auth mode — is the authoritative signal that
	// drives the §27.6 idle-timeout override and duration cap, the
	// §27.8 origin=playground dashboard slice, and any §11 policy rule
	// matching on it. It is empty on every non-playground token.
	Origin string `json:"origin,omitempty"`

	// Extras captures every payload claim verbatim so callers can read
	// claims under a deployment-configurable name (the §10.2
	// `auth.tenantIdClaim` Helm value defaults to `tenant_id` but maps
	// to a different OIDC claim in many IdPs). Verify populates this
	// from the raw JWT payload; Sign leaves it nil. Use ClaimString to
	// resolve a string claim by name across the union of the typed
	// fields above and Extras.
	// spec: §10.2. F-10.2.9.
	Extras map[string]json.RawMessage `json:"-"`
}

// ClaimString returns the string value of the JWT payload claim named
// name. Standard typed fields take precedence over the raw Extras map
// so `tenant_id` resolves identically regardless of which path
// populated the value. Returns "" when the claim is absent or non-
// string. spec: §10.2. F-10.2.9.
func (c Claims) ClaimString(name string) string {
	switch name {
	case "tenant_id":
		if c.TenantID != "" {
			return c.TenantID
		}
	case "sub":
		if c.Subject != "" {
			return c.Subject
		}
	case "iss":
		if c.Issuer != "" {
			return c.Issuer
		}
	case "jti":
		if c.JWTID != "" {
			return c.JWTID
		}
	case "session_id":
		if c.SessionID != "" {
			return c.SessionID
		}
	case "caller_type":
		if c.CallerType != "" {
			return c.CallerType
		}
	case "scope":
		if c.Scope != "" {
			return c.Scope
		}
	case "origin":
		if c.Origin != "" {
			return c.Origin
		}
	}
	if c.Extras == nil {
		return ""
	}
	raw, ok := c.Extras[name]
	if !ok || len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}

// HasRole reports whether c carries r in its Roles slice. Useful for
// admin-gated endpoints that lift a precondition for callers holding a
// specific role (e.g., the §7.1 derive `allowIsolationDowngrade`
// override requires `platform-admin`).
func (c Claims) HasRole(r auth.Role) bool {
	for _, q := range c.Roles {
		if q == r {
			return true
		}
	}
	return false
}

// ExpiryTime returns the Claims expiry as a time.Time. Zero when
// Expiry is unset.
func (c Claims) ExpiryTime() time.Time {
	if c.Expiry == 0 {
		return time.Time{}
	}
	return time.Unix(c.Expiry, 0).UTC()
}

// Signer signs Claims into a compact JWT string. Sign takes the
// caller's request context because the production signer sends the
// signing input's digest to the KMS Sign operation, which is a remote
// call bounded by that context.
//
// spec: §10.2 (Authentication).
type Signer interface {
	Sign(ctx context.Context, c Claims) (string, error)
	KeyID() string
}

// Verifier validates and decodes a compact JWT string into Claims.
type Verifier interface {
	Verify(token string) (Claims, error)
}

// HMACSigner is the HMAC-SHA256 signer and verifier behind the dev-only
// embedded OIDC trust hook and the tests that stand in for an external
// IdP. Its Sign takes no context, so it does not satisfy Signer: no
// Lenny-issued token is HMAC-signed (§10.2 "Development"), and no
// production wiring can pass it where a Signer is expected. It is safe
// for concurrent use; the secret is read-only after construction.
type HMACSigner struct {
	secret []byte
	keyID  string
}

// NewHMACSigner returns an HMAC signer and verifier backed by secret. The
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
	return decodeClaims(parts[1], time.Now())
}

// decodeClaims decodes the base64url payload segment of a token whose
// signature the caller has already checked, and applies the claim
// checks every verifier in this package shares: the exp and nbf bounds
// under the §13.3 skew allowance.
func decodeClaims(payloadB64 string, now time.Time) (Claims, error) {
	payload, err := decode(payloadB64)
	if err != nil {
		return Claims{}, &VerifyError{Reason: "malformed", Detail: "payload is not base64url"}
	}
	var claims Claims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return Claims{}, &VerifyError{Reason: "malformed", Detail: fmt.Sprintf("payload not JSON: %v", err)}
	}
	// spec: §10.2. Capture every payload claim verbatim so the
	// auth middleware can read tenant under a deployment-configurable
	// claim name (the `auth.tenantIdClaim` Helm value defaults to
	// `tenant_id`). The typed Claims fields above stay authoritative for
	// `tenant_id`; Extras is the fallback for non-default names.
	// F-10.2.9.
	extras := map[string]json.RawMessage{}
	if err := json.Unmarshal(payload, &extras); err == nil {
		claims.Extras = extras
	}
	if err := checkTimeClaims(claims, now.Unix()); err != nil {
		return Claims{}, err
	}
	return claims, nil
}

// checkTimeClaims rejects an expired or not-yet-valid token under the
// ±JWTSkewAllowance window.
func checkTimeClaims(claims Claims, now int64) error {
	skew := int64(JWTSkewAllowance / time.Second)
	// §13.3 ±JWTSkewAllowance skew allowance.
	if claims.Expiry > 0 && claims.Expiry+skew < now {
		return &VerifyError{Reason: "expired"}
	}
	// spec: §10.2 — the standard auth chain validates nbf
	// alongside signature/exp. A token whose not-before lies in the
	// future (beyond the ±skew window) is rejected as not-yet-valid.
	if claims.NotBefore > 0 && claims.NotBefore-skew > now {
		return &VerifyError{Reason: "not_yet_valid"}
	}
	return nil
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
