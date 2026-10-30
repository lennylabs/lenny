// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local coverage for the §11.2 enforcer's deny flag during a
// budget termination. A randomized interleaving runs a terminating call (the
// episode fan-out's TerminateSession, or a Record whose extension seam
// resolves Terminal) against fan-out RaiseBudget calls on the same session.
// Once the terminating call has returned, Allow must never report true for
// the session, whatever order the raises landed in.
//
// spec: §8.6, §11.2
package tier7a_load_local_test

import (
	"context"
	"math/rand"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/session/sessionbudget"
)

// nopBudgetTerminator stands in for the gateway's force-terminate path; the
// test reads the deny flag through Allow rather than the terminal pipeline.
type nopBudgetTerminator struct{}

func (nopBudgetTerminator) TerminateSession(_ /*sessionID*/, _ /*reason*/ string) {}

// yieldJitter yields the scheduler a random number of times so the goroutines of
// one round interleave differently from round to round.
func yieldJitter(rng *rand.Rand) {
	for n := rng.Intn(4); n > 0; n-- {
		runtime.Gosched()
	}
}

// terminateDenyRound runs one interleaving and reports whether Allow read true
// after the terminating call returned.
func terminateDenyRound(rng *rand.Rand, viaRecord bool, raisers int) (violated bool) {
	const id = "s1"
	seam := func(_, _ context.Context, _, _ string, _, _ int64) sessionbudget.Outcome {
		return sessionbudget.Terminal
	}
	e := sessionbudget.New(nopBudgetTerminator{}, seam, nil)
	bg := context.Background()
	// Seed the counter below its budget so the session is admitted and a
	// RaiseBudget has an entry to act on.
	e.Record(bg, bg, "acme", id, 100, 10)

	seeds := make([]int64, raisers+2)
	for i := range seeds {
		seeds[i] = rng.Int63()
	}
	var terminated, stop atomic.Bool
	var bad atomic.Bool
	var wg sync.WaitGroup
	start := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		r := rand.New(rand.NewSource(seeds[0]))
		<-start
		yieldJitter(r)
		if viaRecord {
			e.Record(bg, bg, "acme", id, 100, 200) // crosses the budget; the seam resolves Terminal
		} else {
			e.TerminateSession(id)
		}
		terminated.Store(true)
	}()
	for w := 0; w < raisers; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			r := rand.New(rand.NewSource(seed))
			<-start
			for k := 0; k < 3; k++ {
				yieldJitter(r)
				e.RaiseBudget(id, 1000)
			}
		}(seeds[w+1])
	}
	observerDone := make(chan struct{})
	go func() {
		defer close(observerDone)
		<-start
		for !stop.Load() {
			// Read the flag before Allow so a true Allow is attributed only
			// to a read that started after the terminating call returned.
			after := terminated.Load()
			if e.Allow(id) && after {
				bad.Store(true)
			}
			runtime.Gosched()
		}
	}()
	close(start)
	wg.Wait()
	stop.Store(true)
	<-observerDone
	if e.Allow(id) {
		bad.Store(true)
	}
	return bad.Load()
}

// spec: §8.6, §11.2
// diagnosis: a fan-out RaiseBudget cleared the deny flag of a session that
// TerminateSession or Record's terminal branch had already committed to
// termination, so Allow admitted a further proxied request on a session that
// is ending. RaiseBudget must leave deny set while the counter's terminating
// flag is set, and the flag must be set in the same critical section as the
// deny it protects.
func TestEnforcerTerminateNeverReopensAllow_spec_11_2(t *testing.T) {
	seed := time.Now().UnixNano()
	t.Logf("seed %d", seed)
	rng := rand.New(rand.NewSource(seed))
	deadline := time.Now().Add(5 * time.Second)
	rounds := 0
	for rounds < 2000 && time.Now().Before(deadline) {
		viaRecord := rng.Intn(2) == 0
		raisers := 1 + rng.Intn(4)
		if terminateDenyRound(rng, viaRecord, raisers) {
			t.Fatalf("round %d (seed %d, viaRecord=%v, raisers=%d): Allow true after the terminating call returned",
				rounds, seed, viaRecord, raisers)
		}
		rounds++
	}
	if rounds < 100 {
		t.Fatalf("only %d rounds ran, want at least 100", rounds)
	}
}
