// SPDX-License-Identifier: MIT

package schemamigrate

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/lennylabs/lenny/migrations"
	embpostgres "github.com/lennylabs/lenny/tests/testinfra/embpg"
)

// setupPhaseDB brings up an embedded Postgres, applies migration 0134
// (the schema_migration_phase table), and returns a connected *sql.DB
// plus the migration DSN. It downloads the PostgreSQL bundle, so it is
// skipped under -short. spec: §24.13 line 150.
func setupPhaseDB(t *testing.T) (*sql.DB, string, context.Context) {
	t.Helper()
	if testing.Short() {
		t.Skip("downloads the PostgreSQL bundle; skipped under -short")
	}
	pg := embpostgres.New(embpostgres.Config{
		DataDir:      t.TempDir(),
		Port:         15534,
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

	db, err := sql.Open("pgx", pg.DSN())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	up, err := migrations.FS.ReadFile("0134_schema_migration_phase.up.sql")
	if err != nil {
		t.Fatalf("read 0134: %v", err)
	}
	if _, err := db.ExecContext(ctx, string(up)); err != nil {
		t.Fatalf("apply 0134: %v", err)
	}
	return db, pg.DSN(), ctx
}

// TestPhaseStoreUpsertAndList exercises the recordPhase UPSERT and the
// listPhases read against a real Postgres: a re-recorded version updates
// the phase, gate, and Job name while preserving the original applied_at.
// spec: §24.13 line 150.
func TestPhaseStoreUpsertAndList_spec_24_13_150(t *testing.T) {
	db, _, ctx := setupPhaseDB(t)

	applied := time.Date(2026, 6, 4, 9, 0, 0, 0, time.UTC)
	if err := recordPhase(ctx, db, PhaseRow{
		Version:          5,
		Phase:            string(PhaseComplete),
		GateCheckResult:  string(GateNotRun),
		MigrationJobName: "lenny-migrate-1",
		AppliedAt:        applied,
		UpdatedAt:        applied,
	}); err != nil {
		t.Fatalf("recordPhase insert: %v", err)
	}

	// Re-record version 5 a day later with a richer phase + gate result
	// and a new Job name; the UPSERT must update those fields but keep the
	// original applied_at.
	later := applied.Add(24 * time.Hour)
	if err := recordPhase(ctx, db, PhaseRow{
		Version:          5,
		Phase:            string(PhasePhase3Applied),
		GateCheckResult:  "fail:3_rows",
		MigrationJobName: "lenny-migrate-2",
		AppliedAt:        later, // should be ignored by the UPSERT
		UpdatedAt:        later,
	}); err != nil {
		t.Fatalf("recordPhase upsert: %v", err)
	}

	phases, err := listPhases(ctx, db)
	if err != nil {
		t.Fatalf("listPhases: %v", err)
	}
	got, ok := phases[5]
	if !ok {
		t.Fatalf("version 5 missing from %+v", phases)
	}
	if got.Phase != string(PhasePhase3Applied) {
		t.Errorf("phase = %q, want phase3_applied", got.Phase)
	}
	if got.GateCheckResult != "fail:3_rows" {
		t.Errorf("gate = %q, want fail:3_rows", got.GateCheckResult)
	}
	if got.MigrationJobName != "lenny-migrate-2" {
		t.Errorf("job = %q, want lenny-migrate-2", got.MigrationJobName)
	}
	if !got.AppliedAt.Equal(applied) {
		t.Errorf("applied_at = %s, want preserved %s", got.AppliedAt, applied)
	}
}

// TestRecordRunStampsAdvancedVersions verifies RecordRun UPSERTs a row for
// every version in (previous, current] and leaves earlier versions and
// pending versions untouched. spec: §24.13 line 150.
func TestRecordRunStampsAdvancedVersions_spec_24_13_150(t *testing.T) {
	db, dsn, ctx := setupPhaseDB(t)

	m := &Manager{dsn: dsn, versions: []uint{1, 2, 3, 4, 5}}
	now := time.Date(2026, 6, 4, 10, 0, 0, 0, time.UTC)
	// A Job that advanced the schema from version 2 to version 4.
	if err := m.RecordRun(ctx, 2, true, 4, "lenny-migrate-9", now); err != nil {
		t.Fatalf("RecordRun: %v", err)
	}

	phases, err := listPhases(ctx, db)
	if err != nil {
		t.Fatalf("listPhases: %v", err)
	}
	if len(phases) != 2 {
		t.Fatalf("want rows for versions 3 and 4, got %d: %+v", len(phases), phases)
	}
	for _, v := range []uint{3, 4} {
		row, ok := phases[v]
		if !ok {
			t.Errorf("version %d not recorded", v)
			continue
		}
		if row.Phase != string(PhaseComplete) || row.GateCheckResult != string(GateNotRun) {
			t.Errorf("version %d: phase=%q gate=%q", v, row.Phase, row.GateCheckResult)
		}
		if row.MigrationJobName != "lenny-migrate-9" {
			t.Errorf("version %d job = %q", v, row.MigrationJobName)
		}
	}
	for _, v := range []uint{1, 2, 5} {
		if _, ok := phases[v]; ok {
			t.Errorf("version %d should not be recorded (outside the advanced range)", v)
		}
	}
}

// TestListPhasesToleratesMissingTable verifies that a database migrated to
// a version below 0134 (no schema_migration_phase table) yields an empty
// map rather than an error, so the status surface still reports the
// synthesized projection. spec: §24.13 line 150.
func TestListPhasesToleratesMissingTable_spec_24_13_150(t *testing.T) {
	db, _, ctx := setupPhaseDB(t)

	if _, err := db.ExecContext(ctx, "DROP TABLE schema_migration_phase"); err != nil {
		t.Fatalf("drop table: %v", err)
	}
	phases, err := listPhases(ctx, db)
	if err != nil {
		t.Fatalf("listPhases on missing table should not error: %v", err)
	}
	if len(phases) != 0 {
		t.Errorf("want empty map, got %+v", phases)
	}
}
