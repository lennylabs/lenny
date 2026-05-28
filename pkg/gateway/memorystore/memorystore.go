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
//
// §9.4 names Postgres + pgvector as the default backend. Write embeds
// each memory's content with the configured Embedder and stores the
// vector; Query embeds the query text and ranks the records that match
// the query string by vector distance, falling back to recency order
// when an embedding is unavailable. The Embedder seam keeps the
// gateway free of a hard dependency on an external embedding API: the
// HashingEmbedder default is deterministic and dependency-free, and a
// deployer can substitute a provider-backed Embedder.
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
	// Embedding is the §9.4 pgvector semantic-search vector for Content,
	// computed by the store's Embedder on Write. It is nil when no
	// Embedder is configured or the Embedder declined the text; Query
	// then ranks the record by recency rather than vector distance.
	Embedding []float32
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
	embedder   Embedder
	// observer receives the §9.4 instrumentation events: per-operation
	// duration, error counter, record-count gauge, over-threshold
	// counter. Nil disables emission (the default for unit tests).
	observer Observer
}

// NewInMemory returns an empty store. A non-positive maxPerUser
// selects DefaultMaxMemoriesPerUser; a nil clock defaults to time.Now.
// The store embeds memory content with HashingEmbedder; use
// NewInMemoryWithEmbedder to supply a different Embedder.
func NewInMemory(maxPerUser int, clock func() time.Time) *InMemory {
	return NewInMemoryWithEmbedder(maxPerUser, clock, NewHashingEmbedder())
}

// NewInMemoryWithEmbedder returns an empty store that embeds memory
// content with embedder. A non-positive maxPerUser selects
// DefaultMaxMemoriesPerUser; a nil clock defaults to time.Now; a nil
// embedder disables semantic ranking, so Query orders matches by
// recency only.
func NewInMemoryWithEmbedder(maxPerUser int, clock func() time.Time, embedder Embedder) *InMemory {
	if maxPerUser <= 0 {
		maxPerUser = DefaultMaxMemoriesPerUser
	}
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &InMemory{
		memories:   map[string]Memory{},
		maxPerUser: maxPerUser,
		clock:      clock,
		embedder:   embedder,
	}
}

// SetObserver wires the §9.4 instrumentation hook. A nil value clears
// it. The default backend is "memory"; callers (the gateway) can
// override via SetObserverBackend to surface the correct backend
// label.
func (m *InMemory) SetObserver(obs Observer) {
	m.observer = obs
}

// MaxPerUser returns the §9.4 line 202 per-user capacity bound. It is
// the value used when evaluating whether a Write commit crossed the
// 80%-of-cap headroom threshold.
func (m *InMemory) MaxPerUser() int {
	return m.maxPerUser
}

// inMemoryBackendLabel is the §16.1 `backend` label the InMemory
// store stamps on every observation. Tests assert it; production never
// reaches this code path (the gateway uses the Postgres backend when a
// pool is configured).
const inMemoryBackendLabel = "memory"

// Write implements Store. spec: §9.4 line 200.
func (m *InMemory) Write(_ context.Context, scope MemoryScope, memories []Memory) (err error) {
	defer m.observe(OpWrite, time.Now(), &err)
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
		// Compute the §9.4 semantic-search embedding from the content
		// unless the caller already supplied one. A nil-returning or
		// absent Embedder leaves the field nil.
		if mem.Embedding == nil && m.embedder != nil {
			if vec, err := m.embedder.Embed(mem.Content); err == nil {
				mem.Embedding = vec
			}
		}
		m.memories[mem.ID] = cloneMemory(mem)
	}
	m.evictOldest(scope.TenantID, scope.UserID)
	// §9.4 line 202 — surface the per-user 80% headroom counter once
	// the write has committed. The user's count is computed against
	// the post-eviction map so an unbounded series of writes never
	// double-counts the threshold crossing.
	userCount := m.countUserLocked(scope.TenantID, scope.UserID)
	tenantCount := m.countTenantLocked(scope.TenantID)
	if m.observer != nil {
		if userCount >= ThresholdCount(m.maxPerUser) {
			m.observer.IncUserOverThreshold(scope.TenantID)
			LogOverThreshold(inMemoryBackendLabel, scope.TenantID, scope.UserID, userCount, m.maxPerUser)
		}
		m.observer.SetRecordCount(scope.TenantID, tenantCount)
	}
	return nil
}

// countUserLocked returns the in-memory map's record count for
// (tenantID, userID). The caller holds the lock.
func (m *InMemory) countUserLocked(tenantID, userID string) int {
	n := 0
	for _, mem := range m.memories {
		if mem.TenantID == tenantID && mem.UserID == userID {
			n++
		}
	}
	return n
}

