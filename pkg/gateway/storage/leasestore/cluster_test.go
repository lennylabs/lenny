// SPDX-License-Identifier: MIT

package leasestore_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/storage/leasestore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/rediskeys"
	"github.com/lennylabs/lenny/pkg/redisconn/clustertest"
)

// spec: §12.4 Redis HA and Failure Modes (Redis Cluster at Tier 3; the
// `t:{tenant_id}:` prefix hashes a tenant's keys to one slot); §12.8
// (erasure scope). Every lease key carries the tenant's hash tag, so all of
// a tenant's leases sit on one master, and DeleteByTenant on a
// multi-master cluster removes all of them (a bare SCAN on the cluster
// client reaches one master only) while other tenants' leases survive.
func TestDeleteByTenantRemovesEveryLeaseOnCluster_spec_12_8(t *testing.T) {
	cl := clustertest.New(t, 3)
	s := leasestore.New(cl.Client)
	ctx := context.Background()
	const n = 20
	for _, tenant := range []string{"acme", "globex", "initech"} {
		for i := 0; i < n; i++ {
			if _, err := s.Acquire(ctx, tenant, fmt.Sprintf("sess-%d", i), "replica-a", time.Minute); err != nil {
				t.Fatalf("Acquire %s/%d: %v", tenant, i, err)
			}
		}
	}
	owner := cl.ServerFor(rediskeys.TenantPrefix("acme"))
	if !owner.Exists(leasestore.Key("acme", "sess-0")) {
		t.Fatal("acme lease is not on the tenant's master")
	}

	got, err := s.DeleteByTenant(ctx, "acme")
	if err != nil || got != n {
		t.Fatalf("DeleteByTenant = (%d, %v), want (%d, nil)", got, err, n)
	}
	remaining := 0
	for _, k := range cl.Keys() {
		if strings.HasPrefix(k, leasestore.TenantKeyPrefix("acme")) {
			t.Errorf("acme lease %q survived tenant erasure", k)
		}
		remaining++
	}
	if remaining != 2*n {
		t.Fatalf("remaining leases = %d, want %d", remaining, 2*n)
	}
}
