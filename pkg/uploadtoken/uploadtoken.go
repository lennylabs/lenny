// SPDX-License-Identifier: MIT

// Package uploadtoken implements the §7.1 `uploadToken` HMAC-SHA256
// scheme used by the §15.1 `POST /v1/sessions/{id}/upload` and
// `POST /v1/sessions/{id}/upload-archive` endpoints.
//
// Token wire format: `<session_id>.<expiry_unix_seconds>.<hmac_hex>`
// where the HMAC covers `session_id || "." || expiry_unix_seconds`
// under a gateway-held signing key.
//
// Spec invariants this package enforces:
//
//   - TTL — the token carries an absolute expiry; Verify rejects
//     expired tokens with ErrExpired (mapped to
//     `401 UPLOAD_TOKEN_EXPIRED`).
//   - Session scope — the embedded `session_id` must equal the
//     session being uploaded to; mismatches return ErrSessionMismatch
//     (`403 UPLOAD_TOKEN_MISMATCH`).
//   - Single-use invalidation — the production gateway records the
//     invalidation on the session row and checks it in the session
//     core, so every replica sees it. A Verifier built with a
//     ConsumedTracker (the in-memory tracker of the tests) checks the
//     tracker instead: Consume marks a token's digest consumed, and a
//     later Verify against that digest returns ErrConsumed
//     (`410 UPLOAD_TOKEN_CONSUMED`).
//   - Cryptographic protection — HMAC-SHA256 over the canonical
//     signing input; signature mismatches return ErrInvalid
//     (`401 UPLOAD_TOKEN_INVALID`).
//   - Key rotation — the Issuer, Verifier, and Redactor read their
//     keys through the Keys interface. The gateway passes the shared
//     ring of pkg/signingkeys (NewSharedKeys), whose keys every replica
//     holds and whose rotation and overlap sweep run across replicas;
//     the static KeyRing serves the tests.
//
// **Secret-credential handling — gateway side.** Per §7.1 the
// uploadToken is a secret credential: clients MUST NOT log it, embed
// it in URLs, or include it in client-side error reports. On the
// gateway side this package is the sole holder of the plaintext
// signing keys and the only producer of token strings. The gateway:
//
//   - Returns the token in the response body of `POST /v1/sessions`
//     and `POST /v1/sessions/start` only; never in a URL, redirect
//     header, or log line.
//   - Persists only the SHA-256 digest of the token on the session
//     row (`uploadTokenDigest`), never the token itself; the digest
//     is opaque and cannot be replayed.
//   - Stores the consumed-tracker keyed by digest so the §11.7 audit
//     log and any operator-side observability surface never see the
//     raw token.
//
// Callers in the gateway (`pkg/gateway/uploadhandler/upload.go` and
// friends) preserve this boundary: a verify failure logs the digest
// and the §15.1 error code, never the raw token bytes.
//
// The package holds no store of its own: the shared keys come from
// pkg/signingkeys, and the consumed state lives on the session row.
package uploadtoken

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Spec-defined defaults.
const (
	// DefaultTTL is the §7.1 default token lifetime: 300 s, matching
	// `maxCreatedStateTimeoutSeconds`.
	DefaultTTL = 5 * time.Minute

	// DefaultRotationInterval is the §7.1 default signing-key
	// rotation cadence: 24 hours.
	DefaultRotationInterval = 24 * time.Hour

	// DefaultOverlapWindow is the §7.1 default overlap window during
	// which the previous signing key remains valid for verification.
	DefaultOverlapWindow = 5 * time.Minute
)

