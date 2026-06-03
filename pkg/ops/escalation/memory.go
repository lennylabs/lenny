// SPDX-License-Identifier: MIT

package escalation

import (
	"context"
	"sort"
	"sync"
	"time"
)

// bufferCapacity is the §25.4 Tier 3 in-memory buffer cap: the oldest
// escalation is evicted when a new one would exceed it (§25.4 line 2384).
const bufferCapacity = 100

// MemStore is the §25.4 Tier 3 in-memory escalation buffer: a capped
// ring of the most recent escalations. It is the always-present fallback
// tier — a record can be created here when both Postgres and Redis are
// unreachable — and it is the source the reconciliation flush drains
// upward when a durable store recovers. MemStore is safe for concurrent
// use.
type MemStore struct {
	mu       sync.Mutex
	byID     map[string]*Escalation
	order    []string // creation order, for capped eviction
	capacity int
}

// NewMemStore returns an empty Tier 3 buffer capped at 100 entries.
func NewMemStore() *MemStore {
	return &MemStore{byID: make(map[string]*Escalation), capacity: bufferCapacity}
}

// Tier reports the buffered-memory persistence label.
func (m *MemStore) Tier() string { return PersistenceBufferedMemory }

// Put inserts or replaces esc by ID and evicts the oldest entries beyond
// the capacity. A replace (same ID) keeps the record's position in the
// eviction order so a status update does not reset its age.
func (m *MemStore) Put(_ context.Context, esc Escalation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.byID[esc.ID]; !exists {
		m.order = append(m.order, esc.ID)
	}
	stored := esc
	m.byID[esc.ID] = &stored
	for len(m.order) > m.capacity {
		oldest := m.order[0]
		m.order = m.order[1:]
		delete(m.byID, oldest)
	}
	return nil
}

// Get returns the escalation by id, or (nil, nil) when absent.
func (m *MemStore) Get(_ context.Context, id string) (*Escalation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	esc, ok := m.byID[id]
	if !ok {
		return nil, nil
	}
	return cloneEscalation(esc), nil
}

// List returns the buffered escalations matching f, newest-first, capped
// by limit.
func (m *MemStore) List(_ context.Context, f Filter, limit int) ([]Escalation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	statuses := csvSet(f.Status)
	severities := csvSet(f.Severity)
	out := make([]Escalation, 0, len(m.byID))
	for _, esc := range m.byID {
		if len(statuses) > 0 && !statuses[esc.Status] {
			continue
		}
		if len(severities) > 0 && !severities[esc.Severity] {
			continue
		}
		if !f.Since.IsZero() && esc.CreatedAt.Before(f.Since) {
			continue
		}
		out = append(out, *cloneEscalation(esc))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// SetStatus moves the escalation to status and stamps the lifecycle
// timestamps from now. It returns (nil, nil) when the id is absent.
func (m *MemStore) SetStatus(_ context.Context, id, status string, now time.Time) (*Escalation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	esc, ok := m.byID[id]
	if !ok {
		return nil, nil
	}
	ApplyStatus(esc, status, now)
	return cloneEscalation(esc), nil
}

// SetEmitted flips the emitted flag true; a no-op when the id is absent.
func (m *MemStore) SetEmitted(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if esc, ok := m.byID[id]; ok {
		esc.Emitted = true
	}
	return nil
}

// PendingEmission returns the buffered escalations whose emitted flag is
// still false.
func (m *MemStore) PendingEmission(_ context.Context) ([]Escalation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Escalation
	for _, esc := range m.byID {
		if !esc.Emitted {
			out = append(out, *cloneEscalation(esc))
		}
	}
	return out, nil
}

// Buffered returns a snapshot of every buffered escalation, newest-first,
// for the §25.4 reconciliation flush. The records are copies; mutating
// them does not touch the buffer.
func (m *MemStore) Buffered() []Escalation {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Escalation, 0, len(m.order))
	for _, id := range m.order {
		if esc, ok := m.byID[id]; ok {
			out = append(out, *cloneEscalation(esc))
		}
	}
	return out
}

// Remove drops a buffered escalation after it has been flushed upward to
// a durable tier. It is a no-op for an unknown id.
func (m *MemStore) Remove(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.byID[id]; !ok {
		return
	}
	delete(m.byID, id)
	for i, oid := range m.order {
		if oid == id {
			m.order = append(m.order[:i], m.order[i+1:]...)
			break
		}
	}
}

// ApplyStatus stamps the lifecycle status and timestamps on esc. Shared
// by the in-memory and Redis Stores so the acknowledged/resolved-at
// semantics are identical across tiers (the Postgres store applies the
// same rule in SQL).
func ApplyStatus(esc *Escalation, status string, now time.Time) {
	esc.Status = status
	esc.UpdatedAt = now
	switch status {
	case StatusAcknowledged:
		if esc.AcknowledgedAt == nil {
			t := now
			esc.AcknowledgedAt = &t
		}
	case StatusResolved:
		if esc.ResolvedAt == nil {
			t := now
			esc.ResolvedAt = &t
		}
	}
}
