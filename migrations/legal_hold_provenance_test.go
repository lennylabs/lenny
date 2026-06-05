// SPDX-License-Identifier: MIT

package migrations

import (
	"strings"
	"testing"
)

// spec: §15.1 lines 864-865 — the GET /v1/admin/legal-holds list reports
// each active hold's provenance (setBy, setAt, note), and the POST note is
// required when hold is true. Migration 0145 adds the legal_hold_set_by /
// legal_hold_set_at / legal_hold_note columns to both the sessions and
// artifact_store tables so the provenance is durable. F-15.1.3.
func TestLegalHoldProvenanceMigration_spec_15_1_865(t *testing.T) {
	b, err := FS.ReadFile("0145_legal_hold_provenance.up.sql")
	if err != nil {
		t.Fatalf("read migration 0145: %v", err)
	}
	sql := string(b)
	for _, want := range []string{
		"ALTER TABLE sessions",
		"ALTER TABLE artifact_store",
		"legal_hold_set_by TEXT        NOT NULL DEFAULT ''",
		"legal_hold_set_at TIMESTAMPTZ",
		"legal_hold_note   TEXT        NOT NULL DEFAULT ''",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("migration 0145 up missing %q", want)
		}
	}
	// Both tables must gain all three columns: two ADD COLUMN statements,
	// three provenance columns each.
	if got := strings.Count(sql, "legal_hold_set_by"); got != 2 {
		t.Errorf("legal_hold_set_by added %d times, want 2 (sessions + artifact_store)", got)
	}

	down, err := FS.ReadFile("0145_legal_hold_provenance.down.sql")
	if err != nil {
		t.Fatalf("read migration 0145 down: %v", err)
	}
	d := string(down)
	for _, want := range []string{
		"DROP COLUMN IF EXISTS legal_hold_set_by",
		"DROP COLUMN IF EXISTS legal_hold_set_at",
		"DROP COLUMN IF EXISTS legal_hold_note",
	} {
		if !strings.Contains(d, want) {
			t.Errorf("migration 0145 down missing %q", want)
		}
	}
}
