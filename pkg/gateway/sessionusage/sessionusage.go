// SPDX-License-Identifier: MIT

// Package sessionusage is the §8.8 per-session token accumulator. The
// §4.9 LLM proxy extracts the authoritative input/output token counts of
// every proxied request and folds them here, keyed by the originating
// session. The §8.8 TaskResult.usage and TaskResult.treeUsage rollups
// read these per-session totals: a settling task's own token usage comes
// from its row, and treeUsage sums the totals across a settled subtree.
//
// The accumulator is durable so the rollup is correct across gateway
// replicas: a proxied request may be served by any replica, and a task's
// terminal/archive materialization may run on a different replica from
// the one that recorded its tokens. An in-process accumulator would
// undercount in either case, so production uses the Postgres-backed Store
// (pkg/gateway/sessionusage/pgstore); the in-memory Memory here backs the
// minimal single-process gateway.
//
// spec: §8.8 lines 897-917; §4.9 line 1468.
package sessionusage

import (
	"context"
	"sync"
)

// Tokens is the §8.8 input/output token pair accumulated for a session.
type Tokens struct {
	Input  int64
	Output int64
}

// Store is the §8.8 per-session token accumulator contract. Every method
// is goroutine-safe.
type Store interface {
	// Add atomically folds one proxied request's token counts into the
	// session's running lifetime totals. Negative deltas are ignored. The
	// call is best-effort from the proxy's perspective: a transient store
	// fault must not fail the proxied LLM call.
	Add(ctx context.Context, tenantID, sessionID string, input, output int64) error

	// Get returns a session's accumulated tokens. A session with no
	// recorded usage returns the zero Tokens and a nil error.
	Get(ctx context.Context, tenantID, sessionID string) (Tokens, error)

	// GetMany returns the accumulated tokens for several sessions in one
	// tenant, keyed by session id. Sessions with no recorded usage are
	// absent from the map (the caller treats a missing key as zero). It
	// lets the treeUsage rollup read a whole subtree's token totals in one
	// round trip.
	GetMany(ctx context.Context, tenantID string, sessionIDs []string) (map[string]Tokens, error)
}

// Memory is the in-memory Store implementation.
type Memory struct {
	mu     sync.RWMutex
	tokens map[string]Tokens
}

// NewMemory returns an empty in-memory accumulator.
func NewMemory() *Memory { return &Memory{tokens: map[string]Tokens{}} }

var _ Store = (*Memory)(nil)

// key joins the tenant and session ids. The NUL separator cannot appear
// in an id, so distinct pairs never collide.
func key(tenantID, sessionID string) string { return tenantID + "\x00" + sessionID }

// Add implements Store.
func (m *Memory) Add(_ context.Context, tenantID, sessionID string, input, output int64) error {
	if tenantID == "" || sessionID == "" {
		return nil
	}
	if input < 0 {
		input = 0
	}
	if output < 0 {
		output = 0
	}
	if input == 0 && output == 0 {
		return nil
	}
	m.mu.Lock()
	t := m.tokens[key(tenantID, sessionID)]
	t.Input += input
	t.Output += output
	m.tokens[key(tenantID, sessionID)] = t
	m.mu.Unlock()
	return nil
}

// Get implements Store.
func (m *Memory) Get(_ context.Context, tenantID, sessionID string) (Tokens, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.tokens[key(tenantID, sessionID)], nil
}

// GetMany implements Store.
func (m *Memory) GetMany(_ context.Context, tenantID string, sessionIDs []string) (map[string]Tokens, error) {
	out := make(map[string]Tokens, len(sessionIDs))
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, id := range sessionIDs {
		if t, ok := m.tokens[key(tenantID, id)]; ok {
			out[id] = t
		}
	}
	return out, nil
}
