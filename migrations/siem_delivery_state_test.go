// SPDX-License-Identifier: MIT

package migrations

import (
	"strings"
	"testing"
)

// spec: §12.3 line 97 — the SIEM outbox forwarder must checkpoint its
// delivery position durably in a siem_delivery_state table so a restart
// replays from the last confirmed delivery point without duplication or
// gap. Migration 0107 creates that table keyed by tenant chain with the
// per-tenant high-water mark columns, grants the gateway role DML, and
// the down migration drops it.
func TestSIEMDeliveryStateMigration_spec_12_3_97(t *testing.T) {
	b, err := FS.ReadFile("0107_siem_delivery_state.up.sql")
	if err != nil {
		t.Fatalf("read migration 0107: %v", err)
	}
	sql := string(b)
	for _, want := range []string{
		"CREATE TABLE siem_delivery_state",
		"tenant_id             TEXT        NOT NULL PRIMARY KEY",
		"last_acked_sequence   BIGINT      NOT NULL DEFAULT 0",
		"last_acked_created_at TIMESTAMPTZ",
		"GRANT SELECT, INSERT, UPDATE, DELETE ON siem_delivery_state TO lenny_app",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("migration 0107 missing %q", want)
		}
	}

	down, err := FS.ReadFile("0107_siem_delivery_state.down.sql")
	if err != nil {
		t.Fatalf("read migration 0107 down: %v", err)
	}
	if !strings.Contains(string(down), "DROP TABLE IF EXISTS siem_delivery_state") {
		t.Error("migration 0107 down must drop siem_delivery_state")
	}
}
