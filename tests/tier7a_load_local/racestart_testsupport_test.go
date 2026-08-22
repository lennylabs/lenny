// SPDX-License-Identifier: MIT

//go:build load_local

// Rendezvous support shared by the tier-7a cases that pin a decision two
// concurrent adapter calls take on one pod.
//
// A case that launches two goroutines back to back does not drive an
// interleaving; the fully serialized schedule is a legal and likely
// outcome, and a check-then-act implementation passes on it. These cases
// exist to fail on the schedule where both calls are inside the same
// window at once, so each caller announces that it has reached the call
// site and blocks until every participant has, and the case repeats the
// whole fixture so both orderings out of that window are reached.
package tier7a_load_local_test

import (
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

// raceAttempts is how many times a case rebuilds its fixture and re-runs
// the race. One attempt establishes only whichever schedule the runtime
// picked; repeating over fresh fixtures reaches both orderings out of the
// rendezvous. The number is a budget rather than a guarantee: each of
// these cases also carries a stress budget for the long sweep.
const raceAttempts = 40

// raceStart releases a fixed number of goroutines from one point so their
// critical sections overlap. Each participant calls arrive at the call
// site it is racing; the case calls release once, and every participant
// returns from arrive at that instant.
//
// The release is a spin rather than a channel close. A participant parked
// on a channel is woken by the scheduler, and the wake-up skew between two
// participants is wide enough that the window this rendezvous exists to
// enter is usually already closed by the time the second one runs. A
// participant spinning on an already-running thread leaves the barrier
// within nanoseconds of the store, which is what puts both callers inside
// the same short critical section.
type raceStart struct {
	arrived  atomic.Int64
	released atomic.Bool
	n        int
}

// newRaceStart returns a rendezvous for n participants.
func newRaceStart(n int) *raceStart {
	return &raceStart{n: n}
}

// arrive reports that this participant has reached the call site and
// spins until every other participant has reached its own.
func (r *raceStart) arrive() {
	// The participant holds its own thread from here on, so the release
	// is not waiting on a scheduler wake-up and the racing call keeps that
	// thread. The thread is not handed back: it is retired when this
	// goroutine ends, which is the end of the attempt.
	runtime.LockOSThread()
	r.arrived.Add(1)
	// A tight spin is what keeps the departure skew short. The yield after
	// it bounds the case on a host with fewer processors than the barrier
	// has participants, where an unyielding spin would keep the releasing
	// goroutine off the processor for good.
	for i := 0; !r.released.Load(); i++ {
		if i > 1<<20 {
			runtime.Gosched()
		}
	}
}

// release waits for every participant to arrive and then unblocks them
// together. It fails the case rather than hanging the suite when a
// participant never arrives.
func (r *raceStart) release(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for r.arrived.Load() < int64(r.n) {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the racing calls to reach the rendezvous")
		}
		runtime.Gosched()
	}
	r.released.Store(true)
}
