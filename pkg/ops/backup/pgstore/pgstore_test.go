// SPDX-License-Identifier: MIT

package pgstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/migrations"
	"github.com/lennylabs/lenny/pkg/ops/backup"
	"github.com/lennylabs/lenny/pkg/ops/backup/pgstore"
	embpostgres "github.com/lennylabs/lenny/tests/testinfra/embpg"
)

// newTestStore brings up an embedded Postgres, applies the §25.11 backup
// schema (migrations 0123 + 0127), and returns a connected Store. It
// downloads the PostgreSQL bundle, so it is skipped under -short.
//
// spec: §25.11 lines 3963-4295.
func newTestStore(t *testing.T) (*pgstore.Store, context.Context) {
	t.Helper()
	if testing.Short() {
		t.Skip("downloads the PostgreSQL bundle; skipped under -short")
	}
	pg := embpostgres.New(embpostgres.Config{
		DataDir:      t.TempDir(),
		Port:         15517,
		Database:     "lenny",
		Username:     "lenny",
		Password:     "lenny",
		StartTimeout: 3 * time.Minute,
	})
	if err := pg.Start(); err != nil {
		t.Fatalf("embedded postgres Start: %v", err)
	}
	t.Cleanup(func() { _ = pg.Stop() })

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, pg.DSN())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)

	for _, mig := range []string{"0123_ops_backups.up.sql", "0127_ops_restore_state_completion.up.sql"} {
		up, err := migrations.FS.ReadFile(mig)
		if err != nil {
			t.Fatalf("read %s: %v", mig, err)
		}
		if _, err := pool.Exec(ctx, string(up)); err != nil {
			t.Fatalf("apply %s: %v", mig, err)
		}
	}
	return pgstore.New(pool), ctx
}

// TestBackupRoundTrip exercises the §25.11 ops_backups create/read/list
// lifecycle, including the JSONB components column, the nullable
// completion fields, and the duration_ms display-string round-trip.
func TestBackupRoundTrip_spec_25_11(t *testing.T) {
	s, ctx := newTestStore(t)

	completed := time.Date(2026, 6, 3, 2, 5, 0, 0, time.UTC)
	expires := completed.Add(720 * time.Hour)
	b := backup.Backup{
		ID:              "bkp-1",
		Type:            string(backup.TypeFull),
		Status:          backup.StatusCompleted,
		StartedAt:       completed.Add(-90 * time.Second),
		CompletedAt:     &completed,
		SizeBytes:       4096,
		Duration:        "1.5s",
		StoragePath:     "backups/full/bkp-1/x.tar.gz.enc",
		Checksum:        "sha256:abc",
		Components:      []backup.BackupComponent{{Name: "postgres", Status: "completed", SizeBytes: 4096}},
		StartedBy:       "alice@acme.com",
		OperationID:     "op-1",
		JobID:           "lenny-backup-1",
		PlatformVersion: "v1.2.3",
		SchemaVersion:   42,
		ExpiresAt:       &expires,
	}
	if err := s.InsertBackup(ctx, b); err != nil {
		t.Fatalf("InsertBackup: %v", err)
	}
	got, err := s.GetBackup(ctx, "bkp-1")
	if err != nil {
		t.Fatalf("GetBackup: %v", err)
	}
	if got.Duration != "1.5s" {
		t.Errorf("Duration round-trip = %q, want 1.5s", got.Duration)
	}
	if got.SizeBytes != 4096 || got.Checksum != "sha256:abc" || got.SchemaVersion != 42 {
		t.Errorf("scalar round-trip mismatch: %+v", got)
	}
	if len(got.Components) != 1 || got.Components[0].Name != "postgres" {
		t.Errorf("components round-trip = %+v", got.Components)
	}
	if got.CompletedAt == nil || !got.CompletedAt.Equal(completed) {
		t.Errorf("completedAt round-trip = %v, want %v", got.CompletedAt, completed)
	}
	if got.ExpiresAt == nil || !got.ExpiresAt.Equal(expires) {
		t.Errorf("expiresAt round-trip = %v", got.ExpiresAt)
	}

	// A missing row is ErrNotFound.
	if _, err := s.GetBackup(ctx, "nope"); err != backup.ErrNotFound {
		t.Errorf("GetBackup(missing) = %v, want ErrNotFound", err)
	}
}

