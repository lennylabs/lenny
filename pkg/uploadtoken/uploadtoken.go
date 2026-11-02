// SPDX-License-Identifier: MIT

// Package uploadtoken implements the §7.1 `uploadToken` used by the
// §15.1 `POST /v1/sessions/{id}/upload`,
// `POST /v1/sessions/{id}/upload-archive`, and
// `POST /v1/sessions/{id}/finalize` endpoints.
//
// Token wire format: `<session_id>.<expiry_unix_seconds>.<secret_hex>`,
// where the third segment is 256 random bits in hexadecimal.
//
// A token authenticates by digest against the session row. The issuer
// draws the secret and returns the SHA-256 digest of the whole token,
// the gateway persists that digest and the expiry on the row, and every
// replica verifies a presented token against the row. No key exists, so
// a token minted on one replica verifies on any other and across a
// restart. The digest covers the embedded expiry, so a client that edits
// the second segment fails the digest check.
//
// Verify makes these checks, in this order:
//
//   - Format: three non-empty segments, an integer second segment, and
//     a hexadecimal third segment. A malformed token returns ErrInvalid
//     (`401 UPLOAD_TOKEN_INVALID`).
//   - Session scope: the embedded `session_id` must equal the session
//     row's id. A mismatch returns ErrSessionMismatch
//     (`403 UPLOAD_TOKEN_MISMATCH`).
//   - Digest: the token's digest must equal the digest on the row in
//     constant time. A mismatch, or a row with no digest, returns
//     ErrInvalid (`401 UPLOAD_TOKEN_INVALID`).
//   - TTL: the row's expiry must not have passed. Otherwise Verify
//     returns ErrExpired (`401 UPLOAD_TOKEN_EXPIRED`). The token's own
//     second segment is not read for enforcement.
//
// Consumption is derived from the session state by the caller through
// `session.UploadTokenConsumed`, so this package holds no record of it.
// The caller maps a consumed row to ErrConsumed
// (`410 UPLOAD_TOKEN_CONSUMED`).
//
// **Secret-credential handling — gateway side.** Per §7.1 the
// uploadToken is a secret credential: clients MUST NOT log it, embed
// it in URLs, or include it in client-side error reports. On the
// gateway side this package is the only producer of token strings. The
// gateway:
//
//   - Returns the token in the response body of `POST /v1/sessions`
//     and `POST /v1/sessions/start` only; never in a URL, redirect
//     header, or log line.
//   - Persists only the SHA-256 digest of the token on the session
//     row (`uploadTokenDigest`), never the token itself; the digest
//     is opaque and cannot be replayed.
//
// Callers in the gateway (`pkg/gateway/sessionserver/upload.go` and
// friends) preserve this boundary: a verify failure logs the digest
// and the §15.1 error code, never the raw token bytes.
//
// The package is pure: no Redis, no Postgres. The caller reads the
// session row and passes its digest and expiry to Verify.
package uploadtoken

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Spec-defined defaults.
const (
	// DefaultTTL is the §7.1 default token lifetime: 300 s, matching
	// `maxCreatedStateTimeoutSeconds`.
	DefaultTTL = 5 * time.Minute
)

// Sentinel errors. The gateway maps each to its §15.1 / §7.1 error
// code.
var (
	// ErrInvalid corresponds to `401 UPLOAD_TOKEN_INVALID`: a missing or
	// malformed token, a digest that differs from the session row's, or
	// a session row with no digest.
	ErrInvalid = errors.New("uploadtoken: invalid token")

	// ErrExpired corresponds to `401 UPLOAD_TOKEN_EXPIRED`.
	ErrExpired = errors.New("uploadtoken: token expired")

	// ErrSessionMismatch corresponds to `403 UPLOAD_TOKEN_MISMATCH`.
	ErrSessionMismatch = errors.New("uploadtoken: session-id mismatch")

	// ErrConsumed corresponds to `410 UPLOAD_TOKEN_CONSUMED`. Verify does
	// not return it: the session server returns it from the row state.
	ErrConsumed = errors.New("uploadtoken: token already consumed")
)

// secretBytes is the §7.1 size of the token's random third segment:
// 256 bits.
const secretBytes = 32

