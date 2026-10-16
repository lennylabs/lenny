//go:build component

// SPDX-License-Identifier: MIT

// Package lifecycle_test runs the terminal-writer and resume-attempt cell
// tables on the lifecycle harness over a Postgres session store with the
// production migrations applied.
package lifecycle_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	sessionpg "github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/pgstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/lifecycleharness"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// pgSessionStore starts Postgres with the production migrations and returns
// a session store over it with the harness tenant seeded.
func pgSessionStore(t *testing.T) sessionstore.Store {
	t.Helper()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	if _, err := pg.Pool.Exec(context.Background(),
		`INSERT INTO tenants (id, genesis_nonce) VALUES ($1, '\x00') ON CONFLICT (id) DO NOTHING`,
		lifecycleharness.Tenant); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	return sessionpg.New(pg.Pool)
}

// spec: §7.2 (Interactive Session Model), §8.8 (TaskRecord and TaskResult
// Schema), §10.1.5 (Stale Replica Behavior)
// diagnosis: a failure names the writer, the pre-state, and the placement of
// a terminal write that wrote over a state its guard refuses, wrote nothing
// over a state its guard admits, or left a claim, lease, binding, or queue
// entry the invariant checker found, on the Postgres session store.
func TestTerminalWriterCellsOnPostgres_spec_7_2(t *testing.T) {
	lifecycleharness.RunTerminalWriterCells(t, pgSessionStore)
}

// spec: §7.3 (Resume driver), §7.3 (Credential leases on the replacement pod),
// §6.2 (Pod State Machine)
// diagnosis: a failure names a resume-attempt exit that did not reach its
// final state, or left a claim, lease, binding, or queue entry the invariant
// checker found, on the Postgres session store.
func TestResumeAttemptExitCellsOnPostgres_spec_7_3(t *testing.T) {
	lifecycleharness.RunResumeExitCells(t, pgSessionStore)
}