// Sentinel errors. The gateway maps each to its §15.1 / §7.1 error
// code.
var (
	// ErrInvalid corresponds to `401 UPLOAD_TOKEN_INVALID` — malformed
	// token or HMAC mismatch.
	ErrInvalid = errors.New("uploadtoken: invalid token")

	// ErrExpired corresponds to `401 UPLOAD_TOKEN_EXPIRED`.
	ErrExpired = errors.New("uploadtoken: token expired")

	// ErrSessionMismatch corresponds to `403 UPLOAD_TOKEN_MISMATCH`.
	ErrSessionMismatch = errors.New("uploadtoken: session-id mismatch")

	// ErrConsumed corresponds to `410 UPLOAD_TOKEN_CONSUMED`.
	ErrConsumed = errors.New("uploadtoken: token already consumed")

	// ErrSigningUnavailable is returned by the Issuer when its keys refuse
	// to sign: the replica's shared ring has not been refreshed within
	// the overlap window, or holds no active key. Session creation maps
	// it to `503 SESSION_CREATION_FAILED`.
	ErrSigningUnavailable = errors.New("uploadtoken: no signing key may be used")
)

// Keys is the signing-key source of an Issuer, a Verifier, and a
// Redactor. Implementations are safe for concurrent use.
type Keys interface {
	// SigningKey returns the key a new token is signed with. An error
	// wraps ErrSigningUnavailable.
	SigningKey() (SigningKey, error)

	// VerificationKeys returns every key a token may verify under, most
	// recent first.
	VerificationKeys() []SigningKey
}

// SigningKey is one key in the KeyRing. KeyID identifies the key for
// observability + rotation auditing; Secret carries the HMAC secret
// bytes. The secret is never returned to callers and is included in
// the canonical signing input only.
type SigningKey struct {
	KeyID  string
	Secret []byte
}

// KeyRing is a static, in-process Keys: the active signing key plus zero
// or more previously-active keys the caller rotated out. It serves the
// tests and a session server built without the gateway's shared ring.
// The zero KeyRing is not usable; construct with NewKeyRing.
type KeyRing struct {
	mu      sync.RWMutex
	active  SigningKey
	overlap []SigningKey
}

// NewKeyRing returns a KeyRing with `active` as the signing key and
// no overlap keys. Subsequent Rotate calls move `active` into the
// overlap slice and install the new key as active.
func NewKeyRing(active SigningKey) *KeyRing {
	return &KeyRing{active: active}
}

// Active returns the current signing key (a copy of the SigningKey
// struct; the underlying secret bytes are shared).
func (r *KeyRing) Active() SigningKey {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.active
}

// Rotate installs `next` as the new active key and pushes the
// previous active key onto the overlap slice. The overlap key is
// removed automatically when Expire is called with a time after
// its "valid until" instant — managed by the caller (typically a
// 24-hour rotation timer paired with a 5-minute overlap sweep).
func (r *KeyRing) Rotate(next SigningKey) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.overlap = append(r.overlap, r.active)
	r.active = next
}

// Expire removes any overlap keys whose `KeyID` appears in the
// `expiredKeyIDs` set. Use this in the overlap-window sweep
// goroutine.
func (r *KeyRing) Expire(expiredKeyIDs ...string) int {
	if len(expiredKeyIDs) == 0 {
		return 0
	}
	expired := make(map[string]struct{}, len(expiredKeyIDs))
	for _, id := range expiredKeyIDs {
		expired[id] = struct{}{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	kept := r.overlap[:0]
	dropped := 0
	for _, k := range r.overlap {
		if _, drop := expired[k.KeyID]; drop {
			dropped++
			continue
		}
		kept = append(kept, k)
	}
	r.overlap = kept
	return dropped
}

// SigningKey implements Keys: the active key. A static ring always signs.
func (r *KeyRing) SigningKey() (SigningKey, error) { return r.Active(), nil }

// VerificationKeys implements Keys: the active key plus every overlap
// key, most recent first.
func (r *KeyRing) VerificationKeys() []SigningKey {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]SigningKey, 0, 1+len(r.overlap))
	out = append(out, r.active)
	for i := len(r.overlap) - 1; i >= 0; i-- {
		out = append(out, r.overlap[i])
	}
	return out
}

