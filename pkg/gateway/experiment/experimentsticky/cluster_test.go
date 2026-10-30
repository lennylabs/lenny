// SPDX-License-Identifier: MIT

package experimentsticky

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/storage/rediskeys"
	"github.com/lennylabs/lenny/pkg/redisconn/clustertest"
)

// spec: §12.4 Redis HA and Failure Modes (Redis Cluster at Tier 3; the
// `t:{tenant_id}:` prefix hashes a tenant's keys to one slot); §10.7
// (sticky cache flush); §12.8 (erasure scope). On a multi-master cluster
// the experiment flush and the per-user erasure remove every matching
// assignment (a bare SCAN on the cluster client reaches one master only)
// and leave the other tenant's assignments in place.
func TestFlushAndUserErasureCoverClusterMasters_spec_12_4(t *testing.T) {
	cl := clustertest.New(t, 3)
	c := NewRedis(cl.Client)
	ctx := context.Background()
	const users = 15
	for _, tenant := range []string{"acme", "globex"} {
		for _, exp := range []string{"exp-1", "exp-2"} {
			for i := 0; i < users; i++ {
				if err := c.Put(ctx, tenant, exp, fmt.Sprintf("user-%d", i), "treatment"); err != nil {
					t.Fatalf("Put: %v", err)
				}
			}
		}
	}
	if !cl.ServerFor(rediskeys.TenantPrefix("acme")).Exists(assignmentKey("acme", "exp-1", "user-0")) {
		t.Fatal("acme assignment is not on the tenant's master")
	}

	if n, err := c.Flush(ctx, "acme", "exp-1", "concluded"); err != nil || n != users {
		t.Fatalf("Flush = (%d, %v), want (%d, nil)", n, err, users)
	}
	if n, err := c.DeleteByUser(ctx, "acme", "user-3"); err != nil || n != 1 {
		t.Fatalf("DeleteByUser = (%d, %v), want (1, nil)", n, err)
	}
	acme, globex := 0, 0
	for _, k := range cl.Keys() {
		switch {
		case strings.HasPrefix(k, rediskeys.TenantPrefix("acme")):
			acme++
		case strings.HasPrefix(k, rediskeys.TenantPrefix("globex")):
			globex++
		}
	}
	if acme != users-1 || globex != 2*users {
		t.Fatalf("remaining acme=%d globex=%d, want %d and %d", acme, globex, users-1, 2*users)
	}
}

// spec: 12.8 (Compliance Interfaces: Tenant deletion lifecycle, Phase 4);
// 12.4 Redis HA and Failure Modes. On a multi-master cluster the tenant
// sticky delete reaches the master that owns the tenant's slot and leaves
// the other tenant's assignments in place.
func TestDeleteByTenantCoversClusterMaster_spec_12_8(t *testing.T) {
	cl := clustertest.New(t, 3)
	c := NewRedis(cl.Client)
	ctx := context.Background()
	for _, tenant := range []string{"acme", "globex"} {
		for i := 0; i < 10; i++ {
			if err := c.Put(ctx, tenant, fmt.Sprintf("exp-%d", i%3), fmt.Sprintf("user-%d", i), "treatment"); err != nil {
				t.Fatalf("Put: %v", err)
			}
		}
	}
	if n, err := c.DeleteByTenant(ctx, "acme"); err != nil || n != 10 {
		t.Fatalf("DeleteByTenant = (%d, %v), want (10, nil)", n, err)
	}
	for _, k := range cl.Keys() {
		if strings.HasPrefix(k, rediskeys.TenantPrefix("acme")) {
			t.Fatalf("acme key %q survived tenant deletion", k)
		}
	}
	if got := len(cl.Keys()); got != 10 {
		t.Fatalf("remaining keys = %d, want 10 (globex's)", got)
	}
}
