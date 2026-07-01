// SPDX-License-Identifier: MIT

// Package breakerstore is the §11.6 operator-managed circuit-breaker
// registry. It backs the gateway's circuit-breaker middleware
// (pkg/gateway/middleware/circuitbreaker) and the §15.1 admin
// `circuit-breakers` endpoints.
//
// Production wires this to Redis (so cross-replica state stays
// consistent); the in-memory implementation here is sufficient for
// tests and the minimal gateway.
package breakerstore

import (
	"context"
	"errors"
	"sort"
	"sync"

	"github.com/lennylabs/lenny/pkg/circuitbreaker"
	cbmw "github.com/lennylabs/lenny/pkg/gateway/middleware/circuitbreaker"
)

// Sentinel errors.
var (
	// ErrNotFound — no breaker with this name exists.
	ErrNotFound = errors.New("breakerstore: breaker not found")

	// ErrScopeImmutable — admin tried to mutate the scope of an
	// existing breaker per §11.6 "scope immutability".
	ErrScopeImmutable = errors.New("breakerstore: cannot change limit_tier/scope of an existing breaker")
)

// Store is the §11.6 breaker registry contract.
type Store interface {
	// Open creates or transitions a breaker to `open`. The scope is
	// pinned on first open; subsequent opens against the same name
	// must match the original (LimitTier, Scope) or return
	// ErrScopeImmutable per §11.6 invariant.
	Open(ctx context.Context, b circuitbreaker.Breaker) (circuitbreaker.Breaker, error)

	// Close transitions a named breaker to `closed`. Returns
	// ErrNotFound when the breaker is not in the registry.
	Close(ctx context.Context, name string) (circuitbreaker.Breaker, error)

	// Get returns the named breaker. Returns ErrNotFound when missing.
	Get(ctx context.Context, name string) (circuitbreaker.Breaker, error)

	// List returns every breaker in name-ascending order.
	List(ctx context.Context) ([]circuitbreaker.Breaker, error)
}

// Memory is the in-memory Store. Satisfies cbmw.Registry so the
// gateway middleware can use the same backing store.
type Memory struct {
	mu       sync.RWMutex
	breakers map[string]circuitbreaker.Breaker
}

// NewMemory returns an empty Memory store.
func NewMemory() *Memory {
	return &Memory{breakers: map[string]circuitbreaker.Breaker{}}
}

// Open implements Store.
func (m *Memory) Open(_ context.Context, b circuitbreaker.Breaker) (circuitbreaker.Breaker, error) {
	if err := b.Validate(); err != nil {
		return circuitbreaker.Breaker{}, err
	}
	if b.State != circuitbreaker.StateOpen {
		b.State = circuitbreaker.StateOpen
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	existing, exists := m.breakers[b.Name]
	if exists {
		// §11.6 immutability: scope cannot change between opens.
		if !circuitbreaker.ScopeMatches(existing, b) {
			return circuitbreaker.Breaker{}, ErrScopeImmutable
		}
	}
	m.breakers[b.Name] = b
	return b, nil
}

// Close implements Store.
func (m *Memory) Close(_ context.Context, name string) (circuitbreaker.Breaker, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	existing, ok := m.breakers[name]
	if !ok {
		return circuitbreaker.Breaker{}, ErrNotFound
	}
	existing.State = circuitbreaker.StateClosed
	m.breakers[name] = existing
	return existing, nil
}

// Get implements Store.
func (m *Memory) Get(_ context.Context, name string) (circuitbreaker.Breaker, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	b, ok := m.breakers[name]
	if !ok {
		return circuitbreaker.Breaker{}, ErrNotFound
	}
	return b, nil
}

// List implements Store.
func (m *Memory) List(_ context.Context) ([]circuitbreaker.Breaker, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]circuitbreaker.Breaker, 0, len(m.breakers))
	for _, b := range m.breakers {
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Snapshot implements cbmw.Registry so the middleware can use this
// store directly without a separate registry layer.
func (m *Memory) Snapshot(_ context.Context) ([]circuitbreaker.Breaker, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]circuitbreaker.Breaker, 0, len(m.breakers))
	for _, b := range m.breakers {
		if b.State == circuitbreaker.StateOpen {
			out = append(out, b)
		}
	}
	return out, nil
}

// Ensure Memory satisfies cbmw.Registry at compile time.
var _ cbmw.Registry = (*Memory)(nil)
