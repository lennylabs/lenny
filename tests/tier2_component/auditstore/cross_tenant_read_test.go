//go:build component

// SPDX-License-Identifier: MIT

// Tier-2 component coverage for the §12.3
// cross_tenant_read emission on the background-worker code paths
// (auditstore.PendingTranslation and PendingRepublish). Each worker
// invocation runs inside a pgtenant.InAllTenants transaction and
// MUST emit one cross_tenant_read audit row to the `platform`
// chain.
package auditstore_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/audit/auditstore"
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

// spec: §12.3
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

// spec: §12.3
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

// spec: 12.3 (Postgres HA Requirements), 12.8 (User-level erasure invocation)
// diagnosis: EmitCrossTenantRead is the event the erasure-job sentinel
// reads record. A failure means the event does not land on the `platform`
// chain, drops the endpoint or caller of a request-driven read, or writes
// empty endpoint and caller fields that the workers' events never carried.
func TestEmitCrossTenantReadCarriesEndpointAndCaller(t *testing.T) {
	t.Parallel()
	store, _ := startAuditStore(t)
	ctx := context.Background()

	if err := store.EmitCrossTenantRead(ctx, auditstore.CrossTenantRead{
		Category: "erasure_job", RowCount: 1,
		Endpoint: "GET /v1/admin/erasure-jobs/{jobId}", CallerSubject: "alice@acme.com",
	}); err != nil {
		t.Fatalf("EmitCrossTenantRead with caller: %v", err)
	}
	if err := store.EmitCrossTenantRead(ctx, auditstore.CrossTenantRead{Category: "erasure_job", RowCount: 0}); err != nil {
		t.Fatalf("EmitCrossTenantRead without caller: %v", err)
	}
	rows, err := store.Rows(ctx, "platform")
	if err != nil {
		t.Fatalf("Rows(platform): %v", err)
	}
	var payloads []map[string]any
	for _, r := range rows {
		if r.EventType != "cross_tenant_read" {
			continue
		}
		var p map[string]any
		if err := json.Unmarshal(r.Payload, &p); err != nil {
			t.Fatalf("decode payload %s: %v", r.Payload, err)
		}
		payloads = append(payloads, p)
	}
	if len(payloads) != 2 {
		t.Fatalf("cross_tenant_read rows = %d, want 2", len(payloads))
	}
	full := payloads[0]
	if len(full) != 4 || full["category"] != "erasure_job" || full["row_count"] != float64(1) ||
		full["endpoint"] != "GET /v1/admin/erasure-jobs/{jobId}" || full["caller_subject"] != "alice@acme.com" {
		t.Errorf("payload with caller = %v, want all four fields", full)
	}
	bare := payloads[1]
	if len(bare) != 2 || bare["category"] != "erasure_job" || bare["row_count"] != float64(0) {
		t.Errorf("payload without caller = %v, want exactly category and row_count", bare)
	}
}
