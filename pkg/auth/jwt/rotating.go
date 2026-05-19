// SPDX-License-Identifier: MIT

package jwt

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// DefaultOverlapWindow is the §10.3 key-rotation overlap window: how
// long a token signed by a now-previous key version stays acceptable
// after that key is retired. The spec pins the default at 24h, long
// enough that every in-flight short-lived session JWT signed under the
// old key expires on its own before the window closes.
const DefaultOverlapWindow = 24 * time.Hour

// keyedVerifier is a per-key Verifier that also reports the kid it is
// keyed under. Both HMACSigner (dev) and KMSSigner (prod) satisfy it,
// so RotatingVerifier is generic over the verification backend and
// never special-cases HMAC against KMS.
type keyedVerifier interface {
	Verifier
	KeyID() string
}

// keyVersion is one entry in a RotatingVerifier's key set: a per-key
// verifier plus, for retired keys, the instant the key left "current"
// status. retiredAt is the zero time for the current key.
type keyVersion struct {
	verifier  keyedVerifier
	retiredAt time.Time
}

// RotationEvent describes a single §10.3 key-rotation lifecycle
// transition. Rotate emits one promote_current event per call;
// RetireExpired emits one retire_previous event per pruned previous
// key. The struct is the wire format the gateway hands to the audit
// pipeline so the `platform.jwt_signing_key_rotated` audit row
// carries the same fields regardless of the observing call site.
type RotationEvent struct {
	// FromKeyID is the kid leaving the role named by Transition.
	// For promote_current it is the outgoing current key (now in the
	// previous set); for retire_previous it is the pruned previous
	// key. Empty when the transition has no outgoing key (the
	// initial NewRotatingVerifier call does not emit a transition).
	FromKeyID string

	// ToKeyID is the kid taking the role named by Transition. For
	// promote_current it is the incoming current key. For
	// retire_previous it is empty: the previous key is dropped, no
	// successor takes its slot.
	ToKeyID string

	// Transition names the lifecycle step. The set is closed:
	// "promote_current" (Rotate) or "retire_previous"
	// (RetireExpired). Future transitions extend this enum.
	Transition string

	// At is the verifier-clock instant the transition committed.
	At time.Time
}

// Transition enumerates the §10.3 key-rotation lifecycle steps a
// RotationObserver receives. The two values are exported so call
// sites can build a switch without redefining the literals; the
// audit pipeline pins these names verbatim onto the
// `platform.jwt_signing_key_rotated` row.
const (
	// TransitionPromoteCurrent fires from Rotate: the incoming key
	// becomes current and the outgoing current key joins the
	// previous set.
	TransitionPromoteCurrent = "promote_current"

	// TransitionRetirePrevious fires from RetireExpired: a previous
	// key whose §10.3 overlap window has closed is dropped from the
	// key set.
	TransitionRetirePrevious = "retire_previous"
)

// RotationObserver receives one RotationEvent per §10.3 rotation
// lifecycle transition. The gateway-side wiring writes a
// `platform.jwt_signing_key_rotated` audit row per event; tests
// substitute a recording observer. The callback runs synchronously
// on the calling goroutine and MUST NOT block on external IO —
// Rotate and RetireExpired hold the verifier's write lock across
// the callback, so a slow observer stalls every concurrent Verify.
// Returning an error is recorded by the observer (e.g., to log a
// failed audit append); it does not roll back the rotation, which
// has already committed to the key set.
type RotationObserver interface {
	OnRotation(ev RotationEvent)
}

// RotatingVerifier is the §10.3 multi-key JWT verifier. It holds one
// current key version and zero or more previous key versions, each
// identified by its JOSE `kid` header value. On Verify it reads the
// token's `kid`, selects the matching key version, and verifies with
// that key's underlying Verifier.
//
// A token signed by the current key always verifies (subject to the
// usual signature and expiry checks). A token signed by a previous key
// verifies only while now is inside that key's overlap window — that
// is, while now < retiredAt + overlapWindow. Past the overlap window a
// previous-key token is rejected with a *VerifyError whose Reason is
// "key_retired", which lets callers distinguish a structurally valid
// token signed under a wound-down key from a forged or corrupt one.
//
// Rotate advances the key set: it stamps the current key's retiredAt
// and installs a fresh current key. The §10.3 "zero downtime" property
// follows — after Rotate, tokens minted before the rotation keep
// verifying against the now-previous key until the overlap window
// closes, by which point every short-lived session JWT has expired.
//
// RotatingVerifier is safe for concurrent use. The gateway verifies
// pod→gateway tokens concurrently; a sync.RWMutex guards the key set so
// Verify calls run in parallel and only Rotate / RetireExpired take the
// write lock.
type RotatingVerifier struct {
	mu            sync.RWMutex
	current       keyVersion
	previous      map[string]keyVersion
	overlapWindow time.Duration
	now           func() time.Time
	observer      RotationObserver
}

