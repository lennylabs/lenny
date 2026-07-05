// SPDX-License-Identifier: MIT

//go:build load_local

// Package directusage_teardown exercises the §11.2 direct-mode usage
// poll loop's session bind/teardown churn against the running loop, at the
// tier-7a load_local flake budget with the race detector enabled.
//
// The poll loop (proposal 0024 S9, cmd/lenny-gateway/direct_usage.go) is a
// single context-tied goroutine that on each tick snapshots the replica's
// live pod bindings from the session-scoped podsession.Registry
// (Snapshot), then re-reads each session through the registry (Get) before
// pulling its adapter, so it stops pulling a session the moment its binding
// is removed at teardown and never dials a closed adapter. Its two
// concurrency invariants:
//
//   - The registry Snapshot/Get access must be -race clean while sessions
//     are concurrently bound (Put) and torn down (Remove) from the bind
//     lifecycle.
//   - The loop goroutine's only exit is ctx.Done, so it exits promptly and
//     leaks no goroutine when the gateway watchdog context is cancelled,
//     even while bind/teardown churn is in flight.
//
// The in-package tests (cmd/lenny-gateway/direct_usage_test.go) pin the
// single-threaded teardown and context-cancel-exit behavior; this package
// supplies the flake-budget concurrency stage the proposal defers to tier
// 7a: it churns bind/teardown from many goroutines against the running
// loop, then cancels and asserts the goroutine exits with a goroutine-count
// delta around zero. Run to a stress budget with:
//
//	lenny-test stress --test TestDirectUsageLoopTeardownChurnIsLeakFree_spec_11_2 --runs 50
//
// so a data race between the poll and the teardown, or a leaked loop
// goroutine, surfaces across many runs.
//
// The loop's Snapshot/Get teardown protocol is reproduced here against the
// real podsession.Registry rather than importing the unexported cmd
// package. The pull step is a no-op read of the re-read binding, exercising
// the exact registry access pattern (Snapshot then per-session Get) the
// production loop runs, so a registry race under churn is caught. The
// context-tied goroutine exit mirrors directUsageLoop.Run.
//
// TESTING.md §12.7.a regression scenarios.
package directusage_teardown

import (
	"context"
	"fmt"
	"math/rand"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
)

// pollLoop mirrors the production directUsageLoop's registry-keyed poll
// protocol (cmd/lenny-gateway/direct_usage.go): a context-tied goroutine
// that snapshots the registry each tick and re-reads each session through
// Get before "pulling" it, so it stops at teardown and never touches a
// removed binding. The pull is a no-op read of the re-read binding; the
// point under test is the concurrent Snapshot/Get access under bind/teardown
// churn and the ctx.Done exit, which are the loop's real concurrency
// surfaces. pulls counts how many sessions the loop observed as still bound
// at pull time, used only to prove the loop did real work.
type pollLoop struct {
	registry *podsession.Registry
	interval time.Duration
	pulls    atomic.Int64
}

// run drives the poll until ctx is cancelled, exactly as
// directUsageLoop.Run does: the only exit is ctx.Done.
func (l *pollLoop) run(ctx context.Context) {
	ticker := time.NewTicker(l.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			l.pollOnce()
		}
	}
}

// pollOnce reproduces directUsageLoop.pollOnce + pullSession: snapshot the
// registry, then re-read each session through Get. A session removed between
// the snapshot and the Get is skipped (Get returns not-ok), which is what
// makes the loop stop at teardown and never dial a closed adapter.
func (l *pollLoop) pollOnce() {
	for _, b := range l.registry.Snapshot() {
		got, ok := l.registry.Get(b.SessionID)
		if !ok || got == nil {
			// Torn down between the snapshot and this re-read: skip, the exact
			// teardown-safe behavior the production loop relies on.
			continue
		}
		// A live pull would dial got.Adapter here; the no-op read of the
		// re-read binding exercises the same registry access under churn.
		_ = got.PodIP
		l.pulls.Add(1)
	}
}

