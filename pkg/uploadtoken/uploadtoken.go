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
//   - Single-use invalidation — calls to Consume mark a token's
//     digest as consumed; subsequent Verify against that digest
//     returns ErrConsumed (`410 UPLOAD_TOKEN_CONSUMED`).
//   - Cryptographic protection — HMAC-SHA256 over the canonical
//     signing input; signature mismatches return ErrInvalid
//     (`401 UPLOAD_TOKEN_INVALID`).
//   - Key rotation — the KeyRing carries one signing key plus zero or
//     more verification keys still inside the §7.1 overlap window
//     (default 5 minutes). Verify accepts any key in the ring. The
//     gateway's Rotator (see rotator.go) drives the 24h rotation
//     cadence and the overlap-window sweep.
//
// **Secret-credential handling — gateway side.** Per §7.1 line 63 the
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
// Callers in the gateway (`pkg/gateway/sessionserver/upload.go` and
// friends) preserve this boundary: a verify failure logs the digest
// and the §15.1 error code, never the raw token bytes.
//
// The package is pure: no Redis, no Postgres. The
// `ConsumedTracker` interface lets production swap in a Redis-backed
// implementation; the in-memory `NewMemoryTracker()` is sufficient
// for tests and the minimal gateway.
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
)

// SigningKey is one key in the KeyRing. KeyID identifies the key for
// observability + rotation auditing; Secret carries the HMAC secret
// bytes. The secret is never returned to callers and is included in
// the canonical signing input only.
type SigningKey struct {
	KeyID  string
	Secret []byte
}

// KeyRing holds the active signing key plus zero or more
// previously-active keys still inside the §7.1 overlap window. The
// zero KeyRing is not usable; construct with NewKeyRing.
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

// keys returns the active key plus every overlap key, in
// most-recent-first order. Used by Verify to try every candidate.
func (r *KeyRing) keys() []SigningKey {
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
// invalidated per §7.1. Production wires this to Redis; the
// in-memory implementation is sufficient for tests and the minimal
// gateway. Implementations must be goroutine-safe.
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
	ring  *KeyRing
	clock func() time.Time
}

// NewIssuer returns an Issuer that signs with ring's active key. The
// clock is used for the issuance "now" stamp; pass nil to default to
// time.Now.
func NewIssuer(ring *KeyRing, clock func() time.Time) *Issuer {
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &Issuer{ring: ring, clock: clock}
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
	now := i.clock()
	expiry := now.Add(ttl).Unix()
	active := i.ring.Active()
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
	ring    *KeyRing
	tracker ConsumedTracker
	clock   func() time.Time
}

// NewVerifier returns a Verifier. Pass a non-nil ConsumedTracker to
// enable single-use enforcement; pass nil to disable single-use
// enforcement entirely (the gateway only does so for unit tests).
func NewVerifier(ring *KeyRing, tracker ConsumedTracker, clock func() time.Time) *Verifier {
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &Verifier{ring: ring, tracker: tracker, clock: clock}
}

// Verify checks token against sessionID and the current clock.
func (v *Verifier) Verify(token, sessionID string) (ParsedToken, error) {
	parsed, key, err := parseAndVerifyHMAC(token, v.ring)
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

// parseAndVerifyHMAC tries every key in ring (active first, then
// overlap) and returns the parsed token + matching key on success.
func parseAndVerifyHMAC(token string, ring *KeyRing) (ParsedToken, SigningKey, error) {
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
	for _, k := range ring.keys() {
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
