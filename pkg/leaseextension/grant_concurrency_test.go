// SPDX-License-Identifier: MIT

package leaseextension_test

import (
	"sync"
	"testing"

	"github.com/lennylabs/lenny/pkg/leaseextension"
)

// This concurrency contract test runs the package's public surface from
// N goroutines with the race detector on and asserts the documented
// invariants hold under contention, as
// `pkg/gateway/slotcounter/slotcounter_test.go::TestReserveIsAtomicUnderRace`
// does (TESTING.md §6.2).

// TestLeaseExtensionGrantNeverOversteps asserts Grant always returns
// a non-negative granted increment that keeps current+granted ≤ ceiling
// under N goroutines.
func TestLeaseExtensionGrantNeverOversteps(t *testing.T) {
	t.Parallel()
	const ceiling = int64(100)
	const N = 500
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			current := int64(i % 80)
			requested := current + int64(i%50)
			granted, _ := leaseextension.Grant(current, requested, ceiling)
			if granted < 0 {
				t.Errorf("§4.9 violated: granted=%d < 0", granted)
			}
			if current+granted > ceiling {
				t.Errorf("§4.9 violated: current(%d)+granted(%d) > ceiling(%d)", current, granted, ceiling)
			}
		}()
	}
	wg.Wait()
}
