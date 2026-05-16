// SPDX-License-Identifier: MIT

// Package memorystore implements the §9.4 MemoryStore role: the
// pluggable, tenant-scoped agent-memory store behind the
// lenny/memory_write and lenny/memory_query platform MCP tools.
//
// Every operation is strictly scoped to MemoryScope.TenantID — a
// cross-tenant read or write is impossible, and an empty TenantID is
// rejected rather than silently defaulted. The store enforces the
// §9.4 per-user capacity limit (maxMemoriesPerUser) by evicting the
// oldest memories on write, and exposes the §12.8 mandatory erasure
// primitives DeleteByUser and DeleteByTenant.
package memorystore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

// DefaultMaxMemoriesPerUser is the §9.4 default per-user capacity
// limit. A write that would exceed it evicts the user's oldest
// memories.
const DefaultMaxMemoriesPerUser = 10000

// Memory is one §9.4 memory record.
type Memory struct {
	// ID is the record identifier, assigned on Write when empty.
	ID string
	// TenantID and UserID are the §9.4 owning scope, stamped on Write.
	TenantID string
	UserID   string
	// AgentType and SessionID are the optional finer scope the memory
	// was written under.
	AgentType string
	SessionID string
	// Content is the memory text.
	Content string
	// Metadata holds arbitrary caller key-value pairs.
	Metadata map[string]any
	// CreatedAt is the server-stamped write time; the eviction order.
	CreatedAt time.Time
}

// MemoryScope identifies the tenant and user (and optional agent type
// and session) a memory operation is scoped to.
type MemoryScope struct {
	TenantID  string
	UserID    string
	AgentType string
	SessionID string
}

// MemoryFilter narrows a List result. A zero Limit means no cap.
type MemoryFilter struct {
	Limit int
}

// Scope errors. The §9.4 interface boundary rejects empty scope ids
// rather than silently defaulting them.
var (
	// ErrEmptyTenant — a scoped operation supplied an empty TenantID.
	ErrEmptyTenant = errors.New("memorystore: scope.TenantID is required")
	// ErrEmptyUser — a scoped operation supplied an empty UserID.
	ErrEmptyUser = errors.New("memorystore: scope.UserID is required")
)

// Store is the §9.4 MemoryStore role contract. Every method is
// goroutine-safe and tenant-scoped.
type Store interface {
	// Write stores memories under the scope, stamping the scope and a
	// fresh id/timestamp, and evicts the user's oldest memories beyond
	// the capacity limit.
	Write(ctx context.Context, scope MemoryScope, memories []Memory) error
	// Query returns the scope's memories whose content contains the
	// query string, newest first, capped at limit (0 means no cap).
	Query(ctx context.Context, scope MemoryScope, query string, limit int) ([]Memory, error)
	// Delete removes the identified memories that fall within the scope.
	Delete(ctx context.Context, scope MemoryScope, ids []string) error
	// List returns the scope's memories, newest first, narrowed by the
	// filter.
	List(ctx context.Context, scope MemoryScope, filter MemoryFilter) ([]Memory, error)
	// DeleteByUser removes every memory keyed to (tenantID, userID) —
	// the §12.8 GDPR-erasure primitive. Idempotent; rejects empty ids.
	DeleteByUser(ctx context.Context, tenantID, userID string) error
	// DeleteByTenant removes every memory scoped to tenantID — the
	// §12.8 tenant-deletion primitive. Idempotent; rejects an empty id.
	DeleteByTenant(ctx context.Context, tenantID string) error
}

// InMemory is the in-memory Store implementation — the test and
// minimal-gateway backend. Production swaps in a Postgres + pgvector
// backend behind the same interface.
type InMemory struct {
	mu         sync.RWMutex
	memories   map[string]Memory // keyed by Memory.ID
	maxPerUser int
	clock      func() time.Time
}

// NewInMemory returns an empty store. A non-positive maxPerUser
// selects DefaultMaxMemoriesPerUser; a nil clock defaults to time.Now.
func NewInMemory(maxPerUser int, clock func() time.Time) *InMemory {
	if maxPerUser <= 0 {
		maxPerUser = DefaultMaxMemoriesPerUser
	}
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &InMemory{memories: map[string]Memory{}, maxPerUser: maxPerUser, clock: clock}
}

