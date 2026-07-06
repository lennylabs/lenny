// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"sync"
	"testing"

	"google.golang.org/grpc/codes"

	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/slothealth"
	"github.com/lennylabs/lenny/pkg/sandbox/slotstate"
)

// concurrentSlotBinder is a race-safe slotBinder that always fails a bind with
// a non-retryable reason and leaks the failed slot (release errors). It counts
// drains under a lock so a concurrent -race run observes the whole-pod
// replacement trigger firing exactly once for the pod.
type concurrentSlotBinder struct {
	mu      sync.Mutex
	drained int
}

func (c *concurrentSlotBinder) BindSlot(_ context.Context, _ podsession.SlotBindRequest) (*podsession.BindResult, error) {
	// A non-retryable reason so a single attempt leaks and returns without a
	// second bind; each goroutine contributes exactly one leak to the pod.
	return nil, slotBindErr("pod-hot", "slot", "workspace_prep", codes.InvalidArgument)
}

func (c *concurrentSlotBinder) ReleaseSlotReservation(_ context.Context, _, _ string) error {
	// Release always errors, so every failed slot is leaked and counted
	// persistently via RecordLeak.
	return errors.New("slot cleanup timed out")
}

func (c *concurrentSlotBinder) DrainSandbox(_ context.Context, _ string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.drained++
	return nil
}

func (c *concurrentSlotBinder) drains() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.drained
}

// diagnosis: a failure means the persistent leaked-slot accumulation in
// slothealth is not concurrency-safe or the bind-time RecordLeak split
// (SPEC-D / CODE-D/D1) miscounts leaks under concurrent request goroutines, so
// the §5.2 whole-pod replacement trigger either never fires for a pod
// accumulating permanent leaks or drains it more than once. Because bind-time
// leaks are now counted persistently rather than in the rolling window, a slow
// or bursty stream of leaks on the same pod must still combine to reach the
// ceil(maxConcurrent/2) threshold.
//
// spec: §6.2 "`leaked` slot semantics" (leaked slots counted persistently
// toward the threshold), §5.2 "Concurrent-workspace slot retry policy"
// (whole-pod replacement trigger, ceil(maxConcurrent/2) fail-or-leak).
func TestSlotRetryConcurrentLeaksReachThreshold_spec_6_2(t *testing.T) {
	// The slothealth Tracker is shared across all request goroutines binding
	// slots on the same pod; drive many concurrent bind-time leaks against it
	// under -race. maxConcurrent=8 → threshold ceil(8/2)=4: once four leaks
	// accumulate persistently the pod is drained. The Tracker is Forget-cleared
	// on the first drain, and each subsequent leak re-accumulates from a clean
	// slate, so the exact drain count depends on scheduling; the load-bearing
	// assertion is that the persistent count reaches the threshold and the pod
	// drains at least once (it never drains under a windowed count that ages
	// individual leaks out before four coincide).
	const goroutines = 64
	health := slothealth.New()
	slots := slotstate.NewRegistry()
	binder := &concurrentSlotBinder{}

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			_, _ = applySlotRetryPolicy(
				context.Background(), binder, health, slots,
				func(string) {}, func(_, _ string, _ int) {}, req("pool-hot", 8),
			)
		}()
	}
	wg.Wait()

	if binder.drains() == 0 {
		t.Errorf("pod-hot never drained: %d concurrent persistent leaks must reach the ceil(8/2)=4 threshold", goroutines)
	}
}
