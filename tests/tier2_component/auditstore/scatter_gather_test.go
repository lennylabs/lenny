//go:build component

// SPDX-License-Identifier: MIT

// Tier-2 component coverage for the §25.9 line 3668 platform-admin
// cross-tenant audit scatter-gather read (ScatterGatherRows): it reads
// every tenant's §11.7 chain across all audit shards under the
// pgtenant.InAllTenants (`__all__`) transaction, merged and ordered by
// (tenant_id, sequence_number), and emits one §12.3 line 141
// cross_tenant_read receipt per invocation. F-25.9.11.
package auditstore_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/audit/auditstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

func startMultiTenantAuditStore(t *testing.T) *auditstore.Store {
	t.Helper()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: schematest.RepoRoot(t) + "/migrations",
	})
	for _, id := range []string{"platform", "acme", "globex"} {
		if _, err := pg.Pool.Exec(context.Background(),
			`INSERT INTO tenants (id, genesis_nonce) VALUES ($1, '\x00')`, id); err != nil {
			t.Fatalf("seed tenant %s: %v", id, err)
		}
	}
	return auditstore.New(pg.Router(t))
}

// spec: §25.9 line 3668 / line 3709 / §12.3 line 141.
// diagnosis: ScatterGatherRows must surface every tenant's rows merged
// and grouped by tenant_id (so each per-tenant chain stays contiguous),
// and emit exactly one cross_tenant_read receipt for the invocation.
func TestScatterGatherRowsMergesAllTenants_spec_25_9_3668(t *testing.T) {
	t.Parallel()
	store := startMultiTenantAuditStore(t)
	ctx := context.Background()

	if _, err := store.Append(ctx, "acme", "session.created", json.RawMessage(`{"actor_id":"alice"}`), time.Time{}); err != nil {
		t.Fatalf("append acme #1: %v", err)
	}
	if _, err := store.Append(ctx, "acme", "session.completed", json.RawMessage(`{"actor_id":"alice"}`), time.Time{}); err != nil {
		t.Fatalf("append acme #2: %v", err)
	}
	if _, err := store.Append(ctx, "globex", "session.created", json.RawMessage(`{"actor_id":"bob"}`), time.Time{}); err != nil {
		t.Fatalf("append globex #1: %v", err)
	}

	rows, missing, err := store.ScatterGatherRows(ctx)
	if err != nil {
		t.Fatalf("ScatterGatherRows: %v", err)
	}
	if len(missing) != 0 {
		t.Fatalf("missingShards = %v, want none (single reachable shard)", missing)
	}

	// Every tenant is represented and rows are grouped by tenant in
	// ascending (tenant_id, sequence_number) order.
	var acme, globex int
	var lastTenant string
	var lastSeq uint64
	for _, r := range rows {
		switch r.TenantID {
		case "acme":
			acme++
		case "globex":
			globex++
		}
		if r.TenantID == lastTenant && r.Seq < lastSeq {
			t.Errorf("rows out of order within tenant %s: seq %d after %d", r.TenantID, r.Seq, lastSeq)
		}
		lastTenant = r.TenantID
		lastSeq = r.Seq
	}
	if acme != 2 {
		t.Errorf("acme rows = %d, want 2", acme)
	}
	if globex != 1 {
		t.Errorf("globex rows = %d, want 1", globex)
	}
	// The receipt the invocation emits lands on the platform chain. It is
	// committed after the read snapshot, so it is not in `rows`; verify it
	// landed by reading the platform chain directly.
	platform, err := store.Rows(ctx, "platform")
	if err != nil {
		t.Fatalf("Rows(platform): %v", err)
	}
	var receipts int
	for _, r := range platform {
		if r.EventType == "cross_tenant_read" {
			receipts++
		}
	}
	if receipts != 1 {
		t.Errorf("cross_tenant_read receipts = %d, want 1", receipts)
	}
}
