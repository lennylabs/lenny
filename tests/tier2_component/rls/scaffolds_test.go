// SPDX-License-Identifier: MIT

//go:build component

// Tier-2 component scaffolds for §12.2.2 RLS and tenant isolation.
// This is the self-contained suite that does not test a single
// component — it connects to Postgres directly, seeds tenants A and
// B on every tenant-scoped table, and asserts the documented RLS
// invariants.
//
// Implemented entries have moved to sibling files:
// TestRLSRequiresTenantContext, TestRLSPreventsCrossTenantRead, and
// TestSchemaLinterIdentifiesTenantScopedTables in rls_test.go;
// TestRLSAllTenantsContext in all_tenants_test.go; and
// TestRLSPoolerReuseDoesNotLeakContext in pgbouncer_test.go.

package rls_test

// TestRLSAllTenantsContext is implemented in all_tenants_test.go,
// which exercises the §4.2 / §12.3 platform-admin __all__
// cross-tenant bypass (trigger + RLS policy) and the §12.3 line 141
// cross_tenant_read audit emission tied to it.

// TestRLSPoolerReuseDoesNotLeakContext is implemented in
// pgbouncer_test.go, which exercises the §12.2.2 connection-pooler
// reuse invariant against the compose-profile PgBouncer in
// session-pooling mode.
