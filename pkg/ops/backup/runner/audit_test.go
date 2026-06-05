// SPDX-License-Identifier: MIT

package runner_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/backup"
	"github.com/lennylabs/lenny/pkg/ops/backup/runner"
)

// spec: §25.11 line 4343 — every backup terminal transition is audited;
// §16.7 backup.completed / backup.failed are written from the Job pod.

// recordingSink captures the §16.7 audit events the runner emits so a
// test can assert on the type, outcome, and payload the Job pod wrote.
type recordingSink struct{ events []backup.AuditEvent }

func (r *recordingSink) sink() backup.AuditSink {
	return func(ev backup.AuditEvent) { r.events = append(r.events, ev) }
}

func (r *recordingSink) only(t *testing.T, eventType string) backup.AuditEvent {
	t.Helper()
	var got []backup.AuditEvent
	for _, ev := range r.events {
		if ev.Type == eventType {
			got = append(got, ev)
		}
	}
	if len(got) != 1 {
		t.Fatalf("emitted %d %s events, want exactly 1 (all=%+v)", len(got), eventType, r.events)
	}
	return got[0]
}

func (r *recordingSink) none(t *testing.T, eventType string) {
	t.Helper()
	for _, ev := range r.events {
		if ev.Type == eventType {
			t.Fatalf("emitted an unexpected %s event: %+v", eventType, ev)
		}
	}
}

// spec: §16.7 backup.completed — a successful run writes one
// backup.completed row carrying the terminal-state payload SIEM
// consumers index on (type, sizeBytes, checksum, storagePath).
func TestRunEmitsBackupCompletedAudit(t *testing.T) {
	rec := &recordingSink{}
	result, err := runner.Run(context.Background(), runner.Config{
		BackupID: "bkp-audit-1",
		Mode:     runner.ModeFull,
		Dumper:   fakeDumper{},
		Archiver: fakeArchiver{},
		Uploader: newFakeUploader(),
		Reporter: &fakeReporter{},
		Audit:    rec.sink(),
		Now:      func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	ev := rec.only(t, "backup.completed")
	if ev.BackupID != "bkp-audit-1" {
		t.Errorf("BackupID = %q, want bkp-audit-1", ev.BackupID)
	}
	if ev.Outcome != "success" {
		t.Errorf("Outcome = %q, want success", ev.Outcome)
	}
	if ev.At != result.CompletedAt {
		t.Errorf("At = %v, want the run completion time %v", ev.At, result.CompletedAt)
	}
	if ev.Fields["storagePath"] != result.StoragePath {
		t.Errorf("storagePath field = %v, want %q", ev.Fields["storagePath"], result.StoragePath)
	}
	if ev.Fields["checksum"] != result.Checksum || result.Checksum == "" {
		t.Errorf("checksum field = %v, want %q", ev.Fields["checksum"], result.Checksum)
	}
	// A successful run writes no backup.failed row.
	rec.none(t, "backup.failed")
}

// spec: §16.7 backup.failed — a dump failure writes one backup.failed
// row with outcome=failed and the failure reason in Detail, and never a
// backup.completed row.
func TestRunEmitsBackupFailedAuditOnDumpFailure(t *testing.T) {
	rec := &recordingSink{}
	_, err := runner.Run(context.Background(), runner.Config{
		BackupID: "bkp-audit-2",
		Mode:     runner.ModePostgres,
		Dumper:   fakeDumper{pgErr: errors.New("shard 0 unreachable")},
		Archiver: fakeArchiver{},
		Uploader: newFakeUploader(),
		Reporter: &fakeReporter{},
		Audit:    rec.sink(),
	})
	if err == nil {
		t.Fatal("Run should have failed when the dump failed")
	}
	ev := rec.only(t, "backup.failed")
	if ev.BackupID != "bkp-audit-2" {
		t.Errorf("BackupID = %q, want bkp-audit-2", ev.BackupID)
	}
	if ev.Outcome != "failed" {
		t.Errorf("Outcome = %q, want failed", ev.Outcome)
	}
	if ev.Detail == "" {
		t.Error("Detail is empty, want the failure reason")
	}
	rec.none(t, "backup.completed")
}

// spec: §16.7 backup.failed — an upload failure also writes a
// backup.failed row from the corresponding terminal transition.
func TestRunEmitsBackupFailedAuditOnUploadFailure(t *testing.T) {
	rec := &recordingSink{}
	uploader := newFakeUploader()
	uploader.uploadErr = errors.New("MinIO unreachable")
	_, err := runner.Run(context.Background(), runner.Config{
		BackupID: "bkp-audit-3",
		Mode:     runner.ModeFull,
		Dumper:   fakeDumper{},
		Archiver: fakeArchiver{},
		Uploader: uploader,
		Reporter: &fakeReporter{},
		Audit:    rec.sink(),
	})
	if err == nil {
		t.Fatal("Run should have failed when the upload failed")
	}
	ev := rec.only(t, "backup.failed")
	if ev.Outcome != "failed" || ev.Detail == "" {
		t.Errorf("event = %+v, want outcome=failed with a non-empty Detail", ev)
	}
	rec.none(t, "backup.completed")
}

// spec: §16.7 backup.failed — a checksum mismatch caught before upload
// is a terminal failure and writes a backup.failed row.
func TestRunEmitsBackupFailedAuditOnChecksumMismatch(t *testing.T) {
	rec := &recordingSink{}
	_, err := runner.Run(context.Background(), runner.Config{
		BackupID: "bkp-audit-4",
		Mode:     runner.ModePostgres,
		Dumper:   fakeDumper{},
		Archiver: badChecksumArchiver{},
		Uploader: newFakeUploader(),
		Reporter: &fakeReporter{},
		Audit:    rec.sink(),
	})
	if err == nil {
		t.Fatal("Run should have failed on a checksum mismatch")
	}
	ev := rec.only(t, "backup.failed")
	if ev.Outcome != "failed" {
		t.Errorf("Outcome = %q, want failed", ev.Outcome)
	}
	rec.none(t, "backup.completed")
}

// A nil Audit sink is the cold-start posture: the run still completes
// and records its status; only the durable audit row is dropped.
func TestRunNilAuditSinkIsNoOp(t *testing.T) {
	reporter := &fakeReporter{}
	_, err := runner.Run(context.Background(), runner.Config{
		BackupID: "bkp-audit-5",
		Mode:     runner.ModeFull,
		Dumper:   fakeDumper{},
		Archiver: fakeArchiver{},
		Uploader: newFakeUploader(),
		Reporter: reporter,
		Audit:    nil,
	})
	if err != nil {
		t.Fatalf("Run with a nil audit sink: %v", err)
	}
	if reporter.completed == nil {
		t.Error("the run did not record completion with a nil audit sink")
	}
}
