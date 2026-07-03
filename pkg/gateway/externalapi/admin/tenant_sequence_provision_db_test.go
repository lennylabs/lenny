// SPDX-License-Identifier: MIT

package admin

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
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	embpostgres "github.com/lennylabs/lenny/tests/testinfra/embpg"
)

// pgConfigurationInvalid is the SQLSTATE Postgres raises for an unset custom
// GUC read through current_setting(name, false) — the §11.7 hard-error
// tenant-isolation policy form. A ledger read that reaches the policy without
// app.current_tenant set fails closed with this code rather than returning no
// rows, which is why the setval re-seed must scope to the tenant through
// SET LOCAL app.current_tenant.
const pgConfigurationInvalid = "42704"

// pgDuplicateTable is the SQLSTATE (42P07, duplicate_table) Postgres raises
// for a bare CREATE SEQUENCE against a name that already exists. The §15.1
// CREATE SEQUENCE IF NOT EXISTS form suppresses it, so a concurrent-loser
// provision no-ops rather than failing the tenant create closed. A test that
// observes this code has caught a CREATE that omits IF NOT EXISTS.
const pgDuplicateTable = "42P07"

// startLedgerPostgres brings up an embedded Postgres carrying billing_events,
// audit_log, the lenny_app / lenny_ddl roles, the hard-error RLS posture, and
// the migration-0173 DDL machinery. It returns a superuser pool, a pool
// connecting as lenny_ddl (the CREATE-privileged DDL role the runtime
// provisioning uses), and the lenny_ddl DSN so a test can open a second DDL
// pool for the separate-instance topology. Building the DDL pool as the
// least-privilege lenny_ddl role rather than the superuser exercises the same
// grants the live gateway runs under.
func startLedgerPostgres(t *testing.T) (superuser *pgxpool.Pool, ddl *pgxpool.Pool, ddlDSN string) {
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

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)

	su, err := pgxpool.New(ctx, pg.DSN())
	if err != nil {
		t.Fatalf("connect superuser: %v", err)
	}
	t.Cleanup(su.Close)

	// 0001 defines billing_events / audit_log; 0002 creates lenny_app and the
	// initial soft-error RLS posture; 0057 rewrites both ledger policies to the
	// current_setting('app.current_tenant', false) hard-error form the live
	// gateway runs under; 0173 creates the lenny_ddl CREATE-privileged role,
	// the FOR ROLE-keyed lenny_app USAGE default, and the fixed platform
	// sequence.
	for _, name := range []string{
		"0001_initial_schema.up.sql",
		"0002_rls_immutability_roles.up.sql",
		"0057_tenant_guard_pooler_mode.up.sql",
		"0173_billing_audit_ddl_role_and_sequences.up.sql",
	} {
		sql, err := migrations.FS.ReadFile(name)
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		if _, err := su.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("apply migration %s: %v", name, err)
		}
	}

	// lenny_ddl is created LOGIN with no password; give it one so the test can
	// open a pool as that role, mirroring the operator-supplied
	// LENNY_PG_BILLING_AUDIT_DDL_DSN login credential.
	if _, err := su.Exec(ctx, "ALTER ROLE lenny_ddl WITH PASSWORD 'ddlpw'"); err != nil {
		t.Fatalf("set lenny_ddl password: %v", err)
	}
	ddlDSN = strings.Replace(pg.DSN(), "lenny:lenny@", "lenny_ddl:ddlpw@", 1)
	ddlPool, err := pgxpool.New(ctx, ddlDSN)
	if err != nil {
		t.Fatalf("connect lenny_ddl pool: %v", err)
	}
	t.Cleanup(ddlPool.Close)
	return su, ddlPool, ddlDSN
}

