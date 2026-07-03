// SPDX-License-Identifier: MIT

package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
)

// lazyPool builds a *pgxpool.Pool that never dials: MinConns=0 keeps the
// pool from opening a connection at construction, so the test stays
// hermetic (tier 1, no Postgres). The pool is used only for pointer
// identity, so it is closed immediately after the assertion.
func lazyPool(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	cfg.MinConns = 0
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("new pool: %v", err)
	}
	return pool
}

// TestNewRouter_ThreadsSequenceProvisioningOptions pins the S3 wiring: the
// CREATE-privileged DDL pools and the §12.3 R-03 billing/audit-shard
// resolvers passed through admin.Options must reach the Router, because the
// S4 tenant-provisioning helper reads exactly those fields to issue the
// per-tenant `CREATE SEQUENCE` on the billing/audit instance and the primary.
// A NewRouter that ignored these Options (the pre-S3 signature) would leave
// every field nil and this test would fail.
//
// spec: §12.3, §15.1. F-11.2.10
func TestNewRouter_ThreadsSequenceProvisioningOptions(t *testing.T) {
	baDDL := lazyPool(t, "postgres://ddl@127.0.0.1:1/lenny")
	defer baDDL.Close()
	primaryDDL := lazyPool(t, "postgres://primary-ddl@127.0.0.1:1/lenny")
	defer primaryDDL.Close()

	billingMarker := lazyPool(t, "postgres://billing-shard@127.0.0.1:1/lenny")
	defer billingMarker.Close()
	auditMarker := lazyPool(t, "postgres://audit-shard@127.0.0.1:1/lenny")
	defer auditMarker.Close()

	var billingArg, auditArg string
	billingResolver := func(_ context.Context, tenantID string) (*pgxpool.Pool, error) {
		billingArg = tenantID
		return billingMarker, nil
	}
	auditResolver := func(_ context.Context, tenantID string) (*pgxpool.Pool, error) {
		auditArg = tenantID
		return auditMarker, nil
	}

	r := NewRouter(tenantstore.NewMemory(), Options{
		BillingAuditDDLPool: baDDL,
		PrimaryDDLPool:      primaryDDL,
		BillingShard:        billingResolver,
		AuditShard:          auditResolver,
	})

	if r.billingAuditDDLPool != baDDL {
		t.Errorf("billingAuditDDLPool = %p, want %p (Options field not threaded)", r.billingAuditDDLPool, baDDL)
	}
	if r.primaryDDLPool != primaryDDL {
		t.Errorf("primaryDDLPool = %p, want %p (Options field not threaded)", r.primaryDDLPool, primaryDDL)
	}
	if r.billingShard == nil {
		t.Fatal("billingShard resolver not threaded (nil)")
	}
	if r.auditShard == nil {
		t.Fatal("auditShard resolver not threaded (nil)")
	}

	// The resolvers must be the exact functions passed, resolving to the
	// billing/audit instance pool for the tenant so S4's setval re-seed reads
	// the ledger MAX on the same instance the sequence is created on.
	gotBilling, err := r.billingShard(context.Background(), "acme")
	if err != nil {
		t.Fatalf("billingShard: %v", err)
	}
	if gotBilling != billingMarker || billingArg != "acme" {
		t.Errorf("billingShard resolved pool=%p arg=%q, want %p / acme", gotBilling, billingArg, billingMarker)
	}
	gotAudit, err := r.auditShard(context.Background(), "acme")
	if err != nil {
		t.Fatalf("auditShard: %v", err)
	}
	if gotAudit != auditMarker || auditArg != "acme" {
		t.Errorf("auditShard resolved pool=%p arg=%q, want %p / acme", gotAudit, auditArg, auditMarker)
	}
}

// TestNewRouter_SequenceProvisioningOptionsDefaultNil confirms a Router built
// without the S3 options leaves the sequence-provisioning fields nil, the
// in-memory / SQLite topology where no Postgres sequence exists. S4's helper
// must treat nil as "no runtime provisioning" rather than dereferencing a nil
// pool.
//
// spec: §12.3, §15.1. F-11.2.10
func TestNewRouter_SequenceProvisioningOptionsDefaultNil(t *testing.T) {
	r := NewRouter(tenantstore.NewMemory(), Options{})
	if r.billingAuditDDLPool != nil {
		t.Error("billingAuditDDLPool should default nil with no Options")
	}
	if r.primaryDDLPool != nil {
		t.Error("primaryDDLPool should default nil with no Options")
	}
	if r.billingShard != nil {
		t.Error("billingShard should default nil with no Options")
	}
	if r.auditShard != nil {
		t.Error("auditShard should default nil with no Options")
	}
}

