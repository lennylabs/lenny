// SPDX-License-Identifier: MIT

package backup_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/backup"
)

// ptime returns a pointer to t, for the optional CompletedAt field.
func ptime(t time.Time) *time.Time { return &t }

// seedBackup writes one ops_backups row into the store for the
// LastSuccessfulBackupTimes tests.
func seedBackup(t *testing.T, store *backup.MemStore, id, typ, status string, completed *time.Time) {
	t.Helper()
	if err := store.InsertBackup(context.Background(), backup.Backup{
		ID:          id,
		Type:        typ,
		Status:      status,
		StartedAt:   fixedNow,
		CompletedAt: completed,
	}); err != nil {
		t.Fatalf("InsertBackup(%s): %v", id, err)
	}
}

// spec: §25.11 line 4309 — with no successful backup the gauge source is
// empty so the caller leaves the series unset.
func TestLastSuccessfulBackupTimes_Empty_spec_25_11(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	got, err := svc.LastSuccessfulBackupTimes(context.Background())
	if err != nil {
		t.Fatalf("LastSuccessfulBackupTimes: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
}

// spec: §25.11 line 4309 — one completed backup per type reports that
// type's completion time.
func TestLastSuccessfulBackupTimes_PerType_spec_25_11(t *testing.T) {
	svc, store, _, _ := newTestService(t)
	full := fixedNow.Add(-1 * time.Hour)
	pg := fixedNow.Add(-10 * time.Minute)
	seedBackup(t, store, "b-full", "full", backup.StatusCompleted, ptime(full))
	seedBackup(t, store, "b-pg", "postgres", backup.StatusCompleted, ptime(pg))

	got, err := svc.LastSuccessfulBackupTimes(context.Background())
	if err != nil {
		t.Fatalf("LastSuccessfulBackupTimes: %v", err)
	}
	if !got["full"].Equal(full) {
		t.Errorf("full = %v, want %v", got["full"], full)
	}
	if !got["postgres"].Equal(pg) {
		t.Errorf("postgres = %v, want %v", got["postgres"], pg)
	}
}

// spec: §25.11 line 4309 — the gauge tracks the most recent successful
// completion when several backups of one type have completed.
func TestLastSuccessfulBackupTimes_TakesMax_spec_25_11(t *testing.T) {
	svc, store, _, _ := newTestService(t)
	older := fixedNow.Add(-6 * time.Hour)
	newer := fixedNow.Add(-1 * time.Hour)
	seedBackup(t, store, "b-old", "full", backup.StatusCompleted, ptime(older))
	seedBackup(t, store, "b-new", "full", backup.StatusCompleted, ptime(newer))

	got, err := svc.LastSuccessfulBackupTimes(context.Background())
	if err != nil {
		t.Fatalf("LastSuccessfulBackupTimes: %v", err)
	}
	if !got["full"].Equal(newer) {
		t.Errorf("full = %v, want the newer completion %v", got["full"], newer)
	}
}

// spec: §25.11 — only completed/verified rows with a completion time
// count; pending, running, failed, and completed-without-timestamp rows
// are excluded so the alert never reads a non-success as a success.
func TestLastSuccessfulBackupTimes_IgnoresNonSuccess_spec_25_11(t *testing.T) {
	svc, store, _, _ := newTestService(t)
	verified := fixedNow.Add(-2 * time.Hour)
	seedBackup(t, store, "b-pending", "full", backup.StatusPending, nil)
	seedBackup(t, store, "b-running", "full", backup.StatusRunning, nil)
	seedBackup(t, store, "b-failed", "full", backup.StatusFailed, ptime(fixedNow))
	// A completed row missing its completion timestamp must not count.
	seedBackup(t, store, "b-nocomplete", "full", backup.StatusCompleted, nil)
	// A verified config backup is a success.
	seedBackup(t, store, "b-config", "config", backup.StatusVerified, ptime(verified))

	got, err := svc.LastSuccessfulBackupTimes(context.Background())
	if err != nil {
		t.Fatalf("LastSuccessfulBackupTimes: %v", err)
	}
	if _, ok := got["full"]; ok {
		t.Errorf("full should be absent (no completed full with a timestamp), got %v", got["full"])
	}
	if !got["config"].Equal(verified) {
		t.Errorf("config = %v, want verified completion %v", got["config"], verified)
	}
}
