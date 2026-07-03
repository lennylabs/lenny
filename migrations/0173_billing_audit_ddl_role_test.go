// SPDX-License-Identifier: MIT

package migrations_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/migrations"
	"github.com/lennylabs/lenny/pkg/common/seqname"
	embpostgres "github.com/lennylabs/lenny/tests/testinfra/embpg"
)

// pgInsufficientPrivilege is the SQLSTATE Postgres returns for a denied
// privilege (permission denied for schema / sequence / relation).
const pgInsufficientPrivilege = "42501"

// pgConfigurationInvalid is the SQLSTATE Postgres raises for an unset custom
// GUC read through current_setting(name, false) — "unrecognized configuration
// parameter". The §11.7 tenant-isolation policy uses this hard-error form
// (migration 0051, applied at runtime through the guarded 0057 rewrite), so a
// ledger read that reaches the policy without app.current_tenant set fails
// closed with this code rather than returning no rows.
const pgConfigurationInvalid = "42704"

// platformAuditSeq is the fixed platform-chain audit sequence name the
// migration embeds. It is the §10.2 derivation applied to the
// compile-time-constant chain id 'platform'. The SQL literal must equal
// the value the shared seqname helper computes, verified in
// TestBillingAuditDDLRoleMigrationSQL below.
var platformAuditSeq = seqname.AuditSequenceName("platform")

// TestBillingAuditDDLRoleMigrationSQL asserts the static SQL surface of
// migration 0173: the up creates the CREATE-privileged lenny_ddl login
// role, grants it CREATE ON SCHEMA public and SELECT on the ledger tables
// (and no write privilege), keys the lenny_app USAGE default privilege to
// lenny_ddl via ALTER DEFAULT PRIVILEGES FOR ROLE, and creates the fixed
// platform-chain audit sequence under the §10.2-derived name. The down
// reverses each. The embedded platform-sequence literal must equal the
// seqname derivation because migrations cannot call Go.
//
// spec: §11.7 item 7 (least-privilege DDL role), §15.1 (tenant-create
// sequence provisioning), §10.2 (length-bounded safe-derived name).
func TestBillingAuditDDLRoleMigrationSQL_spec_11_7_15_1_10_2(t *testing.T) {
	up := readMigration0173(t, "0173_billing_audit_ddl_role_and_sequences.up.sql")

	for _, want := range []string{
		// A distinct CREATE-privileged login role, guarded IF NOT EXISTS
		// like migration 0002's role creation.
		"IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'lenny_ddl')",
		"CREATE ROLE lenny_ddl LOGIN",
		"GRANT USAGE, CREATE ON SCHEMA public TO lenny_ddl",
		"GRANT SELECT ON billing_events, audit_log TO lenny_ddl",
		// The FOR ROLE clause is load-bearing: it attaches the USAGE
		// default to sequences lenny_ddl creates at runtime.
		"ALTER DEFAULT PRIVILEGES FOR ROLE lenny_ddl IN SCHEMA public",
		"GRANT USAGE ON SEQUENCES TO lenny_app",
		// The fixed platform-chain audit sequence.
		"CREATE SEQUENCE IF NOT EXISTS " + platformAuditSeq,
		"START WITH 1 INCREMENT BY 1 NO CYCLE",
		"GRANT USAGE ON SEQUENCE " + platformAuditSeq + " TO lenny_app",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("migration 0173 up missing %q", want)
		}
	}

	// lenny_ddl must not gain any ledger write privilege, and lenny_app
	// must never gain CREATE ON SCHEMA (it stays least-privilege).
	for _, forbidden := range []string{
		"INSERT ON billing_events",
		"UPDATE ON billing_events",
		"DELETE ON billing_events",
		"INSERT ON audit_log",
		"UPDATE ON audit_log",
		"DELETE ON audit_log",
		"CREATE ON SCHEMA public TO lenny_app",
	} {
		if strings.Contains(up, forbidden) {
			t.Errorf("migration 0173 up must not contain %q", forbidden)
		}
	}

	// lenny_ddl must not be created or altered with BYPASSRLS as a role
	// attribute (the word BYPASSRLS appears in a comment explaining its
	// absence, so match only the role-attribute statement forms).
	for _, forbidden := range []string{
		"ROLE lenny_ddl LOGIN BYPASSRLS",
		"ROLE lenny_ddl BYPASSRLS",
		"lenny_ddl WITH BYPASSRLS",
	} {
		if strings.Contains(up, forbidden) {
			t.Errorf("migration 0173 up must not grant lenny_ddl BYPASSRLS: %q", forbidden)
		}
	}

	// The embedded platform-sequence literal must be the §10.2 derivation
	// of 'platform', not a hand-typed digest that could drift from the
	// shared helper. platformAuditSeq is seqname.AuditSequenceName, so this
	// asserts the literal in the SQL equals the runtime derivation.
	if !strings.Contains(up, "audit_seq_d294fcce0cc88587843099d85dd805aeef1b09a6") {
		t.Errorf("migration 0173 up must embed the SHA-256('platform') derived name")
	}
	if platformAuditSeq != "audit_seq_d294fcce0cc88587843099d85dd805aeef1b09a6" {
		t.Errorf("seqname derivation drifted from the embedded literal: %q", platformAuditSeq)
	}

	down := readMigration0173(t, "0173_billing_audit_ddl_role_and_sequences.down.sql")
	for _, want := range []string{
		"DROP SEQUENCE IF EXISTS " + platformAuditSeq,
		// DROP OWNED BY clears the runtime per-tenant sequences lenny_ddl
		// created and the FOR ROLE default-privilege ACL, so the role can
		// be dropped.
		"DROP OWNED BY lenny_ddl",
		"DROP ROLE lenny_ddl",
	} {
		if !strings.Contains(down, want) {
			t.Errorf("migration 0173 down missing %q", want)
		}
	}
}

