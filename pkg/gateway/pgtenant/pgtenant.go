// SPDX-License-Identifier: MIT

// Package pgtenant holds the helpers shared by the Postgres-backed
// store implementations. The central one is InTx: every tenant-scoped
// table carries the §12.3 lenny_tenant_guard trigger, which rejects
// any write whose transaction has not set app.current_tenant, so each
// store operation runs inside a transaction that sets it first.
package pgtenant

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// InTx runs fn inside a transaction that has set app.current_tenant
// to tenantID (transaction-local, per §12.3). fn's error is returned
// verbatim — not wrapped — so callers can errors.Is it against their
// store sentinels. The transaction commits when fn returns nil and
// rolls back otherwise.
func InTx(ctx context.Context, pool *pgxpool.Pool, tenantID string, fn func(pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("pgtenant: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx,
		"SELECT set_config('app.current_tenant', $1, true)", tenantID); err != nil {
		return fmt.Errorf("pgtenant: set tenant context: %w", err)
	}
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("pgtenant: commit: %w", err)
	}
	return nil
}

// AllTenantsSentinel is the §4.2 platform-admin cross-tenant value
// for app.current_tenant. The lenny_tenant_guard trigger allows it
// (skipping the per-row tenant_id match check); every
// lenny_tenant_isolation RLS policy includes an OR-clause that lets
// the value bypass per-tenant filtering on SELECT. The gateway sets
// this only on a code path that has verified the caller holds the
// platform-admin role, and per §12.3 line 141 every such call MUST
// also emit a cross_tenant_read audit event.
const AllTenantsSentinel = "__all__"

// InAllTenants runs fn inside a transaction that has set
// app.current_tenant = '__all__'. The platform-admin RBAC check
// and the §12.3 cross_tenant_read audit emission are the caller's
// responsibility; InAllTenants only establishes the DB-level
// sentinel. Use this on any code path that needs to SELECT, INSERT,
// UPDATE, or DELETE rows across tenants (tenant list, usage
// dashboards, erasure jobs).
func InAllTenants(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("pgtenant: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx,
		"SELECT set_config('app.current_tenant', $1, true)", AllTenantsSentinel); err != nil {
		return fmt.Errorf("pgtenant: set all-tenants context: %w", err)
	}
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("pgtenant: commit: %w", err)
	}
	return nil
}

// NullTime maps a zero time.Time to a SQL NULL, so a nullable
// TIMESTAMPTZ column distinguishes "unset" from a real instant.
func NullTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// NullString maps an empty string to a SQL NULL, so a nullable TEXT
// column distinguishes "absent" from "empty".
func NullString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// MonotonicNext returns now (UTC, truncated to the Postgres
// timestamptz microsecond resolution) when it is strictly after prev,
// and prev + 1µs otherwise. Stores use it for updated_at columns so
// the value strictly advances even when two writes land within the
// same microsecond.
func MonotonicNext(prev, now time.Time) time.Time {
	now = now.UTC().Truncate(time.Microsecond)
	if now.After(prev) {
		return now
	}
	return prev.Add(time.Microsecond)
}