// TestCreateSequenceStmt_IsIfNotExists pins the §15.1-mandated CREATE
// SEQUENCE IF NOT EXISTS statement form (Decision 7 / S3 "It becomes" text).
// The IF NOT EXISTS clause is load-bearing: it is what makes a concurrent or
// retried provision of the same tenant sequence a benign no-op rather than a
// 42P07 failure that fails the tenant create closed. The pre-fix code emitted
// a bare `CREATE SEQUENCE <name> ...`, so this deterministic tier-1 assertion
// fails against it and passes only when the statement carries IF NOT EXISTS.
//
// spec: §15.1, §10.2. F-11.2.10
func TestCreateSequenceStmt_IsIfNotExists(t *testing.T) {
	stmt := createSequenceStmt("billing_seq_deadbeef")
	if !strings.Contains(stmt, "CREATE SEQUENCE IF NOT EXISTS billing_seq_deadbeef") {
		t.Errorf("statement must use the §15.1 CREATE SEQUENCE IF NOT EXISTS form, got: %q", stmt)
	}
	// The START/INCREMENT/CYCLE clause the spec text names must survive.
	if !strings.Contains(stmt, "START WITH 1 INCREMENT BY 1 NO CYCLE") {
		t.Errorf("statement must carry START WITH 1 INCREMENT BY 1 NO CYCLE, got: %q", stmt)
	}
}

// TestProvisionTenantSequences_NilPoolNoOp pins the in-memory / SQLite
// topology contract: with no CREATE-privileged DDL pool wired,
// provisionTenantSequences is a no-op that returns nil rather than
// dereferencing a nil pool. This is the branch handleCreateTenant and
// upsertTenants exercise in every in-memory admin test, so a helper that
// panicked on a nil pool would break tenant creation on every non-Postgres
// deployment.
//
// spec: §12.3, §15.1. F-11.2.10
func TestProvisionTenantSequences_NilPoolNoOp(t *testing.T) {
	r := NewRouter(tenantstore.NewMemory(), Options{})
	if err := r.provisionTenantSequences(context.Background(), "acme"); err != nil {
		t.Fatalf("provisionTenantSequences with nil DDL pool must be a no-op, got: %v", err)
	}
}

// TestProvisionTenantSequences_CreateFailurePropagates confirms a
// CREATE SEQUENCE failure on the DDL connection surfaces as a wrapped error
// from provisionTenantSequences rather than being swallowed, so
// handleCreateTenant fails the create closed and the operator sees the tenant
// was not fully provisioned. A closed DDL pool makes the first Exec fail
// deterministically without a running Postgres (tier 1).
//
// spec: §15.1. F-11.2.10
func TestProvisionTenantSequences_CreateFailurePropagates(t *testing.T) {
	pool := lazyPool(t, "postgres://ddl@127.0.0.1:1/lenny")
	pool.Close() // closing makes every subsequent Exec fail

	r := NewRouter(tenantstore.NewMemory(), Options{
		BillingAuditDDLPool: pool,
		PrimaryDDLPool:      pool,
	})
	err := r.provisionTenantSequences(context.Background(), "acme")
	if err == nil {
		t.Fatal("provisionTenantSequences must return an error when the DDL Exec fails")
	}
	if !strings.Contains(err.Error(), "provision billing sequence for tenant acme") {
		t.Errorf("error must be wrapped with the provisioning context, got: %v", err)
	}
}

