// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/ops/auditrate"
	"github.com/lennylabs/lenny/pkg/ops/backup"
	"github.com/lennylabs/lenny/pkg/ops/driftservice"
	"github.com/lennylabs/lenny/pkg/ops/opsaudit"
	"github.com/lennylabs/lenny/pkg/ops/upgradeservice"
)

// recordingAppender captures every durable append with its payload so a
// test can assert the §11.7 row each lenny-ops audit sink committed.
type recordingAppender struct {
	rows []recordedRow
}

type recordedRow struct {
	eventType string
	fields    map[string]any
}

func (a *recordingAppender) Append(_ context.Context, _, eventType string, payload json.RawMessage, _ time.Time) (audit.Row, error) {
	var fields map[string]any
	_ = json.Unmarshal(payload, &fields)
	a.rows = append(a.rows, recordedRow{eventType: eventType, fields: fields})
	return audit.Row{}, nil
}

func (a *recordingAppender) only(t *testing.T) recordedRow {
	t.Helper()
	if len(a.rows) != 1 {
		t.Fatalf("durable rows = %d, want 1", len(a.rows))
	}
	return a.rows[0]
}

// spec: F-25.4.22 — buildPlatformAuditRecorder degrades to log-only when no
// StoreRouter is wired (no Postgres), so single-process dev never panics.
func TestBuildPlatformAuditRecorder_DegradedWithoutRouter(t *testing.T) {
	rec := buildPlatformAuditRecorder(nil)
	if rec == nil {
		t.Fatal("buildPlatformAuditRecorder(nil) = nil")
	}
	if rec.Durable() {
		t.Error("recorder Durable() = true without a router")
	}
}

// spec: §25.11 line 4343 — every backup/restore transition commits a
// durable audit row; a blank outcome defaults to "success". F-25.4.22.
func TestBackupAuditSink_RecordsDurable_spec_25_11(t *testing.T) {
	app := &recordingAppender{}
	sink := backupAuditSink(opsaudit.New(app))
	sink(backup.AuditEvent{Type: "backup.created", BackupID: "b1", Actor: "alice"})

	row := app.only(t)
	if row.eventType != "backup.created" {
		t.Errorf("event_type = %q, want backup.created", row.eventType)
	}
	if row.fields["backupId"] != "b1" || row.fields["actor"] != "alice" {
		t.Errorf("fields = %v, missing backupId/actor", row.fields)
	}
	if row.fields["outcome"] != "success" {
		t.Errorf("outcome = %v, want success (default)", row.fields["outcome"])
	}
}

// spec: §25.4 line 2415 — each escalation flush commits a durable
// remediation.escalation_persisted row. F-25.4.22.
func TestEscalationAuditSink_RecordsDurable_spec_25_4_2415(t *testing.T) {
	app := &recordingAppender{}
	sink := escalationAuditSink{recorder: opsaudit.New(app)}
	sink.EscalationPersisted(context.Background(), "e1", "buffered-memory", "durable-postgres", 42)

	row := app.only(t)
	if row.eventType != "remediation.escalation_persisted" {
		t.Errorf("event_type = %q", row.eventType)
	}
	if row.fields["escalationId"] != "e1" || row.fields["sourceTier"] != "buffered-memory" ||
		row.fields["destTier"] != "durable-postgres" {
		t.Errorf("fields = %v, missing tier transition", row.fields)
	}
}

// spec: §25.10 line 3871 — each drift event commits a durable audit row
// carrying the actor and merged detail fields. F-25.4.22.
func TestDriftAuditSink_RecordsDurable_spec_25_10(t *testing.T) {
	app := &recordingAppender{}
	sink := driftAuditSink{recorder: opsaudit.New(app)}
	sink.Emit(driftservice.AuditEvent{
		Type:    "drift.reconcile_applied",
		Actor:   "bob",
		Details: map[string]any{"resourceType": "Deployment"},
	})

	row := app.only(t)
	if row.eventType != "drift.reconcile_applied" {
		t.Errorf("event_type = %q", row.eventType)
	}
	if row.fields["actor"] != "bob" || row.fields["resourceType"] != "Deployment" {
		t.Errorf("fields = %v, missing actor/detail", row.fields)
	}
}

// spec: §16.7 platform-upgrade events — each lifecycle transition commits
// a durable audit row with the phase transition. F-25.4.22.
func TestUpgradeAuditSink_RecordsDurable_spec_16_7(t *testing.T) {
	app := &recordingAppender{}
	sink := upgradeAuditSink(opsaudit.New(app))
	sink(upgradeservice.AuditEvent{
		Type:          "platform.upgrade_progressed",
		OperationID:   "upgrade-1",
		OldPhase:      "draining",
		NewPhase:      "upgrading",
		TargetVersion: "v1.2.3",
		At:            time.Now(),
	})

	row := app.only(t)
	if row.eventType != "platform.upgrade_progressed" {
		t.Errorf("event_type = %q", row.eventType)
	}
	if row.fields["operationId"] != "upgrade-1" || row.fields["newPhase"] != "upgrading" ||
		row.fields["targetVersion"] != "v1.2.3" {
		t.Errorf("fields = %v, missing phase/version", row.fields)
	}
}

// spec: §25.9 — the coalesced diagnostic audit event commits a durable row
// carrying the resource and caller. F-25.4.22.
func TestDiagnosticsAudit_RecordsDurable_spec_25_9(t *testing.T) {
	app := &recordingAppender{}
	cfg := buildDiagnosticsAudit(60, opsaudit.New(app))
	cfg.Emit(auditrate.Event{
		EventType:       "diagnostics.pool_diagnosed",
		ResourceType:    "pool",
		ResourceID:      "default-gvisor",
		ServiceAccount:  "sa-probe",
		InvocationCount: 3,
	})

	row := app.only(t)
	if row.eventType != "diagnostics.pool_diagnosed" {
		t.Errorf("event_type = %q", row.eventType)
	}
	if row.fields["resourceId"] != "default-gvisor" || row.fields["serviceAccount"] != "sa-probe" {
		t.Errorf("fields = %v, missing resource/caller", row.fields)
	}
}
