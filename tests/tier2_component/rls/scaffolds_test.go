// SPDX-License-Identifier: MIT

//go:build component

// Tier-2 component scaffolds for §12.2.2 RLS and tenant isolation.
// This is the self-contained suite that does not test a single
// component — it connects to Postgres directly, seeds tenants A and
// B on every tenant-scoped table, and asserts the documented RLS
// invariants.

package rls_test

import "testing"

// TestRLSPreventsCrossTenantRead — a query in tenant-A context
// returns zero rows for tenant-B data on every tenant-scoped table.
func TestRLSPreventsCrossTenantRead(t *testing.T) {
	t.Skip("not implemented: §12.2.2 RLS — requires the full migration set with RLS policies on every tenant-scoped table + a seed helper that inserts tenants A and B")
}

// TestRLSRequiresTenantContext — a query without SET LOCAL
// app.current_tenant raises an exception via lenny_tenant_guard.
func TestRLSRequiresTenantContext(t *testing.T) {
	t.Skip("not implemented: §12.2.2 RLS — requires the lenny_tenant_guard trigger function and the per-statement RAISE EXCEPTION when app.current_tenant is unset")
}

// TestRLSAllTenantsContext — a query with app.current_tenant =
// '__all__' succeeds and emits a cross_tenant_read audit event.
func TestRLSAllTenantsContext(t *testing.T) {
	t.Skip("not implemented: §12.2.2 RLS — requires the __all__ tenant context bypass and the cross_tenant_read audit emission tied to it")
}

// TestRLSPoolerReuseDoesNotLeakContext — connection-pooler reuse does
// not leak the previous transaction's app.current_tenant.
func TestRLSPoolerReuseDoesNotLeakContext(t *testing.T) {
	t.Skip("not implemented: §12.2.2 RLS — requires pgbouncer (or equivalent) in the compose profile with the documented session-pooling configuration")
}

// TestSchemaLinterIdentifiesTenantScopedTables — the schema linter
// (scripts/lint-schema.sh) running on the migration set identifies
// all tenant-scoped tables and confirms tenant_id is the leading
// index column or is annotated.
func TestSchemaLinterIdentifiesTenantScopedTables(t *testing.T) {
	t.Skip("not implemented: §12.2.2 + R-01 — requires scripts/lint-schema.sh to be authored and a representative migration set to lint against")
}