// Issuer mints uploadTokens. The zero value is not usable; construct
// with NewIssuer.
type Issuer struct {
	clock func() time.Time
}

// NewIssuer returns an Issuer. The clock is used for the issuance "now"
// stamp; pass nil to default to time.Now.
func NewIssuer(clock func() time.Time) *Issuer {
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &Issuer{clock: clock}
}

// Issue mints a new uploadToken for sessionID with the supplied TTL.
// When ttl is zero, DefaultTTL is used. A caller that persists the
// session row uses IssueDetailed, because a token authenticates only
// against the digest on the row.
func (i *Issuer) Issue(sessionID string, ttl time.Duration) (string, error) {
	tok, _, err := i.IssueDetailed(sessionID, ttl)
	return tok, err
}

// IssueDetailed mints a new uploadToken and additionally returns the
// parsed form so the caller can persist the digest + expiry alongside
// the session row. Each call draws a fresh secret, so two calls for one
// session return different tokens.
// spec: §7.1 (uploadToken format and security properties)
func (i *Issuer) IssueDetailed(sessionID string, ttl time.Duration) (string, ParsedToken, error) {
	if sessionID == "" {
		return "", ParsedToken{}, fmt.Errorf("uploadtoken: empty session id")
	}
	if strings.ContainsAny(sessionID, ".") {
		// The wire format uses `.` as the field separator, so session
		// ids must not contain dots. The §15.1 session-id pattern is
		// `sess_<hex>` and never contains dots; reject anything else
		// at the issuance boundary.
		return "", ParsedToken{}, fmt.Errorf("uploadtoken: session id %q contains a forbidden \".\"", sessionID)
	}
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	expiry := i.clock().Add(ttl).Unix()
	var secret [secretBytes]byte
	// crypto/rand.Read never returns an error under Go 1.24 and later.
	_, _ = rand.Read(secret[:])
	token := fmt.Sprintf("%s.%d.%s", sessionID, expiry, hex.EncodeToString(secret[:]))
	parsed := ParsedToken{
		SessionID: sessionID,
		Expiry:    time.Unix(expiry, 0).UTC(),
		Digest:    digestToken(token),
	}
	return token, parsed, nil
}

// ParsedToken is the issuance record of a token: the fields the caller
// persists on the session row.
type ParsedToken struct {
	SessionID string
	Expiry    time.Time
	Digest    string // canonical sha256 digest of the full token, stored on the session row
}

// Verifier verifies uploadTokens against a session row and a clock.
type Verifier struct {
	clock func() time.Time
}

// NewVerifier returns a Verifier. Pass a nil clock to default to time.Now.
func NewVerifier(clock func() time.Time) *Verifier {
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &Verifier{clock: clock}
}

// Verify checks token against the session row's id, digest, and expiry. It
// returns ErrInvalid for a malformed token, ErrSessionMismatch for a foreign
// session, ErrInvalid for an empty rowDigest or a digest mismatch, and
// ErrExpired once rowExpiry has passed, checked in that order. A token is
// admitted at the rowExpiry instant.
// spec: §7.1 (uploadToken format and security properties)
func (v *Verifier) Verify(token, sessionID, rowDigest string, rowExpiry time.Time) error {
	embeddedSession, err := parseToken(token)
	if err != nil {
		return err
	}
	if embeddedSession != sessionID {
		return ErrSessionMismatch
	}
	if rowDigest == "" || subtle.ConstantTimeCompare([]byte(digestToken(token)), []byte(rowDigest)) != 1 {
		return ErrInvalid
	}
	if v.clock().After(rowExpiry) {
		return ErrExpired
	}
	return nil
}

// parseToken checks the §7.1 wire format and returns the embedded session id.
func parseToken(token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", ErrInvalid
	}
	if _, err := strconv.ParseInt(parts[1], 10, 64); err != nil {
		return "", ErrInvalid
	}
	if _, err := hex.DecodeString(parts[2]); err != nil {
		return "", ErrInvalid
	}
	return parts[0], nil
}

// digestToken returns the SHA-256 hex digest of the full token wire
// bytes. The session row stores this digest in place of the token.
func digestToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
