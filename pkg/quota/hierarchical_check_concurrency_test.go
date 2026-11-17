// SPDX-License-Identifier: MIT

package quota_test

import (
	"sync"
	"testing"

	"github.com/lennylabs/lenny/pkg/quota"
)

// This concurrency contract test runs the package's public surface from
// N goroutines with the race detector on and asserts the documented
// invariants hold under contention, as
// `pkg/gateway/slotcounter/slotcounter_test.go::TestReserveIsAtomicUnderRace`
// does (TESTING.md §6.2).

// TestQuotaHierarchicalCheckIsPure asserts HierarchicalCheck has no
// hidden shared state: N goroutines invoking it on independent inputs
// observe the documented State for their inputs.
func TestQuotaHierarchicalCheckIsPure(t *testing.T) {
	t.Parallel()
	h := quota.Hierarchy{Global: 1000, Tenant: 100, User: 10}
	const N = 500
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			// User-exceeded scenario.
			res := quota.HierarchicalCheck(int64(i%50), int64(i%50), 100, h)
			if res.State != quota.StateHardExceeded {
				t.Errorf("State=%s want HardExceeded (user limit hit)", res.State)
			}
		}()
	}
	wg.Wait()
}
