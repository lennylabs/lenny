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

// RemoveIf drops the session's binding only when the registry still holds b,
// and reports whether it did. A binding published since b, or one another
// caller already removed, is left alone. Because the comparison and the
// delete run under one hold of the lock, exactly one of several concurrent
// evictors of the same binding removes it, and that caller alone owns the
// binding's adapter connection afterwards (see BindResult.CloseAdapter).
// spec: §10.1.5 (Stale Replica Behavior).
func (r *Registry) RemoveIf(sessionID string, b *BindResult) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	cur, ok := r.sessions[sessionID]
	if !ok || cur != b {
		return false
	}
	delete(r.sessions, sessionID)
	return true
}

// CloseAdapter closes the binding's gateway-to-pod adapter connection. A nil
// binding or a binding without an adapter is a no-op. Call it only on a
// binding the caller removed from the Registry (Remove returned it, or
// RemoveIf returned true): the remover is the single owner of the connection,
// so each connection is closed exactly once and a binding still published is
// never closed under a concurrent reader. An eviction that drops a binding
// without closing it leaks the gRPC channel and its background goroutines
// for the rest of the process lifetime.
// spec: §10.1.1 (Stateless Replicas and Per-Session Coordination), §10.1.5
// (Stale Replica Behavior).
func (b *BindResult) CloseAdapter() error {
	if b == nil || b.Adapter == nil {
		return nil
	}
	return b.Adapter.Close()
}

// Len reports how many session bindings this replica holds.
func (r *Registry) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.sessions)
}

// Snapshot returns every binding the registry currently holds. The
// returned slice is a copy, so a caller may iterate it without holding
// the registry lock while bindings are added or removed concurrently.
func (r *Registry) Snapshot() []*BindResult {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*BindResult, 0, len(r.sessions))
	for _, b := range r.sessions {
		out = append(out, b)
	}
	return out
}
