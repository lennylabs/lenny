// SPDX-License-Identifier: MIT

package migrations_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/migrations"
	embpostgres "github.com/lennylabs/lenny/pkg/embedded/postgres"
)

// TestSessionConditionsMigrationSQL_spec_6_49_7_1 asserts the static SQL
// surface of migration 0168: the up adds the §7.1 conversation_continuity
// envelope half and the §7.2 / §8.8 Terminated/Suspended session-condition
// columns (terminated_at / terminated_reason, suspended_at /
// suspended_reason) to the sessions table; the down reverses each. The
// reason and continuity columns carry a NOT NULL empty-string default so
// a row predating the migration backfills the empty string until the read
// path resolves the envelope (parallel to migration 0084), and the
// condition timestamps are nullable so a session that never carried the
// condition reads NULL.
//
// spec: 6.49 (session-condition relocation off Sandbox.status.conditions),
// 7.1 (sessionIsolationLevel.conversationContinuity persistence), 7.2 / 8.8
// (Terminated/Suspended conditions on the Postgres session model)
func TestSessionConditionsMigrationSQL_spec_6_49_7_1(t *testing.T) {
	up, err := migrations.FS.ReadFile("0168_session_conditions_conversation_continuity.up.sql")
	if err != nil {
		t.Fatalf("read 0168 up: %v", err)
	}
	ups := string(up)
	for _, want := range []string{
		"ALTER TABLE sessions",
		"ADD COLUMN conversation_continuity TEXT",
		"NOT NULL DEFAULT ''",
		"ADD COLUMN terminated_at           TIMESTAMPTZ",
		"ADD COLUMN terminated_reason",
		"ADD COLUMN suspended_at            TIMESTAMPTZ",
		"ADD COLUMN suspended_reason",
	} {
		if !strings.Contains(ups, want) {
			t.Errorf("0168 up missing %q", want)
		}
	}
	// The condition-timestamp columns must be nullable (no NOT NULL): a
	// NULL value is the §7.2 "condition has not fired" sentinel, so a
	// NOT NULL DEFAULT on a timestamp would forge a fired condition on
	// every legacy row.
	if strings.Contains(ups, "terminated_at           TIMESTAMPTZ NOT NULL") ||
		strings.Contains(ups, "suspended_at            TIMESTAMPTZ NOT NULL") {
		t.Error("0168 up must keep terminated_at / suspended_at nullable; a NOT NULL timestamp forges a fired condition on legacy rows")
	}

	down, err := migrations.FS.ReadFile("0168_session_conditions_conversation_continuity.down.sql")
	if err != nil {
		t.Fatalf("read 0168 down: %v", err)
	}
	downs := string(down)
	for _, want := range []string{
		"ALTER TABLE sessions",
		"DROP COLUMN IF EXISTS conversation_continuity",
		"DROP COLUMN IF EXISTS terminated_at",
		"DROP COLUMN IF EXISTS terminated_reason",
		"DROP COLUMN IF EXISTS suspended_at",
		"DROP COLUMN IF EXISTS suspended_reason",
	} {
		if !strings.Contains(downs, want) {
			t.Errorf("0168 down missing %q", want)
		}
	}
}

