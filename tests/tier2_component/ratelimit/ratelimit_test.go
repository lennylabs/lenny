//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §11.1 request-rate counter, exercising the
// Redis-backed pkg/gateway/ratelimit/redisstore against a real Redis
// container. Covers within-window accumulation, the minute-window
// reset, and atomic counting under concurrent increments.
package ratelimit_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/policy/ratelimit/redisstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

func rateKey(t *testing.T) string {
	t.Helper()
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return "k-" + hex.EncodeToString(b[:])
}

// spec: 11.1
// diagnosis: the Redis-backed request-rate counter in
// pkg/gateway/ratelimit/redisstore did not behave as specified. Counts
// must accumulate within a one-minute window, the window must reset
// when the minute advances, and concurrent increments must be counted
// atomically without losing an increment.
func TestRateLimitCounterContract(t *testing.T) {
	t.Parallel()
	rd := containers.StartRedis(t, containers.RedisOptions{})
	counter := redisstore.New(rd.Client)
	ctx := context.Background()
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	t.Run("counts accumulate within a one-minute window", func(t *testing.T) {
		key := rateKey(t)
		for want := 1; want <= 4; want++ {
			n, err := counter.Incr(ctx, key, base.Add(time.Duration(want)*time.Second))
			if err != nil {
				t.Fatalf("Incr: %v", err)
			}
			if n != want {
				t.Errorf("Incr #%d: got count %d, want %d", want, n, want)
			}
		}
	})

	t.Run("the window resets when the minute advances", func(t *testing.T) {
		key := rateKey(t)
		if _, err := counter.Incr(ctx, key, base); err != nil {
			t.Fatalf("Incr: %v", err)
		}
		if _, err := counter.Incr(ctx, key, base); err != nil {
			t.Fatalf("Incr: %v", err)
		}
		n, err := counter.Incr(ctx, key, base.Add(time.Minute))
		if err != nil {
			t.Fatalf("Incr: %v", err)
		}
		if n != 1 {
			t.Errorf("a new minute window must restart at 1, got %d", n)
		}
	})

	t.Run("concurrent increments are counted atomically", func(t *testing.T) {
		key := rateKey(t)
		const requests = 50
		seen := make([]bool, requests+1)
		var mu sync.Mutex
		var wg sync.WaitGroup
		for i := 0; i < requests; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				n, err := counter.Incr(ctx, key, base)
				if err != nil {
					return
				}
				mu.Lock()
				if n >= 1 && n <= requests {
					seen[n] = true
				}
				mu.Unlock()
			}()
		}
		wg.Wait()
		// Atomic INCR hands out every value 1..requests exactly once.
		for n := 1; n <= requests; n++ {
			if !seen[n] {
				t.Errorf("count %d was never returned — an increment was lost", n)
			}
		}
	})
}
