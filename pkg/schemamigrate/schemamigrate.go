// SPDX-License-Identifier: MIT

// Package schemamigrate backs the §15.1 / §24.13 schema-migration
// management surface: `GET /v1/admin/schema/migrations/status` and
// `POST /v1/admin/schema/migrations/{version}/down`. It wraps the
// embedded numbered Postgres migrations (the same set lenny-migrate
// applies) so an operator can read the migration state and roll back a
// partially applied migration through the supported admin interface
// without direct database access.
//
// spec: §10.5 line 417; §15.1 lines 891-892; §24.13 lines 150-151.
package schemamigrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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

// Errors surfaced to the admin handler so it can map them to §15.1 HTTP
// status codes.
var (
	// ErrNoVersion reports that no migration has been applied, so there
	// is nothing to roll back.
	ErrNoVersion = errors.New("schemamigrate: no migration applied")
	// ErrVersionMismatch reports that the requested down-migration
	// version is not the most recently applied version. §24.13 line 151
	// scopes `migrate down` to reversing the current (most-recent)
	// migration only.
	ErrVersionMismatch = errors.New("schemamigrate: version is not the most recently applied migration")
)

// Phase is the §15.1 expand-contract migration phase. v1 migrations are
// single-file (not split into expand-contract phases), so an applied
// migration is reported as `complete`; the richer phase values
// (`phase1_applied`, `phase2_deployed`, `phase3_applied`) are reserved
// for the phased-migration build (F-10.5.2) and share this wire shape
// so that work needs no API change. spec: §15.1 line 891.
type Phase string

const (
	// PhaseComplete is an applied, fully-contracted migration.
	PhaseComplete Phase = "complete"
)

// GateCheckResult is the §10.5 Phase 3 enforcement-gate outcome. v1 has
// no Phase 3 gated migrations, so every entry reports `not_run`.
type GateCheckResult string

const (
	// GateNotRun is the result for a migration with no Phase 3 gate.
	GateNotRun GateCheckResult = "not_run"
)

// MigrationStatus is one entry in the §15.1 status response.
type MigrationStatus struct {
	Version          uint   `json:"version"`
	Phase            string `json:"phase"`
	AppliedAt        string `json:"appliedAt,omitempty"`
	GateCheckResult  string `json:"gateCheckResult"`
	MigrationJobName string `json:"migrationJobName,omitempty"`
	// Dirty marks a migration that started but did not complete (the
	// golang-migrate `dirty` flag). §17.7 directs the operator to roll
	// such a version back with `migrate down`.
	Dirty bool `json:"dirty,omitempty"`
}

// StatusReport is the §15.1 `GET …/status` response body.
type StatusReport struct {
	CurrentVersion uint              `json:"currentVersion"`
	Dirty          bool              `json:"dirty"`
	Migrations     []MigrationStatus `json:"migrations"`
}

// DownResult is the §15.1 `POST …/{version}/down` response body. The
// fields mirror the §16.6 `platform.schema_migration_rolled_back` audit
// payload so the handler can reuse them.
type DownResult struct {
	Version               uint `json:"version"`
	DirtyFlagCleared      bool `json:"dirtyFlagCleared"`
	AdvisoryLocksReleased bool `json:"advisoryLocksReleased"`
}

// Manager runs the schema-migration read and rollback operations
// against the embedded migrations and a Postgres database.
type Manager struct {
	dsn      string
	versions []uint
}

// New returns a Manager over the embedded migrations and the Postgres
// database at dsn. It validates that the embedded migrations parse but
// does not open a database connection until a request runs.
func New(dsn string) (*Manager, error) {
	versions, err := embeddedVersions()
	if err != nil {
		return nil, err
	}
	return &Manager{dsn: dsn, versions: versions}, nil
}

// Status returns the §15.1 expand-contract migration state. It reports
// every applied migration as `complete` and surfaces a dirty version
// (a migration that did not finish) so the operator can decide whether
// to forward-fix or roll back.
func (m *Manager) Status(_ context.Context) (StatusReport, error) {
	mig, closeFn, err := m.open()
	if err != nil {
		return StatusReport{}, err
	}
	defer closeFn()

	v, dirty, err := mig.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		return buildStatus(m.versions, 0, false, false), nil
	}
	if err != nil {
		return StatusReport{}, fmt.Errorf("read schema version: %w", err)
	}
	return buildStatus(m.versions, v, true, dirty), nil
}

