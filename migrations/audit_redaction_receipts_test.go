// SPDX-License-Identifier: MIT

package migrations

import (
	"strings"
	"testing"
)

// spec: §12.8 — migration 0160 creates the append-only
// audit_redaction_receipts table that pins every §12.8 step-14 in-place
// GDPR redaction to a signed RedactionReceipt. The receipt is the
// provenance token the §11.7 chain verifier and the §16.5
// AuditRedactionReceiptMissing alert read to distinguish an authorized
// redaction from a tamper. F-11.7.15.
func TestAuditRedactionReceiptsMigration_spec_12_8_812(t *testing.T) {
	up := mustReadMigration(t, "0160_audit_redaction_receipts.up.sql")
	for _, want := range []string{
		"CREATE TABLE IF NOT EXISTS audit_redaction_receipts",
		"receipt_id           UUID        PRIMARY KEY",
		"original_hash        BYTEA       NOT NULL",
		"new_hash             BYTEA       NOT NULL",
		"legal_basis          TEXT        NOT NULL",
		"signature_kms_key_id TEXT",
		"UNIQUE (tenant_id, sequence_number)",
		// §12.8 — the legal_basis enum is closed; an unrecognized
		// value must be rejected at write time.
		"'gdpr_art17'",
		"'gdpr_art17_with_art17_3_exception'",
		"'operator_acknowledged_override'",
		// §12.8 — grant separation: erasure inserts, app reads,
		// no role updates or deletes.
		"GRANT INSERT ON audit_redaction_receipts TO lenny_erasure",
		"GRANT SELECT ON audit_redaction_receipts TO lenny_app",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("migration 0160 up missing %q", want)
		}
	}
	// The table is append-only: no role is granted UPDATE or DELETE.
	for _, forbidden := range []string{"GRANT UPDATE", "GRANT DELETE"} {
		if strings.Contains(up, forbidden) {
			t.Errorf("migration 0160 up must not grant %q on the append-only receipt store", forbidden)
		}
	}

	down := mustReadMigration(t, "0160_audit_redaction_receipts.down.sql")
	if !strings.Contains(down, "DROP TABLE IF EXISTS audit_redaction_receipts") {
		t.Errorf("migration 0160 down must drop audit_redaction_receipts")
	}
}

// spec: §12.8 (Compliance Interfaces: RedactionReceipt schema), §16.4
// (Logging) — migration 0160 declares audit_event_id with an inline
// REFERENCES audit_log (id), and migration 0188 drops that key under its
// default name, because a receipt outlives the audit row it names: the
// retention sweep and tenant-deletion Phase 4 delete audit rows and never
// delete receipts. The drop keeps the event index and every grant, and
// the down migration re-adds the key NOT VALID so receipts whose row was
// deleted in the meantime do not block the rollback.
func TestAuditRedactionReceiptsForeignKeyDropped_spec_12_8(t *testing.T) {
	created := mustReadMigration(t, "0160_audit_redaction_receipts.up.sql")
	if !strings.Contains(created, "REFERENCES audit_log (id)") {
		t.Fatalf("migration 0160 up no longer declares the inline REFERENCES audit_log (id) that 0188 drops")
	}

	up := mustReadMigration(t, "0188_audit_redaction_receipts_drop_fk.up.sql")
	if !strings.Contains(up, "DROP CONSTRAINT IF EXISTS audit_redaction_receipts_audit_event_id_fkey") {
		t.Errorf("migration 0188 up must drop audit_redaction_receipts_audit_event_id_fkey idempotently")
	}
	for _, forbidden := range []string{"DROP INDEX", "REVOKE", "ADD CONSTRAINT", "GRANT"} {
		if strings.Contains(up, forbidden) {
			t.Errorf("migration 0188 up must change only the foreign key, found %q", forbidden)
		}
	}

	down := mustReadMigration(t, "0188_audit_redaction_receipts_drop_fk.down.sql")
	for _, want := range []string{
		"ADD CONSTRAINT audit_redaction_receipts_audit_event_id_fkey",
		"FOREIGN KEY (audit_event_id) REFERENCES audit_log (id) NOT VALID",
	} {
		if !strings.Contains(down, want) {
			t.Errorf("migration 0188 down missing %q", want)
		}
	}
}
