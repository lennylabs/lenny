// SPDX-License-Identifier: MIT

package migrations

import (
	"strings"
	"testing"
)

// spec: §12.5 line 309 — the artifact_store Postgres table stores
// `artifact_size_bytes` for every artifact; the §12.4 line 210 / §11.2
// storage-quota rehydration query sums that column across live rows on
// Redis restart. The column name must match the spec exactly so the
// rehydration query and the catalog SQL agree.
func TestArtifactStoreSizeColumnMatchesSpec_spec_12_5_309(t *testing.T) {
	b, err := FS.ReadFile("0049_artifact_store.up.sql")
	if err != nil {
		t.Fatalf("read migration 0049: %v", err)
	}
	sql := string(b)
	if !strings.Contains(sql, "artifact_size_bytes") {
		t.Error("migration 0049 must declare the §12.5 line 309 column artifact_size_bytes")
	}
	// The bare `size_bytes` column name diverges from the spec and breaks
	// the rehydration query; guard against a regression to it. The check is
	// word-boundary aware so it does not trip on the spec-named column.
	for _, line := range strings.Split(sql, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "size_bytes") {
			t.Errorf("migration 0049 declares the non-spec column name in line: %q", trimmed)
		}
	}
}