// countTenantLocked returns the in-memory map's record count for
// tenantID. The caller holds the lock.
func (m *InMemory) countTenantLocked(tenantID string) int {
	n := 0
	for _, mem := range m.memories {
		if mem.TenantID == tenantID {
			n++
		}
	}
	return n
}

// observe records the §16.1 line 151 duration histogram observation
// and (when err points at a non-nil error) the §16.1 line 152 error
// counter. The caller defers it at function entry. A nil observer is
// a no-op.
func (m *InMemory) observe(op string, start time.Time, err *error) {
	if m.observer == nil {
		return
	}
	m.observer.ObserveOperation(op, time.Since(start).Seconds())
	if err != nil && *err != nil {
		m.observer.IncError(op, ClassifyError(*err))
	}
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

// Query implements Store. It filters the scope's memories to those
// whose content contains the query string (case-insensitive), then
// orders the matches by §9.4 semantic similarity: when the Embedder
// produces a query vector and a candidate carries an embedding, the
// candidate is ranked by ascending cosine distance to the query;
// candidates without an embedding, and every match when no Embedder is
// configured, fall back to newest-first. The result is capped at
// limit (0 means no cap).
func (m *InMemory) Query(_ context.Context, scope MemoryScope, query string, limit int) (_ []Memory, err error) {
	defer m.observe(OpQuery, time.Now(), &err)
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
	m.rankByEmbedding(out, query)
	return capSlice(out, limit), nil
}

// rankByEmbedding orders matches by §9.4 vector similarity to the
// query in place. It embeds query with the store's Embedder; when that
// yields a vector, matches that carry an embedding sort by ascending
// cosine distance and matches without one sort after them by recency.
// When no Embedder is configured or the query embeds to nil, every
// match falls back to newest-first.
func (m *InMemory) rankByEmbedding(matches []Memory, query string) {
	var qv []float32
	if m.embedder != nil && strings.TrimSpace(query) != "" {
		if v, err := m.embedder.Embed(query); err == nil {
			qv = v
		}
	}
	if qv == nil {
		sortNewestFirst(matches)
		return
	}
	sort.SliceStable(matches, func(i, j int) bool {
		hi, hj := matches[i].Embedding != nil, matches[j].Embedding != nil
		if hi != hj {
			// An embedded candidate always ranks ahead of a non-embedded
			// one, which can only fall back to recency.
			return hi
		}
		if !hi {
			return matches[i].CreatedAt.After(matches[j].CreatedAt)
		}
		di := CosineDistance(qv, matches[i].Embedding)
		dj := CosineDistance(qv, matches[j].Embedding)
		if di != dj {
			return di < dj
		}
		return matches[i].CreatedAt.After(matches[j].CreatedAt)
	})
}

// Delete implements Store.
func (m *InMemory) Delete(_ context.Context, scope MemoryScope, ids []string) (err error) {
	defer m.observe(OpDelete, time.Now(), &err)
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
	if m.observer != nil {
		m.observer.SetRecordCount(scope.TenantID, m.countTenantLocked(scope.TenantID))
	}
	return nil
}

// List implements Store.
func (m *InMemory) List(_ context.Context, scope MemoryScope, filter MemoryFilter) (_ []Memory, err error) {
	defer m.observe(OpList, time.Now(), &err)
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
func (m *InMemory) DeleteByUser(_ context.Context, tenantID, userID string) (err error) {
	defer m.observe(OpDeleteByUser, time.Now(), &err)
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
	if m.observer != nil {
		m.observer.SetRecordCount(tenantID, m.countTenantLocked(tenantID))
	}
	return nil
}

// DeleteByTenant implements Store.
func (m *InMemory) DeleteByTenant(_ context.Context, tenantID string) (err error) {
	defer m.observe(OpDeleteByTenant, time.Now(), &err)
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
	if m.observer != nil {
		// The tenant's records are fully purged; emit 0 so the gauge
		// reflects the post-condition without waiting for the next
		// periodic sample.
		m.observer.SetRecordCount(tenantID, 0)
	}
	return nil
}

// TenantRecordCounts returns the per-tenant memory record count map.
// The §9.4 line 202 record-count gauge sampler calls this method to
// refresh the gauge.
func (m *InMemory) TenantRecordCounts(_ context.Context) (map[string]int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := map[string]int{}
	for _, mem := range m.memories {
		out[mem.TenantID]++
	}
	return out, nil
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
