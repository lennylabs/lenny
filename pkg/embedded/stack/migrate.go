// SPDX-License-Identifier: MIT

package stack

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"sort"
	"strconv"
	"strings"

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
// extension. When pgvector is unavailable, the §9.4 semantic-memory
// migration cannot apply. applyMigrations detects this, applies every
// migration up to the one before the first pgvector-dependent
// migration, and skips the rest. The embedded stack then runs without
// §9.4 semantic search; every other feature is fully migrated. out
// receives a one-line note when the pgvector-dependent migrations are
// skipped.
func applyMigrations(dsn string, out io.Writer) error {
	db, err := openSQL(dsn)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	m, closeFn, err := buildMigrator(db)
	if err != nil {
		return err
	}
	defer closeFn()

	if pgvectorAvailable(db) {
		if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
			return fmt.Errorf("embedded migrate: apply schema: %w", err)
		}
		return nil
	}

	// pgvector is unavailable. Migrate to the highest version that
	// does not depend on pgvector.
	target, ok, err := highestNonPgvectorVersion()
	if err != nil {
		return err
	}
	if !ok {
		// No migration depends on pgvector: apply everything.
		if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
			return fmt.Errorf("embedded migrate: apply schema: %w", err)
		}
		return nil
	}
	if err := m.Migrate(target); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("embedded migrate: apply schema up to v%d: %w", target, err)
	}
	fmt.Fprintln(out, "lenny up: note: the embedded Postgres bundle lacks pgvector; "+
		"the §9.4 semantic-memory migrations are skipped and semantic search is unavailable")
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
func pgvectorAvailable(db *sql.DB) bool {
	var available bool
	err := db.QueryRow(
		`SELECT EXISTS (SELECT 1 FROM pg_available_extensions WHERE name = 'vector')`,
	).
		Scan(&available)
	return err == nil && available
}

// highestNonPgvectorVersion scans the embedded migration files and
// returns the highest version number that precedes the first
// pgvector-dependent migration. ok is false when no migration depends
// on pgvector.
func highestNonPgvectorVersion() (version uint, ok bool, err error) {
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return 0, false, fmt.Errorf("embedded migrate: list migrations: %w", err)
	}
	type upFile struct {
		version uint
		pgvec   bool
	}
	var ups []upFile
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		v, perr := parseMigrationVersion(name)
		if perr != nil {
			return 0, false, perr
		}
		raw, rerr := migrations.FS.ReadFile(name)
		if rerr != nil {
			return 0, false, fmt.Errorf("embedded migrate: read %s: %w", name, rerr)
		}
		ups = append(ups, upFile{version: v, pgvec: dependsOnPgvector(raw)})
	}
	sort.Slice(ups, func(i, j int) bool { return ups[i].version < ups[j].version })

	firstPgvec := -1
	for i, u := range ups {
		if u.pgvec {
			firstPgvec = i
			break
		}
	}
	if firstPgvec <= 0 {
		// No pgvector migration, or the first migration itself depends
		// on it (which the schema never does).
		return 0, false, nil
	}
	return ups[firstPgvec-1].version, true, nil
}

// parseMigrationVersion extracts the leading numeric version from a
// migration file name of the form NNNN_description.up.sql.
func parseMigrationVersion(name string) (uint, error) {
	idx := strings.IndexByte(name, '_')
	if idx <= 0 {
		return 0, fmt.Errorf("embedded migrate: migration %q has no version prefix", name)
	}
	v, err := strconv.ParseUint(name[:idx], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("embedded migrate: migration %q has a non-numeric version: %w", name, err)
	}
	return uint(v), nil
}

// dependsOnPgvector reports whether a migration's SQL requires the
// pgvector extension. It matches the vector extension, the vector
// column type, and the ivfflat / hnsw index access methods pgvector
// provides.
func dependsOnPgvector(sql []byte) bool {
	s := strings.ToLower(string(sql))
	return strings.Contains(s, "extension if not exists vector") ||
		strings.Contains(s, "extension vector") ||
		strings.Contains(s, "vector(") ||
		strings.Contains(s, "using ivfflat") ||
		strings.Contains(s, "using hnsw")
}
