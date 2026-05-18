//go:build component

// SPDX-License-Identifier: MIT

// Exercises the cmd/lenny-migrate runner end-to-end against a real
// Postgres container. The runner embeds the production migrations and
// is what a deployment runs as a Helm pre-install / pre-upgrade Job.

package migrations_test

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// spec: 12.3
// diagnosis: the lenny-migrate runner did not apply or reverse the
// embedded migrations as specified. `up` against a bare database must
// create the schema, `version` must report a clean version, `down`
// must remove every object, and a second `up` must restore the
// schema — the §18.5 forward-and-back round trip the Phase 1.5 gate
// requires of the production binary.
func TestLennyMigrateRoundTrip(t *testing.T) {
	t.Parallel()
	root := schematest.RepoRoot(t)
	bin := filepath.Join(t.TempDir(), "lenny-migrate")
	build := exec.Command("go", "build", "-o", bin, "./cmd/lenny-migrate")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build lenny-migrate: %v\n%s", err, out)
	}

	// A bare container — no migrations applied — so `up` does the work.
	pg := containers.StartPostgres(t, containers.PostgresOptions{})
	ctx := context.Background()

	run := func(args ...string) (string, error) {
		full := append([]string{"--postgres-dsn", pg.DSN}, args...)
		out, err := exec.CommandContext(ctx, bin, full...).CombinedOutput()
		return string(out), err
	}
	sessionsTableExists := func() bool {
		var ok bool
		if err := pg.Pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables
			 WHERE table_schema = 'public' AND table_name = 'sessions')`).Scan(&ok); err != nil {
			t.Fatalf("schema probe: %v", err)
		}
		return ok
	}

	if out, err := run("up"); err != nil {
		t.Fatalf("up: %v\n%s", err, out)
	}
	if !sessionsTableExists() {
		t.Fatal("up did not create the production schema")
	}
	if out, err := run("version"); err != nil {
		t.Fatalf("version: %v\n%s", err, out)
	}
	if out, err := run("down"); err != nil {
		t.Fatalf("down: %v\n%s", err, out)
	}
	if sessionsTableExists() {
		t.Fatal("down did not remove the production schema")
	}
	if out, err := run("up"); err != nil {
		t.Fatalf("re-up after down: %v\n%s", err, out)
	}
	if !sessionsTableExists() {
		t.Fatal("re-up did not restore the production schema")
	}
}
