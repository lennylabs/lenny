// SPDX-License-Identifier: MIT

// Command lenny-migrate applies the numbered Postgres schema
// migrations (§18.5 Phase 1.5). The migrations are embedded in the
// binary, so a deployment runs it as a Helm pre-install / pre-upgrade
// Job without a source checkout.
//
// Usage:
//
//	lenny-migrate up                 Apply every pending migration.
//	lenny-migrate version            Print the current schema version.
//	lenny-migrate down  --break-glass --confirm   Roll every migration back (break-glass).
//	lenny-migrate goto <version> --break-glass --confirm   Migrate to an exact version (break-glass).
//
// `up` and `version` are the deploy-Job and diagnosis surface. The
// destructive `down` and `goto` operations bypass the §24.13 / §10.5
// expand-contract phase discipline (no phase tracking, no audit event,
// no confirmation contract), so they are fenced behind `--break-glass`
// + `--confirm` and are intended only for a DBA recovering a wedged
// schema directly against the database. Routine, audited rollbacks go
// through `lenny-ctl migrate down --version <N> --confirm`
// (POST /v1/admin/schema/migrations/{version}/down), which records the
// §16.8 `platform.schema_migration_rolled_back` audit event.
//
// The connection string comes from --postgres-dsn or the
// LENNY_POSTGRES_DSN environment variable.
//
// spec: §24.13 lines 150-151 (operator surface is `migrate status` and
// `migrate down --version <N> --confirm`); §10.5 (phase advancement via
// Kubernetes Jobs, destructive path gated and audited).
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/golang-migrate/migrate/v4"
	migratepg "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/lennylabs/lenny/migrations"
	"github.com/lennylabs/lenny/pkg/schemamigrate"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run executes one lenny-migrate subcommand and returns a process exit