// ConsumedTracker records which tokens have been single-use
// invalidated. The production gateway builds its Verifier without one,
// because the consumed state lives on the session row; the in-memory
// tracker serves the tests. Implementations must be goroutine-safe.
type ConsumedTracker interface {
	// MarkConsumed records the supplied digest as consumed. Returns
	// nil if the digest was newly marked, ErrConsumed if the digest
	// was already marked.
	MarkConsumed(digest string, expiry time.Time) error

	// IsConsumed reports whether the digest is already marked
	// consumed.
	IsConsumed(digest string) bool
}

// NewMemoryTracker returns an in-memory ConsumedTracker suitable for
// tests and the minimal gateway. Entries are purged on demand by the
// caller via Sweep; nothing is GC'd otherwise.
func NewMemoryTracker() *MemoryTracker { return &MemoryTracker{m: map[string]time.Time{}} }

// MemoryTracker is the in-memory ConsumedTracker.
type MemoryTracker struct {
	mu sync.Mutex
	m  map[string]time.Time
}

// MarkConsumed implements ConsumedTracker.
func (t *MemoryTracker) MarkConsumed(digest string, expiry time.Time) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.m[digest]; ok {
		return ErrConsumed
	}
	t.m[digest] = expiry
	return nil
}

// IsConsumed implements ConsumedTracker.
func (t *MemoryTracker) IsConsumed(digest string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, ok := t.m[digest]
	return ok
}

// Sweep deletes every consumed entry whose expiry is at or before
// `now`. Returns the number of entries dropped. Callers run this
// periodically to bound memory.
func (t *MemoryTracker) Sweep(now time.Time) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	dropped := 0
	for k, exp := range t.m {
		if !exp.After(now) {
			delete(t.m, k)
			dropped++
		}
	}
	return dropped
}

// Issuer mints uploadTokens. The zero value is not usable; construct
// with NewIssuer.
type Issuer struct {
	keys  Keys
	clock func() time.Time
}

// NewIssuer returns an Issuer that signs with the signing key of keys.
// The clock is used for the issuance "now" stamp; pass nil to default to
// time.Now.
func NewIssuer(keys Keys, clock func() time.Time) *Issuer {
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &Issuer{keys: keys, clock: clock}
}

// Issue mints a new uploadToken for sessionID with the supplied TTL.
// When ttl is zero, DefaultTTL is used.
func (i *Issuer) Issue(sessionID string, ttl time.Duration) (string, error) {
	tok, _, err := i.IssueDetailed(sessionID, ttl)
	return tok, err
}

// IssueDetailed mints a new uploadToken and additionally returns the
// parsed form so the caller can persist the digest + expiry alongside
// the session row. The minted token is identical to what Issue
// returns; the parsed form is computed in-place.
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
	active, err := i.keys.SigningKey()
	if err != nil {
		return "", ParsedToken{}, err
	}
	now := i.clock()
	expiry := now.Add(ttl).Unix()
	signed := sign(active, sessionID, expiry)
	parsed := ParsedToken{
		SessionID: sessionID,
		Expiry:    time.Unix(expiry, 0).UTC(),
		Digest:    digestToken(signed),
		KeyID:     active.KeyID,
	}
	return signed, parsed, nil
}

// Verify validates the token bytes against the supplied sessionID
// and the current clock. Returns the parsed components on success,
// or one of ErrInvalid / ErrExpired / ErrSessionMismatch /
// ErrConsumed.
type ParsedToken struct {
	SessionID string
	Expiry    time.Time
	Digest    string // canonical sha256 digest of the full token, used as the consumed-tracker key
	KeyID     string // key that validated the token
}

// Verifier verifies uploadTokens against a KeyRing and a clock.
type Verifier struct {
	keys    Keys
	tracker ConsumedTracker
	clock   func() time.Time
}

// NewVerifier returns a Verifier over keys. A non-nil ConsumedTracker
// makes Verify check single use against it. The gateway passes nil,
// because the session core checks the consumed state on the session row
// after Verify succeeds.
func NewVerifier(keys Keys, tracker ConsumedTracker, clock func() time.Time) *Verifier {
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &Verifier{keys: keys, tracker: tracker, clock: clock}
}

