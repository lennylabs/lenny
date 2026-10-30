// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 smoke test: the Redis stores against a real three-master Redis
// Cluster. miniredis cannot reproduce the two behaviors F-12.4.28 and
// F-12.4.29 record: a real cluster rejects a command whose keys span hash
// slots with CROSSSLOT, and the go-redis cluster client sends a SCAN to one
// master. This test runs the stores' tenant-scoped multi-key operations and
// their node-routed SCANs on a cluster and checks that each succeeds and
// covers every master.
package tier4_integration_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/storage/rediskeys"
	"github.com/lennylabs/lenny/pkg/redisconn"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// clusterKeysOn returns how many masters hold at least one key matching
// pattern, and the total key count, read node by node.
func clusterKeysOn(t *testing.T, c *redis.ClusterClient, pattern string) (masters, total int) {
	t.Helper()
	ctx := context.Background()
	nodes, err := redisconn.ScanNodes(ctx, c, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range nodes {
		keys, err := n.Keys(ctx, pattern).Result()
		if err != nil {
			t.Fatal(err)
		}
		if len(keys) > 0 {
			masters++
		}
		total += len(keys)
	}
	return masters, total
}

// diagnosis: a CROSSSLOT error means a multi-key command or transaction
// over one tenant's keys spans hash slots (the tenant prefix lost its
// braces); a SCAN count short of the seeded keys means a SCAN ran on one
// master instead of being routed with redisconn.ScanKeys.
//
// spec: §12.4 Redis HA and Failure Modes (Redis Cluster at Tier 3; the
// `t:{tenant_id}:` prefix hashes a tenant's keys to one slot); §12.8
// (erasure scope).
func TestRedisClusterTenantKeysAndScanRouting_spec_12_4(t *testing.T) {
	rc := containers.StartRedisCluster(t)
	c := rc.Client
	ctx := context.Background()

	nodes, err := redisconn.ScanNodes(ctx, c, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 3 {
		t.Fatalf("cluster has %d masters, want 3", len(nodes))
	}

	// A MULTI block and a multi-key command over one tenant's keys stay in
	// one slot.
	acme := rediskeys.TenantPrefix("acme")
	_, err = c.TxPipelined(ctx, func(p redis.Pipeliner) error {
		for i := 0; i < 20; i++ {
			p.Set(ctx, fmt.Sprintf("%sscache:t:%d", acme, i), "v", 0)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("tenant MULTI on cluster: %v", err)
	}
	keys := make([]string, 20)
	for i := range keys {
		keys[i] = fmt.Sprintf("%sscache:t:%d", acme, i)
	}
	if vals, err := c.MGet(ctx, keys...).Result(); err != nil || len(vals) != 20 {
		t.Fatalf("tenant MGET on cluster = %d values, %v", len(vals), err)
	}

	// Negative control: the unbraced form spreads one tenant's keys across
	// slots, and the cluster rejects a multi-key command over them.
	if err := c.MGet(ctx, "t:acme:a", "t:acme:b").Err(); err == nil || !strings.Contains(err.Error(), "CROSSSLOT") {
		t.Fatalf("unbraced MGET on cluster err = %v, want CROSSSLOT", err)
	}

	// Platform keys spread across every master; a routed SCAN finds them
	// all and a bare cluster SCAN does not.
	for i := 0; i < 60; i++ {
		if err := c.Set(ctx, fmt.Sprintf("cb:smoke-%d", i), "{}", 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
	if m, _ := clusterKeysOn(t, c, "cb:smoke-*"); m != 3 {
		t.Fatalf("fixture: platform keys on %d masters, want 3", m)
	}
	var routed int
	if err := redisconn.ScanKeys(ctx, c, "", "cb:smoke-*", 0, func(k []string) error {
		routed += len(k)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if routed != 60 {
		t.Fatalf("routed SCAN found %d of 60 platform keys", routed)
	}

	// Tenant erasure through the routed SCAN removes every tenant key and
	// leaves the platform keys.
	n, err := redisconn.ScanDelete(ctx, c, acme, acme+"*")
	if err != nil || n != 20 {
		t.Fatalf("tenant ScanDelete = %d, %v; want 20, nil", n, err)
	}
	n, err = redisconn.ScanDelete(ctx, c, "", "cb:smoke-*")
	if err != nil || n != 60 {
		t.Fatalf("platform ScanDelete = %d, %v; want 60, nil", n, err)
	}
	if _, total := clusterKeysOn(t, c, "*"); total != 0 {
		t.Fatalf("%d keys left after erasure", total)
	}
}
