// SPDX-License-Identifier: MIT

package migrations_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/common/seqname"
	embpostgres "github.com/lennylabs/lenny/tests/testinfra/embpg"
)

// defaultBillingSeq / defaultAuditSeq are the fixed sequence names
// migration 0174 embeds for the built-in "default" tenant. The SQL
// literals must equal the values the shared seqname helper computes,
// verified in TestDefaultTenantSequencesMigrationSQL below.
var (
	defaultBillingSeq = seqname.BillingSequenceName("default")
	defaultAuditSeq   = seqname.AuditSequenceName("default")
)

// TestDefaultTenantSequencesMigrationSQL asserts the static SQL surface
// of migration 0174: the up creates both the fixed "default"-tenant
// billing and audit sequences under the §10.2-derived names and grants
// lenny_app USAGE on each; the down drops both.
//
// spec: §11.2.1 (billing ledger sequence), §11.7 (audit ledger
// sequence), §15.1 (tenant-create sequence provisioning), §10.2
// (length-bounded safe-derived name).
func TestDefaultTenantSequencesMigrationSQL_spec_11_2_1_11_7_15_1_10_2(t *testing.T) {
	up := readMigration0173(t, "0174_default_tenant_billing_audit_sequences.up.sql")

	for _, want := range []string{
		"CREATE SEQUENCE IF NOT EXISTS " + defaultBillingSeq,
		"GRANT USAGE ON SEQUENCE " + defaultBillingSeq + " TO lenny_app",
		"CREATE SEQUENCE IF NOT EXISTS " + defaultAuditSeq,
		"GRANT USAGE ON SEQUENCE " + defaultAuditSeq + " TO lenny_app",
		"START WITH 1 INCREMENT BY 1 NO CYCLE",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("migration 0174 up missing %q", want)
		}
	}

	// The embedded literals must be the §10.2 derivation of "default", not
	// hand-typed digests that could drift from the shared helper.
	if !strings.Contains(up, "billing_seq_37a8eec1ce19687d132fe29051dca629d164e2c4") {
		t.Errorf("migration 0174 up must embed the SHA-256('default') billing derivation")
	}
	if !strings.Contains(up, "audit_seq_37a8eec1ce19687d132fe29051dca629d164e2c4") {
		t.Errorf("migration 0174 up must embed the SHA-256('default') audit derivation")
	}
	if defaultBillingSeq != "billing_seq_37a8eec1ce19687d132fe29051dca629d164e2c4" {
		t.Errorf("seqname billing derivation drifted from the embedded literal: %q", defaultBillingSeq)
	}
	if defaultAuditSeq != "audit_seq_37a8eec1ce19687d132fe29051dca629d164e2c4" {
		t.Errorf("seqname audit derivation drifted from the embedded literal: %q", defaultAuditSeq)
	}

	down := readMigration0173(t, "0174_default_tenant_billing_audit_sequences.down.sql")
	for _, want := range []string{
		"DROP SEQUENCE IF EXISTS " + defaultBillingSeq,
		"DROP SEQUENCE IF EXISTS " + defaultAuditSeq,
	} {
		if !strings.Contains(down, want) {
			t.Errorf("migration 0174 down missing %q", want)
		}
	}
}

// TestDefaultTenantSequencesMigrationDB exercises migration 0174 against
// a live Postgres: after 0053/0054 insert the built-in "default" tenant
// row directly (bypassing the admin-API tenant-create sequence
// provisioning), 0174 must still leave both the default tenant's
// billing and audit sequences resolvable, and a nextval on each under
// the lenny_app session (the runtime ledger-write role) must succeed --
// exactly the write a §17.6 admin-token rotation or any other
// default-tenant-scoped audit/billing Append performs. The down cleanly
// drops both.
//
// diagnosis: a failure here means the built-in "default" tenant's
// first billing or audit write still fails with "relation ... does not
// exist" on a fresh install, because nothing else provisions its
// sequences: the tenant row is inserted directly by migrations
// 0053/0054, never through the admin-API tenant-create path that
// would otherwise call provisionTenantSequences.
//
// spec: §11.2.1, §11.7, §15.1, §10.2.
func TestDefaultTenantSequencesMigrationDB_spec_11_2_1_11_7_15_1_10_2(t *testing.T) {
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

	// 0173 must run first: it creates lenny_app and the DDL-role grant
	// machinery 0174 assumes is already in place.
	applyMigrations(
		t, ctx, pool,
		"0001_initial_schema.up.sql",
		"0002_rls_immutability_roles.up.sql",
		"0057_tenant_guard_pooler_mode.up.sql",
		"0173_billing_audit_ddl_role_and_sequences.up.sql",
	)

	// Reproduce exactly what migrations 0053/0054 do in the real chain:
	// insert the built-in "default" tenant row directly, bypassing the
	// admin-API tenant-create path that provisions sequences. This is the
	// precondition 0174 fixes: a "default" row can exist with neither
	// sequence ever provisioned.
	mustExec(t, ctx, pool, "INSERT INTO tenants (id, genesis_nonce) VALUES ('default', '\\x00') ON CONFLICT (id) DO NOTHING")
	if sequenceExists(t, ctx, pool, defaultBillingSeq) || sequenceExists(t, ctx, pool, defaultAuditSeq) {
		t.Fatal("test setup: default-tenant sequences must not exist before 0174 runs")
	}

	applyMigrations(t, ctx, pool, "0174_default_tenant_billing_audit_sequences.up.sql")

	if !sequenceExists(t, ctx, pool, defaultBillingSeq) {
		t.Fatalf("migration 0174 must create the fixed default-tenant billing sequence %q", defaultBillingSeq)
	}
	if !sequenceExists(t, ctx, pool, defaultAuditSeq) {
		t.Fatalf("migration 0174 must create the fixed default-tenant audit sequence %q", defaultAuditSeq)
	}

	// A nextval under the lenny_app session (the runtime ledger-write
	// role every gateway replica connects as) must succeed on both --
	// this is the exact call the auditstore/billingstore Append path
	// makes for a "default"-tenant write, and the exact call that failed
	// with "relation ... does not exist" before this migration.
	for _, seq := range []string{defaultBillingSeq, defaultAuditSeq} {
		mustExec(t, ctx, pool, "SET ROLE lenny_app")
		var v int64
		if err := pool.QueryRow(ctx, "SELECT nextval('"+seq+"')").Scan(&v); err != nil {
			t.Errorf("nextval(%q) as lenny_app must succeed, got: %v", seq, err)
		} else if v != 1 {
			t.Errorf("nextval(%q) = %d, want 1", seq, v)
		}
		mustExec(t, ctx, pool, "RESET ROLE")
	}

	applyMigrations(t, ctx, pool, "0174_default_tenant_billing_audit_sequences.down.sql")
	if sequenceExists(t, ctx, pool, defaultBillingSeq) {
		t.Errorf("migration 0174 down must drop the default-tenant billing sequence")
	}
	if sequenceExists(t, ctx, pool, defaultAuditSeq) {
		t.Errorf("migration 0174 down must drop the default-tenant audit sequence")
	}
}
