// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local concurrency coverage for the sticky ended state of the
// sidecar socket transport.
//
// The runtime process lives as long as the pod and is not reconnected inside
// it. When the runtime closes its end of the connection, the transport's
// fan-out reader records the ended state before it closes its subscribers'
// output channels. Once a caller has observed an output channel close, every
// later ServesNextSession reports false and every later Start fails, which is
// what the whole-pod scrub report and the next session's start rely on. The
// window this case drives is the instant the reader stops, with goroutines
// calling ServesNextSession and Start in a loop across it.
//
// The case runs under -race and carries a stress budget:
//
//	lenny-test stress --test TestSocketRuntimeReaderStopIsObservedByEveryLaterCall_spec_4_7_10 --runs 50 --pkg ./tests/tier7a_load_local/... --tag load_local
//
// spec: §4.7.10 (Runtime process lifetime), §5.2 (Runtime not live).
package tier7a_load_local_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// readerStopCallers is how many goroutines loop on the transport across the
// reader's stop.
const readerStopCallers = 4

// spec: 4.7.10 (Runtime process lifetime), 5.2 (Runtime not live)
// diagnosis: a runtime death can be missed at the boundary, or a session can
// start over a dead connection. A ServesNextSession that reads true, or a
// Start that returns nil, after the output channel closed means the ended
// state is recorded after the subscribers close or is not sticky.
func TestSocketRuntimeReaderStopIsObservedByEveryLaterCall_spec_4_7_10(t *testing.T) {
	for attempt := range raceAttempts {
		t.Run(fmt.Sprintf("attempt_%d", attempt), func(t *testing.T) {
			readerStopAttempt(t)
		})
	}
}

// readerStopAttempt runs one race on a fresh runtime process: the runtime
// closes its connection while callers loop on ServesNextSession and Start.
// Each iteration reads whether the output channel had already closed before
// it calls, so a call that began after the close must see the ended state.
func readerStopAttempt(t *testing.T) {
	t.Helper()
	rt, socket := newListenerRaceRuntime(t)
	peer := dialRuntimeSocket(t, socket)
	if err := rt.Start(context.Background(), "alice"); err != nil {
		t.Fatalf("start the first session: %v", err)
	}
	out, err := rt.Output(context.Background(), "alice")
	if err != nil {
		t.Fatalf("Output: %v", err)
	}

	var outClosed, stop atomic.Bool
	var violations atomic.Int64
	var firstViolation atomic.Value
	record := func(msg string) {
		if violations.Add(1) == 1 {
			firstViolation.Store(msg)
		}
	}
	var wg sync.WaitGroup
	for i := range readerStopCallers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			session := fmt.Sprintf("caller-%d", i)
			for !stop.Load() {
				after := outClosed.Load()
				if rt.ServesNextSession() && after {
					record("ServesNextSession() = true after the output channel closed")
				}
				after = outClosed.Load()
				if err := rt.Start(context.Background(), session); err == nil && after {
					record("Start = nil after the output channel closed")
				}
			}
		}(i)
	}

	time.Sleep(2 * time.Millisecond)
	_ = peer.Close()
	select {
	case _, ok := <-out:
		for ok {
			_, ok = <-out
		}
	case <-time.After(5 * time.Second):
		stop.Store(true)
		wg.Wait()
		t.Fatal("the output channel did not close after the runtime closed its end")
	}
	outClosed.Store(true)
	time.Sleep(5 * time.Millisecond)
	stop.Store(true)
	wg.Wait()

	if n := violations.Load(); n != 0 {
		t.Fatalf("%d call(s) missed the ended state; first: %v", n, firstViolation.Load())
	}
	if rt.ServesNextSession() {
		t.Fatal("ServesNextSession() = true once the race settled, want false")
	}
}
