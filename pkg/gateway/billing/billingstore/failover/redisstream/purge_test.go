// SPDX-License-Identifier: MIT

// Coverage for the §12.8 step-5 erasure of the Redis-stream half of the
// billing write-ahead buffer: PurgeUser must XDEL every staged entry for
// the target user within a tenant, leaving other users' and other
// tenants' staged entries intact.
//
// spec: §12.8 line 788 (Billing write-ahead buffer), step 5.
package redisstream

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/billing/billingstore"
)

func newPurgeTier(t *testing.T) (*Tier, redis.UniversalClient) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	tier, err := New(Options{Client: client, ConsumerName: "replica-1"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return tier, client
}

func TestPurgeUser_RemovesOnlyTargetUserEntries_spec_12_8_788(t *testing.T) {
	ctx := context.Background()
	tier, client := newPurgeTier(t)
	for _, e := range []billingstore.Event{
		{TenantID: "acme", UserID: "alice", SequenceNumber: 1, EventType: billingstore.EventSessionCreated},
		{TenantID: "acme", UserID: "alice", SequenceNumber: 2, EventType: billingstore.EventSessionCreated},
		{TenantID: "acme", UserID: "bob", SequenceNumber: 3, EventType: billingstore.EventSessionCreated},
	} {
		if err := tier.Publish(ctx, e); err != nil {
			t.Fatalf("Publish: %v", err)
		}
	}

	n, err := tier.PurgeUser(ctx, "acme", "alice")
	if err != nil {
		t.Fatalf("PurgeUser: %v", err)
	}
	if n != 2 {
		t.Fatalf("purged = %d, want 2", n)
	}

	remaining, err := client.XLen(ctx, streamKey("acme")).Result()
	if err != nil {
		t.Fatalf("XLen: %v", err)
	}
	if remaining != 1 {
		t.Fatalf("stream len = %d, want 1 (acme/bob survives)", remaining)
	}
}

func TestPurgeUser_DoesNotCrossTenants_spec_12_8_788(t *testing.T) {
	ctx := context.Background()
	tier, client := newPurgeTier(t)
	if err := tier.Publish(ctx, billingstore.Event{TenantID: "acme", UserID: "alice", SequenceNumber: 1, EventType: billingstore.EventSessionCreated}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if err := tier.Publish(ctx, billingstore.Event{TenantID: "globex", UserID: "alice", SequenceNumber: 1, EventType: billingstore.EventSessionCreated}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	n, err := tier.PurgeUser(ctx, "acme", "alice")
	if err != nil {
		t.Fatalf("PurgeUser: %v", err)
	}
	if n != 1 {
		t.Fatalf("purged = %d, want 1", n)
	}
	if l, _ := client.XLen(ctx, streamKey("globex")).Result(); l != 1 {
		t.Fatalf("globex stream len = %d, want 1 (untouched)", l)
	}
}

func TestPurgeUser_RejectsEmptyArgs_spec_12_8_788(t *testing.T) {
	tier, _ := newPurgeTier(t)
	if _, err := tier.PurgeUser(context.Background(), "", "alice"); err == nil {
		t.Fatal("empty tenant should error")
	}
	if _, err := tier.PurgeUser(context.Background(), "acme", ""); err == nil {
		t.Fatal("empty user should error")
	}
}
