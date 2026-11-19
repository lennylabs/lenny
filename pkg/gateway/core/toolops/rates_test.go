// SPDX-License-Identifier: MIT

package toolops

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/policy/ratelimit"
)

// TestIncrementRateCountsPerMinute checks that the rate counter counts
// through the wired counter, and in memory when none is wired.
// spec: GWSEAM-IncrementRate
func TestIncrementRateCountsPerMinute(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	for name, c := range map[string]RateCounter{
		"wired":     NewRateCounter(ratelimit.NewMemory()),
		"in memory": NewRateCounter(nil),
	} {
		for want := 1; want <= 2; want++ {
			if got, err := c.IncrementRate(ctx, "msg:out:acme:s1", now); err != nil || got != want {
				t.Errorf("%s: count = %d, %v; want %d", name, got, err, want)
			}
		}
	}
}