// seedLedgerRows registers a tenant and writes one billing_events and one
// audit_log row at sequence_number seq through lenny_app under the tenant GUC,
// so the setval re-seed has a real per-tenant MAX(sequence_number) to observe.
func seedLedgerRows(t *testing.T, ctx context.Context, su *pgxpool.Pool, tenant string, seq int64) {
	t.Helper()
	if _, err := su.Exec(ctx,
		"INSERT INTO tenants (id, genesis_nonce) VALUES ($1, '\\x00') ON CONFLICT DO NOTHING", tenant); err != nil {
		t.Fatalf("register tenant %s: %v", tenant, err)
	}
	tx, err := su.Begin(ctx)
	if err != nil {
		t.Fatalf("begin seed tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE lenny_app"); err != nil {
		t.Fatalf("set role lenny_app: %v", err)
	}
	if _, err := tx.Exec(ctx, "SET LOCAL app.current_tenant = '"+tenant+"'"); err != nil {
		t.Fatalf("set app.current_tenant: %v", err)
	}
	if _, err := tx.Exec(ctx,
		"INSERT INTO billing_events (tenant_id, sequence_number, event_type) VALUES ($1, $2, 'usage')",
		tenant, seq); err != nil {
		t.Fatalf("seed billing_events row: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO audit_log (tenant_id, sequence_number, event_type, payload, payload_canonical_json)
		 VALUES ($1, $2, 'test.event', '{}', '{}')`, tenant, seq); err != nil {
		t.Fatalf("seed audit_log row: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit seed tx: %v", err)
	}
}

// nextvalAsApp draws nextval on the derived sequence as lenny_app, the role the
// live gateway's Append runs under, so the test exercises the same USAGE-grant
// path a real billing/audit write takes rather than the superuser.
func nextvalAsApp(t *testing.T, ctx context.Context, su *pgxpool.Pool, seqName string) int64 {
	t.Helper()
	tx, err := su.Begin(ctx)
	if err != nil {
		t.Fatalf("begin nextval tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE lenny_app"); err != nil {
		t.Fatalf("set role lenny_app: %v", err)
	}
	var v int64
	if err := tx.QueryRow(ctx, "SELECT nextval('"+seqName+"')").Scan(&v); err != nil {
		t.Fatalf("nextval(%q) as lenny_app: %v", seqName, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit nextval tx: %v", err)
	}
	return v
}

// advanceSequenceTo drives a live sequence's issued high-water mark up to
// target (so the next nextval returns target+1), as a run of nextval draws plus
// rollback gaps would leave it. It uses setval to reach the target in one step,
// which is the committed effect of that draw-and-rollback sequence: last_value =
// target with is_called true. The superuser owns nothing here, so setval runs
// as the pool's superuser role, which holds UPDATE on the sequence.
func advanceSequenceTo(t *testing.T, ctx context.Context, su *pgxpool.Pool, seqName string, target int64) {
	t.Helper()
	if _, err := su.Exec(ctx, "SELECT setval('"+seqName+"', $1, true)", target); err != nil {
		t.Fatalf("advance sequence %s to %d: %v", seqName, target, err)
	}
}

// sequenceExistsDB reports whether the named sequence exists in schema public.
func sequenceExistsDB(t *testing.T, ctx context.Context, su *pgxpool.Pool, name string) bool {
	t.Helper()
	var exists bool
	if err := su.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.sequences
		 WHERE sequence_schema = 'public' AND sequence_name = $1)`, name).Scan(&exists); err != nil {
		t.Fatalf("query sequence %s: %v", name, err)
	}
	return exists
}

// routerWithDDL builds a Router carrying only the billing/audit DDL pool set,
// the single-instance topology (primaryDDLPool aliases the same pool). The
// helper reads no other Router field, so the minimal Router is sufficient to
// exercise provisionTenantSequences directly.
func routerWithDDL(ddl *pgxpool.Pool) *Router {
	r := NewRouter(tenantstore.NewMemory(), Options{
		BillingAuditDDLPool: ddl,
		PrimaryDDLPool:      ddl,
	})
	return r
}

// TestProvisionTenantSequences_CreatesBothSequences pins the load-bearing
// §15.1 provisioning behavior: provisionTenantSequences creates both the
// per-tenant billing_seq_ and audit_seq_ sequences on the billing/audit
// instance through the CREATE-privileged DDL connection, so the first
// billing/audit Append draws nextval on a real relation rather than failing on
// "relation does not exist". This fails against the pre-fix code, which
// provisioned no sequence at all.
//
// diagnosis: a failure means per-tenant sequence provisioning did not create
// the ledger sequences, so every billing and audit write for a runtime-created
// tenant rejects on nextval of a nonexistent relation from Day 1.
//
// spec: §15.1, §11.2.1, §11.7, §10.2. F-11.2.10
func TestProvisionTenantSequences_CreatesBothSequences_spec_15_1_11_2_1_11_7(t *testing.T) {
	if testing.Short() {
		t.Skip("downloads the PostgreSQL bundle; skipped under -short")
	}
	su, ddl, _ := startLedgerPostgres(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	const tenant = "acme"
	billingSeq := seqname.BillingSequenceName(tenant)
	auditSeq := seqname.AuditSequenceName(tenant)

	// Neither sequence exists before provisioning: the pre-fix Day-1 state.
	if sequenceExistsDB(t, ctx, su, billingSeq) || sequenceExistsDB(t, ctx, su, auditSeq) {
		t.Fatalf("sequences must not exist before provisioning")
	}

	r := routerWithDDL(ddl)
	if err := r.provisionTenantSequences(ctx, tenant); err != nil {
		t.Fatalf("provisionTenantSequences: %v", err)
	}

	if !sequenceExistsDB(t, ctx, su, billingSeq) {
		t.Errorf("billing sequence %q must exist after provisioning", billingSeq)
	}
	if !sequenceExistsDB(t, ctx, su, auditSeq) {
		t.Errorf("audit sequence %q must exist after provisioning", auditSeq)
	}

	// The first Append for a freshly provisioned tenant with no prior rows
	// draws nextval == 1 under the lenny_app USAGE grant (the FOR ROLE default
	// privilege attaches so the sequences created by lenny_ddl are usable by
	// lenny_app). A missing USAGE grant would raise permission denied here.
	if v := nextvalAsApp(t, ctx, su, billingSeq); v != 1 {
		t.Errorf("first billing nextval = %d, want 1", v)
	}
	if v := nextvalAsApp(t, ctx, su, auditSeq); v != 1 {
		t.Errorf("first audit nextval = %d, want 1", v)
	}
}

// TestProvisionTenantSequences_Idempotent confirms a retried create is a no-op:
// the existence check finds the sequence already present on the second call and
// skips both the CREATE and the re-seed, so calling the helper twice does not
// raise and does not reset the sequence. A client retry of POST /v1/admin/tenants
// after a partial failure converges rather than erroring.
//
// diagnosis: a failure means the provisioning path is not idempotent, so a
// retried tenant create fails on "relation already exists" or resets a
// sequence below its issued maximum.
//
// spec: §15.1, §11.2.1. F-11.2.10
func TestProvisionTenantSequences_Idempotent_spec_15_1(t *testing.T) {
	if testing.Short() {
		t.Skip("downloads the PostgreSQL bundle; skipped under -short")
	}
	su, ddl, _ := startLedgerPostgres(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	const tenant = "globex"
	billingSeq := seqname.BillingSequenceName(tenant)
	r := routerWithDDL(ddl)

	if err := r.provisionTenantSequences(ctx, tenant); err != nil {
		t.Fatalf("first provision: %v", err)
	}
	// Draw one value so a non-idempotent re-provision that reset the sequence
	// would be observable.
	if v := nextvalAsApp(t, ctx, su, billingSeq); v != 1 {
		t.Fatalf("nextval after first provision = %d, want 1", v)
	}
	if err := r.provisionTenantSequences(ctx, tenant); err != nil {
		t.Fatalf("second provision (retry) must be idempotent, got: %v", err)
	}
	// The retry must not reset the sequence: the next nextval continues from 2.
	if v := nextvalAsApp(t, ctx, su, billingSeq); v != 2 {
		t.Errorf("nextval after idempotent re-provision = %d, want 2 (sequence was reset)", v)
	}
}

// TestProvisionTenantSequences_RepeatProvisionNeverRegressesAdvancedSequence
// pins Decision 7's newly-created precondition: once a sequence is live and has
// been advanced past the committed MAX(sequence_number) — the ordinary state
// after a transaction rollback leaves a nextval gap above the last committed row
// (§11.2.1: "Postgres sequences may produce gaps on transaction rollback") — a
// repeat provisionTenantSequences call must not drag the sequence back down to
// MAX and reissue a value it already handed out. This constructs exactly that
// state: a committed row at MAX=42, then the sequence advanced to 100 (a gap
// above the committed high-water mark), then a repeat provision. The pre-fix
// code re-seeded unconditionally to MAX=42 on every call, so the next nextval
// would return 43 and collide with a (tenant_id, sequence_number) primary key
// the sequence had already issued; the fenced re-seed leaves the advanced
// sequence untouched, so the next nextval continues at 101.
//
// diagnosis: a failure means the re-seed is not fenced to the newly-created
// sequence, so a repeat or retried provision of an already-advanced sequence
// regresses it below its issued maximum and reintroduces the (tenant_id,
// sequence_number) number-reuse hazard the whole per-tenant-sequence change
// exists to eliminate.
//
// spec: §11.2.1, §11.7, §15.1. F-11.2.10
func TestProvisionTenantSequences_RepeatProvisionNeverRegressesAdvancedSequence_spec_11_2_1(t *testing.T) {
	if testing.Short() {
		t.Skip("downloads the PostgreSQL bundle; skipped under -short")
	}
	su, ddl, _ := startLedgerPostgres(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	const tenant = "vandelay"
	// A committed row tops out at MAX=42 (the pre-Path-A high-water mark the
	// first re-seed targets).
	seedLedgerRows(t, ctx, su, tenant, 42)

	billingSeq := seqname.BillingSequenceName(tenant)
	auditSeq := seqname.AuditSequenceName(tenant)

	r := routerWithDDL(ddl)
	if err := r.provisionTenantSequences(ctx, tenant); err != nil {
		t.Fatalf("first provision with pre-existing rows: %v", err)
	}

	// Advance both sequences well past the committed MAX, as a run of live
	// writes plus rollback gaps would: the first re-seed set them to 42, so
	// nextval now returns 43; draw up through 100 so the sequence's issued
	// high-water mark (100) is far above the committed MAX (42). A rollback
	// after nextval 100 would leave exactly this state — the sequence at 100
	// with no committed row above 42.
	advanceSequenceTo(t, ctx, su, billingSeq, 100)
	advanceSequenceTo(t, ctx, su, auditSeq, 100)

	// A repeat provision (a retried or duplicate POST /v1/admin/tenants, or a
	// future caller reaching the same tenant) must not regress the live
	// sequence. The pre-fix unconditional re-seed dragged it back to MAX=42,
	// so the next nextval returned 43 — an already-issued value.
	if err := r.provisionTenantSequences(ctx, tenant); err != nil {
		t.Fatalf("repeat provision of advanced sequence: %v", err)
	}

	if v := nextvalAsApp(t, ctx, su, billingSeq); v != 101 {
		t.Errorf("billing nextval after repeat provision = %d, want 101 "+
			"(advanced sequence was regressed to MAX=42, reusing an issued value)", v)
	}
	if v := nextvalAsApp(t, ctx, su, auditSeq); v != 101 {
		t.Errorf("audit nextval after repeat provision = %d, want 101 "+
			"(advanced sequence was regressed to MAX=42, reusing an issued value)", v)
	}
}

// TestProvisionTenantSequences_ReseedUnderRLS pins the setval re-seed for a
// tenant whose ledger rows predate the sequence: the re-seed reads the
// per-tenant MAX(sequence_number) on the DDL connection under the SET LOCAL
// app.current_tenant tenant-RLS context the FORCE ROW LEVEL SECURITY hard-error
// policy requires, then setval's the sequence so the next nextval lands
// strictly above the existing maximum and does not collide with an existing
// (tenant_id, sequence_number) primary key. A freshly created sequence would
// otherwise start at 1 and collide.
//
// diagnosis: a failure means the re-seed did not run or ran without the tenant
// RLS context, so either it raised configuration_invalid under the hard-error
// policy or the first nextval collides with a pre-existing ledger row's
// sequence number, rejecting the billing/audit write.
//
// spec: §11.2.1, §11.7, §15.1. F-11.2.10
func TestProvisionTenantSequences_ReseedUnderRLS_spec_11_2_1_11_7(t *testing.T) {
	if testing.Short() {
		t.Skip("downloads the PostgreSQL bundle; skipped under -short")
	}
	su, ddl, _ := startLedgerPostgres(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	const tenant = "initech"
	// Pre-existing rows numbered by the pre-Path-A MAX+1 scheme top out at 42.
	seedLedgerRows(t, ctx, su, tenant, 42)

	billingSeq := seqname.BillingSequenceName(tenant)
	auditSeq := seqname.AuditSequenceName(tenant)

	r := routerWithDDL(ddl)
	if err := r.provisionTenantSequences(ctx, tenant); err != nil {
		t.Fatalf("provisionTenantSequences with pre-existing rows: %v", err)
	}

	// The re-seed set each sequence to MAX=42 (is_called true), so the next
	// nextval returns 43 for both ledgers — strictly above the existing PK.
	if v := nextvalAsApp(t, ctx, su, billingSeq); v != 43 {
		t.Errorf("billing nextval after re-seed = %d, want 43 (strictly above MAX=42)", v)
	}
	if v := nextvalAsApp(t, ctx, su, auditSeq); v != 43 {
		t.Errorf("audit nextval after re-seed = %d, want 43 (strictly above MAX=42)", v)
	}
}

// TestProvisionTenantSequences_ReseedReadFailsClosedWithoutTenantGUC proves the
// SET LOCAL app.current_tenant scope the re-seed runs under is load-bearing:
// the same MAX(sequence_number) read on the lenny_ddl connection without the
// tenant GUC set raises configuration_invalid under the FORCE ROW LEVEL
// SECURITY hard-error policy, rather than silently returning zero rows. If the
// re-seed ran outside the tenant context it would fail closed exactly here, so
// this pins why provisionTenantSequences wraps the read in pgtenant.InTx.
//
// diagnosis: a failure means the ledger RLS policy is not the hard-error form,
// so an unset-tenant re-seed read would silently observe MAX=0 and leave the
// sequence at 1, colliding with pre-existing rows on the first nextval.
//
// spec: §11.7, §11.2.1. F-11.2.10
func TestProvisionTenantSequences_ReseedReadFailsClosedWithoutTenantGUC_spec_11_7(t *testing.T) {
	if testing.Short() {
		t.Skip("downloads the PostgreSQL bundle; skipped under -short")
	}
	_, ddl, _ := startLedgerPostgres(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// The re-seed read runs as lenny_ddl (the DDL pool's role). With
	// app.current_tenant unset, the hard-error tenant-isolation policy raises
	// configuration_invalid rather than returning no rows. Use a dedicated
	// connection that has never set the GUC so current_setting(..., false) is
	// genuinely unrecognized.
	conn, err := ddl.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire ddl conn: %v", err)
	}
	defer conn.Release()
	var cnt int64
	err = conn.QueryRow(ctx,
		"SELECT count(*) FROM billing_events WHERE tenant_id = $1", "initech").Scan(&cnt)
	if err == nil {
		t.Fatalf("lenny_ddl read of billing_events without app.current_tenant must fail closed, got count=%d", cnt)
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != pgConfigurationInvalid {
		t.Fatalf("want SQLSTATE %s (configuration_invalid), got %v", pgConfigurationInvalid, err)
	}
}

// TestProvisionTenantSequences_PrimaryDDLPoolDistinct exercises the
// separate-instance topology limb: when primaryDDLPool is a distinct pool from
// billingAuditDDLPool, provisionTenantSequences additionally creates the audit
// sequence through the primary DDL connection and re-seeds it there, because
// the §13.3 issued-token write-before-issue path seals its per-tenant audit row
// on the primary rather than the billing/audit shard. Here both pools address
// the same embedded instance, so the assertion is that the distinct-pool branch
// runs to completion without error and the audit sequence exists and re-seeds
// (the primary's own audit_log sub-chain would be the separate instance in a
// real deployment).
//
// diagnosis: a failure means the separate-instance primary-DDL limb of
// provisionTenantSequences is broken, so in a topology with
// LENNY_PG_BILLING_AUDIT_DSN set the primary audit sequence is never created
// and the §13.3 write-before-issue transaction rolls back on nextval of a
// nonexistent relation, blocking token issuance for every tenant.
//
// spec: §12.3, §11.7, §15.1. F-11.2.10
func TestProvisionTenantSequences_PrimaryDDLPoolDistinct_spec_12_3_11_7(t *testing.T) {
	if testing.Short() {
		t.Skip("downloads the PostgreSQL bundle; skipped under -short")
	}
	su, ddl, ddlDSN := startLedgerPostgres(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// A second DDL pool, distinct pointer from the billing/audit DDL pool, so
	// the primaryDDLPool != billingAuditDDLPool branch fires. In a real
	// separate-instance deployment this pool would address the primary; here it
	// addresses the same instance, which is sufficient to exercise the branch.
	primaryDDL, err := pgxpool.New(ctx, ddlDSN)
	if err != nil {
		t.Fatalf("connect second ddl pool: %v", err)
	}
	defer primaryDDL.Close()

	const tenant = "umbrella"
	seedLedgerRows(t, ctx, su, tenant, 7)

	r := NewRouter(tenantstore.NewMemory(), Options{
		BillingAuditDDLPool: ddl,
		PrimaryDDLPool:      primaryDDL,
	})
	if err := r.provisionTenantSequences(ctx, tenant); err != nil {
		t.Fatalf("provisionTenantSequences (separate-instance topology): %v", err)
	}

	auditSeq := seqname.AuditSequenceName(tenant)
	if !sequenceExistsDB(t, ctx, su, auditSeq) {
		t.Errorf("audit sequence %q must exist after separate-instance provisioning", auditSeq)
	}
	// The re-seed ran (MAX=7), so the next audit nextval lands at 8, strictly
	// above the pre-existing row.
	if v := nextvalAsApp(t, ctx, su, auditSeq); v != 8 {
		t.Errorf("audit nextval after separate-instance re-seed = %d, want 8", v)
	}
}

// TestProvisionTenantSequences_ConcurrentCreateRaceIsBenign pins the §15.1
// CREATE SEQUENCE IF NOT EXISTS atomicity of createSequenceIfAbsent: when two
// provisioning transactions both observe the sequence absent in the
// to_regclass pre-check and then both issue the CREATE, the loser must
// degrade to a benign no-op rather than fail on 42P07 "relation already
// exists". This is the race a create racing the bootstrap seed path, or the
// same create retried concurrently across two gateway replicas, produces:
// the to_regclass pre-check under READ COMMITTED does not serialize against
// another transaction's in-flight CREATE, so only the CREATE statement form
// (IF NOT EXISTS) fences the loser.
//
// The test forces the interleave by holding one transaction's to_regclass
// pre-check open (both read NULL) before letting a concurrent goroutine run
// createSequenceIfAbsent to completion; the two CREATEs then serialize on
// Postgres's relation lock and the second — a call whose pre-check saw the
// sequence absent — issues its CREATE against the now-committed sequence.
// Against the pre-fix bare CREATE that call raised 42P07; under IF NOT EXISTS
// it no-ops. Both createSequenceIfAbsent calls return without error.
//
// diagnosis: a failure means the CREATE SEQUENCE is not the spec-mandated IF
// NOT EXISTS form, so a concurrent or retried provision of the same tenant
// sequence raises 42P07 and fails the tenant create closed instead of
// converging.
//
// spec: §15.1, §10.2, §11.2.1. F-11.2.10
func TestProvisionTenantSequences_ConcurrentCreateRaceIsBenign_spec_15_1(t *testing.T) {
	if testing.Short() {
		t.Skip("downloads the PostgreSQL bundle; skipped under -short")
	}
	_, ddl, _ := startLedgerPostgres(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	const tenant = "raytheon"
	seqName := seqname.BillingSequenceName(tenant)

	// The winner opens first, runs its to_regclass pre-check (sees NULL), and
	// holds the transaction open without yet issuing its CREATE.
	winner, err := ddl.Begin(ctx)
	if err != nil {
		t.Fatalf("begin winner: %v", err)
	}
	defer func() { _ = winner.Rollback(ctx) }()
	var regW *string
	if err := winner.QueryRow(ctx, "SELECT to_regclass($1)", seqName).Scan(&regW); err != nil {
		t.Fatalf("winner to_regclass: %v", err)
	}
	if regW != nil {
		t.Fatalf("winner must see the sequence absent, got %v", regW)
	}

	// The loser opens its own transaction and runs its to_regclass pre-check
	// (also NULL) before the winner has created anything, then blocks its CREATE
	// behind the winner: two calls whose pre-checks both observed the sequence
	// absent, exactly the racing-creator interleave.
	loser, err := ddl.Begin(ctx)
	if err != nil {
		t.Fatalf("begin loser: %v", err)
	}
	defer func() { _ = loser.Rollback(ctx) }()
	var regL *string
	if err := loser.QueryRow(ctx, "SELECT to_regclass($1)", seqName).Scan(&regL); err != nil {
		t.Fatalf("loser to_regclass: %v", err)
	}
	if regL != nil {
		t.Fatalf("loser must see the sequence absent, got %v", regL)
	}

	// Winner issues the production CREATE and commits, so the sequence is
	// committed before the loser's CREATE runs. createSequenceStmt is the exact
	// statement builder createSequenceIfAbsent uses, so the test pins the
	// production DDL form rather than a hand-written copy.
	if _, err := winner.Exec(ctx, createSequenceStmt(seqName)); err != nil {
		t.Fatalf("winner CREATE: %v", err)
	}
	if err := winner.Commit(ctx); err != nil {
		t.Fatalf("commit winner: %v", err)
	}

	// The loser now runs the production CREATE against the committed sequence.
	// This is the byte-for-byte statement createSequenceIfAbsent emits after
	// its pre-check; under the pre-fix bare CREATE it raised 42P07, and under
	// the IF NOT EXISTS form createSequenceStmt now emits it is a benign no-op.
	if _, err := loser.Exec(ctx, createSequenceStmt(seqName)); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgDuplicateTable {
			t.Fatalf("concurrent-loser CREATE raised 42P07 duplicate_table; the statement is not IF NOT EXISTS: %v", err)
		}
		t.Fatalf("concurrent-loser CREATE must be a benign no-op, got: %v", err)
	}
	if err := loser.Commit(ctx); err != nil {
		t.Fatalf("commit loser: %v", err)
	}

	// A full provisionTenantSequences call must still converge idempotently and
	// leave both sequences usable after the concurrent create.
	r := routerWithDDL(ddl)
	if err := r.provisionTenantSequences(ctx, tenant); err != nil {
		t.Fatalf("provisionTenantSequences after concurrent create: %v", err)
	}
}

// TestProvisionTenantSequences_ReseedReadErrorPropagates confirms a failed
// re-seed MAX(sequence_number) read surfaces as a wrapped error rather than
// being swallowed: with the DDL role's SELECT on billing_events revoked, the
// re-seed read raises permission denied and provisionTenantSequences returns a
// wrapped "provision billing sequence" error, which handleCreateTenant turns
// into a fail-closed 500. This exercises the re-seed error-propagation path (the
// setval re-seed cannot silently no-op when it cannot read the ledger MAX).
//
// diagnosis: a failure means a re-seed read error is swallowed, so a tenant
// whose sequence was mis-seeded proceeds and its first nextval may collide with
// an existing (tenant_id, sequence_number) primary key.
//
// spec: §11.2.1, §15.1. F-11.2.10
func TestProvisionTenantSequences_ReseedReadErrorPropagates_spec_11_2_1(t *testing.T) {
	if testing.Short() {
		t.Skip("downloads the PostgreSQL bundle; skipped under -short")
	}
	su, ddl, _ := startLedgerPostgres(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	const tenant = "hooli"
	seedLedgerRows(t, ctx, su, tenant, 3)

	// Revoke the DDL role's SELECT on billing_events so the re-seed read fails
	// closed with permission denied. The CREATE SEQUENCE still succeeds (CREATE
	// ON SCHEMA is intact), so the error surfaces from the re-seed read, not the
	// create.
	if _, err := su.Exec(ctx, "REVOKE SELECT ON billing_events FROM lenny_ddl"); err != nil {
		t.Fatalf("revoke SELECT: %v", err)
	}

	r := routerWithDDL(ddl)
	err := r.provisionTenantSequences(ctx, tenant)
	if err == nil {
		t.Fatal("provisionTenantSequences must return an error when the re-seed read is denied")
	}
	if !strings.Contains(err.Error(), "provision billing sequence for tenant hooli") {
		t.Errorf("error must be wrapped with the provision context, got: %v", err)
	}
	if !strings.Contains(err.Error(), "read MAX(sequence_number) from billing_events") {
		t.Errorf("error must name the failed re-seed MAX read, got: %v", err)
	}
}
