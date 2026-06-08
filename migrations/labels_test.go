// SPDX-License-Identifier: MIT

package migrations

import (
	"strings"
	"testing"
)

// spec: §14 line 106 — session labels are filterable in GET /v1/usage and
// GET /v1/metering/events. Migrations 0152 / 0153 denormalize the
// session's labels onto usage_events / billing_events as a nullable JSONB
// column with a GIN index backing the `@>` containment filter; the down
// migrations drop the index and column. F-14.1.13.
func TestLabelsMigrations_spec_14_106(t *testing.T) {
	cases := []struct {
		name     string
		upFile   string
		downFile string
		table    string
		index    string
	}{
		{
			name:     "usage_events",
			upFile:   "0152_usage_events_labels.up.sql",
			downFile: "0152_usage_events_labels.down.sql",
			table:    "usage_events",
			index:    "idx_usage_events_labels",
		},
		{
			name:     "billing_events",
			upFile:   "0153_billing_events_labels.up.sql",
			downFile: "0153_billing_events_labels.down.sql",
			table:    "billing_events",
			index:    "idx_billing_events_labels",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := FS.ReadFile(tc.upFile)
			if err != nil {
				t.Fatalf("read %s: %v", tc.upFile, err)
			}
			up := string(b)
			for _, want := range []string{
				"ALTER TABLE " + tc.table,
				"ADD COLUMN IF NOT EXISTS labels JSONB",
				"CREATE INDEX IF NOT EXISTS " + tc.index,
				"USING GIN (labels)",
			} {
				if !strings.Contains(up, want) {
					t.Errorf("%s missing %q", tc.upFile, want)
				}
			}
			// The column must be nullable: a NULL value is the "no labels"
			// representation that never matches a non-empty containment filter.
			if strings.Contains(up, "labels JSONB NOT NULL") {
				t.Errorf("%s: labels must be nullable, not NOT NULL", tc.upFile)
			}

			b, err = FS.ReadFile(tc.downFile)
			if err != nil {
				t.Fatalf("read %s: %v", tc.downFile, err)
			}
			down := string(b)
			for _, want := range []string{
				"DROP INDEX IF EXISTS " + tc.index,
				"DROP COLUMN IF EXISTS labels",
			} {
				if !strings.Contains(down, want) {
					t.Errorf("%s missing %q", tc.downFile, want)
				}
			}
		})
	}
}
