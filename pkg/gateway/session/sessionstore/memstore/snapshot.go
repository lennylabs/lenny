// SPDX-License-Identifier: MIT

package memstore

import (
	"encoding/json"

	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// snapshot is the Source Mode encoding of the store: the session map and
// the session-ID reservation map, exported under one lock so a reservation
// and the session row that ends it restore from the same instant.
type snapshot struct {
	Sessions     map[string]sessionstore.Session `json:"sessions"`
	Reservations map[string]reservation          `json:"reservations,omitempty"`
}

// ExportState and ImportState let the §17.4 Source-Mode embedded-SQLite
// durability layer (pkg/gateway/sqlitestore) snapshot and restore the
// in-memory session store across a process restart. The session map and the
// reservation map serialize as one JSON document; the domain Session type is
// the same value the Postgres backend persists, so the snapshot carries every
// field the store contract exposes.
//
// spec: §17.4 — embedded SQLite for session and metadata storage; §12.8
// (Lifecycle fence and drain) for the reservations.
func (s *Store) ExportState() ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return json.Marshal(snapshot{Sessions: s.sessions, Reservations: s.reservations})
}

// ImportState replaces the store's contents with a snapshot produced by
// ExportState. A nil or empty snapshot resets the store to empty.
func (s *Store) ImportState(data []byte) error {
	snap := snapshot{}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &snap); err != nil {
			return err
		}
	}
	if snap.Sessions == nil {
		snap.Sessions = make(map[string]sessionstore.Session)
	}
	if snap.Reservations == nil {
		snap.Reservations = make(map[string]reservation)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions = snap.Sessions
	s.reservations = snap.Reservations
	return nil
}
