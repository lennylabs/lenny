//go:build component

// SPDX-License-Identifier: MIT

// Tier-2 component coverage for the §12.3 line 141
// cross_tenant_read emission on the background-worker code paths
// (auditstore.PendingTranslation and PendingRepublish). Each worker
// invocation runs inside a pgtenant.InAllTenants transaction and
// MUST emit one cross_tenant_read audit row to the `platform`
// chain.
package auditstore_test

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/auditstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

func startAuditStore(t *testing.T) (*auditstore.Store, *containers.Postgres) {
	t.Helper()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: schematest.RepoRoot(t) + "/migrations",
	})
	if _, err := pg.Pool.Exec(context.Background(),
		`INSERT INTO tenants (id, genesis_nonce) VALUES ('platform', '\x00')`); err != nil {
		t.Fatalf("seed platform tenant: %v", err)
	}
	return auditstore.New(pg.Router(t)), pg
}

// spec: §12.3 line 141
// diagnosis: PendingTranslation runs a cross-tenant SELECT under
// app.current_tenant='__all__' and MUST emit one cross_tenant_read
// row to the `platform` chain per worker invocation. The audit
// payload names the worker category.
func TestPendingTranslationEmitsCrossTenantRead(t *testing.T) {
	t.Parallel()
	store, _ := startAuditStore(t)
	ctx := context.Background()

	if _, err := store.PendingTranslation(ctx, 100); err != nil {
		t.Fatalf("PendingTranslation: %v", err)
	}
	rows, err := store.Rows(ctx, "platform")
	if err != nil {
		t.Fatalf("Rows(platform): %v", err)
	}
	var matched int
	for _, r := range rows {
		if r.EventType == "cross_tenant_read" {
			matched++
		}
	}
	if matched != 1 {
		t.Errorf("cross_tenant_read emissions = %d, want 1", matched)
	}
}

// spec: §12.3 line 141
// diagnosis: PendingRepublish runs the same cross-tenant SELECT
// under __all__ and MUST emit one cross_tenant_read row per worker
// invocation.
func TestPendingRepublishEmitsCrossTenantRead(t *testing.T) {
	t.Parallel()
	store, _ := startAuditStore(t)
	ctx := context.Background()

	if _, err := store.PendingRepublish(ctx, 5, 100); err != nil {
		t.Fatalf("PendingRepublish: %v", err)
	}
	rows, err := store.Rows(ctx, "platform")
	if err != nil {
		t.Fatalf("Rows(platform): %v", err)
	}
	var matched int
	for _, r := range rows {
		if r.EventType == "cross_tenant_read" {
			matched++
		}
	}
	if matched != 1 {
		t.Errorf("cross_tenant_read emissions = %d, want 1", matched)
	}
}
