//go:build component

// SPDX-License-Identifier: MIT

// Component test for the tenant-deletion Phase 4 erasure of the two
// Redis-resident stores that hold tenant data outside Postgres: the
// experiment sticky-assignment cache (experimentsticky.RedisCache) and the
// Tier 1 billing write-ahead stream (redisstream.Tier). It runs both
// against a real three-master Redis Cluster, where a SCAN reaches one
// master only and XGROUP DESTROY on a missing key replies with an error,
// two behaviors miniredis reproduces only in part.
package stores_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/billing/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/billing/billingstore/failover/redisstream"
	"github.com/lennylabs/lenny/pkg/gateway/experiment/experimentsticky"
	"github.com/lennylabs/lenny/pkg/gateway/storage/rediskeys"
	"github.com/lennylabs/lenny/pkg/redisconn"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// billingStreamKey is the §11.2.1 per-tenant billing stream key.
func billingStreamKey(tenant string) string {
	return rediskeys.TenantPrefix(tenant) + "billing:stream"
}

// tenantKeys lists the keys under tenant's prefix matching suffix, read on
// every master.
func tenantKeys(t *testing.T, c *redis.ClusterClient, tenant, suffix string) []string {
	t.Helper()
	ctx := context.Background()
	nodes, err := redisconn.ScanNodes(ctx, c, "")
	if err != nil {
		t.Fatalf("ScanNodes: %v", err)
	}
	var out []string
	for _, n := range nodes {
		keys, err := n.Keys(ctx, rediskeys.TenantPrefix(tenant)+suffix).Result()
		if err != nil {
			t.Fatalf("KEYS: %v", err)
		}
		out = append(out, keys...)
	}
	return out
}

// countingInserter records how many staged events a flush replayed.
type countingInserter struct {
	mu sync.Mutex
	n  int
}

func (c *countingInserter) InsertFromStream(context.Context, billingstore.Event, string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n++
	return nil
}

func (c *countingInserter) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

// diagnosis: acme sticky keys surviving means the tenant sweep did not
// reach the master owning acme's slot (a bare SCAN on the cluster client)
// or used the wrong glob; a globex key missing means the glob is not
// tenant-scoped; a non-zero repeat count means the delete is not
// idempotent.
//
// spec: 12.8 (Compliance Interfaces: Tenant deletion lifecycle, Phase 4),
// 12.4 (Redis HA and Failure Modes)
func TestStickyCacheDeleteByTenantOnRedisCluster_spec_12_8(t *testing.T) {
	rc := containers.StartRedisCluster(t)
	ctx := context.Background()
	cache := experimentsticky.NewRedis(rc.Client)
	const users = 12
	for _, tenant := range []string{"acme", "globex"} {
		for i := 0; i < users; i++ {
			if err := cache.Put(ctx, tenant, fmt.Sprintf("exp-%d", i%3), fmt.Sprintf("user-%d", i), "treatment"); err != nil {
				t.Fatalf("Put: %v", err)
			}
		}
	}

	n, err := cache.DeleteByTenant(ctx, "acme")
	if err != nil || n != users {
		t.Fatalf("DeleteByTenant = (%d, %v), want (%d, nil)", n, err, users)
	}
	if left := tenantKeys(t, rc.Client, "acme", "exp:*:sticky:*"); len(left) != 0 {
		t.Fatalf("acme sticky keys survived: %v", left)
	}
	if left := tenantKeys(t, rc.Client, "globex", "exp:*:sticky:*"); len(left) != users {
		t.Fatalf("globex sticky keys = %d, want %d", len(left), users)
	}
	if n, err := cache.DeleteByTenant(ctx, "acme"); err != nil || n != 0 {
		t.Fatalf("repeated DeleteByTenant = (%d, %v), want (0, nil)", n, err)
	}
}

// diagnosis: an acme stream key surviving the purge means XGROUP DESTROY
// or DEL did not run against the tenant's key; an error on the repeat or
// on the never-published tenant means a missing stream is not treated as
// success; a stream key after the Flush means the purge cleared the
// replica's known set and the flusher recreated the stream with XGROUP
// CREATE MKSTREAM; a globex change means the purge is not tenant-scoped.
//
// spec: 12.8 (Compliance Interfaces: Tenant deletion lifecycle, Phase 4),
// 11.2.1 (billing write-ahead stream), 12.4 (Redis HA and Failure Modes)
func TestBillingStreamPurgeTenantOnRedisCluster_spec_12_8(t *testing.T) {
	rc := containers.StartRedisCluster(t)
	ctx := context.Background()
	ins := &countingInserter{}
	tier, err := redisstream.New(redisstream.Options{Client: rc.Client, ConsumerName: "replica-1", Inserter: ins})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, tenant := range []string{"acme", "globex"} {
		for i := 1; i <= 3; i++ {
			e := billingstore.Event{TenantID: tenant, UserID: "alice", SequenceNumber: uint64(i), EventType: billingstore.EventSessionCreated}
			if err := tier.Publish(ctx, e); err != nil {
				t.Fatalf("Publish(%s): %v", tenant, err)
			}
		}
	}

	n, err := tier.PurgeTenant(ctx, "acme")
	if err != nil || n != 3 {
		t.Fatalf("PurgeTenant = (%d, %v), want (3, nil)", n, err)
	}
	if exists, _ := rc.Client.Exists(ctx, billingStreamKey("acme")).Result(); exists != 0 {
		t.Fatal("acme billing stream survived the purge")
	}
	if l, err := rc.Client.XLen(ctx, billingStreamKey("globex")).Result(); err != nil || l != 3 {
		t.Fatalf("globex stream len = (%d, %v), want 3", l, err)
	}
	groups, err := rc.Client.XInfoGroups(ctx, billingStreamKey("globex")).Result()
	if err != nil || len(groups) != 1 {
		t.Fatalf("globex groups = (%v, %v), want the billing-flusher group", groups, err)
	}

	if n, err := tier.PurgeTenant(ctx, "acme"); err != nil || n != 0 {
		t.Fatalf("repeated PurgeTenant = (%d, %v), want (0, nil)", n, err)
	}
	if n, err := tier.PurgeTenant(ctx, "initech"); err != nil || n != 0 {
		t.Fatalf("PurgeTenant of a tenant with no stream = (%d, %v), want (0, nil)", n, err)
	}

	// The same Tier's flusher runs after the purge. Whatever it returns,
	// it must not recreate the stream or replay a purged event.
	_, flushErr := tier.Flush(ctx, "acme")
	if exists, _ := rc.Client.Exists(ctx, billingStreamKey("acme")).Result(); exists != 0 {
		t.Fatalf("Flush after the purge (err=%v) recreated the acme stream", flushErr)
	}
	if ins.count() != 0 {
		t.Fatalf("Flush after the purge replayed %d events", ins.count())
	}
	t.Logf("Flush after the purge returned %v", flushErr)
}