// TestBillingAuditDDLRoleMigrationDB exercises migration 0173 against a
// live Postgres: the FOR ROLE-keyed default privilege attaches so a
// sequence created as lenny_ddl carries the lenny_app USAGE grant, the
// fixed platform sequence exists so a platform-chain nextval resolves a
// real relation, and the least-privilege posture holds (lenny_ddl reads
// but cannot write the ledgers; lenny_app cannot CREATE on schema).
//
// diagnosis: a failure means per-tenant sequence provisioning is broken.
// If the FOR ROLE default privilege does not attach, every tenant's first
// nextval under the lenny_app session raises permission denied for
// sequence and no billing or audit event is written. If the platform
// sequence is absent, the first platform-chain audit Append fails on
// nextval of a nonexistent relation and the platform-admin compliance
// chain silently drops from Day 1. If lenny_ddl can write the ledgers or
// lenny_app can CREATE on schema, the least-privilege separation the §11.7
// role model requires is broken.
//
// spec: §11.7 item 7 (least-privilege DDL role), §15.1 (tenant-create
// sequence provisioning), §10.2 (length-bounded safe-derived name).
func TestBillingAuditDDLRoleMigrationDB_spec_11_7_15_1_10_2(t *testing.T) {
	if testing.Short() {
		t.Skip("downloads the PostgreSQL bundle; skipped under -short")
	}
	pg := embpostgres.New(embpostgres.Config{
		DataDir:      t.TempDir(),
		Port:         0,
		Database:     "lenny",
		Username:     "lenny",
		Password:     "lenny",
		StartTimeout: 3 * time.Minute,
	})
	if err := pg.Start(); err != nil {
		t.Fatalf("embedded postgres Start: %v", err)
	}
	defer func() { _ = pg.Stop() }()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, pg.DSN())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	// 0001 defines billing_events and audit_log; 0002 creates lenny_app and
	// the RLS/immutability posture with the initial soft-error policy form;
	// 0057 rewrites the lenny_tenant_isolation policy on both ledgers to the
	// current_setting('app.current_tenant', false) hard-error form (the same
	// hard-error form migration 0051 introduced, applied through the guarded
	// 0057 rewrite so it lands cleanly on this minimal schema without the
	// intervening tables 0051's unconditional CREATE POLICY assumes); 0173
	// adds the DDL role, the FOR ROLE default privilege, and the platform
	// sequence. The hard-error form is the runtime posture the setval re-seed
	// read must run under: a lenny_ddl SELECT with app.current_tenant unset
	// raises configuration_invalid (SQLSTATE 42704) rather than returning no
	// rows, so the re-seed must scope to the tenant through SET LOCAL
	// app.current_tenant, which is exactly what the runtime pgtenant.InTx
	// helper does.
	applyMigrations(
		t, ctx, pool,
		"0001_initial_schema.up.sql",
		"0002_rls_immutability_roles.up.sql",
		"0057_tenant_guard_pooler_mode.up.sql",
		"0173_billing_audit_ddl_role_and_sequences.up.sql",
	)

	// The fixed platform-chain audit sequence exists after the migration:
	// a platform-chain nextval resolves a real relation rather than failing
	// on "relation does not exist".
	if !sequenceExists(t, ctx, pool, platformAuditSeq) {
		t.Fatalf("migration 0173 must create the fixed platform audit sequence %q", platformAuditSeq)
	}

	// FOR ROLE-keyed default privilege: a sequence created AS lenny_ddl
	// (SET ROLE, mirroring the runtime DDL pool that connects as lenny_ddl)
	// must carry the lenny_app USAGE default so a subsequent nextval under
	// the lenny_app session succeeds. This is the load-bearing regression:
	// an ALTER DEFAULT PRIVILEGES without FOR ROLE lenny_ddl would attach
	// only to the migration role's own sequences, and this nextval would
	// raise permission denied for sequence.
	billingSeq := seqname.BillingSequenceName("acme")
	auditSeq := seqname.AuditSequenceName("acme")
	for _, seq := range []string{billingSeq, auditSeq} {
		mustExec(t, ctx, pool, "SET ROLE lenny_ddl")
		mustExec(t, ctx, pool,
			"CREATE SEQUENCE IF NOT EXISTS "+seq+" START WITH 1 INCREMENT BY 1 NO CYCLE")
		mustExec(t, ctx, pool, "RESET ROLE")

		mustExec(t, ctx, pool, "SET ROLE lenny_app")
		var v int64
		if err := pool.QueryRow(ctx, "SELECT nextval('"+seq+"')").Scan(&v); err != nil {
			t.Errorf("nextval(%q) as lenny_app must succeed via the FOR ROLE default privilege, got: %v", seq, err)
		} else if v != 1 {
			t.Errorf("nextval(%q) = %d, want 1", seq, v)
		}
		mustExec(t, ctx, pool, "RESET ROLE")
	}

	// lenny_app must not hold CREATE ON SCHEMA: a CREATE SEQUENCE through
	// the lenny_app session is denied, so lenny_app stays least-privilege
	// and the DDL role is genuinely required for provisioning.
	mustExec(t, ctx, pool, "SET ROLE lenny_app")
	_, err = pool.Exec(ctx, "CREATE SEQUENCE app_should_not_create START WITH 1")
	assertDenied(t, err, "CREATE SEQUENCE as lenny_app")
	mustExec(t, ctx, pool, "RESET ROLE")

	// The setval re-seed reads MAX(sequence_number) per tenant on the
	// lenny_ddl connection. Both ledgers carry FORCE ROW LEVEL SECURITY with
	// the §11.7 hard-error tenant-isolation policy
	// (current_setting('app.current_tenant', false)), so this read must run
	// under the tenant-scoped RLS GUC exactly as the runtime pgtenant.InTx
	// re-seed does. Seed one row per ledger for tenant 'acme' (through the
	// lenny_app INSERT grant under the tenant GUC) so the re-seed read has a
	// real row to observe.
	seedLedgerRow(t, ctx, pool, "acme")

	// Positive re-seed read: inside a transaction that first sets
	// app.current_tenant = 'acme', the lenny_ddl SELECT returns the tenant's
	// own row (MAX(sequence_number) = 5). This is the load-bearing RLS
	// interaction the SELECT grant exists to satisfy: the DDL role is not the
	// table owner and holds no BYPASSRLS, so the policy admits only the rows of
	// the tenant named in the GUC.
	for _, tbl := range []string{"billing_events", "audit_log"} {
		maxSeq := ddlReseedRead(t, ctx, pool, tbl, "acme")
		if maxSeq != 5 {
			t.Errorf("lenny_ddl re-seed read of MAX(sequence_number) on %s under the tenant GUC = %d, want 5", tbl, maxSeq)
		}
	}

	// Negative re-seed read: the same lenny_ddl SELECT with app.current_tenant
	// unset fails closed with configuration_invalid (SQLSTATE 42704) rather
	// than returning no rows. This is why the runtime re-seed must scope to
	// the tenant through SET LOCAL app.current_tenant: under the hard-error
	// policy an unset GUC raises, it does not silently read zero rows. Run it
	// in a transaction so SET LOCAL ROLE is bound to the connection that
	// executes the read regardless of pool connection reuse.
	for _, tbl := range []string{"billing_events", "audit_log"} {
		err = ddlReadWithoutTenantGUC(t, ctx, pg.DSN(), tbl, "acme")
		assertPgCode(t, err, pgConfigurationInvalid,
			"lenny_ddl SELECT on "+tbl+" with app.current_tenant unset")
	}

	// No INSERT: lenny_ddl provisions and reads but never writes the ledger.
	// The table-level INSERT privilege check fails closed with
	// insufficient_privilege before RLS policy evaluation, so this holds
	// whether or not the tenant GUC is set.
	mustExec(t, ctx, pool, "SET ROLE lenny_ddl")
	_, err = pool.Exec(ctx,
		"INSERT INTO billing_events (tenant_id, sequence_number, event_type) VALUES ('acme', 6, 'x')")
	assertDenied(t, err, "INSERT into billing_events as lenny_ddl")
	mustExec(t, ctx, pool, "RESET ROLE")

	// The down migration drops the platform sequence and the lenny_ddl role
	// (with its grants and the FOR ROLE default privilege) cleanly.
	applyMigrations(t, ctx, pool, "0173_billing_audit_ddl_role_and_sequences.down.sql")
	if sequenceExists(t, ctx, pool, platformAuditSeq) {
		t.Errorf("migration 0173 down must drop the platform audit sequence")
	}
	if roleExists(t, ctx, pool, "lenny_ddl") {
		t.Errorf("migration 0173 down must drop the lenny_ddl role")
	}
}