// spec: §11.2 line 42 (direct-mode usage poll loop, registry-keyed teardown),
// §4.7 (ReportUsage pull over the session-scoped registry). This test exercises
// the loop's concurrent Snapshot/Get teardown safety and its context-tied
// goroutine exit, which are §11.2/§4.7 loop-concurrency surfaces; the §6.2 line
// 253 idle-clock reset is a distinct behavior driven by the idle stamper and is
// pinned by the in-package TestDirectUsageLoopHungPodIdleTerminates_spec_6_2_253
// and TestDirectUsageLoopFailedPullDoesNotStamp_spec_6_2_253, so this test does
// not cite §6.2.
// diagnosis: a failure means the §11.2 direct-mode poll loop raced the session
// bind/teardown lifecycle on the shared podsession.Registry, or leaked its poll
// goroutine when the gateway watchdog context was cancelled under churn, so a
// wedged replica would accumulate poll goroutines and/or corrupt the live-binding
// set (proposal 0024 S9 registry-keyed teardown / context-tied exit not race-safe
// under the flake budget).
func TestDirectUsageLoopTeardownChurnIsLeakFree_spec_11_2(t *testing.T) {
	// Not parallel: the goroutine-count delta reads runtime.NumGoroutine,
	// which other parallel subtests would perturb.
	registry := podsession.NewRegistry()

	// A tight tick so the poll genuinely interleaves with the churn within
	// the tier-7a per-scenario budget.
	loop := &pollLoop{registry: registry, interval: 200 * time.Microsecond}

	// Settle any lazily-started runtime goroutines, then baseline.
	runtime.GC()
	time.Sleep(20 * time.Millisecond)
	baseline := runtime.NumGoroutine()

	ctx, cancel := context.WithCancel(context.Background())
	loopDone := make(chan struct{})
	go func() {
		loop.run(ctx)
		close(loopDone)
	}()

	// Churn: many goroutines repeatedly bind and tear down sessions against
	// the shared registry while the loop polls it. Each worker owns a
	// disjoint session-id space so a teardown never races another worker's
	// bind of the same id, isolating the loop-vs-lifecycle race from a
	// lifecycle-vs-lifecycle one.
	const (
		workers    = 16
		perWorker  = 400
		liveWindow = 8
	)
	var churnWG sync.WaitGroup
	for w := 0; w < workers; w++ {
		churnWG.Add(1)
		go func(w int) {
			defer churnWG.Done()
			rng := rand.New(rand.NewSource(int64(w) + 1))
			live := make([]string, 0, liveWindow)
			for i := 0; i < perWorker; i++ {
				sid := fmt.Sprintf("w%02d-s%04d", w, i)
				registry.Put(&podsession.BindResult{
					SessionID: sid,
					TenantID:  "acme",
					PodIP:     "10.0.0.1",
				})
				live = append(live, sid)
				// Tear down an older binding once the live window fills, so at
				// any instant the loop sees a mix of just-bound and
				// about-to-be-removed sessions.
				if len(live) > liveWindow {
					victim := live[rng.Intn(len(live))]
					registry.Remove(victim)
					// Compact: drop the removed id from the live slice.
					out := live[:0]
					for _, s := range live {
						if s != victim {
							out = append(out, s)
						}
					}
					live = out
				}
			}
			// Drain: tear down every remaining binding this worker owns.
			for _, sid := range live {
				registry.Remove(sid)
			}
		}(w)
	}
	churnWG.Wait()

	// Every binding is torn down; the registry must be empty. This proves the
	// churn and the concurrent poll left the live-binding set consistent (no
	// lost Remove, no resurrected binding).
	if n := registry.Len(); n != 0 {
		t.Errorf("registry not empty after full teardown: %d bindings remain (a Remove was lost under churn)", n)
	}

	// The loop must have done real work against the churning registry, or the
	// test proves nothing about the concurrent access.
	if l := loop.pulls.Load(); l == 0 {
		t.Error("poll loop observed no bound session under churn; the race window was not exercised")
	}

	// Cancel the watchdog context; the loop's only exit is ctx.Done, so it
	// must return promptly and leak no goroutine.
	cancel()
	select {
	case <-loopDone:
	case <-time.After(3 * time.Second):
		t.Fatal("poll loop did not exit within 3s of context cancel (goroutine leak)")
	}

	// Goroutine-count delta around zero: the churn goroutines have joined and
	// the loop goroutine has exited, so the count must return to the baseline
	// (allowing a small slack for runtime/GC bookkeeping). A persistent delta
	// is a leaked poll or churn goroutine.
	assertNoGoroutineLeak(t, baseline)
}

// assertNoGoroutineLeak polls the goroutine count down toward baseline,
// tolerating a small slack for runtime bookkeeping and giving joined
// goroutines a moment to be reaped by the scheduler. A delta that never
// closes is a leak.
func assertNoGoroutineLeak(t *testing.T, baseline int) {
	t.Helper()
	const slack = 3
	deadline := time.Now().Add(2 * time.Second)
	var last int
	for time.Now().Before(deadline) {
		runtime.GC()
		last = runtime.NumGoroutine()
		if last-baseline <= slack {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("goroutine leak: %d goroutines after teardown vs %d baseline (delta=%d > %d)", last, baseline, last-baseline, slack)
}
