// SPDX-License-Identifier: MIT

package migrations

import (
	"strings"
	"testing"
)

// spec: §16.4 line 378 — the EventStore session-log and stream-cursor
// tables are partitioned by time using native Postgres range
// partitioning so a background job can drop whole partitions beyond the
// retention window. Migration 0149 creates both as range-partitioned
// parents with a DEFAULT safety-net partition, the §12.3 tenant-scoped
// posture (guard trigger, RLS, grants), and the down migration drops
// them.
func TestEventStorePartitionedLogsMigration_spec_16_4_378(t *testing.T) {
	b, err := FS.ReadFile("0149_eventstore_partitioned_logs.up.sql")
	if err != nil {
		t.Fatalf("read migration 0149: %v", err)
	}
	sql := string(b)
	for _, want := range []string{
		"CREATE TABLE session_logs",
		"PARTITION BY RANGE (created_at)",
		"PRIMARY KEY (tenant_id, session_id, seq, created_at)",
		"CREATE TABLE session_logs_default PARTITION OF session_logs DEFAULT",
		"CREATE TABLE stream_cursors",
		"PRIMARY KEY (tenant_id, session_id, consumer_id, created_at)",
		"CREATE TABLE stream_cursors_default PARTITION OF stream_cursors DEFAULT",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("migration 0149 missing %q", want)
		}
	}

	// The partition key (created_at) must appear in the primary key of
	// every partitioned table; Postgres rejects the CREATE otherwise.
	if strings.Count(sql, "PARTITION BY RANGE (created_at)") != 2 {
		t.Errorf("both EventStore tables must be range-partitioned on created_at")
	}

	// §12.3 tenant-scoped posture on both tables.
	for _, tbl := range []string{"session_logs", "stream_cursors"} {
		for _, want := range []string{
			"CREATE TRIGGER lenny_tenant_guard\n    BEFORE INSERT OR UPDATE OR DELETE ON " + tbl,
			"ALTER TABLE " + tbl + " ENABLE ROW LEVEL SECURITY",
			"ALTER TABLE " + tbl + " FORCE ROW LEVEL SECURITY",
			"CREATE POLICY lenny_tenant_isolation ON " + tbl,
			"GRANT SELECT, INSERT, UPDATE, DELETE ON " + tbl + " TO lenny_app",
		} {
			if !strings.Contains(sql, want) {
				t.Errorf("migration 0149 missing %q for %s", want, tbl)
			}
		}
	}

	down, err := FS.ReadFile("0149_eventstore_partitioned_logs.down.sql")
	if err != nil {
		t.Fatalf("read migration 0149 down: %v", err)
	}
	ds := string(down)
	for _, want := range []string{
		"DROP TABLE IF EXISTS stream_cursors",
		"DROP TABLE IF EXISTS session_logs",
	} {
		if !strings.Contains(ds, want) {
			t.Errorf("migration 0149 down missing %q", want)
		}
	}
}