// Down reverses the most recently applied migration at version. §24.13
// line 151 scopes the operation to the current version; a request for
// any other version returns ErrVersionMismatch. A dirty version is
// first forced clean (clearing the dirty flag) and then its down.sql is
// applied, matching the §15.1 line 892 contract. golang-migrate manages
// its own session-scoped advisory lock for the run, so a stale lock
// from a crashed migration on a now-dead connection is already
// released; AdvisoryLocksReleased records that the run completed without
// a held lock.
func (m *Manager) Down(_ context.Context, version uint) (DownResult, error) {
	mig, closeFn, err := m.open()
	if err != nil {
		return DownResult{}, err
	}
	defer closeFn()

	cur, dirty, err := mig.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		return DownResult{}, ErrNoVersion
	}
	if err != nil {
		return DownResult{}, fmt.Errorf("read schema version: %w", err)
	}
	if cur != version {
		return DownResult{}, fmt.Errorf("%w: current=%d requested=%d", ErrVersionMismatch, cur, version)
	}

	cleared := false
	if dirty {
		// Force sets the version clean without running SQL so the
		// subsequent Steps(-1) can run the down migration. spec: §15.1
		// line 892 ("clears the dirty flag on success").
		if err := mig.Force(int(version)); err != nil {
			return DownResult{}, fmt.Errorf("clear dirty flag: %w", err)
		}
		cleared = true
	}
	if err := mig.Steps(-1); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return DownResult{}, fmt.Errorf("apply down migration: %w", err)
	}
	return DownResult{Version: version, DirtyFlagCleared: cleared, AdvisoryLocksReleased: true}, nil
}

// buildStatus composes the per-migration status list from the embedded
// version set and the current golang-migrate (version, dirty) tuple. It
// is split out from Status so the projection is unit-testable without a
// database. Only applied migrations (version ≤ current) are reported as
// active; a pending migration above the current version is omitted.
func buildStatus(versions []uint, current uint, hasCurrent, dirty bool) StatusReport {
	rep := StatusReport{Migrations: make([]MigrationStatus, 0, len(versions))}
	if hasCurrent {
		rep.CurrentVersion = current
		rep.Dirty = dirty
	}
	for _, v := range versions {
		if !hasCurrent || v > current {
			continue
		}
		entry := MigrationStatus{
			Version:         v,
			Phase:           string(PhaseComplete),
			GateCheckResult: string(GateNotRun),
		}
		if dirty && v == current {
			// A dirty current version has not completed; reflect that
			// rather than reporting it complete.
			entry.Dirty = true
		}
		rep.Migrations = append(rep.Migrations, entry)
	}
	return rep
}

// open builds a migrate.Migrate over the embedded migrations and the
// Postgres database. The returned close function releases the source and
// the database handle.
func (m *Manager) open() (*migrate.Migrate, func(), error) {
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return nil, nil, fmt.Errorf("embedded migrations: %w", err)
	}
	db, err := sql.Open("pgx", m.dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("open postgres: %w", err)
	}
	driver, err := migratepg.WithInstance(db, &migratepg.Config{})
	if err != nil {
		_ = db.Close()
		return nil, nil, fmt.Errorf("migrate driver: %w", err)
	}
	mig, err := migrate.NewWithInstance("iofs", src, "postgres", driver)
	if err != nil {
		_ = db.Close()
		return nil, nil, fmt.Errorf("migrate new: %w", err)
	}
	return mig, func() { _, _ = mig.Close() }, nil
}

// embeddedVersions returns the sorted set of migration versions parsed
// from the embedded up-migration file names (e.g. `0007_users.up.sql`
// yields 7).
func embeddedVersions() ([]uint, error) {
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return nil, fmt.Errorf("list embedded migrations: %w", err)
	}
	var out []uint
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		us := strings.IndexByte(name, '_')
		if us <= 0 {
			return nil, fmt.Errorf("malformed migration filename %q", name)
		}
		v, err := strconv.ParseUint(name[:us], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse migration version %q: %w", name, err)
		}
		out = append(out, uint(v))
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}
