// SPDX-License-Identifier: MIT

package prodsource

import "sort"

// maxHotKeys bounds the §25.6 hot-key list so a credential pool with many
// credentials does not return an unbounded analysis. spec: §25.6
// CredentialPoolDiagnosis.hotKeys.
const maxHotKeys = 5

// hotKeys returns the credential ids carrying the most active leases,
// highest first, capped at maxHotKeys. Ties break by credential id so the
// ordering is deterministic. Credentials with no leases are excluded.
// spec: §25.6 hot-key analysis. F-25.6.1.
func hotKeys(leasesByCredential map[string]int) []string {
	type entry struct {
		id    string
		count int
	}
	entries := make([]entry, 0, len(leasesByCredential))
	for id, count := range leasesByCredential {
		if count <= 0 {
			continue
		}
		entries = append(entries, entry{id: id, count: count})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].count != entries[j].count {
			return entries[i].count > entries[j].count
		}
		return entries[i].id < entries[j].id
	})
	if len(entries) > maxHotKeys {
		entries = entries[:maxHotKeys]
	}
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.id
	}
	return out
}
