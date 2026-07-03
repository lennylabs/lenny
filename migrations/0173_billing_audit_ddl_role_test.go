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
	"github.com/lennylabs/lenny/pkg/common/seqname"
	embpostgres "github.com/lennylabs/lenny/tests/testinfra/embpg"
)

// pgInsufficientPrivilege is the SQLSTATE Postgres returns for a denied
// privilege (permission denied for schema / sequence / relation).
const pgInsufficientPrivilege = "42501"

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
	// the RLS/immutability posture; 0173 adds the DDL role, the FOR ROLE
	// default privilege, and the platform sequence.
	applyMigrations(
		t, ctx, pool,
		"0001_initial_schema.up.sql",
		"0002_rls_immutability_roles.up.sql",
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

	// lenny_ddl holds SELECT on both ledgers (so the setval re-seed can read
	// MAX(sequence_number)) but no write privilege: an INSERT into either
	// ledger as lenny_ddl is denied. The read is exercised inside the
	// tenant-RLS GUC context both ledgers' FORCE ROW LEVEL SECURITY hard-error
	// policy requires; without a matching row the SELECT simply returns no
	// rows, which is the least-privilege read the re-seed performs.
	mustExec(t, ctx, pool, "SET ROLE lenny_ddl")
	for _, tbl := range []string{"billing_events", "audit_log"} {
		var cnt int64
		if err := pool.QueryRow(ctx,
			"SELECT count(*) FROM "+tbl+" WHERE tenant_id = 'acme'").Scan(&cnt); err != nil {
			t.Errorf("lenny_ddl SELECT on %s must succeed for the setval re-seed, got: %v", tbl, err)
		}
	}
	// No INSERT: lenny_ddl provisions and reads but never writes the ledger.
	_, err = pool.Exec(ctx,
		"INSERT INTO billing_events (tenant_id, sequence_number, event_type) VALUES ('acme', 1, 'x')")
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
