// SPDX-License-Identifier: MIT

package memstore

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// TranscriptCopier copies the transcript of sourceSessionID into
// destSessionID. transcriptstore.Memory.CopySession has this signature.
type TranscriptCopier func(ctx context.Context, tenantID, sourceSessionID, destSessionID string) error

// WithCreationMarkers wires the tenant and user rules of the creation fence.
// Create, CreateReserved, CreateWithTranscript, and Reserve call read under
// the store's lock, before the insert, and refuse with
// *sessionstore.CreationRefusal as CreationMarkers.Refusal states. Without it
// those rules admit every insert; the parent rule (fenceLocked) runs either
// way.
//
// Holding the lock across the read and the insert gives the in-memory store
// the serialization the Postgres FOR SHARE read gives: a drain lists under
// the same lock, so a create either inserts before the listing or reads a
// marker that committed before it. read takes the tenant and user stores'
// locks while this store's lock is held. No closure the gateway passes to
// those stores' Update touches the session store, so the order admits no
// cycle.
//
// A missing tenant row (TenantFound false) is reachable only here. In
// Postgres the sessions.tenant_id foreign key rejects that insert first.
// spec: §12.8 (Lifecycle fence and drain).
func WithCreationMarkers(read sessionstore.MarkerReader) Option {
	return func(s *Store) { s.markers = read }
}

// WithTranscriptCopier wires the copy CreateWithTranscript runs under the
// store's lock, after every check Create makes and before the row is
// stored, so a copy error stores nothing. The lock order is this store's
// lock, then the transcript store's lock, which no transcript store method
// reverses.
// spec: §12.8 (Lifecycle fence and drain), §15.1 (REST API).
func WithTranscriptCopier(copyFn TranscriptCopier) Option {
	return func(s *Store) { s.copyTranscript = copyFn }
}

// reservation is one session-ID reservation. The exported fields are the
// Source Mode snapshot encoding.
type reservation struct {
	TenantID  string    `json:"tenantId"`
	SessionID string    `json:"sessionId"`
	UserID    string    `json:"userId"`
	CreatedAt time.Time `json:"createdAt"`
}

func reservationKey(tenantID, sessionID string) string { return tenantID + "/" + sessionID }

// fenceLocked runs the creation fence. Callers hold s.mu. Without a marker
// reader the tenant and user rules admit, and the parent rule still runs
// when checkParent is true: a delegated child is refused when its parent
// row in this store is terminal or absent, and an empty parentSessionID
// names no row. Reading the parent under the same lock its terminal Update
// takes gives the serialization the Postgres FOR SHARE read gives. Reserve
// passes checkParent false, so a reservation runs only the tenant and user
// rules.
// spec: §12.8 (Lifecycle fence and drain), §8.2 (Parent freshness).
func (s *Store) fenceLocked(ctx context.Context, tenantID, ownerUserID string, delegatedChild, checkParent bool, parentSessionID string) error {
	var m sessionstore.CreationMarkers
	if s.markers != nil {
		read, err := s.markers(ctx, tenantID, ownerUserID)
		if err != nil {
			return err
		}
		m = read
	}
	m.IsDelegatedChild = delegatedChild
	if delegatedChild && checkParent {
		m.ParentSessionID = parentSessionID
		m.ParentTerminal = s.parentTerminalLocked(tenantID, parentSessionID)
	}
	if ref := m.Refusal(tenantID, ownerUserID); ref != nil {
		return ref
	}
	return nil
}

// parentTerminalLocked reports whether the parent session row is terminal or
// absent from tenantID. An empty parentSessionID names no row. Callers hold
// s.mu.
// spec: §12.8 (Lifecycle fence and drain).
func (s *Store) parentTerminalLocked(tenantID, parentSessionID string) bool {
	if parentSessionID == "" {
		return true
	}
	parent, ok := s.sessions[parentSessionID]
	return !ok || parent.TenantID != tenantID || session.IsTerminal(parent.State)
}

// deleteOwnReservationLocked deletes the reservation whose ID and owner match
// sess and reports whether it deleted one. Callers hold s.mu.
func (s *Store) deleteOwnReservationLocked(sess sessionstore.Session) bool {
	if sess.UserID == "" {
		return false
	}
	k := reservationKey(sess.TenantID, sess.ID)
	r, ok := s.reservations[k]
	if !ok || r.UserID != sess.UserID {
		return false
	}
	delete(s.reservations, k)
	return true
}

// CreateReserved implements sessionstore.Store. A row with an owner whose
// reservation is gone is refused after the fence admits it.
// spec: §12.8 (Lifecycle fence and drain).
func (s *Store) CreateReserved(ctx context.Context, sess sessionstore.Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.createLocked(ctx, sess, func(row sessionstore.Session) error {
		if row.UserID == "" {
			return nil
		}
		r, ok := s.reservations[reservationKey(row.TenantID, row.ID)]
		if !ok || r.UserID != row.UserID {
			return sessionstore.ReservationEndedRefusal(row.UserID)
		}
		return nil
	})
}

// CreateWithTranscript implements sessionstore.Store.
// spec: §12.8 (Lifecycle fence and drain), §15.1 (REST API).
func (s *Store) CreateWithTranscript(ctx context.Context, sess sessionstore.Session, transcriptSourceID string) error {
	if s.copyTranscript == nil {
		return sessionstore.ErrTranscriptCopierUnwired
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.createLocked(ctx, sess, func(row sessionstore.Session) error {
		if err := s.copyTranscript(ctx, row.TenantID, transcriptSourceID, row.ID); err != nil {
			return fmt.Errorf("copy transcript %s to %s: %w", transcriptSourceID, row.ID, err)
		}
		return nil
	})
}

// Reserve implements sessionstore.Store.
// spec: §12.8 (Lifecycle fence and drain).
func (s *Store) Reserve(ctx context.Context, tenantID, ownerUserID, sessionID string, delegatedChild bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fenceLocked(ctx, tenantID, ownerUserID, delegatedChild, false, ""); err != nil {
		return err
	}
	if ownerUserID == "" {
		return nil
	}
	k := reservationKey(tenantID, sessionID)
	if _, ok := s.reservations[k]; ok {
		return sessionstore.ErrAlreadyExists
	}
	s.reservations[k] = reservation{
		TenantID: tenantID, SessionID: sessionID, UserID: ownerUserID, CreatedAt: s.now().UTC(),
	}
	return nil
}

// ReleaseReservation implements sessionstore.Store.
// spec: §12.8 (Lifecycle fence and drain).
func (s *Store) ReleaseReservation(_ context.Context, tenantID, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.reservations, reservationKey(tenantID, sessionID))
	return nil
}

// ReservedSessionIDs implements sessionstore.Store, oldest reservation first.
// spec: §12.8 (Lifecycle fence and drain).
func (s *Store) ReservedSessionIDs(_ context.Context, tenantID, userID string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var rs []reservation
	for _, r := range s.reservations {
		if r.TenantID == tenantID && r.UserID == userID {
			rs = append(rs, r)
		}
	}
	sort.Slice(rs, func(i, j int) bool {
		if !rs[i].CreatedAt.Equal(rs[j].CreatedAt) {
			return rs[i].CreatedAt.Before(rs[j].CreatedAt)
		}
		return rs[i].SessionID < rs[j].SessionID
	})
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.SessionID
	}
	return out, nil
}
