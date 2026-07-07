// SPDX-License-Identifier: MIT

//go:build component

// Tier-4 integration test: the §10.5 expand-contract schema-migration
// journey against a live Postgres container, plus the real
// cmd/lenny-gateway binary's continuity across a schema change and a
// warm restart. Before this test, the §10.5 Phase 3 enforcement gate
// and the dirty-migration recovery path were exercised only against the
// production migrations/ set (tests/tier2_component/migrations,
// tests/tier8_chaos/config_drift_test.go::TestSchemaMigrationDirtyFlag),
// neither of which drives a genuine in-flight Phase 1 (expand) -> Phase 2
// (coexistence) -> Phase 3 (gated contract) sequence with a controlled
// gate-fail-then-pass outcome, and no test exercised a live gateway
// process staying healthy while an additive migration lands underneath
// it and a second ("new replica") gateway process boots against the
// same, now-changed schema.
//
// The fixture migrations under
// tests/testinfra/fixtures/expand_contract_migration/ are test-only: a
// "widgets" table unrelated to any product schema, migrated through its
// own migrations table (expand_contract_fixture_migrations) so it can
// run against the same live Postgres the gateway uses without colliding
// with the production schema_migrations tracking row.
package tier4_integration_test

import (
	"database/sql"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	migratepg "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file" // file:// source for migrate

	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/gateway"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// fixtureMigrationsTable is a distinct golang-migrate tracking table so
// the fixture migrations can run against the same Postgres database the
// production migrations/ set (and a live gateway) already occupies
// without the two competing over the single default "schema_migrations"
// row.
const fixtureMigrationsTable = "expand_contract_fixture_migrations"

// fixtureMigrationsDir returns the absolute path to the test-only
// expand-contract fixture under tests/testinfra/fixtures/.
func fixtureMigrationsDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(schematest.RepoRoot(t), "tests/testinfra/fixtures/expand_contract_migration")
}

// newFixtureMigrator opens a *migrate.Migrate over the fixture directory
// and pg, scoped to fixtureMigrationsTable. The caller must Close it.
func newFixtureMigrator(t *testing.T, pg *containers.Postgres) *migrate.Migrate {
	t.Helper()
	source := "file://" + fixtureMigrationsDir(t)
	db, err := sql.Open("pgx", pg.DSN)
	if err != nil {
		t.Fatalf("newFixtureMigrator: sql.Open: %v", err)
	}
	driver, err := migratepg.WithInstance(db, &migratepg.Config{MigrationsTable: fixtureMigrationsTable})
	if err != nil {
		_ = db.Close()
		t.Fatalf("newFixtureMigrator: driver: %v", err)
	}
	m, err := migrate.NewWithDatabaseInstance(source, "postgres", driver)
	if err != nil {
		_ = db.Close()
		t.Fatalf("newFixtureMigrator: new: %v", err)
	}
	return m
}