// TestUpdateBackupPreservesJobWrittenFields asserts the read-modify-write
// discipline: a status-only orchestrator update does not clobber the
// completion fields the lenny-backup Job pod wrote directly to Postgres.
func TestUpdateBackupPreservesJobWrittenFields_spec_25_11(t *testing.T) {
	s, ctx := newTestStore(t)

	// Insert a pending row (no completion fields), as CreateBackup does.
	if err := s.InsertBackup(ctx, backup.Backup{
		ID: "bkp-2", Type: string(backup.TypeFull), Status: backup.StatusPending,
		StartedAt: time.Now().UTC(), StartedBy: "scheduler", JobID: "",
		PlatformVersion: "v1", SchemaVersion: 1,
	}); err != nil {
		t.Fatalf("InsertBackup: %v", err)
	}

	// Simulate the Job pod completing: load, fill completion fields, store.
	row, _ := s.GetBackup(ctx, "bkp-2")
	done := time.Now().UTC().Truncate(time.Millisecond)
	row.Status = backup.StatusCompleted
	row.CompletedAt = &done
	row.SizeBytes = 8192
	row.Duration = "2s"
	row.Checksum = "sha256:def"
	if err := s.UpdateBackup(ctx, row); err != nil {
		t.Fatalf("UpdateBackup (Job completion): %v", err)
	}

	// The orchestrator later marks the row expired (read-modify-write).
	loaded, _ := s.GetBackup(ctx, "bkp-2")
	loaded.Status = backup.StatusExpired
	exp := time.Now().UTC()
	loaded.ExpiresAt = &exp
	if err := s.UpdateBackup(ctx, loaded); err != nil {
		t.Fatalf("UpdateBackup (expire): %v", err)
	}

	final, _ := s.GetBackup(ctx, "bkp-2")
	if final.Status != backup.StatusExpired {
		t.Errorf("status = %q, want expired", final.Status)
	}
	if final.SizeBytes != 8192 || final.Checksum != "sha256:def" || final.Duration != "2s" {
		t.Errorf("Job-written fields clobbered: size=%d checksum=%q duration=%q",
			final.SizeBytes, final.Checksum, final.Duration)
	}

	// Updating a missing row is ErrNotFound.
	if err := s.UpdateBackup(ctx, backup.Backup{ID: "ghost"}); err != backup.ErrNotFound {
		t.Errorf("UpdateBackup(missing) = %v, want ErrNotFound", err)
	}
}

// TestListBackupsFilterAndOrder asserts the §25.11 list filters and the
// newest-first ordering the cursor pagination relies on.
func TestListBackupsFilterAndOrder_spec_25_11(t *testing.T) {
	s, ctx := newTestStore(t)
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	insert := func(id, typ, status string, at time.Time) {
		if err := s.InsertBackup(ctx, backup.Backup{
			ID: id, Type: typ, Status: status, StartedAt: at, StartedBy: "x",
			JobID: "j", PlatformVersion: "v1", SchemaVersion: 1,
		}); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}
	insert("a", string(backup.TypeFull), backup.StatusCompleted, base)
	insert("b", string(backup.TypePostgres), backup.StatusCompleted, base.Add(time.Hour))
	insert("c", string(backup.TypeFull), backup.StatusFailed, base.Add(2*time.Hour))

	all, err := s.ListBackups(ctx, backup.BackupFilter{})
	if err != nil {
		t.Fatalf("ListBackups: %v", err)
	}
	if len(all) != 3 || all[0].ID != "c" || all[2].ID != "a" {
		t.Errorf("ordering = %v, want newest-first c,b,a", ids(all))
	}

	full, _ := s.ListBackups(ctx, backup.BackupFilter{Type: string(backup.TypeFull)})
	if len(full) != 2 {
		t.Errorf("type=full filter returned %d, want 2", len(full))
	}
	failed, _ := s.ListBackups(ctx, backup.BackupFilter{Status: backup.StatusFailed})
	if len(failed) != 1 || failed[0].ID != "c" {
		t.Errorf("status=failed filter = %v, want [c]", ids(failed))
	}
	since, _ := s.ListBackups(ctx, backup.BackupFilter{Since: base.Add(90 * time.Minute)})
	if len(since) != 1 || since[0].ID != "c" {
		t.Errorf("since filter = %v, want [c]", ids(since))
	}
}

// TestScheduleAndPolicySingletons asserts the §25.11 ops_backup_schedule
// and ops_retention_policy singletons seed defaults when absent and
// persist an edit.
func TestScheduleAndPolicySingletons_spec_25_11(t *testing.T) {
	s, ctx := newTestStore(t)

	// Absent rows seed the §25.11 defaults.
	sc, err := s.GetSchedule(ctx)
	if err != nil {
		t.Fatalf("GetSchedule: %v", err)
	}
	if sc.Full != "0 2 * * *" || sc.Postgres != "0 */6 * * *" || !sc.Enabled {
		t.Errorf("default schedule = %+v", sc)
	}
	pol, _ := s.GetPolicy(ctx)
	if pol.RetainDays != 30 || pol.RetainCount != 10 || pol.RetainMinFull != 3 {
		t.Errorf("default policy = %+v", pol)
	}

	// Edits persist and round-trip.
	if err := s.PutSchedule(ctx, backup.BackupSchedule{Full: "0 5 * * *", Postgres: "0 */3 * * *", Enabled: false}); err != nil {
		t.Fatalf("PutSchedule: %v", err)
	}
	if err := s.PutPolicy(ctx, backup.RetentionPolicy{RetainDays: 7, RetainCount: 5, RetainMinFull: 2}); err != nil {
		t.Fatalf("PutPolicy: %v", err)
	}
	sc2, _ := s.GetSchedule(ctx)
	if sc2.Full != "0 5 * * *" || sc2.Enabled {
		t.Errorf("schedule after edit = %+v", sc2)
	}
	pol2, _ := s.GetPolicy(ctx)
	if pol2.RetainDays != 7 || pol2.RetainMinFull != 2 {
		t.Errorf("policy after edit = %+v", pol2)
	}

	// A second PutSchedule upserts (no duplicate-key error).
	if err := s.PutSchedule(ctx, backup.BackupSchedule{Full: "0 1 * * *", Postgres: "0 */6 * * *", Enabled: true}); err != nil {
		t.Fatalf("PutSchedule (upsert): %v", err)
	}
}

