// SPDX-License-Identifier: MIT

package redisstream

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/billing/billingstore"
)

// TestStreamMaxLenForTier verifies the §17.8.2 per-tier
// billingRedisStreamMaxLen default: 72,000 at Tier 3, 50,000 otherwise.
// spec: spec/17_deployment-topology.md lines 1203, 1205.
func TestStreamMaxLenForTier_spec_17_8_2_1203(t *testing.T) {
	cases := map[string]int64{
		"tier1":   DefaultStreamMaxLen,
		"tier2":   DefaultStreamMaxLen,
		"tier3":   Tier3StreamMaxLen,
		"":        DefaultStreamMaxLen,
		"unknown": DefaultStreamMaxLen,
	}
	for tier, want := range cases {
		if got := StreamMaxLenForTier(tier); got != want {
			t.Errorf("StreamMaxLenForTier(%q) = %d, want %d", tier, got, want)
		}
	}
	if Tier3StreamMaxLen != 72_000 {
		t.Errorf("Tier3StreamMaxLen = %d, want 72000", Tier3StreamMaxLen)
	}
}

// TestPublishHonorsConfiguredStreamMaxLen confirms the StreamMaxLen option
// flows into the XADD MAXLEN trim: publishing more events than the cap
// leaves the per-tenant stream trimmed to the configured length. spec:
// spec/17_deployment-topology.md line 1203; §11.2.1 MAXLEN ~billingRedisStreamMaxLen.
func TestPublishHonorsConfiguredStreamMaxLen_spec_17_8_2_1203(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	tier, err := New(Options{Client: client, ConsumerName: "replica-1", StreamMaxLen: 2})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if err := tier.Publish(ctx, billingstore.Event{TenantID: "acme", SequenceNumber: uint64(i + 1)}); err != nil {
			t.Fatalf("Publish %d: %v", i, err)
		}
	}

	got, err := client.XLen(ctx, streamKey("acme")).Result()
	if err != nil {
		t.Fatalf("XLen: %v", err)
	}
	if got != 2 {
		t.Errorf("stream length = %d after 5 publishes with StreamMaxLen=2, want 2 (trimmed)", got)
	}
}
