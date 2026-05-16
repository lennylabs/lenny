// SPDX-License-Identifier: MIT

// Package inputwait is the registry of pending lenny/request_input
// calls. A runtime that calls lenny/request_input blocks until a peer
// resolves the request via lenny/send_message with a matching
// inReplyTo, or until the §11.3 maxRequestInputWaitSeconds timeout
// fires. The registry pairs each pending request with a channel the
// blocked tool handler waits on.
package inputwait

import (
	"errors"
	"sync"
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
type Registry struct {
	mu      sync.Mutex
	pending map[string]chan string
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{pending: map[string]chan string{}}
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
// returned when (sessionID, requestID) is already pending.
func (r *Registry) Register(sessionID, requestID string) (<-chan string, error) {
	k := key(sessionID, requestID)
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.pending[k]; ok {
		return nil, ErrDuplicate
	}
	ch := make(chan string, 1)
	r.pending[k] = ch
	return ch, nil
}

// Resolve delivers answer to the request's waiter and removes the
// pending entry. ErrNotFound is returned when no request matches.
func (r *Registry) Resolve(sessionID, requestID, answer string) error {
	k := key(sessionID, requestID)
	r.mu.Lock()
	defer r.mu.Unlock()
	ch, ok := r.pending[k]
	if !ok {
		return ErrNotFound
	}
	delete(r.pending, k)
	ch <- answer // buffered with cap 1, so this never blocks
	return nil
}

// Cancel removes a pending request without delivering an answer. It is
// the cleanup path for a waiter that stopped waiting on a timeout or a
// cancelled context. Cancel is a no-op when no request matches.
func (r *Registry) Cancel(sessionID, requestID string) {
	k := key(sessionID, requestID)
	r.mu.Lock()
	delete(r.pending, k)
	r.mu.Unlock()
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
