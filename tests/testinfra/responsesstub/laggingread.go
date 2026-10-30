// SPDX-License-Identifier: MIT

package responsesstub

import (
	"context"
	"sync"

	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// LaggingReadStore wraps a session store so that Get models a §12.3 read
// replica that applies the primary's writes in commit order and is a fixed
// number of committed writes behind it. Get answers from the replica's copy,
// so the newest committed Creates and Updates, as many as the lag, are not
// yet visible to it, while every earlier write is. GetPrimary and every write reach the wrapped store and
// see the current row. Writes other than Create and Update pass through
// unmodeled. A test that drives the Open Responses handler through it
// observes whether a decision that turns a miss into a fail-closed 404 reads
// the primary.
//
// spec: §12.3 (Postgres HA Requirements); §15 (Open Responses conversation
// sessions)
type LaggingReadStore struct {
	sessionstore.Store

	mu      sync.Mutex
	replica map[string]sessionstore.Session
	lag     int
	pending []lagWrite
}

// lagWrite is a committed write the replica has not applied yet.
type lagWrite struct {
	key string
	row sessionstore.Session
}

// NewLaggingReadStore wraps inner with a replica one committed write behind.
func NewLaggingReadStore(inner sessionstore.Store) *LaggingReadStore {
	return NewLaggingReadStoreBehind(inner, 1)
}

// NewLaggingReadStoreBehind wraps inner with a replica that has not applied
// the newest committed writes, as many as writes. A lag below one is one.
func NewLaggingReadStoreBehind(inner sessionstore.Store, writes int) *LaggingReadStore {
	if writes < 1 {
		writes = 1
	}
	return &LaggingReadStore{Store: inner, replica: map[string]sessionstore.Session{}, lag: writes}
}

// lagKey keys a row by tenant and id.
func lagKey(tenantID, id string) string { return tenantID + "/" + id }

// Get returns the row as the lagging replica holds it.
func (s *LaggingReadStore) Get(_ context.Context, tenantID, id string) (sessionstore.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.replica[lagKey(tenantID, id)]
	if !ok {
		return sessionstore.Session{}, sessionstore.ErrNotFound
	}
	return row, nil
}

// GetPrimary returns the current row from the wrapped store's primary.
func (s *LaggingReadStore) GetPrimary(ctx context.Context, tenantID, id string) (sessionstore.Session, error) {
	return sessionstore.ReadPrimary(ctx, s.Store, 0, tenantID, id)
}

// Create creates the row on the wrapped store and queues it for the replica.
func (s *LaggingReadStore) Create(ctx context.Context, row sessionstore.Session) error {
	if err := s.Store.Create(ctx, row); err != nil {
		return err
	}
	s.commit(row)
	return nil
}

// Update applies mutate on the wrapped store and, when it commits, queues the
// updated row for the replica.
func (s *LaggingReadStore) Update(ctx context.Context, tenantID, id string, mutate func(*sessionstore.Session) error) (sessionstore.Session, error) {
	out, err := s.Store.Update(ctx, tenantID, id, mutate)
	if err == nil {
		s.commit(out)
	}
	return out, err
}

// commit queues row and applies to the replica, in commit order, every
// queued write older than the lag.
func (s *LaggingReadStore) commit(row sessionstore.Session) {
	row.Responses = append([]sessionstore.ConversationResponse(nil), row.Responses...)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = append(s.pending, lagWrite{key: lagKey(row.TenantID, row.ID), row: row})
	for len(s.pending) > s.lag {
		s.replica[s.pending[0].key] = s.pending[0].row
		s.pending = s.pending[1:]
	}
}