// spec: §10.5 ("Expand-contract discipline: Phase 1 (expand): Add new
// columns/tables, deploy code that writes to both old and new ... Phase 3
// (contract): Drop old columns/tables in a subsequent release ... Every
// Phase 3 migration file must begin with a preflight verification block
// ... and aborts the migration with a non-zero exit code if the result is
// nonzero"); §17.6 line 848 ("Re-run: ... clear the dirty flag
// (`UPDATE schema_migrations SET dirty = false WHERE version = <N>`),
// release any stale advisory locks, and re-run the migration Job").
//
// diagnosis: a failure here means the Phase 3 enforcement gate does not
// abort a contract migration that still has un-migrated rows (data loss
// on DROP COLUMN), or a dirty migration left by a failed gate cannot be
// recovered by the documented clear-dirty-flag-and-retry procedure, over
// a real Postgres and the real golang-migrate driver.
func TestSchemaMigrationJourney_ExpandGateContract(t *testing.T) {
	pg := containers.StartPostgres(t, containers.PostgresOptions{})

	m := newFixtureMigrator(t, pg)
	defer func() { _, _ = m.Close() }()

	// Phase 0: the base table, pre-existing "production" data written
	// entirely through the old (price_cents-only) column.
	if err := m.Migrate(1); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("apply fixture version 1: %v", err)
	}
	insert := "INSERT INTO widgets (name, price_cents) VALUES ($1, $2)"
	db, err := sql.Open("pgx", pg.DSN)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(insert, "widget-a", 100); err != nil {
		t.Fatalf("seed widget-a: %v", err)
	}
	if _, err := db.Exec(insert, "widget-b", 200); err != nil {
		t.Fatalf("seed widget-b: %v", err)
	}

	// Phase 1 (expand): the new nullable column lands. Pre-existing rows
	// are un-migrated by construction (price_usd_cents IS NULL) —
	// exactly the "old-version replica" state the §10.5 nullable-columns
	// rule is written to tolerate.
	if err := m.Migrate(2); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("apply fixture version 2 (Phase 1 expand): %v", err)
	}
	var hasColumn bool
	if err := db.QueryRow(
		`SELECT EXISTS (SELECT 1 FROM information_schema.columns
		   WHERE table_name = 'widgets' AND column_name = 'price_usd_cents')`,
	).Scan(&hasColumn); err != nil {
		t.Fatalf("check price_usd_cents column: %v", err)
	}
	if !hasColumn {
		t.Fatalf("Phase 1 expand did not add widgets.price_usd_cents")
	}

	// Phase 2 (migrate reads): the "new code" dual-writes and backfills.
	// widget-a is backfilled (both columns now agree); widget-b is left
	// un-migrated on purpose so the Phase 3 gate below has something real
	// to reject.
	if _, err := db.Exec("UPDATE widgets SET price_usd_cents = price_cents WHERE name = $1", "widget-a"); err != nil {
		t.Fatalf("backfill widget-a: %v", err)
	}

	// Phase 3 (contract) attempted with widget-b still un-migrated: the
	// gate must abort the migration with a non-zero-exit-equivalent
	// error before any DDL commits, and the failed run must leave the
	// fixture's schema_migrations row dirty (§17.6 line 848 line "clear
	// the dirty flag" implies a failed migration leaves one set).
	err = m.Migrate(3)
	if err == nil {
		t.Fatalf("Phase 3 gate did not reject the un-migrated widget-b row")
	}
	if errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("Phase 3 migration reported no change; expected a gate rejection")
	}
	// The gate's RAISE EXCEPTION message names the un-migrated-row count
	// per §10.5's documented format:
	// "Phase 3 gate failed: <N> un-migrated rows remain in <table>.<old_column>."
	got := err.Error()
	if !strings.Contains(got, "Phase 3 gate failed") || !strings.Contains(got, "1 un-migrated rows remain in widgets.price_cents") {
		t.Fatalf("Phase 3 gate error = %q, want it to name the §10.5 gate-failed message and the un-migrated row count", got)
	}

	v, dirty, verr := m.Version()
	if verr != nil {
		t.Fatalf("read version after failed Phase 3: %v", verr)
	}
	if v != 3 || !dirty {
		t.Fatalf("version after failed Phase 3 = (%d, dirty=%v), want (3, dirty=true)", v, dirty)
	}
	var priceCentsStillPresent bool
	if err := db.QueryRow(
		`SELECT EXISTS (SELECT 1 FROM information_schema.columns
		   WHERE table_name = 'widgets' AND column_name = 'price_cents')`,
	).Scan(&priceCentsStillPresent); err != nil {
		t.Fatalf("check price_cents column after failed gate: %v", err)
	}
	if !priceCentsStillPresent {
		t.Fatalf("Phase 3 gate rejection still dropped widgets.price_cents; the whole up-file must roll back in one transaction")
	}

	// Recover per the §17.6 line 848 documented remediation: resolve the
	// data, clear the dirty flag, and re-run. golang-migrate records the
	// dirty flag against the target version it failed to reach (3 here),
	// so "clear the dirty flag" alone leaves the tracker believing
	// version 3 is already current and Migrate(3) would no-op; the driver
	// must also be pointed back at the last version that fully
	// committed. Force(2) does exactly that — "sets the version clean
	// without running SQL", the identical mechanism
	// pkg/schemamigrate.Manager.Down uses to clear a dirty flag before
	// its own retry — so the subsequent Migrate(3) genuinely re-executes
	// the (idempotent, per the §10.5 idempotency requirement) Phase 3
	// up.sql rather than treating it as already applied.
	if _, err := db.Exec("UPDATE widgets SET price_usd_cents = price_cents WHERE name = $1", "widget-b"); err != nil {
		t.Fatalf("backfill widget-b: %v", err)
	}
	if err := m.Force(2); err != nil {
		t.Fatalf("clear the dirty flag left by the rejected Phase 3 attempt: %v", err)
	}

	if err := m.Migrate(3); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("re-run Phase 3 after backfill and dirty-flag clear: %v", err)
	}
	v, dirty, verr = m.Version()
	if verr != nil {
		t.Fatalf("read version after successful Phase 3: %v", verr)
	}
	if v != 3 || dirty {
		t.Fatalf("version after successful Phase 3 = (%d, dirty=%v), want (3, dirty=false)", v, dirty)
	}
	if err := db.QueryRow(
		`SELECT EXISTS (SELECT 1 FROM information_schema.columns
		   WHERE table_name = 'widgets' AND column_name = 'price_cents')`,
	).Scan(&priceCentsStillPresent); err != nil {
		t.Fatalf("check price_cents column after successful gate: %v", err)
	}
	if priceCentsStillPresent {
		t.Fatalf("Phase 3 gate passed but widgets.price_cents was not dropped")
	}
	t.Logf("Phase 1 (expand) -> Phase 2 (backfill) -> Phase 3 gate reject (dirty) -> recover -> Phase 3 gate pass, all verified")
}

