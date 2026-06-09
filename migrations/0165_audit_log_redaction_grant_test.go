// SPDX-License-Identifier: MIT

package migrations

import (
	"strings"
	"testing"
)

// spec: §12.8 lines 810-827 — the step-14 in-place dead-letter redaction
// rewrites the payload and payload_canonical_json of a dead_lettered
// audit_log row. Migration 0165 grants the lenny_erasure role the
// column-scoped UPDATE it needs for that rewrite, and nothing wider: the
// hash-input columns stay outside any role's UPDATE reach.
func TestAuditLogRedactionGrantMigration_spec_12_8_810(t *testing.T) {
	up := mustReadMigration(t, "0165_audit_log_redaction_grant.up.sql")
	if !strings.Contains(up, "GRANT UPDATE (payload, payload_canonical_json) ON audit_log TO lenny_erasure") {
		t.Errorf("migration 0165 up must grant lenny_erasure UPDATE on the two payload columns")
	}
	// The grant is column-scoped and role-scoped: it must not widen to a
	// table-level UPDATE (which would trip the §11.7 item-1 startup grant
	// verification) and must not touch lenny_app.
	for _, forbidden := range []string{
		"GRANT UPDATE ON audit_log",
		"TO lenny_app",
	} {
		if strings.Contains(up, forbidden) {
			t.Errorf("migration 0165 up must not contain %q", forbidden)
		}
	}

	down := mustReadMigration(t, "0165_audit_log_redaction_grant.down.sql")
	if !strings.Contains(down, "REVOKE UPDATE (payload, payload_canonical_json) ON audit_log FROM lenny_erasure") {
		t.Errorf("migration 0165 down must revoke the redaction UPDATE grant")
	}
}