// code. It is split out from main so a test can drive it directly.
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("lenny-migrate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dsn := fs.String("postgres-dsn", os.Getenv("LENNY_POSTGRES_DSN"),
		"Postgres connection string (or set LENNY_POSTGRES_DSN).")
	// §24.13 / §10.5: down and goto bypass the audited expand-contract
	// rollback path, so they are break-glass-only. Both flags are
	// required to run either; up and version ignore them.
	breakGlass := fs.Bool("break-glass", false,
		"acknowledge the destructive down/goto bypasses the audited §24.13 rollback path (required for down/goto).")
	confirm := fs.Bool("confirm", false,
		"confirm a destructive down/goto operation (required for down/goto).")
	// §24.13 line 150: the Kubernetes Job that applied a migration is
	// recorded in the phase-tracking table and surfaced as
	// `migrationJobName` in `lenny-ctl migrate status`. The chart wires
	// this from the downward API (metadata.labels['job-name']) so the
	// status surface attributes each applied version to its Job. Empty is
	// allowed (off-cluster / manual runs); the field is then omitted.
	jobName := fs.String("job-name", os.Getenv("LENNY_MIGRATION_JOB_NAME"),
		"name of the Kubernetes Job applying these migrations, recorded for `migrate status` (or set LENNY_MIGRATION_JOB_NAME).")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: lenny-migrate [--postgres-dsn DSN] [--break-glass --confirm] <up|version|down|goto VERSION>")
		fmt.Fprintln(stderr, "  (down and goto require --break-glass --confirm before the command; use `lenny-ctl migrate down` for audited rollbacks)")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	rest := fs.Args()
	if len(rest) == 0 {
		fs.Usage()
		return 2
	}
	cmd := rest[0]
	// §24.13 / §10.5: fence the destructive down/goto operations behind
	// --break-glass --confirm before any database connection is opened,
	// so a missing guard never touches the schema.
	if cmd == "down" || cmd == "goto" {
		if code := requireBreakGlass(cmd, *breakGlass, *confirm, stderr); code != 0 {
			return code
		}
	}
	if *dsn == "" {
		fmt.Fprintln(stderr, "lenny-migrate: --postgres-dsn is required (or set LENNY_POSTGRES_DSN)")
		return 2
	}

	m, closeMigrator, err := newMigrator(*dsn)
	if err != nil {
		fmt.Fprintf(stderr, "lenny-migrate: %v\n", err)
		return 1
	}
	defer closeMigrator()

	switch cmd {
	case "up":
		// Capture the pre-run version so the end-of-run phase write only
		// stamps the versions this Job advanced through. spec: §24.13
		// line 150 ("phase reflects the last migration Job that completed
		// successfully").
		prev, _, prevErr := m.Version()
		hadPrev := prevErr == nil
		if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
			fmt.Fprintf(stderr, "lenny-migrate: up: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, "lenny-migrate: schema is up to date")
		recordMigrationPhases(*dsn, *jobName, prev, hadPrev, m, stderr)
	case "down":
		if err := m.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
			fmt.Fprintf(stderr, "lenny-migrate: down: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, "lenny-migrate: schema rolled back")
	case "goto":
		if len(rest) != 2 {
			fmt.Fprintln(stderr, "lenny-migrate: goto requires a version argument")
			return 2
		}
		v, err := strconv.ParseUint(rest[1], 10, 64)
		if err != nil {
			fmt.Fprintf(stderr, "lenny-migrate: goto: invalid version %q\n", rest[1])
			return 2
		}
		if err := m.Migrate(uint(v)); err != nil && !errors.Is(err, migrate.ErrNoChange) {
			fmt.Fprintf(stderr, "lenny-migrate: goto %d: %v\n", v, err)
			return 1
		}
		fmt.Fprintf(stdout, "lenny-migrate: schema migrated to version %d\n", v)
	case "version":
		v, dirty, err := m.Version()
		if errors.Is(err, migrate.ErrNilVersion) {
			fmt.Fprintln(stdout, "lenny-migrate: no migrations applied")
			return 0
		}
		if err != nil {
			fmt.Fprintf(stderr, "lenny-migrate: version: %v\n", err)
			return 1
		}
		state := "clean"
		if dirty {
			state = "dirty"
		}
		fmt.Fprintf(stdout, "lenny-migrate: version %d (%s)\n", v, state)
	default:
		fmt.Fprintf(stderr, "lenny-migrate: unknown command %q\n", cmd)
		fs.Usage()
		return 2
	}
	return 0
}

// requireBreakGlass enforces the §24.13 / §10.5 fence on the
// destructive down and goto operations: both --break-glass and --confirm
// must be set before the command runs. It returns 0 when the guard is
// satisfied and a non-zero process exit code otherwise, pointing the
// operator at the audited admin-API rollback path.
//
// spec: §24.13 lines 150-151; §10.5 (rollbacks are audited).
func requireBreakGlass(cmd string, breakGlass, confirm bool, stderr io.Writer) int {
	if breakGlass && confirm {
		return 0
	}
	fmt.Fprintf(stderr,
		"lenny-migrate: %s bypasses the audited §24.13 rollback path and requires --break-glass --confirm (before the command).\n",
		cmd)
	fmt.Fprintln(stderr,
		"lenny-migrate: for a routine, audited rollback use `lenny-ctl migrate down --version <N> --confirm`.")
	return 2
}

// recordMigrationPhases UPSERTs a `complete` / `not_run` phase row for
// every migration version this `up` run advanced through, recording the
// applied-at timestamp and the Job name so `lenny-ctl migrate status`
// reports the real expand-contract state instead of the synthesized
// projection. A read of the post-run version or a phase-table write that
// fails is logged and ignored: the schema migration itself succeeded, and
// the phase data is advisory for the status surface — failing the
// pre-deploy hook over it would block the deployment. spec: §24.13
// line 150.
func recordMigrationPhases(dsn, jobName string, prev uint, hadPrev bool, m *migrate.Migrate, stderr io.Writer) {
	cur, _, err := m.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		return // No migration applied (empty embed); nothing to record.
	}
	if err != nil {
		fmt.Fprintf(stderr, "lenny-migrate: phase tracking (non-fatal): read version: %v\n", err)
		return
	}
	sm, err := schemamigrate.New(dsn)
	if err != nil {
		fmt.Fprintf(stderr, "lenny-migrate: phase tracking (non-fatal): %v\n", err)
		return
	}
	if err := sm.RecordRun(context.Background(), prev, hadPrev, cur, jobName, time.Now().UTC()); err != nil {
		fmt.Fprintf(stderr, "lenny-migrate: phase tracking (non-fatal): %v\n", err)
	}
}

// newMigrator builds a migrate.Migrate over the embedded migrations
// and the Postgres database at dsn. The returned close function
// releases both the source and the database handle.
func newMigrator(dsn string) (*migrate.Migrate, func(), error) {
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return nil, nil, fmt.Errorf("embedded migrations: %w", err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("open postgres: %w", err)
	}
	driver, err := migratepg.WithInstance(db, &migratepg.Config{})
	if err != nil {
		_ = db.Close()
		return nil, nil, fmt.Errorf("migrate driver: %w", err)
	}
	m, err := migrate.NewWithInstance("iofs", src, "postgres", driver)
	if err != nil {
		_ = db.Close()
		return nil, nil, fmt.Errorf("migrate new: %w", err)
	}
	closeFn := func() {
		srcErr, dbErr := m.Close()
		_ = srcErr
		_ = dbErr
	}
	return m, closeFn, nil
}
