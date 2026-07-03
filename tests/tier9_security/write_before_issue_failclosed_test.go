// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security regression for the §13.3 write-before-issue fail-closed
// posture under Path A (the S6 nextval audit-sequencing model). The
// RecordWithAudit / RecordWithRotationAudit / RevokeWithAudit methods bind
// their real-tenant token.exchanged/token.revoked audit INSERT through
// auditstore.AppendInTx -> sealAndInsert inside the same pgtenant.InTx that
// inserts (or stamps) the issued_tokens row, and sealAndInsert draws the
// authoritative sequence_number by nextval on the tenant's per-tenant
// audit_seq_<40hex> sequence. That sequence resolves on the issued-token
// store's own pool (issuedtokenstore.New(w.pgPool), the §12.3 primary in
// production), so when the sequence is absent on that pool the nextval raises
// undefined_table, the whole write-before-issue transaction rolls back, and
// no token is minted, rotated, or revoked.
//
// This is the fail-closed security property S6/S7 introduces on the §13.3
// path: a write that cannot draw its authoritative audit sequence_number must
// not leak a partial write (an issued_tokens row with no audit trail, or an
// audit row with no token). This test uses embedded Postgres directly rather
// than the Kind e2e cluster because it needs to exercise the store method
// against a Postgres where the per-tenant sequence is deliberately withheld,
// which the cluster's provisioned tenants never present.
//
// spec: §13.3, §11.7, §10.2. F-11.2.10.
package tier9_security_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/migrations"
	"github.com/lennylabs/lenny/pkg/gateway/storage/issuedtokenstore"
	auditcatalog "github.com/lennylabs/lenny/pkg/observability/audit"
	embpostgres "github.com/lennylabs/lenny/tests/testinfra/embpg"
)

// pgUndefinedTable is the SQLSTATE (42P01, undefined_table) Postgres raises
// for nextval on a sequence relation that does not exist. A write-before-issue
// method whose audit sequence is absent on its own pool must surface this and
// roll back rather than committing a partial write.
const pgUndefinedTable = "42P01"

// startIssuedTokenPostgres brings up an embedded Postgres carrying the full
// production schema (issued_tokens, audit_log, the tenant guard, and the RLS
// posture) and returns a pool. It applies every migration so the §13.3
// write-before-issue path runs against the real schema, then registers the
// tenant WITHOUT provisioning its per-tenant audit sequence, reproducing the
// state a Path A write hits when the sequence was never created on the store's
// pool.
func startIssuedTokenPostgres(t *testing.T, tenant string) *pgxpool.Pool {
	t.Helper()
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
	t.Cleanup(func() { _ = pg.Stop() })

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)

	pool, err := pgxpool.New(ctx, pg.DSN())
	if err != nil {
		t.Fatalf("connect embedded postgres: %v", err)
	}
	t.Cleanup(pool.Close)

	applyLedgerMigrations(t, ctx, pool)

	// Register the tenant but deliberately leave its per-tenant audit sequence
	// unprovisioned. This is the pre-provisioning Day-1 state the §13.3 path
	// must fail closed on under Path A.
	if _, err := pool.Exec(ctx,
		`INSERT INTO tenants (id, genesis_nonce) VALUES ($1, '\x00')`, tenant); err != nil {
		t.Fatalf("register tenant %q: %v", tenant, err)
	}
	return pool
}

// ledgerMigrations is the curated forward-migration set that carries the
// §13.3 write-before-issue schema: the tenants / issued_tokens / audit_log
// tables and the tenant guard (0001), the lenny_app / lenny_erasure roles and
// the immutability triggers (0002), the current_setting(..., false) hard-error
// RLS form the live gateway runs under, applied to issued_tokens / audit_log /
// billing_events (0057), the issued_tokens dialect-cap columns the store INSERT
// writes (0058), and the migration-0173 lenny_ddl CREATE-privileged role and
// lenny_app USAGE default. This is the same curated set the admin-package
// sequence-provisioning db_test uses, extended with 0058 for the issued_tokens
// columns; the full migration set is not applied because the pgvector-dependent
// migrations (the agent_memory embedding index) require the vector extension the
// embedded PostgreSQL bundle lacks.
var ledgerMigrations = []string{
	"0001_initial_schema.up.sql",
	"0002_rls_immutability_roles.up.sql",
	"0057_tenant_guard_pooler_mode.up.sql",
	"0058_issued_tokens_dialect_cap.up.sql",
	"0173_billing_audit_ddl_role_and_sequences.up.sql",
}

