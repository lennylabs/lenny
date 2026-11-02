// SPDX-License-Identifier: MIT

package connectoroauth

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"sync"
	"time"
)

// Spec-defined defaults.
const (
	// DefaultStateTTL is the §9.3 authorization-request `state`
	// lifetime: "Redis, TTL = 10 min". A callback that arrives after
	// the TTL is rejected with ErrStateExpired.
	DefaultStateTTL = 10 * time.Minute

	// stateNonceBytes is the random-byte count behind a `state` value.
	// §9.3 requires "≥128 bits"; 16 bytes is exactly 128 bits.
	stateNonceBytes = 16
)

// Sentinel errors. The connector OAuth callback handler maps each to
// its HTTP status.
var (
	// ErrStateUnknown — the `state` has no stored flow context. It was
	// never issued, or its context already expired out of the store.
	ErrStateUnknown = errors.New("connectoroauth: state has no stored flow")

	// ErrStateExpired — the stored flow context is past its TTL.
	ErrStateExpired = errors.New("connectoroauth: state has expired")

	// ErrStateConsumed — the stored flow context was already consumed
	// by an earlier callback. A replayed callback fails this way.
	ErrStateConsumed = errors.New("connectoroauth: state was already consumed")
)

// FlowContext is the per-authorization-request state §9.3 binds to a
// `state` value. The connector OAuth callback handler consumes it by
// the returned state, then exchanges the authorization code under it.
type FlowContext struct {
	// ConnectorID is the §9.3 ConnectorDefinition the flow authorizes.
	ConnectorID string

	// TenantID + UserID + Environment scope the resulting connector
	// credential per §4.3. The empty Environment selects the
	// no-environment scope.
	TenantID    string
	UserID      string
	Environment string

	// SessionID is the §9.3 "initiating session ID" the state is bound
	// to. May be empty when the flow is initiated outside a session.
	SessionID string

	// CodeVerifier is the RFC 7636 PKCE verifier "stored alongside the
	// state entry and submitted at token exchange" per §9.3.
	CodeVerifier string

	// RedirectURI is the callback URL sent in the authorization request
	// and replayed verbatim in the token exchange (the OAuth 2.1
	// redirect_uri match).
	RedirectURI string

	// Scopes is the requested OAuth scope set, recorded for the audit
	// trail.
	Scopes []string

	// CreatedAt is the gateway clock instant the flow was initiated.
	CreatedAt time.Time

	// InitiatorIP / InitiatorUA bind the authorize-time request to the
	// authenticated principal. The §9.3 callback carries no Bearer
	// token (it is a browser redirect) and the `state` parameter is
	// the only server-side binding. Capturing the client IP / user
	// agent at authorize time lets the audit row distinguish
	// `initiated_by` from `completed_by` (the same fields captured at
	// callback time) so an operator can investigate a state replay or
	// session hijack between authorize and callback. Empty strings
	// preserve back-compat for code paths that do not stamp them.
	// spec: §9.3 — audit logging is the prescribed forensic
	// surface. F-9.3.11.
	InitiatorIP string
	InitiatorUA string
}

// NewState returns a random §9.3 `state` value: stateNonceBytes of
// crypto/rand entropy in unpadded base64url. The random bytes alone make
// the value unguessable, and the StateStore entry is what a callback
// proves knowledge of. crypto/rand.Read does not return an error: it
// aborts the program when the system random source fails.
func NewState() string {
	nonce := make([]byte, stateNonceBytes)
	_, _ = rand.Read(nonce)
	return base64.RawURLEncoding.EncodeToString(nonce)
}

// StateStore holds the §9.3 per-flow context keyed by the `state`
// value, with a TTL and single-use consumption. Production wires a
// Redis-backed implementation (§9.3 specifies Redis, 10-minute TTL);
// MemoryStateStore is the in-process implementation for tests and the
// minimal gateway. Implementations must be safe for concurrent use.
type StateStore interface {
	// Put stores ctx under state with the given TTL. A second Put for
	// the same state replaces the entry.
	Put(state string, fc FlowContext, ttl time.Duration) error

	// Consume atomically looks up, validates, and removes the flow
	// context for state. It returns ErrStateUnknown when no entry
	// exists, ErrStateExpired when the entry is past its TTL, and
	// ErrStateConsumed when a prior Consume already removed it. A
	// successful Consume removes the entry so the state cannot be
	// replayed.
	Consume(state string, now time.Time) (FlowContext, error)
}

// stateEntry is one MemoryStateStore record.
type stateEntry struct {
	fc        FlowContext
	expiresAt time.Time
	consumed  bool
}

// MemoryStateStore is the in-process StateStore. It retains consumed
// entries (marked, not deleted) until Sweep removes them so a replayed
// callback returns ErrStateConsumed rather than ErrStateUnknown — the
// distinction surfaces a replay attempt in the audit trail.
type MemoryStateStore struct {
	mu      sync.Mutex
	entries map[string]stateEntry
}

// NewMemoryStateStore returns an empty MemoryStateStore.
func NewMemoryStateStore() *MemoryStateStore {
	return &MemoryStateStore{entries: map[string]stateEntry{}}
}

var _ StateStore = (*MemoryStateStore)(nil)

// Put implements StateStore.
func (m *MemoryStateStore) Put(state string, fc FlowContext, ttl time.Duration) error {
	if state == "" {
		return errors.New("connectoroauth: empty state key")
	}
	if ttl <= 0 {
		ttl = DefaultStateTTL
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries[state] = stateEntry{
		fc:        fc,
		expiresAt: fc.CreatedAt.Add(ttl),
	}
	return nil
}

// Consume implements StateStore.
func (m *MemoryStateStore) Consume(state string, now time.Time) (FlowContext, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entries[state]
	if !ok {
		return FlowContext{}, ErrStateUnknown
	}
	if e.consumed {
		return FlowContext{}, ErrStateConsumed
	}
	if now.After(e.expiresAt) {
		// Past TTL: mark consumed so it cannot later be redeemed and so
		// a Sweep reclaims it.
		e.consumed = true
		m.entries[state] = e
		return FlowContext{}, ErrStateExpired
	}
	e.consumed = true
	m.entries[state] = e
	return e.fc, nil
}

// Sweep deletes every entry that is consumed or whose TTL has elapsed
// at or before now. It returns the number of entries dropped. Callers
// run it periodically to bound memory; a Redis-backed store relies on
// native key expiry instead.
func (m *MemoryStateStore) Sweep(now time.Time) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	dropped := 0
	for k, e := range m.entries {
		expired := now.After(e.expiresAt)
		if e.consumed || expired {
			delete(m.entries, k)
			dropped++
		}
	}
	return dropped
}
