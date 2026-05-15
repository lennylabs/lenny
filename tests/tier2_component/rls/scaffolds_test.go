// SPDX-License-Identifier: MIT

//go:build component

// Tier-2 component scaffolds for §12.2.2 RLS and tenant isolation.
// This is the self-contained suite that does not test a single
// component — it connects to Postgres directly, seeds tenants A and
// B on every tenant-scoped table, and asserts the documented RLS
// invariants.
//
// Implemented entries have moved to rls_test.go:
// TestRLSRequiresTenantContext, TestRLSPreventsCrossTenantRead, and
// TestSchemaLinterIdentifiesTenantScopedTables. The two below remain
// scaffolds because they depend on surfaces not yet built.

package rls_test

import "testing"

// TestRLSAllTenantsContext — a query with app.current_tenant =
// '__all__' succeeds and emits a cross_tenant_read audit event.
func TestRLSAllTenantsContext(t *testing.T) {
	t.Skip("not implemented: §12.2.2 RLS — requires the __all__ tenant context bypass in the RLS policy and the gateway-side cross_tenant_read audit emission tied to it")
}

// TestRLSPoolerReuseDoesNotLeakContext — connection-pooler reuse does
// not leak the previous transaction's app.current_tenant.
func TestRLSPoolerReuseDoesNotLeakContext(t *testing.T) {
	t.Skip("not implemented: §12.2.2 RLS — requires pgbouncer (or equivalent) in the compose profile with the documented session-pooling configuration")
}
