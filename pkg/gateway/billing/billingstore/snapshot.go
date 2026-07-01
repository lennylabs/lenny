// SPDX-License-Identifier: MIT

package billingstore

import "encoding/json"

// ExportState and ImportState let the §17.4 Source-Mode embedded-SQLite
// durability layer (pkg/gateway/sqlitestore) snapshot and restore the
// in-memory billing-event ledger across a process restart. Only the
// per-tenant event map is serialized; the injected clock is left intact.
//
// spec: §17.4 line 199 — embedded SQLite for session and metadata storage.
func (m *Memory) ExportState() ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return json.Marshal(m.events)
}

// ImportState replaces the ledger's contents with a snapshot produced by
// ExportState. A nil or empty snapshot resets the ledger to empty.
func (m *Memory) ImportState(data []byte) error {
	e := make(map[string][]Event)
	if len(data) > 0 {
		if err := json.Unmarshal(data, &e); err != nil {
			return err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = e
	return nil
}
