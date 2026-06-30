// SPDX-License-Identifier: MIT

package impersonation

import (
	"context"
	"sort"
	"sync"
	"time"
)

// Store persists impersonation sessions. The minted bearer is the
// security-bearing artifact and is self-expiring (its exp claim); the
// Store backs the operator-facing listing and the expiry sweep that
// emits the §16.7 admin.impersonation_ended event. A gateway-replica-local
// in-memory store is sufficient for v1 — the audit chain, not the ticket
// table, is the durable record.
type Store interface {
	// Put records an active session.
	Put(ctx context.Context, t Ticket) error
	// Get returns the session, or ErrNotFound.
	Get(ctx context.Context, id string) (Ticket, error)
	// MarkEnded stamps the terminal fields on a session and retains it for
	// the listing's recently-ended window. It returns ErrNotFound for an
	// unknown id and ErrAlreadyEnded for one already terminated.
	MarkEnded(ctx context.Context, id string, endedAt time.Time, endedBy string, reason EndReason) (Ticket, error)
	// ListActive returns the not-yet-ended sessions, ordered by IssuedAt.
	ListActive(ctx context.Context) ([]Ticket, error)
	// DueForExpiry returns the active sessions whose ExpiresAt is at or
	// before now, ordered by ExpiresAt — the sweep's work list.
	DueForExpiry(ctx context.Context, now time.Time) ([]Ticket, error)
}

// MemStore is the in-memory Store.
type MemStore struct {
	mu      sync.Mutex
	tickets map[string]Ticket
}

// NewMemStore returns an empty in-memory store.
func NewMemStore() *MemStore { return &MemStore{tickets: map[string]Ticket{}} }

// Put records an active session.
func (m *MemStore) Put(_ context.Context, t Ticket) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tickets[t.ID] = t
	return nil
}

// Get returns the session, or ErrNotFound.
func (m *MemStore) Get(_ context.Context, id string) (Ticket, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tickets[id]
	if !ok {
		return Ticket{}, ErrNotFound
	}
	return t, nil
}

// MarkEnded stamps the terminal fields, idempotently rejecting a
// double-end so the sweep and an explicit end cannot both emit.
func (m *MemStore) MarkEnded(_ context.Context, id string, endedAt time.Time, endedBy string, reason EndReason) (Ticket, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tickets[id]
	if !ok {
		return Ticket{}, ErrNotFound
	}
	if !t.Active() {
		return Ticket{}, ErrAlreadyEnded
	}
	t.EndedAt = endedAt
	t.EndedBy = endedBy
	t.EndReason = reason
	m.tickets[id] = t
	return t, nil
}

// ListActive returns the not-yet-ended sessions ordered by IssuedAt.
func (m *MemStore) ListActive(_ context.Context) ([]Ticket, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Ticket
	for _, t := range m.tickets {
		if t.Active() {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].IssuedAt.Before(out[j].IssuedAt) })
	return out, nil
}

// DueForExpiry returns the active sessions at or past their expiry.
func (m *MemStore) DueForExpiry(_ context.Context, now time.Time) ([]Ticket, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Ticket
	for _, t := range m.tickets {
		if t.Active() && !t.ExpiresAt.After(now) {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ExpiresAt.Before(out[j].ExpiresAt) })
	return out, nil
}