// NewRotatingVerifier returns a RotatingVerifier whose initial current
// key is current, keyed under current.KeyID(). overlapWindow is the
// §10.3 window during which a token signed by a key retired by a later
// Rotate call stays acceptable; pass a value <= 0 to take the 24h
// DefaultOverlapWindow.
//
// current must report a non-empty KeyID: a rotating verifier selects
// keys by `kid`, so an empty-kid key could never be addressed by an
// incoming token and a token minted under it would carry no `kid`
// header at all (see HMACSigner.Sign). NewRotatingVerifier panics on an
// empty KeyID because that is a construction-time programmer error, not
// a per-request condition.
func NewRotatingVerifier(current keyedVerifier, overlapWindow time.Duration) *RotatingVerifier {
	if current == nil {
		panic("jwt: NewRotatingVerifier: nil current key")
	}
	if current.KeyID() == "" {
		panic("jwt: NewRotatingVerifier: current key has an empty kid")
	}
	if overlapWindow <= 0 {
		overlapWindow = DefaultOverlapWindow
	}
	return &RotatingVerifier{
		current:       keyVersion{verifier: current},
		previous:      make(map[string]keyVersion),
		overlapWindow: overlapWindow,
		now:           time.Now,
	}
}

// SetObserver installs (or clears, with nil) the RotationObserver
// that receives a RotationEvent per §10.3 lifecycle transition.
// Setting it from a single goroutine before the verifier sees
// concurrent traffic is the expected usage; a concurrent SetObserver
// is safe but races with in-flight Rotate / RetireExpired callbacks.
func (v *RotatingVerifier) SetObserver(o RotationObserver) {
	v.mu.Lock()
	v.observer = o
	v.mu.Unlock()
}

// Rotate promotes the current key to "previous" status, stamping its
// retiredAt with the current time, and installs next as the new
// current key. Tokens signed under the just-retired key keep verifying
// until that key's overlap window closes; tokens signed under next
// verify immediately.
//
// next must report a non-empty KeyID, and that kid must differ from
// the outgoing current key's kid — reusing a kid across rotations would
// make the retired and current versions indistinguishable during
// verification. Rotate returns a non-nil error on either violation and
// leaves the key set unchanged.
//
// If next's kid collides with an existing previous key (a kid being
// reused after it already rotated out once), the stale previous entry
// is dropped: the incoming key is authoritative for that kid.
//
// On a successful rotation Rotate calls the installed RotationObserver
// (if any) with a TransitionPromoteCurrent event. The observer runs
// under the verifier's write lock and MUST NOT block on external IO.
func (v *RotatingVerifier) Rotate(next keyedVerifier) error {
	if next == nil {
		return &VerifyError{Reason: "rotation_invalid", Detail: "next key is nil"}
	}
	nextKID := next.KeyID()
	if nextKID == "" {
		return &VerifyError{Reason: "rotation_invalid", Detail: "next key has an empty kid"}
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	if nextKID == v.current.verifier.KeyID() {
		return &VerifyError{
			Reason: "rotation_invalid",
			Detail: fmt.Sprintf("next key reuses the current kid %q", nextKID),
		}
	}

	now := v.now()
	retired := v.current
	retired.retiredAt = now
	fromKID := retired.verifier.KeyID()
	v.previous[fromKID] = retired

	// A reused kid: the fresh current key supersedes any same-kid
	// previous entry.
	delete(v.previous, nextKID)
	v.current = keyVersion{verifier: next}

	if v.observer != nil {
		v.observer.OnRotation(RotationEvent{
			FromKeyID:  fromKID,
			ToKeyID:    nextKID,
			Transition: TransitionPromoteCurrent,
			At:         now,
		})
	}
	return nil
}

// RetireExpired drops every previous key whose overlap window has
// closed (now >= retiredAt + overlapWindow) and returns the count
// removed. It is an optional housekeeping call: Verify already rejects
// a token signed by a window-closed key, so an un-pruned key set never
// admits a stale token. Pruning only bounds the previous-key map.
//
// For each pruned key the installed RotationObserver (if any) receives
// one TransitionRetirePrevious event under the verifier's write lock.
// The observer MUST NOT block on external IO; a slow observer stalls
// every concurrent Verify.
func (v *RotatingVerifier) RetireExpired() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	now := v.now()
	removed := 0
	for kid, kv := range v.previous {
		if !now.Before(kv.retiredAt.Add(v.overlapWindow)) {
			delete(v.previous, kid)
			removed++
			if v.observer != nil {
				v.observer.OnRotation(RotationEvent{
					FromKeyID:  kid,
					Transition: TransitionRetirePrevious,
					At:         now,
				})
			}
		}
	}
	return removed
}