// applyLedgerMigrations applies the curated ledgerMigrations set in order so
// the embedded Postgres carries the §13.3 write-before-issue schema.
func applyLedgerMigrations(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	for _, name := range ledgerMigrations {
		sql, err := migrations.FS.ReadFile(name)
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		if _, err := pool.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("apply migration %s: %v", name, err)
		}
	}
}

// issuedTokenRowCount returns the number of issued_tokens rows for a tenant,
// read as a superuser so no partial write can hide behind RLS.
func issuedTokenRowCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenant string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM issued_tokens WHERE tenant_id = $1`, tenant).Scan(&n); err != nil {
		t.Fatalf("count issued_tokens for %q: %v", tenant, err)
	}
	return n
}

// auditRowCount returns the number of audit_log rows for a tenant.
func auditRowCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenant string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE tenant_id = $1`, tenant).Scan(&n); err != nil {
		t.Fatalf("count audit_log for %q: %v", tenant, err)
	}
	return n
}

// assertUndefinedTable fails the test unless err is a Postgres undefined_table
// error, the SQLSTATE nextval raises for a missing per-tenant audit sequence.
func assertUndefinedTable(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("write-before-issue committed with no per-tenant audit sequence; "+
			"want a fail-closed undefined_table (%s) rollback (a token must not be "+
			"minted/rotated/revoked when its authoritative audit sequence_number "+
			"cannot be drawn on the store's own pool)", pgUndefinedTable)
	}
	var pgErr *pgconn.PgError
	// The error may be wrapped by pgtenant.InTx / the store; unwrap to the
	// underlying Postgres error and require the undefined_table SQLSTATE.
	if !asPgError(err, &pgErr) || pgErr.Code != pgUndefinedTable {
		t.Fatalf("want fail-closed undefined_table (%s), got %v", pgUndefinedTable, err)
	}
}

