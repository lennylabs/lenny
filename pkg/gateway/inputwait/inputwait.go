// SPDX-License-Identifier: MIT

// Package inputwait is the registry of pending lenny/request_input
// calls. A runtime that calls lenny/request_input blocks until a peer
// resolves the request via lenny/send_message with a matching
// inReplyTo, or until the §11.3 maxRequestInputWaitSeconds timeout
// fires. The registry pairs each pending request with a channel the
// blocked tool handler waits on.
package inputwait

import (
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"time"
)

// Registry errors.
var (
	// ErrDuplicate — a request with the same (session, id) is already
	// pending.
	ErrDuplicate = errors.New("inputwait: a request with this id is already pending")
	// ErrNotFound — no pending request matches (session, id).
	ErrNotFound = errors.New("inputwait: no pending request with this id")
)

// Registry tracks pending lenny/request_input calls keyed by
// (sessionID, requestID). It is goroutine-safe.
//
// The Registry also maintains a per-session lifetime counter of
// lenny/request_input rounds the session has consumed (`Consumed`).
// The counter is monotonically non-decreasing for a session — Register
// increments it on every accepted request, regardless of subsequent
// Resolve / Cancel — so the gateway can enforce the §8.8 line 869
// `one_shot` constraint: a runtime whose `capabilities.interaction:
// one_shot` declaration limits it to a single `request_input` call
// over the task lifetime. The counter is process-local; a one_shot
// runtime is short-lived by definition so v1 in-memory tracking is
// sufficient for the §8.8 contract. spec: §8.8 line 869. F-8.8.10.
type Registry struct {
	mu       sync.Mutex
	pending  map[string]*entry
	consumed map[string]int
	// now sources the wall clock for the §8.8 line 990 `blockedSince`
	// timestamp stamped on each pending request. It is overridable in
	// tests so the §8.8 deadlock detector's blocked-since witnesses are
	// deterministic. spec: §8.8 line 990. F-8.8.6.
	now func() time.Time
}

// entry is a single pending request: the answer channel plus the §8.8
// line 951 question `parts` the lenny/await_children partial result
// carries back to the awaiting parent. spec: §8.8 line 951; F-8.8.5.
type entry struct {
	ch           chan string
	parts        []json.RawMessage
	registeredAt time.Time
}

// PendingRequest is one session's pending lenny/request_input round as
// surfaced to lenny/await_children. RequestID is the correlation id the
// parent answers with `inReplyTo`; Parts is the OutputPart[] question.
// BlockedSince is the wall-clock instant the request was registered —
// the §8.8 line 990 `blockedSince` witness the subtree deadlock
// detector reports on each `blockedRequests` entry. spec: §8.8 line
// 951, line 990; F-8.8.5 / F-8.8.6.
type PendingRequest struct {
	RequestID    string
	Parts        []json.RawMessage
	BlockedSince time.Time
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		pending:  map[string]*entry{},
		consumed: map[string]int{},
		now:      time.Now,
	}
}

// SetClock overrides the registry's wall clock. It exists for tests
// that assert the §8.8 `blockedSince` witness; production callers use
// the default time.Now wired by NewRegistry.
func (r *Registry) SetClock(now func() time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.now = now
}

func (r *Registry) clock() time.Time {
	if r.now == nil {
		return time.Now()
	}
	return r.now()
}

// key joins a session and request id into a registry key. The NUL
// separator cannot appear in either component, so distinct pairs never
// collide.
func key(sessionID, requestID string) string {
	return sessionID + "\x00" + requestID
}

// Register records a pending request and returns the channel its
// answer arrives on. The channel is buffered so a Resolve call never
// blocks even when the waiter has already left. ErrDuplicate is
// returned when (sessionID, requestID) is already pending. Register
// also bumps the §8.8 per-session lifetime counter so a Consumed()
// caller can detect a second request before allocating a channel.
// The §8.8 line 951 question `parts` are stored alongside the channel
// so a lenny/await_children call can surface them in the input_required
// partial result without a second round-trip to the child. A nil parts
// slice is permitted (the registry does not interpret the payload).
func (r *Registry) Register(sessionID, requestID string, parts []json.RawMessage) (<-chan string, error) {
	k := key(sessionID, requestID)
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.pending[k]; ok {
		return nil, ErrDuplicate
	}
	ch := make(chan string, 1)
	r.pending[k] = &entry{ch: ch, parts: parts, registeredAt: r.clock()}
	r.consumed[sessionID]++
	return ch, nil
}

// Consumed returns the cumulative count of lenny/request_input rounds
// the session has registered over its lifetime. The counter is
// monotonically non-decreasing — Resolve and Cancel do not decrement.
// Returns zero when the session has never registered a request.
// spec: §8.8 line 869. F-8.8.10.
func (r *Registry) Consumed(sessionID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.consumed[sessionID]
}

// ForgetSession drops the per-session counter for sessionID. Callers
// invoke it on session terminal transition so a long-running gateway
// process does not accumulate counters for sessions whose rows have
// been reclaimed. A no-op when the session is not tracked.
func (r *Registry) ForgetSession(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.consumed, sessionID)
}

// Resolve delivers answer to the request's waiter and removes the
// pending entry. ErrNotFound is returned when no request matches.
func (r *Registry) Resolve(sessionID, requestID, answer string) error {
	k := key(sessionID, requestID)
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.pending[k]
	if !ok {
		return ErrNotFound
	}
	delete(r.pending, k)
	e.ch <- answer // buffered with cap 1, so this never blocks
	return nil
}

// Cancel removes a pending request without delivering an answer and
// closes the channel so the waiter unblocks with the zero value. The
// blocked tool handler distinguishes a real Resolve (ok=true on
// receive) from a Cancel (ok=false on a closed channel) so it can
// translate the latter into a structured cancellation error rather
// than treating it as an empty answer. Cancel is idempotent: a second
// call (or a race with Resolve) finds no entry and returns silently.
func (r *Registry) Cancel(sessionID, requestID string) {
	k := key(sessionID, requestID)
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.pending[k]; ok {
		close(e.ch)
		delete(r.pending, k)
	}
}

// Pending reports whether a request is registered for (sessionID,
// requestID).
func (r *Registry) Pending(sessionID, requestID string) bool {
	k := key(sessionID, requestID)
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.pending[k]
	return ok
}

// PendingForSession returns every request id currently registered for
// sessionID. The order is unspecified; callers that need a stable
// pick (e.g. the §7.2 line 153 `ReattachedChild.pending_request_id`
// surface) MUST sort the result themselves. Returns nil when no
// request is pending for the session.
func (r *Registry) PendingForSession(sessionID string) []string {
	prefix := sessionID + "\x00"
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for k := range r.pending {
		if len(k) > len(prefix) && k[:len(prefix)] == prefix {
			out = append(out, k[len(prefix):])
		}
	}
	return out
}

// PendingDetailsForSession returns every pending request for sessionID
// with its §8.8 line 951 question `parts`, sorted by request id so the
// lenny/await_children input_required partial result is deterministic
// across polls. Returns nil when no request is pending. spec: §8.8
// line 951; F-8.8.5.
func (r *Registry) PendingDetailsForSession(sessionID string) []PendingRequest {
	prefix := sessionID + "\x00"
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []PendingRequest
	for k, e := range r.pending {
		if len(k) > len(prefix) && k[:len(prefix)] == prefix {
			out = append(out, PendingRequest{RequestID: k[len(prefix):], Parts: e.parts, BlockedSince: e.registeredAt})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RequestID < out[j].RequestID })
	return out
}
