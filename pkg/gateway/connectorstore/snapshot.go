// SPDX-License-Identifier: MIT

package connectorstore

import "encoding/json"

// ExportState and ImportState let the §17.4 Source-Mode embedded-SQLite
// durability layer (pkg/gateway/sqlitestore) snapshot and restore the
// in-memory connector store across a process restart.
//
// spec: §17.4 line 199 — embedded SQLite for session and metadata storage.
func (m *Memory) ExportState() ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return json.Marshal(m.connectors)
}

// ImportState replaces the store's contents with a snapshot produced by
// ExportState. A nil or empty snapshot resets the store to empty.
func (m *Memory) ImportState(data []byte) error {
	c := make(map[string]map[string]Connector)
	if len(data) > 0 {
		if err := json.Unmarshal(data, &c); err != nil {
			return err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.connectors = c
	return nil
}
