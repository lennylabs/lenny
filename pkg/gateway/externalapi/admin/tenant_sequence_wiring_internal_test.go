// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
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
