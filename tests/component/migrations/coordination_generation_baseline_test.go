//go:build component

// SPDX-License-Identifier: MIT

package migrations_test

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// coordGenBaselinePriorVersion is the schema version immediately below the
// baseline migration, the point the case rolls back to before seeding the
// pre-baseline row the forward pass must backfill.
const coordGenBaselinePriorVersion = 180

// spec: 4.2 (a newly created session row carries coordination_generation = 1
// and the counter is never reset), 10.1 (the coordinator handoff
// compare-and-swap mints a generation strictly above the value a replica
// already holds), 10.5 (a constraint old-version writes violate belongs to a
// later phase)
// diagnosis: migration 0181 did not move sessions.coordination_generation and
//
//	its coordination_lease mirror onto DEFAULT 1, did not backfill the rows
//	still carrying 0, or tightened the retained CHECK. A missing backfill
//	leaves a session whose resume fence sits at the gateway's floor of 1 while
//	its row reads 0, so its first crash takeover mints 1 as well and the pod
//	refuses that fence as coordinator_handoff_stale. A tightened check rejects
//	every insert the still-running old fleet issues during the rolling window,
//	because the migrate Job completes before the gateway Deployment rolls. A
//	failure on the rollback half means the .down.sql did not restore DEFAULT 0.
func TestCoordinationGenerationBaselineMigration_spec_4_2(t *testing.T) {
	t.Parallel()
	dir := prodMigrations(t)
	pg := containers.StartPostgres(t, containers.PostgresOptions{MigrationsDir: dir})
	ctx := context.Background()

	// Roll back to the schema the backfill runs against and seed a session row
	// carrying the pre-baseline 0, so the forward pass runs over it.
	pg.MigrateTo(t, dir, coordGenBaselinePriorVersion)

	if _, err := pg.Pool.Exec(ctx,
		`INSERT INTO tenants (id, genesis_nonce) VALUES ('acme', '\x00')`); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	const insertSession = `INSERT INTO sessions (id, tenant_id, state, runtime_ref, root_session_id, coordination_generation)
		VALUES ($1, 'acme', 'created', 'echo', $1, $2)`
	const (
		preBaseline = "11111111-1111-4111-8111-111111111111"
		advanced    = "22222222-2222-4222-8222-222222222222"
	)
	for _, row := range []struct {
		id  string
		gen int64
	}{
		// The row the backfill exists for.
		{preBaseline, 0},
		// A row a handoff already advanced; §4.2 makes the counter monotonic,
		// so the backfill must not touch it.
		{advanced, 7},
	} {
		if err := execTenant(ctx, pg, "acme", insertSession, row.id, row.gen); err != nil {
			t.Fatalf("seed session %s: %v", row.id, err)
		}
	}

	pg.MigrateTo(t, dir, coordGenBaselinePriorVersion+1)

	if got := sessionCoordGeneration(t, ctx, pg, preBaseline); got != 1 {
		t.Errorf("sessions.coordination_generation for the pre-baseline row = %d, want 1 (the backfill baselines it)", got)
	}
	if got := sessionCoordGeneration(t, ctx, pg, advanced); got != 7 {
		t.Errorf("sessions.coordination_generation for the advanced row = %d, want it left at 7", got)
	}

	// Both columns state the same baseline: the session row and the
	// coordination_lease mirror the §10.1.8 barrier-target query reads.
	for _, want := range []struct{ table, column string }{
		{"sessions", "coordination_generation"},
		{"coordination_lease", "coordination_generation"},
	} {
		if got := columnDefault(t, ctx, pg, want.table, want.column); got != "1" {
			t.Errorf("%s.%s default = %q, want \"1\"", want.table, want.column, got)
		}
	}

	// The retained CHECK (coordination_generation >= 0) still accepts an
	// explicit zero. The migrate Job is a pre-install/pre-upgrade hook that
	// completes before the gateway Deployment rolls, so the old fleet keeps
	// inserting one for the whole rolling window; §10.5 places the tightening
	// in a later phase.
	const oldBinaryRow = "33333333-3333-4333-8333-333333333333"
	if err := execTenant(ctx, pg, "acme", insertSession, oldBinaryRow, int64(0)); err != nil {
		t.Fatalf("insert at an explicit zero, which the retained check must accept: %v", err)
	}
	if got := sessionCoordGeneration(t, ctx, pg, oldBinaryRow); got != 0 {
		t.Errorf("an explicit zero insert read back as %d, want it stored verbatim at 0", got)
	}

	// Rolling the migration back restores DEFAULT 0 on both columns and rolls
	// no row back, because §4.2 states the counter is never reset.
	pg.MigrateTo(t, dir, coordGenBaselinePriorVersion)
	for _, want := range []struct{ table, column string }{
		{"sessions", "coordination_generation"},
		{"coordination_lease", "coordination_generation"},
	} {
		if got := columnDefault(t, ctx, pg, want.table, want.column); got != "0" {
			t.Errorf("%s.%s default after rollback = %q, want \"0\"", want.table, want.column, got)
		}
	}
	if got := sessionCoordGeneration(t, ctx, pg, preBaseline); got != 1 {
		t.Errorf("sessions.coordination_generation after rollback = %d, want it left at the backfilled 1", got)
	}
}

// sessionCoordGeneration reads one session row's coordination generation.
func sessionCoordGeneration(t *testing.T, ctx context.Context, pg *containers.Postgres, id string) int64 {
	t.Helper()
	var gen int64
	if err := pg.Pool.QueryRow(ctx,
		`SELECT coordination_generation FROM sessions WHERE id = $1::uuid`, id).Scan(&gen); err != nil {
		t.Fatalf("read coordination_generation for session %s: %v", id, err)
	}
	return gen
}
