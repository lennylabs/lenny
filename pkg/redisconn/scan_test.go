// SPDX-License-Identifier: MIT

package redisconn_test

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/redisconn"
	"github.com/lennylabs/lenny/pkg/redisconn/clustertest"
	"github.com/lennylabs/lenny/pkg/redisconn/slotguard"
)

// seedAcrossSlots writes n platform keys `cb:<i>` and n tenant keys under
// the acme hash tag through the cluster client, and returns both sets. The
// platform keys hash to many slots, so they land on every server.
func seedAcrossSlots(t *testing.T, c *clustertest.Cluster, n int) (platform, tenant []string) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < n; i++ {
		pk := fmt.Sprintf("cb:breaker-%d", i)
		tk := fmt.Sprintf("t:{acme}:quota:tokens:user-%d:daily", i)
		for _, k := range []string{pk, tk} {
			if err := c.Client.Set(ctx, k, "1", 0).Err(); err != nil {
				t.Fatalf("seed %s: %v", k, err)
			}
		}
		platform = append(platform, pk)
		tenant = append(tenant, tk)
	}
	return platform, tenant
}

func collect(t *testing.T, c redis.Cmdable, routingKey, pattern string) []string {
	t.Helper()
	var got []string
	err := redisconn.ScanKeys(context.Background(), c, routingKey, pattern, 0, func(keys []string) error {
		got = append(got, keys...)
		return nil
	})
	if err != nil {
		t.Fatalf("ScanKeys(%q): %v", pattern, err)
	}
	sort.Strings(got)
	return got
}

// spec: §12.4 Redis HA and Failure Modes (Redis Cluster at Tier 3). A SCAN
// issued on a cluster client reaches one master, so a cross-tenant pattern
// misses the keys the other masters hold. ScanKeys with an empty routing key
// runs the SCAN on every master and returns every key. The control half
// shows the defect F-12.4.29 records: the bare cluster SCAN returns fewer
// keys than exist.
func TestScanKeysCoversEveryMasterForPlatformPattern_spec_12_4(t *testing.T) {
	c := clustertest.New(t, 3)
	platform, _ := seedAcrossSlots(t, c, 40)
	sort.Strings(platform)

	occupied := 0
	for _, s := range c.Servers {
		for _, k := range s.Keys() {
			if len(k) > 3 && k[:3] == "cb:" {
				occupied++
				break
			}
		}
	}
	if occupied < 2 {
		t.Fatalf("fixture: platform keys landed on %d server(s), want them spread", occupied)
	}

	bare, _, err := c.Client.Scan(context.Background(), 0, "cb:*", 1000).Result()
	if err != nil {
		t.Fatalf("bare scan: %v", err)
	}
	if len(bare) >= len(platform) {
		t.Fatalf("control: bare cluster SCAN returned %d of %d keys; it must miss the other masters' keys", len(bare), len(platform))
	}

	if got := collect(t, c.Client, "", "cb:*"); fmt.Sprint(got) != fmt.Sprint(platform) {
		t.Fatalf("ScanKeys over every master = %d keys, want %d", len(got), len(platform))
	}
}

// spec: §12.4 Redis HA and Failure Modes (the `t:{tenant_id}:` prefix
// hashes a tenant's keys to one slot). A tenant-prefixed pattern routed by
// the tenant prefix scans the one master that owns the tenant's slot and
// returns every tenant key.
func TestScanKeysRoutesTenantPatternToOwningMaster_spec_12_4(t *testing.T) {
	c := clustertest.New(t, 3)
	_, tenant := seedAcrossSlots(t, c, 25)
	sort.Strings(tenant)

	owner := c.ServerFor("t:{acme}:")
	for _, k := range tenant {
		if !owner.Exists(k) {
			t.Fatalf("fixture: tenant key %s is not on the tenant's master", k)
		}
	}
	nodes, err := redisconn.ScanNodes(context.Background(), c.Client, "t:{acme}:")
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 {
		t.Fatalf("ScanNodes(tenant) = %d nodes, want 1", len(nodes))
	}
	if addr := nodes[0].(*redis.Client).Options().Addr; !strings.HasSuffix(owner.Addr(), addr[strings.LastIndexByte(addr, ':'):]) {
		t.Fatalf("ScanNodes(tenant) = %s, want the slot owner %s", addr, owner.Addr())
	}
	if got := collect(t, c.Client, "t:{acme}:", "t:{acme}:quota:*"); fmt.Sprint(got) != fmt.Sprint(tenant) {
		t.Fatalf("ScanKeys(tenant) = %v, want %v", got, tenant)
	}
}

