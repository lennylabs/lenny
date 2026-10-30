// SPDX-License-Identifier: MIT

package auditstore

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/gateway/audit/auditstore/auditbatch"
	"github.com/lennylabs/lenny/pkg/storerouter"
)

type fakeRouter struct {
	audit *pgxpool.Pool
}

func (f *fakeRouter) AuditShard(context.Context, storerouter.TenantID) (*pgxpool.Pool, error) {
	return f.audit, nil
}

func (f *fakeRouter) AuditReadShard(context.Context, storerouter.TenantID) (*pgxpool.Pool, error) {
	return f.audit, nil
}

func (f *fakeRouter) AllAuditShards(context.Context) ([]storerouter.ShardHandle, error) {
	return []storerouter.ShardHandle{{Pool: f.audit}}, nil
}

// spec: §12.3 — the synchronous audit write path prefers the
// dedicated sync write pool when one is wired, and otherwise falls back
// to the router's audit shard.
func TestWriteShard_PrefersSyncPool_spec_12_3(t *testing.T) {
	routerPool := &pgxpool.Pool{}
	syncPool := &pgxpool.Pool{}
	s := New(&fakeRouter{audit: routerPool}, WithSyncWritePool(syncPool))

	got, err := s.writeShard(context.Background(), "acme")
	if err != nil {
		t.Fatalf("writeShard: %v", err)
	}
	if got != syncPool {
		t.Errorf("writeShard returned %p, want the dedicated sync pool %p", got, syncPool)
	}

	// Reads stay on the router shard.
	rd, err := s.shard(context.Background(), "acme")
	if err != nil {
		t.Fatalf("shard: %v", err)
	}
	if rd != routerPool {
		t.Errorf("read shard returned %p, want router pool %p", rd, routerPool)
	}

	// Without a sync pool the write path falls back to the router.
	s2 := New(&fakeRouter{audit: routerPool})
	got2, err := s2.writeShard(context.Background(), "acme")
	if err != nil {
		t.Fatalf("writeShard fallback: %v", err)
	}
	if got2 != routerPool {
		t.Errorf("fallback writeShard returned %p, want router pool %p", got2, routerPool)
	}
}

type fakeEnqueuer struct {
	items []auditbatch.Item
}

func (f *fakeEnqueuer) Enqueue(it auditbatch.Item) { f.items = append(f.items, it) }

// spec: §12.3 — when batching is enabled, the non-PII T2
// cross_tenant_read receipt is enqueued onto the batch buffer instead
// of a synchronous write (which would require the pool).
func TestEmitCrossTenantRead_RoutesToBatchBuffer_spec_12_3(t *testing.T) {
	buf := &fakeEnqueuer{}
	// Nil router: a synchronous Append would panic, proving the buffer
	// path returns before touching the write pool.
	s := New(nil, WithBatchBuffer(buf))

	if err := s.emitCrossTenantRead(context.Background(), "audit_siem_forwarder", 7); err != nil {
		t.Fatalf("emitCrossTenantRead: %v", err)
	}
	if len(buf.items) != 1 {
		t.Fatalf("buffer received %d items, want 1", len(buf.items))
	}
	it := buf.items[0]
	if it.TenantID != "platform" || it.EventType != "cross_tenant_read" {
		t.Errorf("enqueued item = %s/%s, want platform/cross_tenant_read", it.TenantID, it.EventType)
	}
	if !strings.Contains(string(it.Payload), `"audit_siem_forwarder"`) ||
		!strings.Contains(string(it.Payload), `"row_count":7`) {
		t.Errorf("enqueued payload = %s, want category + row_count", it.Payload)
	}
}

// failingShardRouter answers every AuditShard with err.
type failingShardRouter struct{ err error }

func (f failingShardRouter) AuditShard(context.Context, storerouter.TenantID) (*pgxpool.Pool, error) {
	return nil, f.err
}

func (f failingShardRouter) AuditReadShard(context.Context, storerouter.TenantID) (*pgxpool.Pool, error) {
	return nil, f.err
}

func (f failingShardRouter) AllAuditShards(context.Context) ([]storerouter.ShardHandle, error) {
	return nil, f.err
}

// TestEmitCrossTenantRead_ExportedWritesSynchronously_spec_12_3 pins that
// the exported EmitCrossTenantRead, which can carry a caller subject,
// bypasses the batch buffer even when batching is enabled and returns the
// write error, while the workers' unexported path still enqueues a payload
// of exactly category and row_count.
// spec: 12.3 (Postgres HA Requirements)
func TestEmitCrossTenantRead_ExportedWritesSynchronously_spec_12_3(t *testing.T) {
	buf := &fakeEnqueuer{}
	shardErr := errors.New("audit shard unreachable")
	s := New(failingShardRouter{err: shardErr}, WithBatchBuffer(buf))

	err := s.EmitCrossTenantRead(context.Background(), CrossTenantRead{
		Category: "erasure_job", RowCount: 1,
		Endpoint: "GET /v1/admin/erasure-jobs/{jobId}", CallerSubject: "alice@acme.com",
	})
	if !errors.Is(err, shardErr) {
		t.Fatalf("EmitCrossTenantRead = %v, want the shard error", err)
	}
	if len(buf.items) != 0 {
		t.Fatalf("buffer received %d items, want 0", len(buf.items))
	}

	if err := s.emitCrossTenantRead(context.Background(), "audit_siem_forwarder", 3); err != nil {
		t.Fatalf("emitCrossTenantRead: %v", err)
	}
	if len(buf.items) != 1 {
		t.Fatalf("buffer received %d items, want 1", len(buf.items))
	}
	var payload map[string]any
	if err := json.Unmarshal(buf.items[0].Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if len(payload) != 2 || payload["category"] != "audit_siem_forwarder" || payload["row_count"] != float64(3) {
		t.Errorf("payload = %v, want exactly category and row_count", payload)
	}
}
