// SPDX-License-Identifier: MIT

package quotastore_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/quota/quotastore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/rediskeys"
	"github.com/lennylabs/lenny/pkg/quota"
	"github.com/lennylabs/lenny/pkg/redisconn/clustertest"
)

// spec: §12.4 Redis HA and Failure Modes (the `t:{tenant_id}:` prefix
// hashes a tenant's keys to one slot). Every counter key carries the
// tenant's hash tag, so the sliding-window MGET over a user's buckets and
// the per-key scripts run on one slot; the cluster client and the slot
// guard installed by clustertest reject any command that spans slots.
func TestCounterKeysShareTenantSlotOnCluster_spec_12_4(t *testing.T) {
	cl := clustertest.New(t, 3)
	c := quotastore.New(cl.Client)
	ctx := context.Background()
	at := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	if _, err := c.AddHierarchical(ctx, "acme", "alice", quota.ResetHourly, at, 10); err != nil {
		t.Fatalf("AddHierarchical: %v", err)
	}
	if _, err := c.SlidingAdd(ctx, "acme", "alice", time.Hour, time.Minute, at, 5); err != nil {
		t.Fatalf("SlidingAdd: %v", err)
	}
	if _, err := c.SlidingUsage(ctx, "acme", "alice", time.Hour, time.Minute, at); err != nil {
		t.Fatalf("SlidingUsage (MGET over the user's buckets): %v", err)
	}
	owner := cl.ServerFor(rediskeys.TenantPrefix("acme"))
	for _, k := range cl.Keys() {
		if strings.HasPrefix(k, quotastore.TenantKeyPrefix("acme")) && !owner.Exists(k) {
			t.Errorf("tenant key %q is not on the tenant's master", k)
		}
	}
}

// spec: §12.4 Redis HA and Failure Modes (Redis Cluster at Tier 3); §12.8
// (erasure scope). On a multi-master cluster the tenant and user erasures
// remove every counter of the tenant (a bare SCAN on the cluster client
// reaches one master only) and leave the other tenant's and the global
// rollup's counters in place.
func TestErasureRemovesEveryKeyOnCluster_spec_12_8(t *testing.T) {
	cl := clustertest.New(t, 3)
	c := quotastore.New(cl.Client)
	ctx := context.Background()
	at := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	users := []string{"alice", "bob", "carol", "dave"}
	for _, tenant := range []string{"acme", "globex", "initech"} {
		for _, u := range users {
			if _, err := c.AddHierarchical(ctx, tenant, u, quota.ResetDaily, at, 1); err != nil {
				t.Fatalf("AddHierarchical(%s/%s): %v", tenant, u, err)
			}
		}
	}

	if n, err := c.DeleteByUser(ctx, "globex", "alice"); err != nil || n != 1 {
		t.Fatalf("DeleteByUser = (%d, %v), want (1, nil)", n, err)
	}
	// acme: four per-user windows plus the tenant rollup.
	if n, err := c.DeleteByTenant(ctx, "acme"); err != nil || n != len(users)+1 {
		t.Fatalf("DeleteByTenant = (%d, %v), want (%d, nil)", n, err, len(users)+1)
	}
	for _, k := range cl.Keys() {
		if strings.HasPrefix(k, quotastore.TenantKeyPrefix("acme")) {
			t.Errorf("acme key %q survived tenant erasure", k)
		}
		if strings.HasPrefix(k, quotastore.UserKeyPrefix("globex", "alice")) {
			t.Errorf("globex/alice key %q survived user erasure", k)
		}
	}
	got, err := c.UsageHierarchical(ctx, "initech", "bob", quota.ResetDaily, at)
	if err != nil || got.User != 1 || got.Global != 12 {
		t.Fatalf("initech/bob after other erasures = %+v, %v; want user 1, global 12", got, err)
	}
}
