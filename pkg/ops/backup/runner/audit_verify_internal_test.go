// SPDX-License-Identifier: MIT

package runner

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/backup"
)

// spec: §25.11 line 4343, §16.7 backup.verified — RunVerify writes the
// terminal audit row on a successful verification and writes nothing on
// a verification failure (a failure is surfaced through the
// status:verification_failed transition and the §25.11 gauges, which
// have no §16.7 catalog event).

// captureAudit returns a backup.AuditSink that appends every event to
// the supplied slice so a verify test can assert on the emitted rows.
func captureAudit(out *[]backup.AuditEvent) backup.AuditSink {
	return func(ev backup.AuditEvent) { *out = append(*out, ev) }
}

func TestRunVerifyEmitsBackupVerifiedAudit_spec_16_7(t *testing.T) {
	var events []backup.AuditEvent
	rep := &fakeVerifyReporter{}
	err := RunVerify(context.Background(), VerifyConfig{
		BackupID:   "bkp-v1",
		Resolver:   fakeResolver{target: Target{BackupID: "bkp-v1", ObjectPath: "p", Checksum: sha256Hex([]byte("arc"))}},
		Downloader: fakeDownloader{data: []byte("arc")},
		Opener:     fakeOpener{dumps: [][]byte{[]byte("d0")}},
		Inspector:  fakeInspector{},
		Reporter:   rep,
		Audit:      captureAudit(&events),
	})
	if err != nil {
		t.Fatalf("RunVerify: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("emitted %d audit events, want exactly 1 (%+v)", len(events), events)
	}
	ev := events[0]
	if ev.Type != "backup.verified" {
		t.Errorf("event type = %q, want backup.verified", ev.Type)
	}
	if ev.BackupID != "bkp-v1" {
		t.Errorf("BackupID = %q, want bkp-v1", ev.BackupID)
	}
	if ev.Outcome != "success" {
		t.Errorf("Outcome = %q, want success", ev.Outcome)
	}
}

func TestRunVerifyNoAuditOnVerificationFailure_spec_16_7(t *testing.T) {
	var events []backup.AuditEvent
	rep := &fakeVerifyReporter{}
	err := RunVerify(context.Background(), VerifyConfig{
		BackupID:   "bkp-v2",
		Resolver:   fakeResolver{target: Target{BackupID: "bkp-v2", ObjectPath: "p", Checksum: "deadbeef"}},
		Downloader: fakeDownloader{data: []byte("arc")},
		Opener:     fakeOpener{},
		Inspector:  fakeInspector{},
		Reporter:   rep,
		Audit:      captureAudit(&events),
	})
	if err == nil {
		t.Fatal("RunVerify should fail on a checksum mismatch")
	}
	if rep.failedID != "bkp-v2" {
		t.Errorf("MarkVerificationFailed id = %q, want bkp-v2", rep.failedID)
	}
	// A verification failure has no §16.7 catalog event: no audit row.
	if len(events) != 0 {
		t.Errorf("emitted %d audit events on a verification failure, want 0 (%+v)", len(events), events)
	}
}

// A nil Audit sink leaves the verify path working: the status update
// still lands and no panic occurs.
func TestRunVerifyNilAuditSinkIsNoOp(t *testing.T) {
	rep := &fakeVerifyReporter{}
	err := RunVerify(context.Background(), VerifyConfig{
		BackupID:   "bkp-v3",
		Resolver:   fakeResolver{target: Target{BackupID: "bkp-v3", ObjectPath: "p", Checksum: sha256Hex([]byte("arc"))}},
		Downloader: fakeDownloader{data: []byte("arc")},
		Opener:     fakeOpener{dumps: [][]byte{[]byte("d0")}},
		Inspector:  fakeInspector{},
		Reporter:   rep,
		Audit:      nil,
	})
	if err != nil {
		t.Fatalf("RunVerify with a nil audit sink: %v", err)
	}
	if rep.verified != "bkp-v3" {
		t.Errorf("MarkVerified id = %q, want bkp-v3", rep.verified)
	}
}
