// SPDX-License-Identifier: MIT

package podsession

import "sync"

// Registry holds the live pod bindings for the sessions a gateway
// replica coordinates. A session's BindResult — its claimed pod and the
// adapter connection — lives here for the session's duration: the
// session-start path stores it, the message path reads it to reach the
// pod's adapter, and the teardown path removes it before releasing the
// pod. The registry is per-replica; a coordinator handoff re-establishes
// the binding on the new coordinating replica.
type Registry struct {
	mu       sync.Mutex
	sessions map[string]*BindResult
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{sessions: make(map[string]*BindResult)}
}

// Put records the binding for a session, replacing any prior binding.
func (r *Registry) Put(b *BindResult) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions[b.SessionID] = b
}

// Get returns the binding for a session. ok is false when this replica
// holds no binding for the session.
func (r *Registry) Get(sessionID string) (b *BindResult, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	b, ok = r.sessions[sessionID]
	return b, ok
}

// Remove drops the binding for a session and returns it so the caller
// can release the pod. ok is false when this replica held no binding.
func (r *Registry) Remove(sessionID string) (b *BindResult, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	b, ok = r.sessions[sessionID]
	delete(r.sessions, sessionID)
	return b, ok
}

// Len reports how many session bindings this replica holds.
func (r *Registry) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.sessions)
}
