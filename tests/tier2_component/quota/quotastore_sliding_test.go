//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §11.2 sliding-window counter on top of
// pkg/gateway/quotastore. The sliding window sums bucket counters
// across the configured window length so a token quota that
// resets on a rolling boundary is enforceable without waiting for
// a fixed-window rollover. Covers accumulation, bucket isolation,
// window expiry (old buckets drop out of the sum), tenant / user
// isolation, and input validation.
package quota_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/quotastore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// spec: 11.2 (sliding-window counter on top of pkg/gateway/quotastore)
// diagnosis: SlidingAdd / SlidingUsage must behave like the fixed
// counters but sum across the rolling window's bucket interval, so a
// quota that resets continuously is enforceable from a single store.
func TestQuotaSlidingWindowContract(t *testing.T) {
	t.Parallel()
	rd := containers.StartRedis(t, containers.RedisOptions{})
	counter := quotastore.New(rd.Client)
	ctx := context.Background()

	const window = 10 * time.Minute
	const resolution = time.Minute

	t.Run("Add accumulates within the current bucket", func(t *testing.T) {
		tenant, user := "acme-"+uniq(t), "alice"
		total, err := counter.SlidingAdd(ctx, tenant, user, window, resolution, refTime, 100)
		if err != nil {
			t.Fatalf("SlidingAdd: %v", err)
		}
		if total != 100 {
			t.Errorf("first SlidingAdd total = %d, want 100", total)
		}
		total, err = counter.SlidingAdd(ctx, tenant, user, window, resolution, refTime, 50)
		if err != nil {
			t.Fatalf("second SlidingAdd: %v", err)
		}
		if total != 150 {
			t.Errorf("second SlidingAdd total = %d, want 150", total)
		}
		usage, err := counter.SlidingUsage(ctx, tenant, user, window, resolution, refTime)
		if err != nil || usage != 150 {
			t.Errorf("SlidingUsage = %d (err %v), want 150", usage, err)
		}
	})

	t.Run("buckets across the window sum into Usage", func(t *testing.T) {
		tenant, user := "acme-"+uniq(t), "alice"
		// Three writes in three distinct buckets within the window.
		for i := 0; i < 3; i++ {
			at := refTime.Add(time.Duration(i) * resolution)
			if _, err := counter.SlidingAdd(ctx, tenant, user, window, resolution, at, 10); err != nil {
				t.Fatalf("SlidingAdd[%d]: %v", i, err)
			}
		}
		// Sum reads from the most recent bucket back, so the read at
		// refTime+2*resolution sees all three writes.
		usage, err := counter.SlidingUsage(ctx, tenant, user, window, resolution, refTime.Add(2*resolution))
		if err != nil {
			t.Fatalf("SlidingUsage: %v", err)
		}
		if usage != 30 {
			t.Errorf("SlidingUsage = %d, want 30", usage)
		}
	})

	t.Run("buckets outside the window are excluded", func(t *testing.T) {
		tenant, user := "acme-"+uniq(t), "alice"
		// Old write outside the 10-minute window.
		old := refTime.Add(-30 * time.Minute)
		if _, err := counter.SlidingAdd(ctx, tenant, user, window, resolution, old, 99); err != nil {
			t.Fatalf("old SlidingAdd: %v", err)
		}
		// Fresh write inside the window.
		if _, err := counter.SlidingAdd(ctx, tenant, user, window, resolution, refTime, 7); err != nil {
			t.Fatalf("fresh SlidingAdd: %v", err)
		}
		usage, err := counter.SlidingUsage(ctx, tenant, user, window, resolution, refTime)
		if err != nil {
			t.Fatalf("SlidingUsage: %v", err)
		}
		// The 99-token write is 30 buckets back — outside the
		// 10-bucket window — so only the 7-token write is summed.
		if usage != 7 {
			t.Errorf("SlidingUsage = %d, want 7 (old bucket must be excluded)", usage)
		}
	})

	t.Run("tenant and user counters are isolated", func(t *testing.T) {
		tenantA, tenantB := "acme-"+uniq(t), "globex-"+uniq(t)
		if _, err := counter.SlidingAdd(ctx, tenantA, "alice", window, resolution, refTime, 25); err != nil {
			t.Fatalf("SlidingAdd: %v", err)
		}
		if u, _ := counter.SlidingUsage(ctx, tenantA, "carol", window, resolution, refTime); u != 0 {
			t.Errorf("other user in same tenant = %d, want 0", u)
		}
		if u, _ := counter.SlidingUsage(ctx, tenantB, "alice", window, resolution, refTime); u != 0 {
			t.Errorf("same user in another tenant = %d, want 0", u)
		}
	})

	t.Run("resolution must divide window", func(t *testing.T) {
		tenant := "acme-" + uniq(t)
		_, err := counter.SlidingAdd(ctx, tenant, "alice",
			10*time.Minute, 3*time.Minute, refTime, 1)
		if err == nil {
			t.Error("expected SlidingAdd to reject a resolution that does not divide the window")
		}
		_, err = counter.SlidingUsage(ctx, tenant, "alice",
			10*time.Minute, 3*time.Minute, refTime)
		if err == nil {
			t.Error("expected SlidingUsage to reject a resolution that does not divide the window")
		}
	})

	t.Run("negative tokens and non-positive window are rejected", func(t *testing.T) {
		tenant := "acme-" + uniq(t)
		if _, err := counter.SlidingAdd(ctx, tenant, "alice", window, resolution, refTime, -1); err == nil {
			t.Error("expected SlidingAdd to reject negative tokens")
		}
		if _, err := counter.SlidingAdd(ctx, tenant, "alice", 0, resolution, refTime, 1); err == nil {
			t.Error("expected SlidingAdd to reject zero window length")
		}
		if _, err := counter.SlidingUsage(ctx, tenant, "alice", 0, resolution, refTime); err == nil {
			t.Error("expected SlidingUsage to reject zero window length")
		}
	})

	t.Run("default resolution selected for zero", func(t *testing.T) {
		tenant := "acme-" + uniq(t)
		total, err := counter.SlidingAdd(ctx, tenant, "alice", time.Hour, 0, refTime, 42)
		if err != nil {
			t.Fatalf("SlidingAdd: %v", err)
		}
		if total != 42 {
			t.Errorf("total = %d, want 42", total)
		}
		usage, err := counter.SlidingUsage(ctx, tenant, "alice", time.Hour, 0, refTime)
		if err != nil || usage != 42 {
			t.Errorf("SlidingUsage = %d (err %v), want 42", usage, err)
		}
	})
}
