// SPDX-License-Identifier: MIT

package partitionmaint

import (
	"strings"
	"testing"
	"time"
)

func TestCreatePartitionSQL_spec_16_4_378(t *testing.T) {
	lower := time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC)
	upper := time.Date(2026, 6, 8, 0, 0, 0, 0, time.UTC)
	sql := createPartitionSQL("session_logs", "session_logs_p20260607", lower, upper)
	for _, want := range []string{
		"CREATE TABLE IF NOT EXISTS session_logs_p20260607",
		"PARTITION OF session_logs",
		"FOR VALUES FROM ('2026-06-07T00:00:00Z') TO ('2026-06-08T00:00:00Z')",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("create SQL missing %q:\n%s", want, sql)
		}
	}
}

func TestDropPartitionSQL(t *testing.T) {
	if got := dropPartitionSQL("session_logs_p20260607"); got != "DROP TABLE IF EXISTS session_logs_p20260607" {
		t.Errorf("drop SQL = %q", got)
	}
}

// spec: §16.4 line 378 — the catch-all occupancy probe counts the
// <parent>_default partition migration 0149 attaches to each EventStore
// parent, so a write that escaped its dated partition is observable.
func TestDefaultPartitionCountSQL_spec_16_4_378(t *testing.T) {
	if got := defaultPartitionName("session_logs"); got != "session_logs_default" {
		t.Errorf("defaultPartitionName = %q, want session_logs_default", got)
	}
	if got := defaultPartitionCountSQL("session_logs_default"); got != "SELECT count(*) FROM session_logs_default" {
		t.Errorf("count SQL = %q", got)
	}
}

func TestDefaultPartitionRows_RejectsUnsafeParent(t *testing.T) {
	d := &PGDriver{} // pool nil: the ident check must fire before any query.
	if _, err := d.DefaultPartitionRows(nil, "bad name"); err == nil {
		t.Error("DefaultPartitionRows admitted an unsafe parent")
	}
}

// checkIdent is the only defense against identifier injection in the
// partition DDL, since identifiers cannot be bound parameters.
func TestCheckIdent(t *testing.T) {
	for _, ok := range []string{"session_logs", "session_logs_p20260607", "audit_log", "_x"} {
		if err := checkIdent(ok); err != nil {
			t.Errorf("checkIdent(%q) rejected a valid name: %v", ok, err)
		}
	}
	for _, bad := range []string{
		"session_logs; DROP TABLE tenants",
		"session logs",
		"Session_Logs",
		"session_logs'",
		"",
		"1table",
		"a-b",
	} {
		if err := checkIdent(bad); err == nil {
			t.Errorf("checkIdent(%q) admitted an unsafe identifier", bad)
		}
	}
}

func TestPGDriverGuards_RejectUnsafeIdents(t *testing.T) {
	d := &PGDriver{} // pool nil: the ident check must fire before any query.
	if _, err := d.ListPartitions(nil, "bad name"); err == nil {
		t.Error("ListPartitions admitted an unsafe parent")
	}
	if err := d.CreatePartition(nil, "ok_parent", "bad child", time.Now(), time.Now()); err == nil {
		t.Error("CreatePartition admitted an unsafe child")
	}
	if err := d.DropPartition(nil, "bad;child"); err == nil {
		t.Error("DropPartition admitted an unsafe child")
	}
}
