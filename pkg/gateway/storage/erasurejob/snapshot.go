// SPDX-License-Identifier: MIT

package erasurejob

import (
	"encoding/json"
	"fmt"
)

// ExportState and ImportState let the §17.4 Source Mode embedded-SQLite
// layer (pkg/gateway/storage/sqlitestore) snapshot and restore the Memory
// store across a process restart. Every Job field round-trips, as the
// sqlitestore.Snapshotter contract requires. ExportState holds the store's
// lock, which each job write also holds across its restriction write, so a
// snapshot never holds a job whose flag write has not finished.
//
// spec: §17.4 (Local Development Mode: Source Mode), §12.8 (Erasure job phase field (crash
// recovery)).
func (m *Memory) ExportState() ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	data, err := json.Marshal(m.jobs)
	if err != nil {
		return nil, fmt.Errorf("erasurejob: export snapshot: %w", err)
	}
	return data, nil
}

// ImportState replaces the store's jobs with a snapshot produced by
// ExportState. A nil or empty snapshot resets the store to empty. It does
// not touch the restriction writer's rows; ReassertRestrictions applies the
// restore rule once both snapshots are loaded.
func (m *Memory) ImportState(data []byte) error {
	jobs := make(map[string]Job)
	if len(data) > 0 {
		if err := json.Unmarshal(data, &jobs); err != nil {
			return fmt.Errorf("erasurejob: import snapshot: %w", err)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.jobs = jobs
	return nil
}
