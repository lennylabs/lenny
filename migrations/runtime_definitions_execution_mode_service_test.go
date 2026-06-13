// SPDX-License-Identifier: MIT

package migrations_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/migrations"
	embpostgres "github.com/lennylabs/lenny/pkg/embedded/postgres"
)

// pgCheckViolation is the SQLSTATE for a CHECK constraint violation,
// returned when an out-of-enum execution_mode is rejected.
const pgCheckViolation = "23514"

// TestExecutionModeServiceMigrationSQL_spec_5_2_12_6 asserts the static
// SQL surface of migration 0167: the up re-keys the runtime_definitions
// execution_mode CHECK to the (session, service) set, retires
// concurrency_style while leaving max_concurrent, and adds the
// agent_pod_state recycle counters; the down reverses each.
//
// spec: 5.2 (execution modes), 12.6 (agent_pod_state schema)
func TestExecutionModeServiceMigrationSQL_spec_5_2_12_6(t *testing.T) {
	up, err := migrations.FS.ReadFile("0167_runtime_definitions_execution_mode_service.up.sql")
	if err != nil {
		t.Fatalf("read 0167 up: %v", err)
	}
	ups := string(up)
	for _, want := range []string{
		"DROP CONSTRAINT runtime_definitions_execution_mode_check",
		"CHECK (execution_mode IN ('session', 'service'))",
		"DROP COLUMN concurrency_style",
		"ADD COLUMN sessions_served",
		"scrub_failure_count INTEGER",
	} {
		if !strings.Contains(ups, want) {
			t.Errorf("0167 up missing %q", want)
		}
	}
	// max_concurrent survives the retirement: the up must not drop it.
	if strings.Contains(ups, "DROP COLUMN max_concurrent") {
		t.Error("0167 up must not drop max_concurrent")
	}

	down, err := migrations.FS.ReadFile("0167_runtime_definitions_execution_mode_service.down.sql")
	if err != nil {
		t.Fatalf("read 0167 down: %v", err)
	}
	downs := string(down)
	for _, want := range []string{
		"DROP COLUMN IF EXISTS sessions_served",
		"DROP COLUMN IF EXISTS scrub_failure_count",
		"ADD COLUMN concurrency_style",
		"CHECK (execution_mode IN ('session', 'task', 'concurrent'))",
	} {
		if !strings.Contains(downs, want) {
			t.Errorf("0167 down missing %q", want)
		}
	}
}

