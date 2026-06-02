// SPDX-License-Identifier: MIT

// Package toolapproval is the registry of pending §7.2 tool-use
// approvals. When a runtime emits a tool_call with approvalRequired
// over the §4.7 Attach stream, the gateway records a KindToolUse
// interaction, publishes a tool_use_requested SSE event, and blocks the
// in-flight tool call until the owning user resolves it via
// POST /v1/sessions/{id}/tool-use/{tool_call_id}/approve|deny. This
// registry pairs each pending tool call with a channel the blocked
// executor read waits on, and the §15.1 resolution endpoints deliver
// the approve / deny verdict onto that channel.
//
// The registry mirrors pkg/gateway/inputwait: it is the unblock half of
// the approval loop. spec: §7.2 lines 124-125, 134. F-7.2.9, F-7.2.18.
package toolapproval

import (
	"errors"
	"sync"
)

// Registry errors.
var (
	// ErrDuplicate — a tool call with the same (session, id) is already
	// awaiting approval.
	ErrDuplicate = errors.New("toolapproval: a tool call with this id is already pending")
	// ErrNotFound — no pending tool call matches (session, id). The
	// §15.1 resolution endpoint treats this as "no executor is blocked
	// on this approval" and returns normally; the interaction phase is
	// still updated so a later reader sees the verdict.
	ErrNotFound = errors.New("toolapproval: no pending tool call with this id")
)

// Decision is the verdict the §15.1 approve / deny endpoint delivers to
// the blocked executor read. Approved is true for an approve call and
// false for a deny; Reason carries the optional §7.2 line 125 deny
// reason.
type Decision struct {
	Approved bool
	Reason   string
}

// Registry tracks pending tool-use approvals keyed by (sessionID,
// toolCallID). It is goroutine-safe.
type Registry struct {
	mu      sync.Mutex
	pending map[string]chan Decision
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{pending: map[string]chan Decision{}}
}

// key joins a session and tool-call id. The NUL separator cannot appear
// in either component, so distinct pairs never collide.
func key(sessionID, toolCallID string) string {
	return sessionID + "\x00" + toolCallID
}

// Register records a pending approval and returns the channel its
// verdict arrives on. The channel is buffered (cap 1) so a Resolve call
// never blocks even when the waiter has already left, and so an approve
// that races ahead of the blocking select is not lost. ErrDuplicate is
// returned when (sessionID, toolCallID) is already pending.
func (r *Registry) Register(sessionID, toolCallID string) (<-chan Decision, error) {
	k := key(sessionID, toolCallID)
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.pending[k]; ok {
		return nil, ErrDuplicate
	}
	ch := make(chan Decision, 1)
	r.pending[k] = ch
	return ch, nil
}

// Resolve delivers the verdict to the tool call's waiter and removes the
// pending entry. ErrNotFound is returned when no tool call matches —
// the resolution endpoint ignores it because the interaction-store
// phase update is the authoritative record and a missing waiter only
// means no executor on this replica is blocked.
func (r *Registry) Resolve(sessionID, toolCallID string, d Decision) error {
	k := key(sessionID, toolCallID)
	r.mu.Lock()
	defer r.mu.Unlock()
	ch, ok := r.pending[k]
	if !ok {
		return ErrNotFound
	}
	delete(r.pending, k)
	ch <- d // buffered with cap 1, so this never blocks
	return nil
}

// Cancel removes a pending approval without delivering a verdict and
// closes the channel so the waiter unblocks with the zero value. The
// blocked executor read distinguishes a real Resolve (ok=true on
// receive) from a Cancel (ok=false on a closed channel) so it can treat
// the latter as an implicit denial rather than an approval. Cancel is
// idempotent: a second call (or a race with Resolve) finds no entry and
// returns silently.
func (r *Registry) Cancel(sessionID, toolCallID string) {
	k := key(sessionID, toolCallID)
	r.mu.Lock()
	defer r.mu.Unlock()
	if ch, ok := r.pending[k]; ok {
		close(ch)
		delete(r.pending, k)
	}
}

// Pending reports whether a tool call is registered for (sessionID,
// toolCallID).
func (r *Registry) Pending(sessionID, toolCallID string) bool {
	k := key(sessionID, toolCallID)
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.pending[k]
	return ok
}
