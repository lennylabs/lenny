// SPDX-License-Identifier: MIT

package idempotency_test

import (
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/idempotency"
)

// This concurrency contract test runs the package's public surface from
// N goroutines with the race detector on and asserts the documented
// invariants hold under contention, as
// `pkg/gateway/slotcounter/slotcounter_test.go::TestReserveIsAtomicUnderRace`
// does (TESTING.md §6.2).

// TestIdempotencyDetectReuseIsPure asserts DetectReuse has no shared
// state between concurrent invocations.
func TestIdempotencyDetectReuseIsPure(t *testing.T) {
	t.Parallel()
	key := idempotency.Key{TenantID: "acme", Value: "k-1"}
	now := time.Now()
	hash := idempotency.HashBody([]byte(`{"x":1}`))
	stored := idempotency.Record{Key: key, BodyHash: hash, StoredAt: now}
	const N = 500
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			action, err := idempotency.DetectReuse(stored, hash, now)
			if action != idempotency.ActionReplay || err != nil {
				t.Errorf("action=%v err=%v want (Replay, nil)", action, err)
			}
		}()
	}
	wg.Wait()
}