// TestUpsertTenants_SequenceProvisionFailureIsPerEntryError pins the §15.1
// bootstrap seed-path fail-closed coupling: when a seed-created tenant's
// per-tenant sequence provisioning fails, upsertTenants records the row as a
// SEED_STORE_ERROR error rather than reporting it created. The tenant row is
// persisted by tenants.Create before provisioning runs, but the operator sees
// the row failed provisioning (it cannot yet bill or audit) instead of a false
// "created" that hides a tenant with no sequences. A closed DDL pool makes the
// first CREATE SEQUENCE Exec fail deterministically without a running Postgres
// (tier 1). Against the pre-fix bootstrap path (which never called
// provisionTenantSequences) this row would report actionCreated with no error,
// so this test fails against pre-fix code.
//
// spec: §15.1, §11.2.1, §11.7. F-11.2.10
func TestUpsertTenants_SequenceProvisionFailureIsPerEntryError(t *testing.T) {
	pool := lazyPool(t, "postgres://ddl@127.0.0.1:1/lenny")
	pool.Close() // closing makes every subsequent CREATE SEQUENCE Exec fail

	r := NewRouter(tenantstore.NewMemory(), Options{
		BillingAuditDDLPool: pool,
		PrimaryDDLPool:      pool,
	})
	req := httptest.NewRequest("POST", "/v1/admin/bootstrap", nil)

	section := r.upsertTenants(req, []TenantPayload{{ID: "acme"}}, bootstrapOptions{})

	if section.CreatedCount != 0 {
		t.Errorf("createdCount=%d, want 0: a provisioning failure must not report the tenant created", section.CreatedCount)
	}
	if len(section.Errors) != 1 {
		t.Fatalf("errors=%d, want 1 (the sequence-provision failure), section=%+v", len(section.Errors), section)
	}
	if got := section.Errors[0].Code; got != seedStoreErrorCode {
		t.Errorf("error code=%q, want %q", got, seedStoreErrorCode)
	}
	if got := section.Errors[0].ID; got != "acme" {
		t.Errorf("error id=%q, want %q", got, "acme")
	}
	if !strings.Contains(section.Errors[0].Message, "provision billing sequence for tenant acme") {
		t.Errorf("error message must carry the provisioning context, got: %q", section.Errors[0].Message)
	}
}

// TestUpsertTenants_DryRunSkipsSequenceProvision confirms the §15.1 dryRun seed
// path does not attempt sequence provisioning: a dry run validates and reports
// the create action without persisting the tenant or issuing DDL, so a closed
// DDL pool that would fail a real provision leaves a dry run clean. This fences
// the provisionTenantSequences call behind the same !opts.dryRun guard as the
// tenants.Create write.
//
// spec: §15.1. F-11.2.10
func TestUpsertTenants_DryRunSkipsSequenceProvision(t *testing.T) {
	pool := lazyPool(t, "postgres://ddl@127.0.0.1:1/lenny")
	pool.Close() // a real provision would fail against this closed pool

	r := NewRouter(tenantstore.NewMemory(), Options{
		BillingAuditDDLPool: pool,
		PrimaryDDLPool:      pool,
	})
	req := httptest.NewRequest("POST", "/v1/admin/bootstrap?dryRun=true", nil)

	section := r.upsertTenants(req, []TenantPayload{{ID: "acme"}}, bootstrapOptions{dryRun: true})

	if len(section.Errors) != 0 {
		t.Fatalf("dry run must not attempt provisioning or error, got errors=%+v", section.Errors)
	}
	if section.CreatedCount != 1 {
		t.Errorf("createdCount=%d, want 1: a dry run reports the create action without provisioning", section.CreatedCount)
	}
}

// TestHandleCreateTenant_SequenceProvisionFailureReturns500 pins the §15.1
// live POST /v1/admin/tenants fail-closed coupling: when the per-tenant
// sequence provisioning fails after the tenant row is persisted, the handler
// returns 500 INTERNAL_ERROR rather than 201 Created, so an operator sees the
// tenant is not fully provisioned (it cannot yet bill or audit) instead of a
// success that hides a tenant whose first ledger write would fail on nextval of
// a nonexistent relation. A closed DDL pool makes the CREATE SEQUENCE Exec fail
// deterministically without a running Postgres (tier 1). Against the pre-fix
// handler (which never provisioned a sequence) this create returned 201, so
// this test fails against pre-fix code.
//
// spec: §15.1, §11.2.1, §11.7. F-11.2.10
func TestHandleCreateTenant_SequenceProvisionFailureReturns500(t *testing.T) {
	pool := lazyPool(t, "postgres://ddl@127.0.0.1:1/lenny")
	pool.Close() // closing makes the CREATE SEQUENCE Exec fail

	store := tenantstore.NewMemory()
	r := NewRouter(store, Options{
		BillingAuditDDLPool: pool,
		PrimaryDDLPool:      pool,
	}).WithSIEMConfigured(true).WithPgauditConfigured(true)

	body, _ := json.Marshal(TenantPayload{ID: "acme", DisplayName: "Acme Corp"})
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewReader(body))
	req = req.WithContext(authmw.WithPrincipal(req.Context(), authmw.Principal{
		Subject:  "admin@acme.com",
		TenantID: "platform",
		Roles:    []pkgauth.Role{pkgauth.RolePlatformAdmin},
	}))
	rr := httptest.NewRecorder()
	r.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d, want 500: a sequence-provisioning failure must fail the create closed; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "sequence provisioning failed") {
		t.Errorf("body must name the sequence-provisioning failure, got: %s", rr.Body.String())
	}
}