// Verify validates token against the key version named by its `kid`
// header and decodes its Claims. It implements the Verifier interface,
// so a RotatingVerifier is a drop-in replacement for a single-key
// Verifier anywhere the gateway validates a JWT.
//
// Failure modes, all returned as *VerifyError:
//   - "malformed": the token is not a three-segment compact JWS, or its
//     header is not base64url-encoded JSON.
//   - "missing_kid": the header carries no `kid`. A rotating verifier
//     holds multiple keys and cannot safely guess which one signed an
//     unkeyed token, so it rejects rather than risk accepting a token
//     under the wrong key. The dev HMACSigner omits `kid` only when its
//     keyID is empty, a configuration that has no place behind a
//     rotating verifier (NewRotatingVerifier rejects an empty-kid key).
//   - "unknown_kid": the `kid` matches neither the current key nor any
//     retained previous key.
//   - "key_retired": the `kid` matches a previous key whose overlap
//     window has closed. The token is structurally intact and its
//     signature would verify, but the key is past its §10.3 rotation
//     window. Detail names the kid.
//   - any *VerifyError the selected per-key Verifier returns
//     (signature_mismatch, expired, malformed payload, ...).
func (v *RotatingVerifier) Verify(token string) (Claims, error) {
	kid, err := kidFromToken(token)
	if err != nil {
		return Claims{}, err
	}

	v.mu.RLock()
	cur := v.current
	prev, isPrev := v.previous[kid]
	overlap := v.overlapWindow
	now := v.now()
	v.mu.RUnlock()

	if kid == cur.verifier.KeyID() {
		return cur.verifier.Verify(token)
	}
	if isPrev {
		if now.Before(prev.retiredAt.Add(overlap)) {
			return prev.verifier.Verify(token)
		}
		return Claims{}, &VerifyError{
			Reason: "key_retired",
			Detail: fmt.Sprintf("kid %q retired at %s, past the %s overlap window",
				kid, prev.retiredAt.UTC().Format(time.RFC3339), overlap),
		}
	}
	return Claims{}, &VerifyError{
		Reason: "unknown_kid",
		Detail: fmt.Sprintf("kid %q matches no current or previous key", kid),
	}
}

// CurrentKeyID returns the `kid` of the key version currently used to
// sign new tokens.
func (v *RotatingVerifier) CurrentKeyID() string {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.current.verifier.KeyID()
}

// PreviousKeyIDs returns the `kid` values of the retained previous key
// versions, in unspecified order. A kid disappears from this slice once
// RetireExpired prunes it after its overlap window closes.
func (v *RotatingVerifier) PreviousKeyIDs() []string {
	v.mu.RLock()
	defer v.mu.RUnlock()
	ids := make([]string, 0, len(v.previous))
	for kid := range v.previous {
		ids = append(ids, kid)
	}
	return ids
}

// joseHeader is the subset of the JOSE header RotatingVerifier needs to
// route a token to a key version. Other header fields are ignored here;
// the selected per-key Verifier re-parses the token in full.
type joseHeader struct {
	KID string `json:"kid"`
}

// kidFromToken extracts the `kid` header value from a compact JWS. It
// returns a *VerifyError with Reason "malformed" when token is not a
// three-segment JWS or its header is not base64url JSON, and Reason
// "missing_kid" when the header is well-formed but carries no `kid`.
func kidFromToken(token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", &VerifyError{
			Reason: "malformed",
			Detail: fmt.Sprintf("token has %d segments, want 3", len(parts)),
		}
	}
	headerJSON, err := decode(parts[0])
	if err != nil {
		return "", &VerifyError{Reason: "malformed", Detail: "header is not base64url"}
	}
	var h joseHeader
	if err := json.Unmarshal(headerJSON, &h); err != nil {
		return "", &VerifyError{
			Reason: "malformed",
			Detail: fmt.Sprintf("header not JSON: %v", err),
		}
	}
	if h.KID == "" {
		return "", &VerifyError{
			Reason: "missing_kid",
			Detail: "JOSE header carries no kid; a rotating verifier cannot select a key",
		}
	}
	return h.KID, nil
}

// IsKeyRetired reports whether err is a *VerifyError with Reason
// "key_retired" — a token signed by a key version whose §10.3 rotation
// overlap window has closed.
func IsKeyRetired(err error) bool {
	var ve *VerifyError
	if errors.As(err, &ve) {
		return ve.Reason == "key_retired"
	}
	return false
}

// Compile-time check: RotatingVerifier satisfies the Verifier
// interface, so it drops in wherever a single-key verifier was used.
var _ Verifier = (*RotatingVerifier)(nil)
