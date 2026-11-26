// SPDX-License-Identifier: MIT

package uploadtoken

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strconv"
	"strings"
)

// redactionInfix separates a redaction reference's session id and expiry
// from its mark. A reference has four dot-separated fields, so the token
// parser refuses it as ErrInvalid.
const redactionInfix = ".redacted."

// redactionDomain prefixes the input of a reference's mark. The token's own
// signing input is `session_id || "." || expiry`, and a session id never
// carries a NUL byte, so no mark input is a token's signing input.
const redactionDomain = "lenny/uploadtoken/idempotency-redaction\x00"

// unmatchedMark is the mark of a reference built from a token-shaped value
// that verified under no key of the ring. No key reproduces it, so Remint
// leaves the reference in place.
var unmatchedMark = strings.Repeat("0", sha256.Size*2)

// tokenPattern matches the §7.1 token wire format inside an arbitrary byte
// string. Session ids are UUIDs, so their characters are letters, digits,
// hyphens, and underscores, none of which JSON escapes; the pattern
// therefore matches a token at any nesting depth of JSON-encoded text.
var tokenPattern = regexp.MustCompile(`([A-Za-z0-9_-]+)\.([0-9]{1,19})\.([0-9a-f]{64})`)

// referencePattern matches a redaction reference written by Strip.
var referencePattern = regexp.MustCompile(`([A-Za-z0-9_-]+)\.([0-9]{1,19})\.redacted\.([0-9a-f]{64})`)

// Redactor removes upload tokens from a response before the gateway stores
// it, and restores them when the stored response is replayed. It reads the
// keys the gateway's Issuer and Verifier share, so it sees rotations as
// they happen, and with the shared ring every replica can restore a token
// any replica signed. A Redactor is safe for concurrent use.
type Redactor struct {
	keys Keys
}

// NewRedactor returns a Redactor over keys.
func NewRedactor(keys Keys) *Redactor {
	return &Redactor{keys: keys}
}

// Strip returns body with every upload token replaced by a redaction
// reference, `<session_id>.<expiry>.redacted.<mark>`. A token that verifies
// under a key of the ring gets a mark only that key reproduces. Any other
// value with the token's format gets a mark no key reproduces, so the store
// keeps no token-shaped value it cannot account for. Strip does not check
// expiry or consumption: an expired or consumed token is still removed. A
// Redactor with no ring fails closed: it removes every token-shaped value
// and accounts for none.
//
// A reference already in body did not come from this call: it came from
// the request, such as a reference copied out of a stored record into a
// session's env, which the create response echoes. Strip gives each such
// reference the mark no key reproduces before it strips the tokens, so
// only the references this call writes can be reminted.
//
// spec: §11.5 (Idempotency), §7.1 (Normal Flow)
func (r *Redactor) Strip(body []byte) []byte {
	var keys []SigningKey
	if r != nil && r.keys != nil {
		keys = r.keys.VerificationKeys()
	}
	if bytes.Contains(body, []byte(redactionInfix)) {
		body = referencePattern.ReplaceAllFunc(body, func(match []byte) []byte {
			sub := referencePattern.FindSubmatch(match)
			return []byte(string(sub[1]) + "." + string(sub[2]) + redactionInfix + unmatchedMark)
		})
	}
	return tokenPattern.ReplaceAllFunc(body, func(match []byte) []byte {
		token := string(match)
		sub := tokenPattern.FindStringSubmatch(token)
		mark := unmatchedMark
		if _, key, err := parseAndVerifyHMAC(token, keys); err == nil {
			mark = redactionMark(key, token)
		}
		return []byte(sub[1] + "." + sub[2] + redactionInfix + mark)
	})
}

// Remint returns stored with every redaction reference that a key of the
// ring accounts for replaced by the token that key signs for the
// reference's session id and expiry. Signing is deterministic, so the
// restored token is the token Strip removed. A reference no key accounts
// for stays in place: the key that signed its token has left the ring, so
// that token would no longer verify either, or the reference did not come
// from a token at all.
//
// spec: §11.5 (Idempotency), §7.1 (Normal Flow)
func (r *Redactor) Remint(stored []byte) []byte {
	// A body with no reference, the common case on replay, costs one scan
	// for the infix rather than a regular-expression pass.
	if r == nil || r.keys == nil || !bytes.Contains(stored, []byte(redactionInfix)) {
		return stored
	}
	keys := r.keys.VerificationKeys()
	return referencePattern.ReplaceAllFunc(stored, func(match []byte) []byte {
		sub := referencePattern.FindSubmatch(match)
		sessionID, expiryText := string(sub[1]), string(sub[2])
		expiry, err := strconv.ParseInt(expiryText, 10, 64)
		if err != nil || strconv.FormatInt(expiry, 10) != expiryText {
			return match
		}
		want, err := hex.DecodeString(string(sub[3]))
		if err != nil {
			return match
		}
		for _, key := range keys {
			token := sign(key, sessionID, expiry)
			got, _ := hex.DecodeString(redactionMark(key, token))
			if hmac.Equal(got, want) {
				return []byte(token)
			}
		}
		return match
	})
}

// redactionMark is the hex HMAC-SHA256, under key, of the domain prefix
// followed by the token. It identifies the token to the key that signed it
// without revealing the token.
func redactionMark(key SigningKey, token string) string {
	mac := hmac.New(sha256.New, key.Secret)
	mac.Write([]byte(redactionDomain))
	mac.Write([]byte(token))
	return hex.EncodeToString(mac.Sum(nil))
}
