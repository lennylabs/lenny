// SPDX-License-Identifier: MIT

package stack

import (
	"database/sql"
	"errors"
	"fmt"
	"io"

	"github.com/golang-migrate/migrate/v4"
	migratepg "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/lennylabs/lenny/migrations"
)

// openSQL opens a database/sql handle to the embedded Postgres. The
// pgx stdlib driver registers under the "pgx" name; importing the
// stdlib package for its side effect makes it available.
func openSQL(dsn string) (*sql.DB, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("embedded migrate: open postgres: %w", err)
	}
	return db, nil
}

// applyMigrations runs the §18.5 schema migrations against the
// embedded Postgres. §17.4 specifies that lenny up runs the standard
// schema-migration path against the embedded Postgres. The migrations
// are embedded in the binary, so this needs no source checkout.
//
// The stock embedded PostgreSQL 16 bundle does not ship the pgvector
// extension that the §9.4 agent_memory.embedding column (migration
// 0044) and the production §9.4 Postgres memory store depend on. When
// pgvector is unavailable, applyMigrations installs a pure-SQL pgvector
// shim (the `vector` type, the text casts, and the `<=>` operator; see
// installVectorShim) before running the migrations, then applies the
// complete migration set. The shim lets the unchanged production schema
// and the unchanged production memory store run against the embedded
// Postgres, so every migration applies and no later migration (for
// example 0077_tenant_credential_policy) is stranded. The §9.4
// semantic-search ivfflat index that needs the real C access method is
// skipped by migration 0044's own guard, so semantic ranking degrades
// to the recency-ordered substring fallback; every other feature is
// fully migrated. out receives a one-line note when the shim is used.
func applyMigrations(dsn string, out io.Writer) error {
	db, err := openSQL(dsn)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	if !pgvectorAvailable(db) {
		if err := installVectorShim(db); err != nil {
			return err
		}
		fmt.Fprintln(out, "lenny up: note: the embedded Postgres bundle lacks pgvector; "+
			"a pure-SQL vector shim is installed and §9.4 semantic search degrades to the "+
			"recency-ordered substring fallback (every other feature is fully migrated)")
	}

	m, closeFn, err := buildMigrator(db)
	if err != nil {
		return err
	}
	defer closeFn()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("embedded migrate: apply schema: %w", err)
	}
	return nil
}

// buildMigrator constructs a golang-migrate migrator over the embedded
// migration set and the given database handle. The returned closeFn
// releases the migrator's source.
func buildMigrator(db *sql.DB) (m *migrate.Migrate, closeFn func(), err error) {
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return nil, nil, fmt.Errorf("embedded migrate: open migration source: %w", err)
	}
	driver, err := migratepg.WithInstance(db, &migratepg.Config{})
	if err != nil {
		return nil, nil, fmt.Errorf("embedded migrate: build postgres driver: %w", err)
	}
	m, err = migrate.NewWithInstance("iofs", src, "postgres", driver)
	if err != nil {
		return nil, nil, fmt.Errorf("embedded migrate: build migrator: %w", err)
	}
	return m, func() { _, _ = m.Close() }, nil
}

// pgvectorAvailable reports whether the connected PostgreSQL server can
// create the pgvector extension. It probes pg_available_extensions
// rather than running CREATE EXTENSION so the check has no side effect.
// When it reports false, applyMigrations installs the pure-SQL vector
// shim (installVectorShim) before running the migrations.
func pgvectorAvailable(db *sql.DB) bool {
	var available bool
	err := db.QueryRow(
		`SELECT EXISTS (SELECT 1 FROM pg_available_extensions WHERE name = 'vector')`,
	).
		Scan(&available)
	return err == nil && available
}