// Write implements Store.
func (m *InMemory) Write(_ context.Context, scope MemoryScope, memories []Memory) error {
	if scope.TenantID == "" {
		return ErrEmptyTenant
	}
	if scope.UserID == "" {
		return ErrEmptyUser
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, mem := range memories {
		mem.TenantID = scope.TenantID
		mem.UserID = scope.UserID
		mem.AgentType = scope.AgentType
		mem.SessionID = scope.SessionID
		if mem.ID == "" {
			mem.ID = NewID()
		}
		if mem.CreatedAt.IsZero() {
			mem.CreatedAt = m.clock()
		}
		m.memories[mem.ID] = cloneMemory(mem)
	}
	m.evictOldest(scope.TenantID, scope.UserID)
	return nil
}

// evictOldest trims the user's memories down to the capacity limit,
// dropping the oldest by CreatedAt. The caller holds the write lock.
func (m *InMemory) evictOldest(tenantID, userID string) {
	var owned []Memory
	for _, mem := range m.memories {
		if mem.TenantID == tenantID && mem.UserID == userID {
			owned = append(owned, mem)
		}
	}
	if len(owned) <= m.maxPerUser {
		return
	}
	sort.Slice(owned, func(i, j int) bool { return owned[i].CreatedAt.Before(owned[j].CreatedAt) })
	for _, mem := range owned[:len(owned)-m.maxPerUser] {
		delete(m.memories, mem.ID)
	}
}

// Query implements Store.
func (m *InMemory) Query(_ context.Context, scope MemoryScope, query string, limit int) ([]Memory, error) {
	if scope.TenantID == "" {
		return nil, ErrEmptyTenant
	}
	if scope.UserID == "" {
		return nil, ErrEmptyUser
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	q := strings.ToLower(query)
	var out []Memory
	for _, mem := range m.memories {
		if !inScope(mem, scope) {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(mem.Content), q) {
			continue
		}
		out = append(out, cloneMemory(mem))
	}
	sortNewestFirst(out)
	return capSlice(out, limit), nil
}

// Delete implements Store.
func (m *InMemory) Delete(_ context.Context, scope MemoryScope, ids []string) error {
	if scope.TenantID == "" {
		return ErrEmptyTenant
	}
	if scope.UserID == "" {
		return ErrEmptyUser
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, id := range ids {
		// A memory is deleted only when it falls within the caller's
		// scope — an id from another tenant or user is never removed.
		if mem, ok := m.memories[id]; ok && mem.TenantID == scope.TenantID && mem.UserID == scope.UserID {
			delete(m.memories, id)
		}
	}
	return nil
}

// List implements Store.
func (m *InMemory) List(_ context.Context, scope MemoryScope, filter MemoryFilter) ([]Memory, error) {
	if scope.TenantID == "" {
		return nil, ErrEmptyTenant
	}
	if scope.UserID == "" {
		return nil, ErrEmptyUser
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []Memory
	for _, mem := range m.memories {
		if inScope(mem, scope) {
			out = append(out, cloneMemory(mem))
		}
	}
	sortNewestFirst(out)
	return capSlice(out, filter.Limit), nil
}

// DeleteByUser implements Store.
func (m *InMemory) DeleteByUser(_ context.Context, tenantID, userID string) error {
	if tenantID == "" {
		return ErrEmptyTenant
	}
	if userID == "" {
		return ErrEmptyUser
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, mem := range m.memories {
		if mem.TenantID == tenantID && mem.UserID == userID {
			delete(m.memories, id)
		}
	}
	return nil
}

// DeleteByTenant implements Store.
func (m *InMemory) DeleteByTenant(_ context.Context, tenantID string) error {
	if tenantID == "" {
		return ErrEmptyTenant
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, mem := range m.memories {
		if mem.TenantID == tenantID {
			delete(m.memories, id)
		}
	}
	return nil
}

// inScope reports whether a memory falls within the scope: the tenant
// and user must match exactly, and the agent type and session, when
// set on the scope, narrow the match further.
func inScope(mem Memory, scope MemoryScope) bool {
	if mem.TenantID != scope.TenantID || mem.UserID != scope.UserID {
		return false
	}
	if scope.AgentType != "" && mem.AgentType != scope.AgentType {
		return false
	}
	if scope.SessionID != "" && mem.SessionID != scope.SessionID {
		return false
	}
	return true
}

func sortNewestFirst(ms []Memory) {
	sort.Slice(ms, func(i, j int) bool { return ms[i].CreatedAt.After(ms[j].CreatedAt) })
}

func capSlice(ms []Memory, limit int) []Memory {
	if limit > 0 && len(ms) > limit {
		return ms[:limit]
	}
	return ms
}

// cloneMemory deep-copies the Metadata map so a stored memory and a
// returned copy never share mutable state.
func cloneMemory(mem Memory) Memory {
	if mem.Metadata != nil {
		md := make(map[string]any, len(mem.Metadata))
		for k, v := range mem.Metadata {
			md[k] = v
		}
		mem.Metadata = md
	}
	return mem
}

// NewID returns a fresh memory identifier — 8 random bytes hex-encoded
// with a `mem_` prefix.
func NewID() string {
	var buf [8]byte
	_, _ = rand.Read(buf[:])
	return "mem_" + hex.EncodeToString(buf[:])
}
