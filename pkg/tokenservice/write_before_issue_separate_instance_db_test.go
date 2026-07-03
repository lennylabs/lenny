// SPDX-License-Identifier: MIT

package tokenservice

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/migrations"
	"github.com/lennylabs/lenny/pkg/common/seqname"
	auditcatalog "github.com/lennylabs/lenny/pkg/observability/audit"

	"github.com/lennylabs/lenny/pkg/gateway/storage/issuedtokenstore"
	embpostgres "github.com/lennylabs/lenny/tests/testinfra/embpg"
)

// This file pins the §13.3 write-before-issue invariant for the standalone
// Token Service in the §12.3 separate-instance topology (LENNY_PG_BILLING_AUDIT_DSN
// set, so the primary/issued-token instance differs from StoreRouter.AuditShard):
// the Token Service's LENNY_POSTGRES_DSN points at the gateway-provisioned
// issued-token/primary instance, so its RecordWithAudit resolves the per-tenant
// audit_seq_<tenant-40hex> sequence the gateway created there through the
// LENNY_PG_PRIMARY_DDL_DSN pool (the CREATE-privileged lenny_ddl role).
//
// The Token Service runs no tenant-creation path, so it never provisions the
// sequence itself; correctness depends on the gateway having created it on the
// shared instance. The test models the gateway's provisioning by creating the
// audit sequence as lenny_ddl (exactly what the admin Router's
// provisionTenantSequences primary-DDL limb does on primaryDDLPool), then draws
// the sequence through the Token Service's issued-token store running as
// lenny_app — the least-privilege login role §11.7 item 7 assigns the gateway
// and Token Service — so the USAGE grant the FOR-ROLE default privilege
// attaches is exercised end to end.
//
// spec: §13.3, §12.3, §11.7, §10.2. F-11.2.10.

// startPrimaryInstance brings up an embedded Postgres carrying the full
// production schema (issued_tokens, audit_log, the tenant guard, RLS, and the
// migration-0173 lenny_ddl / lenny_app roles) and returns a superuser pool, a
// pool connecting as lenny_ddl (the CREATE-privileged DDL role the gateway's
// primary-DDL limb runs under), and a pool connecting as lenny_app (the
// least-privilege role the standalone Token Service store runs under). This is
// the primary/issued-token instance the Token Service's LENNY_POSTGRES_DSN and
// the gateway's issued-token store both point at in the separate-instance
// topology.
func startPrimaryInstance(t *testing.T) (su, ddl, app *pgxpool.Pool) {
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

	su, err := pgxpool.New(ctx, pg.DSN())
	if err != nil {
		t.Fatalf("connect superuser: %v", err)
	}
	t.Cleanup(su.Close)

	applyLedgerMigrationsTS(t, ctx, su)

	// lenny_ddl and lenny_app are created without a login password; give
	// lenny_ddl one so a pool can open as that role (mirroring the
	// operator-supplied LENNY_PG_PRIMARY_DDL_DSN credential), and lenny_app one
	// so the Token Service store connects as its own least-privilege role
	// rather than the superuser.
	if _, err := su.Exec(ctx, "ALTER ROLE lenny_ddl WITH PASSWORD 'ddlpw'"); err != nil {
		t.Fatalf("set lenny_ddl password: %v", err)
	}
	if _, err := su.Exec(ctx, "ALTER ROLE lenny_app WITH LOGIN PASSWORD 'apppw'"); err != nil {
		t.Fatalf("set lenny_app login: %v", err)
	}

	ddlDSN := strings.Replace(pg.DSN(), "lenny:lenny@", "lenny_ddl:ddlpw@", 1)
	ddl, err = pgxpool.New(ctx, ddlDSN)
	if err != nil {
		t.Fatalf("connect lenny_ddl pool: %v", err)
	}
	t.Cleanup(ddl.Close)

	appDSN := strings.Replace(pg.DSN(), "lenny:lenny@", "lenny_app:apppw@", 1)
	app, err = pgxpool.New(ctx, appDSN)
	if err != nil {
		t.Fatalf("connect lenny_app pool: %v", err)
	}
	t.Cleanup(app.Close)

	return su, ddl, app
}

// ledgerMigrationsTS is the curated forward-migration set carrying the §13.3
// write-before-issue schema (tenants / issued_tokens / audit_log, the tenant
// guard, the lenny_app / lenny_ddl roles, the hard-error RLS posture, and the
// issued_tokens dialect-cap columns). The full migration set is not applied
// because the pgvector-dependent migrations require the vector extension the
// embedded PostgreSQL bundle lacks; this is the same curated technique the
// admin-package sequence-provisioning db_test uses.
var ledgerMigrationsTS = []string{
	"0001_initial_schema.up.sql",
	"0002_rls_immutability_roles.up.sql",
	"0057_tenant_guard_pooler_mode.up.sql",
	"0058_issued_tokens_dialect_cap.up.sql",
	"0173_billing_audit_ddl_role_and_sequences.up.sql",
}

