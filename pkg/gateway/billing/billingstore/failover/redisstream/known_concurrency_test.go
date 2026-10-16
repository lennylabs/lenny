// SPDX-License-Identifier: MIT

package redisstream

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/billing/billingstore"
)

// TestConcurrentTenantPublishAndPendingShareTheTier_spec_11_2_1 drives one
// Tier from many goroutines at once, each publishing to its own new tenant
// while others read Pending, which is the access pattern of the per-tenant
// flushers the FlusherManager starts at gateway startup. Before the known
// map was guarded the runtime aborted with "concurrent map writes" and the
// race detector reported the map access. Run it under -race.
// spec: §11.2.1 (billing Redis stream tier)
func TestConcurrentTenantPublishAndPendingShareTheTier_spec_11_2_1(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	tier, err := New(Options{Client: client, ConsumerName: "replica-1"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	const tenants = 32
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make(chan error, 2*tenants)
	for i := 0; i < tenants; i++ {
		tenant := fmt.Sprintf("acme-%d", i)
		wg.Add(2)
		go func() {
			defer wg.Done()
			errs <- tier.Publish(ctx, billingstore.Event{TenantID: tenant, SequenceNumber: 1})
		}()
		go func() {
			defer wg.Done()
			_, err := tier.Pending(ctx)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Publish or Pending: %v", err)
		}
	}

	if got := len(tier.knownTenants()); got != tenants {
		t.Errorf("known tenants = %d, want %d", got, tenants)
	}
	n, err := tier.Pending(ctx)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if n != tenants {
		t.Errorf("Pending = %d, want %d (one event per tenant)", n, tenants)
	}
}