// TestExecutionModeServiceMigrationDB_spec_5_2_12_6 applies the migration
// chain through 0167 against a real Postgres and verifies the re-keyed
// runtime_definitions CHECK accepts a service-mode row, rejects the
// removed task and concurrent values, the concurrency_style column is
// gone while max_concurrent remains, and the nullable agent_pod_state
// recycle counters exist.
//
// diagnosis: a failure means migration 0167 did not re-key the
// runtime_definitions execution_mode enum to (session, service), did not
// retire concurrency_style, or did not add the agent_pod_state recycle
// counters; the database would reject a service-mode runtime definition
// or still permit the removed task and concurrent values.
//
// spec: 5.2 (execution modes), 12.6 (agent_pod_state schema)
func TestExecutionModeServiceMigrationDB_spec_5_2_12_6(t *testing.T) {
	if testing.Short() {
		t.Skip("downloads the PostgreSQL bundle; skipped under -short")
	}
	pg := embpostgres.New(embpostgres.Config{
		DataDir:      t.TempDir(),
		Port:         15567,
		Database:     "lenny",
		Username:     "lenny",
		Password:     "lenny",
		StartTimeout: 3 * time.Minute,
	})
	if err := pg.Start(); err != nil {
		t.Fatalf("embedded postgres Start: %v", err)
	}
	defer func() { _ = pg.Stop() }()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, pg.DSN())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	// The runtime_definitions and agent_pod_state tables (0001), the RLS
	// roles 0167's GRANT-free comments rely on (0002), sandbox_warm_pools
	// with its execution_mode column (0033) and the concurrency_style /
	// max_concurrent columns (0040), and sessions.execution_mode (0084).
	// The full set is not applied because an unrelated later migration
	// needs the pgvector extension the embedded bundle lacks.
	applyMigrations(
		t, ctx, pool,
		"0001_initial_schema.up.sql",
		"0002_rls_immutability_roles.up.sql",
		"0033_sandbox_warm_pools.up.sql",
		"0040_warm_pool_concurrency.up.sql",
		"0084_sessions_isolation_level.up.sql",
		"0167_runtime_definitions_execution_mode_service.up.sql",
	)

	// A service-mode runtime definition inserts under the re-keyed CHECK.
	insertRuntimeDef(t, ctx, pool, "svc-runtime", "service")

	// The removed mode values are rejected by the CHECK constraint.
	for _, mode := range []string{"task", "concurrent"} {
		_, err := pool.Exec(ctx,
			`INSERT INTO runtime_definitions (name, image, execution_mode, isolation_profile, integration_level)
			 VALUES ($1, 'img', $2, 'standard', '')`,
			"rt-"+mode, mode)
		if err == nil {
			t.Errorf("execution_mode %q must be rejected by the re-keyed CHECK", mode)
			continue
		}
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != pgCheckViolation {
			t.Errorf("execution_mode %q: want check_violation (%s), got %v", mode, pgCheckViolation, err)
		}
	}

	// session mode still inserts: the surviving value is unaffected.
	insertRuntimeDef(t, ctx, pool, "sess-runtime", "session")

	// concurrency_style is retired; max_concurrent survives.
	if columnExists(t, ctx, pool, "sandbox_warm_pools", "concurrency_style") {
		t.Error("concurrency_style column must be dropped by 0167")
	}
	if !columnExists(t, ctx, pool, "sandbox_warm_pools", "max_concurrent") {
		t.Error("max_concurrent column must survive 0167")
	}

	// The agent_pod_state recycle counters exist and are nullable.
	for _, col := range []string{"sessions_served", "scrub_failure_count"} {
		if !columnExists(t, ctx, pool, "agent_pod_state", col) {
			t.Errorf("agent_pod_state.%s must be added by 0167", col)
			continue
		}
		if !columnNullable(t, ctx, pool, "agent_pod_state", col) {
			t.Errorf("agent_pod_state.%s must be nullable", col)
		}
	}

	// The down migration reverses every surface against a live database.
	// The service-mode row is removed first: the down restores the
	// (session, task, concurrent) CHECK, which the service value would
	// violate, so an append-only migration must leave the table clean of
	// the new value before re-keying the constraint back.
	if _, err := pool.Exec(ctx,
		`DELETE FROM runtime_definitions WHERE execution_mode = 'service'`); err != nil {
		t.Fatalf("delete service-mode rows before down: %v", err)
	}
	applyMigrations(t, ctx, pool, "0167_runtime_definitions_execution_mode_service.down.sql")

	// The restored CHECK re-admits the old mode set and rejects service.
	insertRuntimeDef(t, ctx, pool, "task-runtime", "task")
	if _, err := pool.Exec(ctx,
		`INSERT INTO runtime_definitions (name, image, execution_mode, isolation_profile, integration_level)
		 VALUES ('svc-after-down', 'img', 'service', 'standard', '')`); err != nil {
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != pgCheckViolation {
			t.Errorf("after down, service mode: want check_violation (%s), got %v", pgCheckViolation, err)
		}
	} else {
		t.Error("after down, the restored CHECK must reject execution_mode 'service'")
	}

	// concurrency_style is restored and the recycle counters are dropped.
	if !columnExists(t, ctx, pool, "sandbox_warm_pools", "concurrency_style") {
		t.Error("down must restore the concurrency_style column")
	}
	for _, col := range []string{"sessions_served", "scrub_failure_count"} {
		if columnExists(t, ctx, pool, "agent_pod_state", col) {
			t.Errorf("down must drop agent_pod_state.%s", col)
		}
	}
}

// insertRuntimeDef inserts a runtime_definitions row with the given
// execution_mode, failing the test on error.
func insertRuntimeDef(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name, mode string) {
	t.Helper()
	_, err := pool.Exec(ctx,
		`INSERT INTO runtime_definitions (name, image, execution_mode, isolation_profile, integration_level)
		 VALUES ($1, 'img', $2, 'standard', '')`,
		name, mode)
	if err != nil {
		t.Fatalf("insert %s-mode runtime definition: %v", mode, err)
	}
}

// columnExists reports whether the given column is present on the table.
func columnExists(t *testing.T, ctx context.Context, pool *pgxpool.Pool, table, column string) bool {
	t.Helper()
	var exists bool
	err := pool.QueryRow(ctx,
		`SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_name = $1 AND column_name = $2
		)`, table, column).Scan(&exists)
	if err != nil {
		t.Fatalf("query column %s.%s: %v", table, column, err)
	}
	return exists
}

// columnNullable reports whether the given column is nullable. The
// caller verifies the column exists via columnExists first.
func columnNullable(t *testing.T, ctx context.Context, pool *pgxpool.Pool, table, column string) bool {
	t.Helper()
	var isNullable string
	err := pool.QueryRow(ctx,
		`SELECT is_nullable FROM information_schema.columns
		 WHERE table_name = $1 AND column_name = $2`, table, column).Scan(&isNullable)
	if err != nil {
		t.Fatalf("query nullability %s.%s: %v", table, column, err)
	}
	return isNullable == "YES"
}

// applyMigrations executes the named embedded migrations in order
// against the connected pool, failing the test on the first error.
func applyMigrations(t *testing.T, ctx context.Context, pool *pgxpool.Pool, names ...string) {
	t.Helper()
	for _, name := range names {
		sql, err := migrations.FS.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if _, err := pool.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}
}