// Verify checks token against sessionID and the current clock.
func (v *Verifier) Verify(token, sessionID string) (ParsedToken, error) {
	parsed, key, err := parseAndVerifyHMAC(token, v.keys.VerificationKeys())
	if err != nil {
		return ParsedToken{}, err
	}
	if parsed.SessionID != sessionID {
		return parsed, ErrSessionMismatch
	}
	if v.clock().After(parsed.Expiry) {
		return parsed, ErrExpired
	}
	if v.tracker != nil && v.tracker.IsConsumed(parsed.Digest) {
		return parsed, ErrConsumed
	}
	parsed.KeyID = key.KeyID
	return parsed, nil
}

// Consume marks the token's digest as consumed in the tracker (if
// one is wired). Returns ErrConsumed if the token was already
// consumed.
func (v *Verifier) Consume(parsed ParsedToken) error {
	if v.tracker == nil {
		return nil
	}
	return v.tracker.MarkConsumed(parsed.Digest, parsed.Expiry)
}

// ConsumeDigest marks the supplied digest as consumed without
// requiring a Verify call first. Useful for callers that persisted
// the digest at issuance time (e.g., the session row) and need to
// invalidate a token from a context where the original token string
// is no longer available.
func (v *Verifier) ConsumeDigest(digest string, expiry time.Time) error {
	if v.tracker == nil {
		return nil
	}
	return v.tracker.MarkConsumed(digest, expiry)
}

// parseAndVerifyHMAC tries every key of keys, a snapshot of a ring with the
// active key first, and returns the parsed token + matching key on success.
// Only the canonical spelling the Issuer writes verifies: a decimal expiry
// with no sign or leading zero, and lowercase hex. Another spelling of the
// same HMAC would carry another digest, so it would escape the consumed
// tracker, which is keyed by digest, and the idempotency redactor, which
// matches the canonical form.
//
// spec: §7.1 (Normal Flow)
func parseAndVerifyHMAC(token string, keys []SigningKey) (ParsedToken, SigningKey, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return ParsedToken{}, SigningKey{}, ErrInvalid
	}
	expiryUnix, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return ParsedToken{}, SigningKey{}, ErrInvalid
	}
	sigBytes, err := hex.DecodeString(parts[2])
	if err != nil {
		return ParsedToken{}, SigningKey{}, ErrInvalid
	}
	if strconv.FormatInt(expiryUnix, 10) != parts[1] || hex.EncodeToString(sigBytes) != parts[2] {
		return ParsedToken{}, SigningKey{}, ErrInvalid
	}
	for _, k := range keys {
		want := computeHMAC(k.Secret, parts[0], expiryUnix)
		if hmac.Equal(want, sigBytes) {
			return ParsedToken{
				SessionID: parts[0],
				Expiry:    time.Unix(expiryUnix, 0).UTC(),
				Digest:    digestToken(token),
			}, k, nil
		}
	}
	return ParsedToken{}, SigningKey{}, ErrInvalid
}

// sign produces the §7.1 wire format.
func sign(key SigningKey, sessionID string, expiry int64) string {
	mac := computeHMAC(key.Secret, sessionID, expiry)
	return fmt.Sprintf("%s.%d.%s", sessionID, expiry, hex.EncodeToString(mac))
}

// computeHMAC returns the HMAC-SHA256 over the §7.1 canonical
// signing input `session_id || "." || expiry`.
func computeHMAC(secret []byte, sessionID string, expiry int64) []byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(sessionID))
	mac.Write([]byte("."))
	mac.Write([]byte(strconv.FormatInt(expiry, 10)))
	return mac.Sum(nil)
}

// digestToken returns the SHA-256 hex digest of the full token wire
// bytes — used as the single-use tracker key so the tracker never
// stores raw HMAC signatures.
func digestToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
