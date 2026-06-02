// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/lennylabs/lenny/pkg/ops/backup"
)

// spec: §25.11 line 4309 — the backup-metrics sampler publishes the
// last successful backup completion time per type on
// lenny_backup_last_successful_timestamp so the BackupOverdue alert has
// a source.
func TestSampleBackupMetrics_PublishesPerType_spec_25_11(t *testing.T) {
	if backupLastSuccessfulTimestamp == nil {
		t.Fatal("lenny_backup_last_successful_timestamp gauge failed to register")
	}
	store := backup.NewMemStore()
	svc, err := backup.NewService(backup.Config{
		Store:    store,
		Launcher: backup.NewFakeLauncher(),
		Locker:   backup.NewMemLocker(),
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	ctx := context.Background()
	full := time.Unix(1_700_000_000, 0).UTC()
	pg := time.Unix(1_700_003_600, 0).UTC()
	for _, b := range []backup.Backup{
		{ID: "b-full", Type: "full", Status: backup.StatusCompleted, CompletedAt: &full},
		{ID: "b-pg", Type: "postgres", Status: backup.StatusVerified, CompletedAt: &pg},
		// A still-running backup must not move the gauge.
		{ID: "b-run", Type: "full", Status: backup.StatusRunning},
	} {
		if err := store.InsertBackup(ctx, b); err != nil {
			t.Fatalf("InsertBackup(%s): %v", b.ID, err)
		}
	}

	if err := sampleBackupMetrics(ctx, svc); err != nil {
		t.Fatalf("sampleBackupMetrics: %v", err)
	}
	if got, want := testutil.ToFloat64(backupLastSuccessfulTimestamp.WithLabelValues("full")), float64(full.Unix()); got != want {
		t.Errorf("full gauge = %v, want %v", got, want)
	}
	if got, want := testutil.ToFloat64(backupLastSuccessfulTimestamp.WithLabelValues("postgres")), float64(pg.Unix()); got != want {
		t.Errorf("postgres gauge = %v, want %v", got, want)
	}
}

// spec: §25.11 — with no successful backup the sampler leaves the gauge
// untouched (it does not publish a 1970 epoch that would trip
// BackupOverdue before the first backup completes).
func TestSampleBackupMetrics_NoSuccessLeavesGaugeUnset_spec_25_11(t *testing.T) {
	store := backup.NewMemStore()
	svc, err := backup.NewService(backup.Config{
		Store:    store,
		Launcher: backup.NewFakeLauncher(),
		Locker:   backup.NewMemLocker(),
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	ctx := context.Background()
	// Only an unfinished backup exists.
	if err := store.InsertBackup(ctx, backup.Backup{ID: "b-pending", Type: "config", Status: backup.StatusPending}); err != nil {
		t.Fatalf("InsertBackup: %v", err)
	}
	before := testutil.ToFloat64(backupLastSuccessfulTimestamp.WithLabelValues("config"))
	if err := sampleBackupMetrics(ctx, svc); err != nil {
		t.Fatalf("sampleBackupMetrics: %v", err)
	}
	if after := testutil.ToFloat64(backupLastSuccessfulTimestamp.WithLabelValues("config")); after != before {
		t.Errorf("config gauge moved from %v to %v without a successful backup", before, after)
	}
}
