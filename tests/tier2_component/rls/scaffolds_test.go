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
//
// spec: 12.2.2
// diagnosis: the __all__ tenant-context bypass is not implemented in
// the §12.2.2 RLS migrations. The lenny_tenant_guard trigger rejects
// every write whose app.current_tenant differs from the row's
// tenant_id, with no documented escape value, and no gateway-side
// cross_tenant_read audit emission exists for the bypass either.
func TestRLSAllTenantsContext(t *testing.T) {
	t.Skip("blocked: §12.2.2 __all__ tenant-context bypass — neither the RLS policy nor the gateway-side cross_tenant_read audit emission tied to it exist; lenny_tenant_guard accepts no documented bypass value today")
}

// TestRLSPoolerReuseDoesNotLeakContext is implemented in
// pgbouncer_test.go, which exercises the §12.2.2 connection-pooler
// reuse invariant against the compose-profile PgBouncer in
// session-pooling mode.