// spec: §12.4 Redis HA and Failure Modes; §12.8 (erasure scope). ScanDelete
// removes every matching key across the cluster, counts each removal once,
// and leaves non-matching keys in place. It deletes with single-key
// commands, so the slot guard on the client never rejects a batch.
func TestScanDeleteRemovesEveryMatchOnCluster_spec_12_4(t *testing.T) {
	c := clustertest.New(t, 3)
	platform, tenant := seedAcrossSlots(t, c, 30)
	ctx := context.Background()

	n, err := redisconn.ScanDelete(ctx, c.Client, "", "cb:*")
	if err != nil {
		t.Fatalf("ScanDelete(platform): %v", err)
	}
	if n != len(platform) {
		t.Fatalf("ScanDelete(platform) = %d, want %d", n, len(platform))
	}
	n, err = redisconn.ScanDelete(ctx, c.Client, "t:{acme}:", "t:{acme}:*")
	if err != nil {
		t.Fatalf("ScanDelete(tenant): %v", err)
	}
	if n != len(tenant) {
		t.Fatalf("ScanDelete(tenant) = %d, want %d", n, len(tenant))
	}
	if left := c.Keys(); len(left) != 0 {
		t.Fatalf("keys left after erasure: %v", left)
	}
}

// spec: §12.4 Redis HA and Failure Modes. A non-cluster client holds the
// whole keyspace, so ScanNodes returns it unchanged and ScanDelete behaves
// as a single-node SCAN and DEL, including the empty case.
func TestScanOnSingleNodeClient_spec_12_4(t *testing.T) {
	mr := miniredis.RunT(t)
	cl := slotguard.Install(redis.NewClient(&redis.Options{Addr: mr.Addr()}))
	t.Cleanup(func() { _ = cl.Close() })
	ctx := context.Background()

	nodes, err := redisconn.ScanNodes(ctx, cl, "t:{acme}:")
	if err != nil || len(nodes) != 1 || nodes[0] != redis.Cmdable(cl) {
		t.Fatalf("ScanNodes(single) = %v, %v; want the client itself", nodes, err)
	}
	if n, err := redisconn.ScanDelete(ctx, cl, "", "t:{acme}:*"); err != nil || n != 0 {
		t.Fatalf("ScanDelete(empty) = %d, %v; want 0, nil", n, err)
	}
	for i := 0; i < 600; i++ {
		_ = mr.Set(fmt.Sprintf("t:{acme}:k%d", i), "v")
	}
	_ = mr.Set("t:{globex}:k", "v")
	n, err := redisconn.ScanDelete(ctx, cl, "t:{acme}:", "t:{acme}:*")
	if err != nil || n != 600 {
		t.Fatalf("ScanDelete(single) = %d, %v; want 600, nil", n, err)
	}
	if !mr.Exists("t:{globex}:k") {
		t.Fatal("ScanDelete removed another tenant's key")
	}
}

// spec: §12.4 Redis HA and Failure Modes. A SCAN error and a callback error
// both stop ScanKeys and surface to the caller, so an erasure never reports
// success over a partial scan.
func TestScanKeysPropagatesErrors_spec_12_4(t *testing.T) {
	mr := miniredis.RunT(t)
	cl := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = cl.Close() })
	ctx := context.Background()
	_ = mr.Set("cb:a", "1")

	stop := fmt.Errorf("stop")
	err := redisconn.ScanKeys(ctx, cl, "", "cb:*", 0, func([]string) error { return stop })
	if err != stop {
		t.Fatalf("callback error = %v, want %v", err, stop)
	}
	mr.Close()
	if _, err := redisconn.ScanDelete(ctx, cl, "", "cb:*"); err == nil {
		t.Fatal("ScanDelete on a closed server returned nil error")
	}
}