// spec: §10.5 ("Gateway: Rolling Deployment updates ... Mixed-version
// replicas must coexist during rollout.")
//
// diagnosis: a failure here means a live gateway process is disrupted by
// an additive schema change landing underneath it, or a second gateway
// process started against the same, now-migrated Postgres (the "new
// replica" a rolling deploy brings up) cannot see data an already-running
// gateway process wrote — i.e. the warm-restart / coexistence guarantee
// the expand-contract discipline exists to provide does not hold over
// the real HTTP admin surface and a real Postgres.
func TestSchemaMigrationJourney_GatewayCoexistsAcrossExpandStepAndRestart(t *testing.T) {
	gateway.SkipUnlessAvailable(t)

	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})

	gw1 := gateway.StartWith(t, "--dev-mode", "--postgres-dsn="+pg.DSN)
	c1 := poolUpgradeClient{t: t, base: gw1.BaseURL()}

	if code, body := c1.do(http.MethodPost, "/v1/admin/bootstrap", map[string]any{
		"tenants": []map[string]any{{"id": "acme", "displayName": "Acme Corp"}},
	}); code != http.StatusOK {
		t.Fatalf("bootstrap tenant: status %d body=%v", code, body)
	}
	const pool = "warm-restart-pool"
	if code, body := c1.do(http.MethodPost, "/v1/admin/pools", map[string]any{"name": pool}); code != http.StatusCreated {
		t.Fatalf("create pool via gw1: status %d body=%v", code, body)
	}

	// Phase 1 (expand): an additive schema change lands on the same live
	// Postgres gw1 is already connected to, via the fixture's own
	// migrations table so it does not touch the production
	// schema_migrations row gw1 depends on.
	m := newFixtureMigrator(t, pg)
	defer func() { _, _ = m.Close() }()
	if err := m.Migrate(2); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("apply fixture Phase 1 expand against the live gateway's Postgres: %v", err)
	}

	// gw1 (the "old" replica) must not be disrupted by the concurrent,
	// unrelated additive schema change underneath it.
	if code, body := c1.do(http.MethodGet, "/v1/admin/pools/"+pool, nil); code != http.StatusOK {
		t.Fatalf("gw1 unaffected by concurrent expand migration: status %d body=%v", code, body)
	}

	// Warm restart: a second gateway process boots against the same,
	// now-migrated Postgres while gw1 remains alive, matching §10.5's
	// "mixed-version replicas must coexist during rollout."
	gw2 := gateway.StartWith(t, "--dev-mode", "--postgres-dsn="+pg.DSN)
	c2 := poolUpgradeClient{t: t, base: gw2.BaseURL()}

	// gw2 sees the pool gw1 created before the restart (data continuity
	// across the warm restart and the intervening schema change).
	if code, body := c2.do(http.MethodGet, "/v1/admin/pools/"+pool, nil); code != http.StatusOK {
		t.Fatalf("gw2 (new replica) cannot see gw1's pool after warm restart: status %d body=%v", code, body)
	}

	// gw1 is still healthy too: this is coexistence, not a handoff.
	if code, body := c1.do(http.MethodGet, "/v1/admin/pools/"+pool, nil); code != http.StatusOK {
		t.Fatalf("gw1 unhealthy after gw2 joined: status %d body=%v", code, body)
	}
}
