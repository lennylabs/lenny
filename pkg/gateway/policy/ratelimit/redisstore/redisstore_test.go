// SPDX-License-Identifier: MIT

package redisstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/policy/ratelimit/redisstore"
	"github.com/lennylabs/lenny/pkg/redisconn/clustertest"
)

// spec: §12.4 Redis HA and Failure Modes (`rl:{key}:{minute_epoch}`: a
// single-key INCR on a per-minute window; Redis Cluster at Tier 3). The
// increment script names one key, so it runs on a cluster under the
// cross-slot hook, and counts within one window accumulate per key.
func TestIncrRunsOnClusterUnderSlotGuard_spec_12_4(t *testing.T) {
	c := clustertest.New(t, 3)
	s := redisstore.New(c.Client)
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0)
	for want := 1; want <= 3; want++ {
		n, err := s.Incr(ctx, "rt:acme:echo", now)
		if err != nil {
			t.Fatalf("Incr: %v", err)
		}
		if n != want {
			t.Fatalf("Incr #%d = %d, want %d", want, n, want)
		}
	}
	if n, err := s.Incr(ctx, "po:globex:default", now); err != nil || n != 1 {
		t.Fatalf("Incr(other key) = %d, %v; want 1, nil", n, err)
	}
}
