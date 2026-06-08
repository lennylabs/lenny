// SPDX-License-Identifier: MIT

package auditstore

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/gateway/auditstore/auditbatch"
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

// spec: §12.3 line 79 — the synchronous audit write path prefers the
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

// spec: §12.3 line 81 — when batching is enabled, the non-PII T2
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