// asPgError unwraps err into a *pgconn.PgError. It is a thin errors.As wrapper
// kept local so the assertion helper reads at one level of abstraction.
func asPgError(err error, target **pgconn.PgError) bool {
	for e := err; e != nil; {
		if pgErr, ok := e.(*pgconn.PgError); ok {
			*target = pgErr
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := e.(unwrapper)
		if !ok {
			return false
		}
		e = u.Unwrap()
	}
	return false
}

// spec: §13.3 / §11.7 / §10.2 — RecordWithAudit fails closed when the
// tenant's per-tenant audit sequence is absent on the store's own pool: the
// nextval in sealAndInsert raises undefined_table, the write-before-issue
// transaction rolls back, and neither the issued_tokens row nor the audit row
// persists, so no token is minted.
// diagnosis: a failure means the §13.3 write-before-issue path did not fail
// closed under the Path A nextval model when its audit sequence is missing on
// the primary pool. A partial write here would leave an issued token with no
// audit trail or an audit row for a token that was never durably issued. This
// asserts the S6-introduced coupling: against the pre-fix dense-ordinal code
// (which drew no nextval) the write would commit with no sequence, so this
// test fails against pre-fix code.
func TestRecordWithAuditFailsClosedWithoutAuditSequence_spec_13_3(t *testing.T) {
	if testing.Short() {
		t.Skip("downloads the PostgreSQL bundle; skipped under -short")
	}
	const tenant = "acme"
	pool := startIssuedTokenPostgres(t, tenant)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	store := issuedtokenstore.New(pool)
	now := time.Now().UTC()
	tok := issuedtokenstore.IssuedToken{
		JTI: "jti-fc-1", TenantID: tenant, Subject: "alice@acme.com",
		TokenHash: []byte("h"), Scope: []string{"sessions:read"},
		Audience: "lenny-gateway", IssuedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	_, err := store.RecordWithAudit(ctx, tok, string(auditcatalog.EventTokenExchanged),
		json.RawMessage(`{"policy_result":"accepted"}`), now)
	assertUndefinedTable(t, err)

	if n := issuedTokenRowCount(t, ctx, pool, tenant); n != 0 {
		t.Errorf("issued_tokens rows=%d after fail-closed rollback, want 0 (no token minted)", n)
	}
	if n := auditRowCount(t, ctx, pool, tenant); n != 0 {
		t.Errorf("audit_log rows=%d after fail-closed rollback, want 0 (no audit row leaked)", n)
	}
}

// spec: §13.3 line 597 / §11.7 / §10.2 — RecordWithRotationAudit fails closed
// when the audit sequence is absent: neither the new token is minted nor the
// predecessor revoked, so an atomic rotation whose audit sequence_number
// cannot be drawn leaves both tokens in their pre-rotation state.
// diagnosis: a failure means the atomic rotation write-before-issue path did
// not fail closed under Path A when its audit sequence is missing. A partial
// rotation could mint the successor while never revoking the predecessor (two
// live tokens), or revoke the predecessor while never minting the successor.
// Against the pre-fix dense-ordinal code the rotation would commit with no
// sequence, so this test fails against pre-fix code.
func TestRotationWriteBeforeIssueFailsClosedWithoutAuditSequence_spec_13_3(t *testing.T) {
	if testing.Short() {
		t.Skip("downloads the PostgreSQL bundle; skipped under -short")
	}
	const tenant = "globex"
	pool := startIssuedTokenPostgres(t, tenant)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	store := issuedtokenstore.New(pool)
	now := time.Now().UTC()

	// A live predecessor exists (Record does not touch the audit chain, so it
	// commits without the sequence).
	prev := issuedtokenstore.IssuedToken{
		JTI: "jti-prev", TenantID: tenant, Subject: "bob@globex.com",
		TokenHash: []byte("p"), IssuedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	if err := store.Record(ctx, prev); err != nil {
		t.Fatalf("Record predecessor: %v", err)
	}

	next := issuedtokenstore.IssuedToken{
		JTI: "jti-next", TenantID: tenant, Subject: "bob@globex.com",
		TokenHash: []byte("n"), IssuedAt: now.Add(time.Minute), ExpiresAt: now.Add(time.Hour),
	}
	_, _, err := store.RecordWithRotationAudit(ctx, next, "jti-prev",
		"rotation_replaced", string(auditcatalog.EventTokenExchanged),
		json.RawMessage(`{"policy_result":"accepted"}`), now.Add(time.Minute))
	assertUndefinedTable(t, err)

	// The successor was not minted.
	if _, err := store.Get(ctx, tenant, "jti-next"); err == nil {
		t.Errorf("successor token minted despite fail-closed rotation; want it absent")
	}
	// The predecessor was not revoked: the rotation rolled back entirely.
	got, err := store.Get(ctx, tenant, "jti-prev")
	if err != nil {
		t.Fatalf("Get predecessor: %v", err)
	}
	if got.Revoked() {
		t.Errorf("predecessor revoked despite fail-closed rotation; the mint-with-revoke must be atomic")
	}
	if n := auditRowCount(t, ctx, pool, tenant); n != 0 {
		t.Errorf("audit_log rows=%d after fail-closed rotation, want 0", n)
	}
}

// spec: §13.3 line 597 / §16.7 / §11.7 / §10.2 — RevokeWithAudit fails closed
// when the audit sequence is absent: the revoked_at stamp and the token.revoked
// audit row share one transaction, so a missing audit sequence rolls back the
// stamp too, leaving the token live rather than durably revoked with no audit
// trail.
// diagnosis: a failure means the durable revoke write-before-issue path did
// not fail closed under Path A when its audit sequence is missing. A partial
// write could stamp revoked_at with no token.revoked audit row (a §12.2
// rehydration would then observe a revocation with no audit trail). Against the
// pre-fix dense-ordinal code the revoke would commit with no sequence, so this
// test fails against pre-fix code.
func TestRevokeWriteBeforeIssueFailsClosedWithoutAuditSequence_spec_13_3(t *testing.T) {
	if testing.Short() {
		t.Skip("downloads the PostgreSQL bundle; skipped under -short")
	}
	const tenant = "initech"
	pool := startIssuedTokenPostgres(t, tenant)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	store := issuedtokenstore.New(pool)
	now := time.Now().UTC()

	tok := issuedtokenstore.IssuedToken{
		JTI: "jti-rev", TenantID: tenant, Subject: "lenny-admin",
		TokenHash: []byte("r"), IssuedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	if err := store.Record(ctx, tok); err != nil {
		t.Fatalf("Record: %v", err)
	}

	_, err := store.RevokeWithAudit(ctx, tenant, "jti-rev", "rotation_replaced",
		string(auditcatalog.EventTokenRevoked), json.RawMessage(`{"revocation_reason":"rotation_replaced"}`),
		now.Add(time.Minute))
	assertUndefinedTable(t, err)

	// The revoked_at stamp rolled back with the audit row: the token is still
	// live, not revoked with a missing audit trail.
	got, err := store.Get(ctx, tenant, "jti-rev")
	if err != nil {
		t.Fatalf("Get after fail-closed revoke: %v", err)
	}
	if got.Revoked() {
		t.Errorf("token revoked despite fail-closed audit write; the stamp and audit row must share one COMMIT")
	}
	if n := auditRowCount(t, ctx, pool, tenant); n != 0 {
		t.Errorf("audit_log rows=%d after fail-closed revoke, want 0", n)
	}
}
