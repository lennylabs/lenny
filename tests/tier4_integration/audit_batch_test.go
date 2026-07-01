// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test: the §12.3 line 81 opt-in T2 audit batching
// path against a live Postgres container. The batch buffer accumulates
// non-PII T2 events and flushes them through auditstore.AppendBatch,
// which seals each row under one per-tenant advisory lock reusing the
// synchronous prev_hash chain construction. The resulting chain must
// verify, proving batched inserts chain correctly.
package tier4_integration_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/gateway/audit/auditstore"
	"github.com/lennylabs/lenny/pkg/gateway/audit/auditstore/auditbatch"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// spec: §12.3 line 81 — batched T2 inserts seal and chain correctly,
// and the resulting audit chain verifies.
// diagnosis: a failure means batched T2 audit inserts do not seal and
// chain correctly, so the audit chain would fail verification after a
// batch flush.
func TestAuditBatchBufferChainsCorrectly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	store := auditstore.New(pg.Router(t))

	buf := auditbatch.New(store.AppendBatch, auditbatch.Config{BatchSize: 1000}, nil)
	store.SetBatchBuffer(buf)

	// Five non-PII T2 operational receipts on the platform chain.
	const n = 5
	for i := 0; i < n; i++ {
		buf.Enqueue(auditbatch.Item{
			TenantID:  "platform",
			EventType: "cross_tenant_read",
			Payload:   json.RawMessage(`{"category":"audit_siem_forwarder","row_count":3}`),
		})
	}
	if err := buf.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	rows, err := store.Rows(ctx, "platform")
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	if len(rows) != n {
		t.Fatalf("platform chain has %d rows, want %d", len(rows), n)
	}

	res, err := store.Verify(ctx, "platform")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if res.Integrity != audit.ChainVerified {
		t.Errorf("batched platform chain integrity = %q, want verified (%s)", res.Integrity, res.Detail)
	}
}
