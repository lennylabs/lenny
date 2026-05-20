// SPDX-License-Identifier: MIT

// Package memstore is an in-memory SessionStore implementation used
// by the minimal gateway in cmd/lenny-gateway and by every tier-3
// REST-contract test. Production deployments use the Postgres-backed
// implementation that ships in a later phase; the wire-level
// behaviour of both backends matches the SessionStore contract from
// pkg/gateway/sessionstore, so swapping the backend changes no
// caller code.
package memstore

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
)

// Store is the in-memory backend. The zero value is not usable;
// construct with New.
type Store struct {
	mu       sync.RWMutex
	sessions map[string]sessionstore.Session
}

// New returns an empty in-memory store.
func New() *Store {
	return &Store{sessions: make(map[string]sessionstore.Session)}
}

// Create inserts the row. Returns ErrAlreadyExists when ID is taken.
func (s *Store) Create(_ context.Context, sess sessionstore.Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[sess.ID]; ok {
		return sessionstore.ErrAlreadyExists
	}
	if sess.CreatedAt.IsZero() {
		sess.CreatedAt = time.Now().UTC()
	}
	if sess.UpdatedAt.IsZero() {
		sess.UpdatedAt = sess.CreatedAt
	}
	s.sessions[sess.ID] = sess
	return nil
}

// Get returns the row keyed by (tenant, id). Returns ErrNotFound
// when the row is missing OR when the row's tenant_id does not match
// — cross-tenant misses look identical to "doesn't exist" by
// design (§4.2 tenant isolation).
func (s *Store) Get(_ context.Context, tenantID, id string) (sessionstore.Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	row, ok := s.sessions[id]
	if !ok || row.TenantID != tenantID {
		return sessionstore.Session{}, sessionstore.ErrNotFound
	}
	return row, nil
}

// Update applies mutate to the row in-place under the store lock,
// then writes the resulting row back. Returns ErrNotFound when the
// row is missing or tenant-mismatched.
//
// UpdatedAt strictly advances on every successful Update — when the
// host wall-clock has not advanced since the prior write (common on
// fast machines), the new timestamp is clamped to the prior
// UpdatedAt + 1ns so callers can rely on strict monotonicity for
// change-detection and watch streams.
func (s *Store) Update(_ context.Context, tenantID, id string, mutate func(*sessionstore.Session) error) (sessionstore.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.sessions[id]
	if !ok || row.TenantID != tenantID {
		return sessionstore.Session{}, sessionstore.ErrNotFound
	}
	prevUpdatedAt := row.UpdatedAt
	if err := mutate(&row); err != nil {
		return sessionstore.Session{}, err
	}
	row.UpdatedAt = monotonicNext(prevUpdatedAt, time.Now().UTC())
	s.sessions[id] = row
	return row, nil
}

// monotonicNext returns now clamped to prev+1ns when the wall-clock
// has not advanced past prev. Used so UpdatedAt strictly advances on
// every Update.
func monotonicNext(prev, now time.Time) time.Time {
	if !now.After(prev) {
		return prev.Add(time.Nanosecond)
	}
	return now.UTC()
}

// List returns every row for the tenant in created-at descending
// order, after applying the supplied filter.
func (s *Store) List(_ context.Context, tenantID string, f sessionstore.ListFilter) ([]sessionstore.Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]sessionstore.Session, 0)
	for _, row := range s.sessions {
		if row.TenantID != tenantID {
			continue
		}
		if f.State != "" && row.State != f.State {
			continue
		}
		if f.RuntimeRef != "" && row.RuntimeRef != f.RuntimeRef {
			continue
		}
		if f.FailureClass != "" && row.FailureClass != f.FailureClass {
			continue
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// Delete removes the row. Returns ErrNotFound when missing or
// tenant-mismatched.
func (s *Store) Delete(_ context.Context, tenantID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.sessions[id]
	if !ok || row.TenantID != tenantID {
		return sessionstore.ErrNotFound
	}
	delete(s.sessions, id)
	return nil
}

// DeleteByUser implements Store — the §12.8 GDPR-erasure adapter.
func (s *Store) DeleteByUser(_ context.Context, tenantID, userID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	deleted := 0
	for id, row := range s.sessions {
		if row.TenantID == tenantID && row.UserID == userID {
			delete(s.sessions, id)
			deleted++
		}
	}
	return deleted, nil
}

// DeleteByTenant implements the §12.2.1 / §14.10 mandatory-erasure
// interface. Removes every session belonging to tenantID.
func (s *Store) DeleteByTenant(_ context.Context, tenantID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	deleted := 0
	for id, row := range s.sessions {
		if row.TenantID == tenantID {
			delete(s.sessions, id)
			deleted++
		}
	}
	return deleted, nil
}
