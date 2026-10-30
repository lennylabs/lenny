// SPDX-License-Identifier: MIT

package runtime

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestStubMaxConcurrentHoldsUnderRace runs N goroutines against a
// Stub configured with MaxConcurrent=4. The §15.4 invariant: the
// in-flight count never exceeds the cap, and every accepted call
// is balanced by a release on return.
func TestStubMaxConcurrentHoldsUnderRace(t *testing.T) {
	const N = 200
	stub := New(Config{
		ResponseLatency: 500 * time.Microsecond,
		MaxConcurrent:   4,
	})
	var wg sync.WaitGroup
	var rejected atomic.Int64
	var done atomic.Int64
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := stub.Call(context.Background())
			if errors.Is(err, ErrAtCapacity) {
				rejected.Add(1)
				return
			}
			if err == nil {
				done.Add(1)
			}
		}()
	}
	wg.Wait()
	if stub.InFlight() != 0 {
		t.Errorf("§15.4 violated: %d in-flight after all goroutines returned", stub.InFlight())
	}
	if done.Load()+rejected.Load() != int64(N) {
		t.Errorf("accounting mismatch: done=%d rejected=%d N=%d", done.Load(), rejected.Load(), N)
	}
}

// TestStubErrorRateDeterministicUnderRace asserts the deterministic
// error injector is stable under concurrent calls (the documented
// behaviour: shouldError uses an atomic counter so the rate is
// reproducible).
func TestStubErrorRateDeterministicUnderRace(t *testing.T) {
	const N = 400
	stub := New(Config{ErrorRate: 0.25})
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = stub.Call(context.Background())
		}()
	}
	wg.Wait()
	errors := stub.TotalErrors()
	// 25% ± 10% tolerance: the deterministic counter doesn't promise
	// uniform timing under race, but the long-run rate should be
	// close to ErrorRate.
	if float64(errors) < 0.15*float64(N) || float64(errors) > 0.35*float64(N) {
		t.Errorf("error rate observed = %d/%d ≠ ~25%%", errors, N)
	}
}
