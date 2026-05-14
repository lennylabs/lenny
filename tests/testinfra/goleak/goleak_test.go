// SPDX-License-Identifier: MIT

package goleak_test

import (
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/goleak"
)

// spec: 17.4 (no-op test: VerifyNone passes when no goroutine leaks)
// diagnosis: VerifyNone fired a leak alert on a test that spawned
//
//	nothing. The baseline diff is wrong.
func TestVerifyNoneClean(t *testing.T) {
	t.Parallel()
	defer goleak.VerifyNone(t)
}

// spec: 17.4 (joined goroutines do not leak)
// diagnosis: A goroutine that was waited on still showed up as a
//
//	leak. The cleanup window is too short, or the snapshot
//	captures stacks before the runtime collects them.
func TestVerifyNoneJoined(t *testing.T) {
	t.Parallel()
	defer goleak.VerifyNone(t)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(10 * time.Millisecond)
	}()
	wg.Wait()
}