// TestRestoreRoundTrip exercises the §25.11 ops_restore_state lifecycle
// including the shard_states JSONB map, the restore Job id, and the
// legal-hold ledger confirmation watermark (migration 0127 columns).
func TestRestoreRoundTrip_spec_25_11(t *testing.T) {
	s, ctx := newTestStore(t)

	confirmed := time.Date(2026, 6, 3, 3, 0, 0, 0, time.UTC)
	r := backup.RestoreState{
		ID:                 "rst-1",
		BackupID:           "bkp-1",
		StartedAt:          time.Now().UTC().Truncate(time.Millisecond),
		Status:             backup.RestoreStatusRunning,
		ShardStates:        map[string]backup.ShardState{"shard-a": {Status: "running"}},
		StartedBy:          "alice@acme.com",
		PreRestoreBackupID: "bkp-pre",
		JobID:              "lenny-restore-1",
		LedgerConfirmedAt:  &confirmed,
		LedgerConfirmedBy:  "carol@acme.com",
	}
	if err := s.InsertRestore(ctx, r); err != nil {
		t.Fatalf("InsertRestore: %v", err)
	}
	got, err := s.GetRestore(ctx, "rst-1")
	if err != nil {
		t.Fatalf("GetRestore: %v", err)
	}
	if got.JobID != "lenny-restore-1" || got.PreRestoreBackupID != "bkp-pre" {
		t.Errorf("restore scalar round-trip = %+v", got)
	}
	if got.ShardStates["shard-a"].Status != "running" {
		t.Errorf("shardStates round-trip = %+v", got.ShardStates)
	}
	if got.LedgerConfirmedAt == nil || !got.LedgerConfirmedAt.Equal(confirmed) || got.LedgerConfirmedBy != "carol@acme.com" {
		t.Errorf("ledger watermark round-trip = %v / %q", got.LedgerConfirmedAt, got.LedgerConfirmedBy)
	}

	// Update to completed; ListRestores filters by status.
	done := time.Now().UTC().Truncate(time.Millisecond)
	got.Status = backup.RestoreStatusCompleted
	got.CompletedAt = &done
	got.ShardStates["shard-a"] = backup.ShardState{Status: "completed"}
	if err := s.UpdateRestore(ctx, got); err != nil {
		t.Fatalf("UpdateRestore: %v", err)
	}
	running, _ := s.ListRestores(ctx, backup.RestoreFilter{Status: backup.RestoreStatusRunning})
	if len(running) != 0 {
		t.Errorf("running filter returned %d, want 0", len(running))
	}
	completed, _ := s.ListRestores(ctx, backup.RestoreFilter{Status: backup.RestoreStatusCompleted})
	if len(completed) != 1 {
		t.Errorf("completed filter returned %d, want 1", len(completed))
	}
}

// TestFailStalePending asserts the §25.11 lines 3976-3977 reconcile: a
// pending row older than the cutoff is failed with JOB_CREATE_FAILED while
// a fresh pending row and a non-pending row are untouched.
func TestFailStalePending_spec_25_11(t *testing.T) {
	s, ctx := newTestStore(t)
	now := time.Now().UTC()
	mk := func(id, status string, age time.Duration) {
		if err := s.InsertBackup(ctx, backup.Backup{
			ID: id, Type: string(backup.TypeFull), Status: status,
			StartedAt: now.Add(-age), StartedBy: "x", JobID: "", PlatformVersion: "v1", SchemaVersion: 1,
		}); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}
	mk("old-pending", backup.StatusPending, 5*time.Minute)
	mk("new-pending", backup.StatusPending, 30*time.Second)
	mk("running", backup.StatusRunning, 5*time.Minute)

	failed, err := s.FailStalePending(ctx, now.Add(-2*time.Minute))
	if err != nil {
		t.Fatalf("FailStalePending: %v", err)
	}
	if len(failed) != 1 || failed[0] != "old-pending" {
		t.Fatalf("FailStalePending = %v, want [old-pending]", failed)
	}
	stale, _ := s.GetBackup(ctx, "old-pending")
	if stale.Status != backup.StatusFailed || stale.Error != "JOB_CREATE_FAILED" {
		t.Errorf("old-pending = %q/%q, want failed/JOB_CREATE_FAILED", stale.Status, stale.Error)
	}
	fresh, _ := s.GetBackup(ctx, "new-pending")
	if fresh.Status != backup.StatusPending {
		t.Errorf("new-pending was failed prematurely: %q", fresh.Status)
	}
}

func ids(bs []backup.Backup) []string {
	out := make([]string, len(bs))
	for i, b := range bs {
		out[i] = b.ID
	}
	return out
}
