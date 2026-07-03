// SPDX-License-Identifier: MIT

package admin

import (
	"context"
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
