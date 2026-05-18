// SPDX-License-Identifier: MIT

// Command lenny-migrate applies the numbered Postgres schema
// migrations (§18.5 Phase 1.5). The migrations are embedded in the
// binary, so a deployment runs it as a Helm pre-install / pre-upgrade
// Job without a source checkout.
//
// Usage:
//
//	lenny-migrate up                 Apply every pending migration.
//	lenny-migrate down               Roll every migration back.
//	lenny-migrate goto <version>     Migrate to an exact version.
//	lenny-migrate version            Print the current schema version.
//
// The connection string comes from --postgres-dsn or the
// LENNY_POSTGRES_DSN environment variable.
package main

import (
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/golang-migrate/migrate/v4"
	migratepg "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/lennylabs/lenny/migrations"
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
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: lenny-migrate [--postgres-dsn DSN] <up|down|goto VERSION|version>")
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
		if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
			fmt.Fprintf(stderr, "lenny-migrate: up: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, "lenny-migrate: schema is up to date")
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