// applyLedgerMigrationsTS applies the curated ledgerMigrationsTS set in order so
// the embedded instance carries the §13.3 write-before-issue schema.
func applyLedgerMigrationsTS(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	for _, name := range ledgerMigrationsTS {
		sql, err := migrations.FS.ReadFile(name)
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		if _, err := pool.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("apply migration %s: %v", name, err)
		}
	}
}

// gatewayProvisionAuditSequence models the gateway's provisionTenantSequences
// primary-DDL limb: it creates the tenant's per-tenant audit sequence on the
// primary instance through the CREATE-privileged lenny_ddl connection, so the
// FOR-ROLE default privilege attaches the lenny_app USAGE grant. The standalone
// Token Service never runs this itself; correctness depends on the gateway
// having created the sequence on the shared instance.
func gatewayProvisionAuditSequence(t *testing.T, ctx context.Context, ddl *pgxpool.Pool, tenant string) {
	t.Helper()
	if _, err := ddl.Exec(ctx,
		"CREATE SEQUENCE IF NOT EXISTS "+seqname.AuditSequenceName(tenant)+
			" START WITH 1 INCREMENT BY 1 NO CYCLE"); err != nil {
		t.Fatalf("gateway provision audit sequence for %q as lenny_ddl: %v", tenant, err)
	}
}

// spec: §13.3 / §12.3 / §11.7 / §10.2 — in the separate-instance topology the
// standalone Token Service's RecordWithAudit resolves the per-tenant audit
// sequence the gateway created on the shared primary/issued-token instance
// through the CREATE-privileged DDL role. The Token Service store runs as
// lenny_app and draws nextval through the FOR-ROLE default USAGE grant, so the
// write-before-issue transaction commits and mints the token.
// diagnosis: a failure means the standalone Token Service cannot resolve the
// gateway-provisioned audit sequence on the shared primary instance, so under
// Path A its §13.3 write-before-issue transaction rolls back on nextval and no
// token is issued whenever a separate billing/audit instance is configured.
func TestStandaloneTokenServiceResolvesGatewayProvisionedAuditSequence_spec_13_3_12_3(t *testing.T) {
	if testing.Short() {
		t.Skip("downloads the PostgreSQL bundle; skipped under -short")
	}
	su, ddl, app := startPrimaryInstance(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	const tenant = "acme"
	if _, err := su.Exec(ctx,
		`INSERT INTO tenants (id, genesis_nonce) VALUES ($1, '\x00')`, tenant); err != nil {
		t.Fatalf("register tenant: %v", err)
	}

	// The gateway provisions the tenant's audit sequence on the primary through
	// its LENNY_PG_PRIMARY_DDL_DSN (lenny_ddl) pool. Until it does, the Token
	// Service write-before-issue path would fail closed (proven separately in
	// tier 9); here we confirm the post-provision resolution succeeds.
	gatewayProvisionAuditSequence(t, ctx, ddl, tenant)

	// The standalone Token Service store connects to the same instance as
	// lenny_app (its LENNY_POSTGRES_DSN points at the gateway-provisioned
	// issued-token/primary instance).
	store := issuedtokenstore.New(app)
	now := time.Now().UTC()
	tok := issuedtokenstore.IssuedToken{
		JTI: "jti-ts-1", TenantID: tenant, Subject: "alice@acme.com",
		TokenHash: []byte("h"), Scope: []string{"sessions:read"},
		Audience: "lenny-gateway", IssuedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	row, err := store.RecordWithAudit(ctx, tok, string(auditcatalog.EventTokenExchanged),
		json.RawMessage(`{"policy_result":"accepted","jti":"jti-ts-1"}`), now)
	if err != nil {
		t.Fatalf("RecordWithAudit against gateway-provisioned sequence: %v", err)
	}
	if row.Seq != 1 {
		t.Errorf("audit row seq=%d, want 1 (first nextval on the gateway-created sequence)", row.Seq)
	}

	// The token committed on the shared instance.
	got, err := store.Get(ctx, tenant, "jti-ts-1")
	if err != nil {
		t.Fatalf("Get after commit: %v", err)
	}
	if got.JTI != "jti-ts-1" {
		t.Errorf("issued token JTI=%q, want jti-ts-1", got.JTI)
	}

	// The sequence advanced exactly once: last_value=1, is_called=true. A dense
	// tail ordinal would leave the gateway-created sequence untouched.
	var last int64
	var called bool
	if err := su.QueryRow(ctx,
		`SELECT last_value, is_called FROM `+seqname.AuditSequenceName(tenant)).Scan(&last, &called); err != nil {
		t.Fatalf("read audit sequence state: %v", err)
	}
	if last != 1 || !called {
		t.Errorf("gateway-created sequence state after Token Service draw = (last=%d, called=%v), want (1, true)", last, called)
	}
}
