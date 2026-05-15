//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §11.2 / §12.4 token-usage counter, exercising
// the Redis-backed pkg/gateway/quotastore against a real Redis
// container. Covers accumulation, window and period separation,
// tenant/user isolation, the window TTL, and composition with the
// pure pkg/quota Check arithmetic.
package quota_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/quotastore"
	"github.com/lennylabs/lenny/pkg/quota"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

func uniq(t *testing.T) string {
	t.Helper()
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return hex.EncodeToString(b[:])
}

var refTime = time.Date(2026, 5, 15, 14, 30, 0, 0, time.UTC)

func TestQuotaCounterContract(t *testing.T) {
	t.Parallel()
	rd := containers.StartRedis(t, containers.RedisOptions{})
	counter := quotastore.New(rd.Client)
	ctx := context.Background()

	t.Run("add accumulates within a window", func(t *testing.T) {
		tenant, user := "acme-"+uniq(t), "alice"
		total, err := counter.Add(ctx, tenant, user, quota.ResetHourly, refTime, 100)
		if err != nil {
			t.Fatalf("Add: %v", err)
		}
		if total != 100 {
			t.Errorf("first Add total = %d, want 100", total)
		}
		total, err = counter.Add(ctx, tenant, user, quota.ResetHourly, refTime, 50)
		if err != nil {
			t.Fatalf("second Add: %v", err)
		}
		if total != 150 {
			t.Errorf("second Add total = %d, want 150", total)
		}
		usage, err := counter.Usage(ctx, tenant, user, quota.ResetHourly, refTime)
		if err != nil || usage != 150 {
			t.Errorf("Usage = %d (err %v), want 150", usage, err)
		}
	})

	t.Run("unused window reads as zero", func(t *testing.T) {
		usage, err := counter.Usage(ctx, "acme-"+uniq(t), "bob", quota.ResetHourly, refTime)
		if err != nil || usage != 0 {
			t.Errorf("Usage of untouched window = %d (err %v), want 0", usage, err)
		}
	})

	t.Run("distinct windows and periods are separate counters", func(t *testing.T) {
		tenant, user := "acme-"+uniq(t), "alice"
		hour15 := refTime.Add(time.Hour)
		if _, err := counter.Add(ctx, tenant, user, quota.ResetHourly, refTime, 10); err != nil {
			t.Fatalf("Add hour 14: %v", err)
		}
		if _, err := counter.Add(ctx, tenant, user, quota.ResetHourly, hour15, 7); err != nil {
			t.Fatalf("Add hour 15: %v", err)
		}
		if _, err := counter.Add(ctx, tenant, user, quota.ResetDaily, refTime, 99); err != nil {
			t.Fatalf("Add daily: %v", err)
		}
		if u, _ := counter.Usage(ctx, tenant, user, quota.ResetHourly, refTime); u != 10 {
			t.Errorf("hour-14 usage = %d, want 10", u)
		}
		if u, _ := counter.Usage(ctx, tenant, user, quota.ResetHourly, hour15); u != 7 {
			t.Errorf("hour-15 usage = %d, want 7", u)
		}
		if u, _ := counter.Usage(ctx, tenant, user, quota.ResetDaily, refTime); u != 99 {
			t.Errorf("daily usage = %d, want 99", u)
		}
	})

	t.Run("tenant and user counters are isolated", func(t *testing.T) {
		tenantA, tenantB := "acme-"+uniq(t), "globex-"+uniq(t)
		if _, err := counter.Add(ctx, tenantA, "alice", quota.ResetHourly, refTime, 40); err != nil {
			t.Fatalf("Add: %v", err)
		}
		if u, _ := counter.Usage(ctx, tenantA, "carol", quota.ResetHourly, refTime); u != 0 {
			t.Errorf("other user in same tenant = %d, want 0", u)
		}
		if u, _ := counter.Usage(ctx, tenantB, "alice", quota.ResetHourly, refTime); u != 0 {
			t.Errorf("same user in another tenant = %d, want 0", u)
		}
	})

	t.Run("window key carries a TTL", func(t *testing.T) {
		tenant := "acme-" + uniq(t)
		if _, err := counter.Add(ctx, tenant, "alice", quota.ResetHourly, refTime, 5); err != nil {
			t.Fatalf("Add: %v", err)
		}
		keys, err := rd.Client.Keys(ctx, "t:"+tenant+":quota:tokens:*").Result()
		if err != nil || len(keys) != 1 {
			t.Fatalf("expected one quota key, got %v (err %v)", keys, err)
		}
		ttl, err := rd.Client.TTL(ctx, keys[0]).Result()
		if err != nil {
			t.Fatalf("TTL: %v", err)
		}
		if ttl <= 0 {
			t.Errorf("window key TTL = %v, want a positive expiry", ttl)
		}
	})

	t.Run("rolling period and negative tokens are rejected", func(t *testing.T) {
		tenant := "acme-" + uniq(t)
		if _, err := counter.Add(ctx, tenant, "alice", quota.ResetRolling, refTime, 1); err == nil {
			t.Error("Add with the rolling period should be rejected")
		}
		if _, err := counter.Add(ctx, tenant, "alice", quota.ResetHourly, refTime, -5); err == nil {
			t.Error("Add with negative tokens should be rejected")
		}
	})

	t.Run("usage composes with the pkg/quota Check arithmetic", func(t *testing.T) {
		tenant, user := "acme-"+uniq(t), "alice"
		const limit = int64(100)
		if _, err := counter.Add(ctx, tenant, user, quota.ResetHourly, refTime, 85); err != nil {
			t.Fatalf("Add: %v", err)
		}
		usage, _ := counter.Usage(ctx, tenant, user, quota.ResetHourly, refTime)
		if got := quota.Check(usage, limit); got != quota.StateSoftWarning {
			t.Errorf("Check(%d, %d) = %q, want soft_warning", usage, limit, got)
		}
		if _, err := counter.Add(ctx, tenant, user, quota.ResetHourly, refTime, 20); err != nil {
			t.Fatalf("Add: %v", err)
		}
		usage, _ = counter.Usage(ctx, tenant, user, quota.ResetHourly, refTime)
		if got := quota.Check(usage, limit); got != quota.StateHardExceeded {
			t.Errorf("Check(%d, %d) = %q, want hard_exceeded", usage, limit, got)
		}
	})
}
