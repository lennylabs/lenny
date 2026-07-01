// SPDX-License-Identifier: MIT

package memstore

import (
	"encoding/json"

	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// ExportState and ImportState let the §17.4 Source-Mode embedded-SQLite
// durability layer (pkg/gateway/sqlitestore) snapshot and restore the
// in-memory session store across a process restart. The full session
// map serializes as JSON; the domain Session type is the same value the
// Postgres backend persists, so the snapshot carries every field the
// store contract exposes.
//
// spec: §17.4 line 199 — embedded SQLite for session and metadata storage.
func (s *Store) ExportState() ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return json.Marshal(s.sessions)
}

// ImportState replaces the store's contents with a snapshot produced by
// ExportState. A nil or empty snapshot resets the store to empty.
func (s *Store) ImportState(data []byte) error {
	m := make(map[string]sessionstore.Session)
	if len(data) > 0 {
		if err := json.Unmarshal(data, &m); err != nil {
			return err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions = m
	return nil
}
