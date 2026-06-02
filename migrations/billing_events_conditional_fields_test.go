// SPDX-License-Identifier: MIT

package migrations

import (
	"strings"
	"testing"
)

// spec: §11.2.1 — Event schema (all events): the event-type-specific
// ("for X events only") conditional fields. Migration 0113 adds them to
// billing_events as a single nullable JSONB blob (the §11.2.1 null/absent
// field contract is a sparse map keyed by the applicable field names);
// the down migration drops the column. F-11.2.12.
func TestBillingEventsConditionalFieldsMigration_spec_11_2_1(t *testing.T) {
	b, err := FS.ReadFile("0113_billing_events_conditional_fields.up.sql")
	if err != nil {
		t.Fatalf("read migration 0113: %v", err)
	}
	up := string(b)
	for _, want := range []string{
		"ALTER TABLE billing_events",
		"ADD COLUMN conditional_fields JSONB",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("migration 0113 up missing %q", want)
		}
	}
	// The column must be nullable (no NOT NULL / DEFAULT): a NULL value is
	// the §11.2.1 "not applicable" representation for events that carry no
	// event-type-specific data.
	if strings.Contains(up, "NOT NULL") {
		t.Error("migration 0113: conditional_fields must be nullable, not NOT NULL")
	}

	b, err = FS.ReadFile("0113_billing_events_conditional_fields.down.sql")
	if err != nil {
		t.Fatalf("read migration 0113 down: %v", err)
	}
	down := string(b)
	if !strings.Contains(down, "DROP COLUMN IF EXISTS conditional_fields") {
		t.Errorf("migration 0113 down missing conditional_fields drop, got: %s", down)
	}
}