// TestSessionConditionsMigrationDB_spec_6_49_7_1 applies the migration
// chain through 0168 against a real Postgres and verifies the sessions
// table gains the §7.1 conversation_continuity column (NOT NULL,
// empty-string default) and the §7.2 / §8.8 Terminated/Suspended condition
// columns, that a row inserted before any backfill defaults
// conversation_continuity and the reason columns to the empty string while
// the condition timestamps default NULL, and that the down reverses every
// column.
//
// diagnosis: a failure means migration 0168 did not add the §7.1
// conversation_continuity envelope half or the §7.2 / §8.8
// Terminated/Suspended session-condition columns, defaulted them
// incorrectly (a non-empty continuity/reason backfill or a non-NULL
// condition timestamp), or the down did not drop them; the gateway would
// be unable to persist the relocated session conditions or the service-mode
// no-continuity contract, or a legacy row would read a forged condition.
//
// spec: 6.49 (session-condition relocation), 7.1
// (sessionIsolationLevel.conversationContinuity persistence), 7.2 / 8.8
// (Terminated/Suspended conditions on the Postgres session model)
func TestSessionConditionsMigrationDB_spec_6_49_7_1(t *testing.T) {
	if testing.Short() {
		t.Skip("downloads the PostgreSQL bundle; skipped under -short")
	}
	pg := embpostgres.New(embpostgres.Config{
		DataDir:      t.TempDir(),
		Port:         15570,
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

	// The sessions and tenants tables (0001), the RLS roles and tenant
	// guard the sessions table relies on (0002), and the §7.1 isolation
	// envelope columns 0168 sits alongside (0084). The full chain is not
	// applied because an unrelated later migration needs the pgvector
	// extension the embedded bundle lacks.
	applyMigrations(
		t, ctx, pool,
		"0001_initial_schema.up.sql",
		"0002_rls_immutability_roles.up.sql",
		"0084_sessions_isolation_level.up.sql",
		"0168_session_conditions_conversation_continuity.up.sql",
	)

	// The new columns exist after the up.
	newCols := []string{
		"conversation_continuity",
		"terminated_at", "terminated_reason",
		"suspended_at", "suspended_reason",
	}
	for _, col := range newCols {
		if !columnExists(t, ctx, pool, "sessions", col) {
			t.Errorf("sessions.%s must be added by 0168", col)
		}
	}
	// The condition timestamps are nullable (the §7.2 "not fired"
	// sentinel); the continuity and reason columns are NOT NULL.
	for _, col := range []string{"terminated_at", "suspended_at"} {
		if columnExists(t, ctx, pool, "sessions", col) && !columnNullable(t, ctx, pool, "sessions", col) {
			t.Errorf("sessions.%s must be nullable", col)
		}
	}
	for _, col := range []string{"conversation_continuity", "terminated_reason", "suspended_reason"} {
		if columnExists(t, ctx, pool, "sessions", col) && columnNullable(t, ctx, pool, "sessions", col) {
			t.Errorf("sessions.%s must be NOT NULL", col)
		}
	}

	// A row inserted with no explicit value for the new columns defaults
	// conversation_continuity and the reason columns to the empty string
	// and the condition timestamps to NULL: the empty-string backfill
	// stands in until the read path resolves the envelope, and a
	// never-fired condition reads NULL.
	const sessID = "00000000-0000-0000-0000-000000000168"
	insertConditionSeedRow(t, ctx, pool, sessID, "t-0168")
	var (
		continuity string
		termReason string
		suspReason string
		termAt     *time.Time
		suspAt     *time.Time
	)
	if err := pool.QueryRow(ctx,
		`SELECT conversation_continuity, terminated_reason, suspended_reason,
		        terminated_at, suspended_at
		   FROM sessions WHERE id = $1`, sessID).
		Scan(&continuity, &termReason, &suspReason, &termAt, &suspAt); err != nil {
		t.Fatalf("read back seed row: %v", err)
	}
	if continuity != "" {
		t.Errorf("conversation_continuity default: want empty backfill sentinel, got %q", continuity)
	}
	if termReason != "" || suspReason != "" {
		t.Errorf("reason defaults: want empty, got terminated_reason=%q suspended_reason=%q", termReason, suspReason)
	}
	if termAt != nil {
		t.Errorf("terminated_at default: want NULL (condition not fired), got %v", *termAt)
	}
	if suspAt != nil {
		t.Errorf("suspended_at default: want NULL (condition not fired), got %v", *suspAt)
	}

	// The down migration reverses every column against a live database.
	applyMigrations(t, ctx, pool, "0168_session_conditions_conversation_continuity.down.sql")
	for _, col := range newCols {
		if columnExists(t, ctx, pool, "sessions", col) {
			t.Errorf("down must drop sessions.%s", col)
		}
	}
}

// insertConditionSeedRow inserts a minimal sessions row with no explicit
// value for the 0168 columns, so the read-back exercises their column
// defaults. It seeds the tenant first to satisfy the sessions.tenant_id
// foreign key and sets app.current_tenant so the lenny_tenant_guard
// BEFORE-INSERT trigger on sessions admits the write.
func insertConditionSeedRow(t *testing.T, ctx context.Context, pool *pgxpool.Pool, sessID, tenantID string) {
	t.Helper()
	nonce := make([]byte, 32)
	if _, err := pool.Exec(ctx,
		`INSERT INTO tenants (id, genesis_nonce) VALUES ($1, $2)
		 ON CONFLICT (id) DO NOTHING`, tenantID, nonce); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire conn: %v", err)
	}
	defer conn.Release()
	// The tenant guard reads app.current_tenant from the session; set it on
	// the same connection the INSERT runs on.
	if _, err := conn.Exec(ctx, `SET app.current_tenant = `+quoteLiteral(tenantID)); err != nil {
		t.Fatalf("set tenant GUC: %v", err)
	}
	if _, err := conn.Exec(ctx,
		`INSERT INTO sessions (id, tenant_id, state, runtime_ref, root_session_id)
		 VALUES ($1, $2, 'created', 'runtime-x', $1)`, sessID, tenantID); err != nil {
		t.Fatalf("seed session row: %v", err)
	}
}

// quoteLiteral wraps a trusted test identifier in single quotes for a SET
// statement, which does not accept a bind parameter. The tenant id is a
// test constant, not user input.
func quoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
