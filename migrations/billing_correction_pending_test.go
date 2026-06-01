// SPDX-License-Identifier: MIT

package migrations

import (
	"strings"
	"testing"
)

// spec: §11.2.1 "Category 2 — Operator-initiated manual corrections" —
// the dual-control workflow persists each request in a
// billing_correction_pending state with a state lifecycle and an
// approval_request_id. Migration 0104 must create the durable registry
// table with the four-state CHECK constraint so an out-of-enum state
// can never be written, and grant lenny_app the full DML the mutable
// (pending → approved | rejected | expired) registry needs. F-11.2.11.
func TestBillingCorrectionPendingMigration_spec_11_2_1(t *testing.T) {
	b, err := FS.ReadFile("0104_billing_correction_pending.up.sql")
	if err != nil {
		t.Fatalf("read migration 0104: %v", err)
	}
	sql := string(b)
	for _, want := range []string{
		"CREATE TABLE billing_correction_pending",
		"REFERENCES tenants(id)",
		"CHECK (state IN ('pending', 'approved', 'rejected', 'expired'))",
		"GRANT SELECT, INSERT, UPDATE, DELETE ON billing_correction_pending TO lenny_app",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("migration 0104 missing %q", want)
		}
	}

	down, err := FS.ReadFile("0104_billing_correction_pending.down.sql")
	if err != nil {
		t.Fatalf("read migration 0104 down: %v", err)
	}
	if !strings.Contains(string(down), "DROP TABLE IF EXISTS billing_correction_pending") {
		t.Error("migration 0104 down must drop billing_correction_pending")
	}
}
