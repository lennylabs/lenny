// SPDX-License-Identifier: MIT

package stack

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	embpostgres "github.com/lennylabs/lenny/pkg/embedded/postgres"
)

func TestParseMigrationVersion(t *testing.T) {
	cases := map[string]uint{
		"0001_init.up.sql":         1,
		"0044_agent_memory.up.sql": 44,
		"0123_something.down.sql":  123,
	}
	for name, want := range cases {
		got, err := parseMigrationVersion(name)
		if err != nil {
			t.Errorf("parseMigrationVersion(%q): %v", name, err)
			continue
		}
		if got != want {
			t.Errorf("parseMigrationVersion(%q) = %d, want %d", name, got, want)
		}
	}
	if _, err := parseMigrationVersion("noversion.up.sql"); err == nil {
		t.Error("expected an error for a name without a version prefix")
	}
}

func TestDependsOnPgvector(t *testing.T) {
	pgvec := []string{
		"CREATE EXTENSION IF NOT EXISTS vector;",
		"ALTER TABLE t ADD COLUMN e vector(256);",
		"CREATE INDEX i ON t USING ivfflat (e vector_cosine_ops);",
		"CREATE INDEX i ON t USING hnsw (e vector_l2_ops);",
	}
	for _, sql := range pgvec {
		if !dependsOnPgvector([]byte(sql)) {
			t.Errorf("dependsOnPgvector(%q) = false, want true", sql)
		}
	}
	plain := []string{
		"CREATE TABLE sessions (id uuid PRIMARY KEY);",
		"ALTER TABLE tenants ADD COLUMN name text;",
	}
	for _, sql := range plain {
		if dependsOnPgvector([]byte(sql)) {
			t.Errorf("dependsOnPgvector(%q) = true, want false", sql)
		}
	}
}

func TestHighestNonPgvectorVersion(t *testing.T) {
	// The embedded migration set includes the §9.4 pgvector migration,
	// so a non-pgvector ceiling must be reported.
	version, ok, err := highestNonPgvectorVersion()
	if err != nil {
		t.Fatalf("highestNonPgvectorVersion: %v", err)
	}
	if !ok {
		t.Fatal("expected a pgvector-dependent migration in the embedded set")
	}
	if version == 0 {
		t.Error("expected a non-zero non-pgvector ceiling version")
	}
}

// TestApplyMigrations brings up an embedded Postgres and applies the
// full §18.5 schema, then confirms the sessions table the gateway's
// startup probe checks for is present. It downloads the PostgreSQL
// bundle, so it is skipped under -short.
func TestApplyMigrations(t *testing.T) {
	if testing.Short() {
		t.Skip("downloads the PostgreSQL bundle; skipped under -short")
	}
	pg := embpostgres.New(embpostgres.Config{
		DataDir:      t.TempDir(),
		Port:         15498,
		Database:     "lenny",
		Username:     "lenny",
		Password:     "lenny",
		StartTimeout: 3 * time.Minute,
	})
	if err := pg.Start(); err != nil {
		t.Fatalf("embedded postgres Start: %v", err)
	}
	defer func() { _ = pg.Stop() }()

	dsn := pg.DSN()
	if err := applyMigrations(dsn, io.Discard); err != nil {
		t.Fatalf("applyMigrations: %v", err)
	}
	// applyMigrations is idempotent: a second run reports no change.
	if err := applyMigrations(dsn, io.Discard); err != nil {
		t.Fatalf("second applyMigrations: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()
	// The gateway's startup schema probe checks for the sessions
	// table; a complete migration run must create it.
	var exists bool
	err = pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables
		 WHERE table_schema = 'public' AND table_name = 'sessions')`).Scan(&exists)
	if err != nil {
		t.Fatalf("schema probe: %v", err)
	}
	if !exists {
		t.Error("sessions table absent after applyMigrations")
	}
}
