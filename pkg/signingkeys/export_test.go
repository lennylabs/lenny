// SPDX-License-Identifier: MIT

package signingkeys

// DeleteKey removes the purpose's key keyID from m, as an operator who
// deletes a row of platform_signing_keys by hand does. It exists for the
// tests of what such a deletion does to the replicas.
func (m *MemoryStore) DeleteKey(purpose, keyID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := m.rows[purpose]
	kept := rows[:0]
	for _, r := range rows {
		if r.KeyID != keyID {
			kept = append(kept, r)
		}
	}
	m.rows[purpose] = kept
}