func mustExec(t *testing.T, ctx context.Context, pool *pgxpool.Pool, sql string) {
	t.Helper()
	if _, err := pool.Exec(ctx, sql); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

// seedLedgerRow registers a tenant and writes one billing_events and one
// audit_log row for it at sequence_number 5, through the lenny_app INSERT
// grant under SET LOCAL app.current_tenant so the tenant-guard trigger and RLS
// policy admit the write. The seeded rows give the lenny_ddl re-seed read a
// real MAX(sequence_number) to observe under the tenant GUC.
func seedLedgerRow(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenant string) {
	t.Helper()
	// tenants is the registry table (not tenant-scoped): lenny_app holds full
	// DML on it, and billing_events.tenant_id is a foreign key to it.
	mustExec(t, ctx, pool,
		"INSERT INTO tenants (id, genesis_nonce) VALUES ('"+tenant+"', '\\x00')")

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin seed tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// SET LOCAL app.current_tenant scopes the RLS policy and satisfies the
	// tenant-guard trigger for the ledger inserts, mirroring the runtime write
	// path. lenny_app holds INSERT on both ledgers.
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE lenny_app"); err != nil {
		t.Fatalf("set role lenny_app: %v", err)
	}
	if _, err := tx.Exec(ctx, "SET LOCAL app.current_tenant = '"+tenant+"'"); err != nil {
		t.Fatalf("set app.current_tenant: %v", err)
	}
	if _, err := tx.Exec(ctx,
		"INSERT INTO billing_events (tenant_id, sequence_number, event_type) VALUES ($1, 5, 'usage')",
		tenant); err != nil {
		t.Fatalf("seed billing_events row: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO audit_log (tenant_id, sequence_number, event_type, payload, payload_canonical_json)
		 VALUES ($1, 5, 'test.event', '{}', '{}')`, tenant); err != nil {
		t.Fatalf("seed audit_log row: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit seed tx: %v", err)
	}
}

// ddlReseedRead runs the setval re-seed's MAX(sequence_number) read on the
// lenny_ddl connection the way the runtime pgtenant.InTx re-seed does: inside a
// transaction that first sets app.current_tenant so the hard-error RLS policy
// admits the tenant's own rows. It returns the observed MAX(sequence_number).
func ddlReseedRead(t *testing.T, ctx context.Context, pool *pgxpool.Pool, table, tenant string) int64 {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin re-seed read tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE lenny_ddl"); err != nil {
		t.Fatalf("set role lenny_ddl: %v", err)
	}
	if _, err := tx.Exec(ctx, "SET LOCAL app.current_tenant = '"+tenant+"'"); err != nil {
		t.Fatalf("set app.current_tenant: %v", err)
	}
	var maxSeq int64
	if err := tx.QueryRow(ctx,
		"SELECT COALESCE(MAX(sequence_number), 0) FROM "+table+" WHERE tenant_id = $1",
		tenant).Scan(&maxSeq); err != nil {
		t.Fatalf("lenny_ddl re-seed read on %s under the tenant GUC must succeed, got: %v", table, err)
	}
	return maxSeq
}

// ddlReadWithoutTenantGUC runs the lenny_ddl ledger count read on a dedicated
// fresh connection whose transaction sets the role but deliberately leaves
// app.current_tenant unset, returning the resulting error. Under the
// current_setting('app.current_tenant', false) hard-error policy the read must
// fail closed with configuration_invalid rather than returning rows. The read
// uses its own connection because a prior SET LOCAL app.current_tenant on a
// pooled connection registers the app.current_tenant placeholder for that
// session's lifetime, after which current_setting(..., false) reads it as an
// empty string rather than raising; the runtime DDL re-seed always opens the
// tenant context per transaction, so a genuinely-unset read only occurs on a
// connection that has never set it.
func ddlReadWithoutTenantGUC(t *testing.T, ctx context.Context, dsn, table, tenant string) error {
	t.Helper()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect for unset-GUC read: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin unset-GUC read tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE lenny_ddl"); err != nil {
		t.Fatalf("set role lenny_ddl: %v", err)
	}
	var cnt int64
	return tx.QueryRow(ctx,
		"SELECT count(*) FROM "+table+" WHERE tenant_id = $1", tenant).Scan(&cnt)
}

// assertPgCode asserts err is a *pgconn.PgError carrying the given SQLSTATE.
func assertPgCode(t *testing.T, err error, code, what string) {
	t.Helper()
	if err == nil {
		t.Errorf("%s must raise %s, but succeeded", what, code)
		return
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != code {
		t.Errorf("%s: want SQLSTATE %s, got %v", what, code, err)
	}
}

func assertDenied(t *testing.T, err error, what string) {
	t.Helper()
	if err == nil {
		t.Errorf("%s must be denied, but succeeded", what)
		return
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != pgInsufficientPrivilege {
		t.Errorf("%s: want insufficient_privilege (%s), got %v", what, pgInsufficientPrivilege, err)
	}
}

func sequenceExists(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string) bool {
	t.Helper()
	var exists bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS (
			SELECT 1 FROM information_schema.sequences
			WHERE sequence_schema = 'public' AND sequence_name = $1
		)`, name).Scan(&exists); err != nil {
		t.Fatalf("query sequence %s: %v", name, err)
	}
	return exists
}

// readMigration0173 reads a migration file from the embedded FS. The
// migrations_test package cannot reach the internal-package
// mustReadMigration helper, so this local reader mirrors it.
func readMigration0173(t *testing.T, name string) string {
	t.Helper()
	b, err := migrations.FS.ReadFile(name)
	if err != nil {
		t.Fatalf("read migration %s: %v", name, err)
	}
	return string(b)
}

func roleExists(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string) bool {
	t.Helper()
	var exists bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, name).Scan(&exists); err != nil {
		t.Fatalf("query role %s: %v", name, err)
	}
	return exists
}
