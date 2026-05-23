// SPDX-License-Identifier: MIT

// Package pgtenant holds the helpers shared by the Postgres-backed
// store implementations. The central one is InTx: every tenant-scoped
// table carries the §12.3 lenny_tenant_guard trigger, which rejects
// any write whose transaction has not set app.current_tenant, so each
// store operation runs inside a transaction that sets it first.
package pgtenant

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// tenantIDPattern is the §12.3 line 53 / migration 0047
// lenny_tenant_guard format check the trigger applies before any row
// is touched. The helper re-validates client-side so the SET LOCAL
// form below can interpolate the value without any chance of SQL
// injection: only [A-Za-z0-9_-]{1,128} reach the SQL string.
// spec: §4.2 line 163 / §12.3 line 53.
var tenantIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// ErrInvalidTenantID reports a tenant ID that does not match the
// §12.3 line 53 format check. The helper rejects the value before
// the SET LOCAL statement runs so a malicious caller cannot smuggle
// SQL into the tenant context. The trigger would catch this even if
// the helper did not, but failing client-side keeps the error
// classification clean and avoids a round trip.
var ErrInvalidTenantID = errors.New("pgtenant: tenant id is not a valid identifier")

// quoteTenantID renders a validated tenant ID as a SQL string
// literal: every embedded single quote is doubled per the SQL spec.
// The regex above already restricts the alphabet to [A-Za-z0-9_-], so
// no escape is strictly required, but doubling is kept as
// defense-in-depth in case a future migration relaxes the format.
func quoteTenantID(tenantID string) string {
	out := make([]byte, 0, len(tenantID)+2)
	out = append(out, '\'')
	for i := 0; i < len(tenantID); i++ {
		if tenantID[i] == '\'' {
			out = append(out, '\'', '\'')
			continue
		}
		out = append(out, tenantID[i])
	}
	out = append(out, '\'')
	return string(out)
}

// InTx runs fn inside a transaction that has set app.current_tenant
// to tenantID (transaction-local, per §12.3). fn's error is returned
// verbatim — not wrapped — so callers can errors.Is it against their
// store sentinels. The transaction commits when fn returns nil and
// rolls back otherwise.
//
// The statement uses the spec-mandated SET LOCAL form
// (`SET LOCAL app.current_tenant = '<id>'`). Postgres SET LOCAL does
// not accept query parameters, so the helper validates the tenant ID
// against the §12.3 line 53 format check (tenantIDPattern) before
// interpolating; an invalid ID returns ErrInvalidTenantID and never
// touches the connection. The trigger re-validates the value, so the
// regex is defense-in-depth. The sentinel values `__all__` and
// `__unset__` are also rejected: callers wanting cross-tenant access
// must use InAllTenants instead, and the unset sentinel is never a
// valid concrete tenant id.
// spec: §4.2 line 163 ("SET LOCAL app.current_tenant = '<tenant_id>'").
func InTx(ctx context.Context, pool *pgxpool.Pool, tenantID string, fn func(pgx.Tx) error) error {
	if tenantID == AllTenantsSentinel || tenantID == "__unset__" {
		return ErrInvalidTenantID
	}
	if !tenantIDPattern.MatchString(tenantID) {
		return ErrInvalidTenantID
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("pgtenant: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// spec: §4.2 line 163 — SET LOCAL form. The value is interpolated
	// after the regex check above; SET LOCAL has no parameter binding,
	// so the regex is the load-bearing guard.
	stmt := "SET LOCAL app.current_tenant = " + quoteTenantID(tenantID)
	if _, err := tx.Exec(ctx, stmt); err != nil {
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
//
// The helper additionally sets lenny.allow_all_sentinel = 'true'
// via SET LOCAL, satisfying the §4.2 line 165 pooler-mode guard:
// the lenny_tenant_guard trigger rejects the __all__ sentinel unless
// this GUC is explicitly opted in, so out-of-process leaks that
// bypass this helper fail closed when LENNY_POOLER_MODE = external.
// spec: §4.2 line 165 ("the __all__ sentinel is rejected by the
// lenny_tenant_guard trigger when LENNY_POOLER_MODE = external and
// the trigger is configured to allow only concrete tenant IDs").
func InAllTenants(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("pgtenant: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// spec: §4.2 line 165 — opt in to the __all__ sentinel before
	// setting app.current_tenant. The trigger reads
	// lenny.allow_all_sentinel = 'true' as the platform-admin opt-in.
	if _, err := tx.Exec(ctx,
		"SET LOCAL lenny.allow_all_sentinel = 'true'"); err != nil {
		return fmt.Errorf("pgtenant: opt in to all-tenants sentinel: %w", err)
	}
	// spec: §4.2 line 163 — SET LOCAL form. The constant AllTenantsSentinel
	// is a static literal, so no parameter binding is needed.
	if _, err := tx.Exec(ctx,
		"SET LOCAL app.current_tenant = '"+AllTenantsSentinel+"'"); err != nil {
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

// InAdminMode runs fn inside a transaction that has set
// lenny.admin_mode = 'true'. The §4.2 line 177 trigger on
// runtime_tenant_access and pool_tenant_access rejects any write
// without this GUC, so platform-admin code paths that mutate the
// tenant-access join tables MUST wrap their writes in this helper.
// The platform-admin RBAC verification is the caller's
// responsibility — InAdminMode only sets the DB-level sentinel that
// satisfies the lenny_admin_mode_required trigger.
//
// spec: §4.2 line 177 ("BEFORE INSERT/UPDATE/DELETE trigger ...
// validates that current_setting('lenny.admin_mode', true) = 'true'
// (set by the gateway only for platform-admin code paths via SET
// LOCAL)")
func InAdminMode(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("pgtenant: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// spec: §4.2 line 163 / line 177 — SET LOCAL form. The value is a
	// static literal, so no parameter binding is needed.
	if _, err := tx.Exec(ctx,
		"SET LOCAL lenny.admin_mode = 'true'"); err != nil {
		return fmt.Errorf("pgtenant: set admin_mode: %w", err)
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
