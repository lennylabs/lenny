// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local concurrency coverage of the supervised runtime: the
// launch mutex and permit, the one outstanding CH-SUPERVISE request, the
// once-per-generation handling of a runtime end under the registry critical
// section, the hold timeout's close, the link close against an outstanding
// request, and the pre-READY exit ownership, each under randomized
// interleavings with the race detector and a goroutine-leak check.
//
// spec: §4.7.10 (Runtime supervisor, Launch; Runtime supervisor, Runtime
// exits; Runtime supervisor, Crash), §4.7.1 (The registry critical section),
// §28.5.3 (CH-SUPERVISE), §4.7.9 (step 4).
package adapter

import (
	"context"
	"errors"
	"math/rand"
	"net"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// checkNoGoroutineLeak fails the test when goroutines started during it
// outlive it.
func checkNoGoroutineLeak(t *testing.T) {
	t.Helper()
	base := runtime.NumGoroutine()
	t.Cleanup(func() {
		deadline := time.Now().Add(5 * time.Second)
		for runtime.NumGoroutine() > base+2 {
			if time.Now().After(deadline) {
				buf := make([]byte, 1<<16)
				n := runtime.Stack(buf, true)
				t.Errorf("goroutines leaked: %d now, %d before\n%s", runtime.NumGoroutine(), base, buf[:n])
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	})
}

// jitter sleeps a random interval. It draws from the package-level source,
// which is safe for the concurrent callers it has; r only names the round's
// seed for a reader.
func jitter(_ *rand.Rand) {
	time.Sleep(time.Duration(rand.Intn(300)) * time.Microsecond)
}

// spec: 4.7.10 (Runtime supervisor, Launch)
// diagnosis: concurrent first starts sent more than one launch, so two
// runtime processes ran for one generation.
func TestSupervisedLaunchAgainstLaunchRace_spec_4_7_10(t *testing.T) {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	for round := 0; round < 20; round++ {
		func() {
			f := newSupervisedFixture(t, LifetimeKeep, LaunchSession, nil)
			var wg sync.WaitGroup
			for i := 0; i < 6; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					jitter(r)
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					if err := f.rt.Start(ctx, "s"); err != nil {
						t.Errorf("round %d: Start: %v", round, err)
					}
				}()
			}
			wg.Wait()
			if n := f.sup.count(superviseLaunch); n != 1 {
				t.Fatalf("round %d: %d launches, want 1", round, n)
			}
		}()
	}
}

// spec: 28.5.3 (CH-SUPERVISE, Preconditions)
// diagnosis: a ping was written while an end was outstanding, so the end's
// reply matched no request and the link failed.
func TestSupervisedEndAgainstPingRace_spec_28_5_3(t *testing.T) {
	checkNoGoroutineLeak(t)
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	f := newSupervisedFixture(t, LifetimeRestart, LaunchSession, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.link.RunPing(ctx, 50*time.Microsecond, time.Second)
	}()
	for i := 0; i < 30; i++ {
		jitter(r)
		start, c := context.WithTimeout(context.Background(), 5*time.Second)
		if err := f.rt.Start(start, "s"); err != nil {
			c()
			t.Fatalf("iteration %d: Start: %v", i, err)
		}
		c()
		if err := f.rt.EndAndSweep(context.Background()); err != nil {
			t.Fatalf("iteration %d: end: %v", i, err)
		}
		if err := f.rt.RebindListeners(); err != nil {
			t.Fatal(err)
		}
	}
	cancel()
	<-done
	if f.link.Failed() {
		t.Fatal("the link failed while ping raced the ends")
	}
	_ = f.rt.CloseListener()
}

// spec: 4.7.10 (Runtime supervisor, Runtime exits)
// diagnosis: an exited report and the ended reply of one generation were both
// handled, or the exited was handled under another row than the end's.
func TestSupervisedExitedAgainstEndRace_spec_4_7_10(t *testing.T) {
	for round := 0; round < 30; round++ {
		e := newExitsFixture(t, LifetimeRestart, LaunchSession)
		e.hold("a")
		e.deregister("a")
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			e.sup.writeLine(`{"type":"exited","gen":1,"signal":9}`)
		}()
		go func() {
			defer wg.Done()
			e.rt.SetEndDisposition(EndScrub)
			_ = e.rt.EndProcess(context.Background(), 0)
		}()
		wg.Wait()
		time.Sleep(5 * time.Millisecond)
		nl, cause := e.rt.NotLive()
		// Whichever event the lock ordered first selects the row: the
		// exited before the disposition was set takes none (runtime exit);
		// afterwards it takes endScrub, which sets nothing.
		if nl && cause != CauseRuntimeExit {
			t.Fatalf("round %d: cause = %s", round, cause)
		}
	}
}

// spec: 4.7.10 (Runtime supervisor, Crash), 4.7.1 (The registry critical
// section)
// diagnosis: a crash failed a session whose entry Shutdown had already
// deregistered, or did not fail one whose entry still stood when the end was
// handled.
func TestSupervisedExitedAgainstDeregistrationRace_spec_4_7_10(t *testing.T) {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	kinds := []struct {
		lifetime   RuntimeLifetime
		concurrent bool
	}{{LifetimeKeep, false}, {LifetimeKeep, true}, {LifetimeRestart, false}, {LifetimeRestart, true}}
	for _, k := range kinds {
		for round := 0; round < 15; round++ {
			e := newExitsFixture(t, k.lifetime, LaunchSession)
			ids := []string{"a"}
			if k.concurrent {
				ids = append(ids, "b")
			}
			e.hold(ids...)
			e.s.mu.Lock()
			entry := e.s.slots["a"]
			e.s.mu.Unlock()
			var crashedAtDereg atomic.Bool
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				jitter(r)
				e.s.mu.Lock()
				crashedAtDereg.Store(entry.crash != nil && isClosed(entry.crash))
				delete(e.s.slots, "a")
				e.s.mu.Unlock()
			}()
			go func() {
				defer wg.Done()
				jitter(r)
				e.sup.writeLine(`{"type":"exited","gen":1,"signal":9}`)
			}()
			wg.Wait()
			requireNotLive(t, e.rt, CauseRuntimeExit)
			e.s.mu.Lock()
			final := entry.crash != nil && isClosed(entry.crash)
			e.s.mu.Unlock()
			if final != crashedAtDereg.Load() {
				t.Fatalf("%s concurrent=%v round %d: crashed=%v but crashed at deregistration=%v",
					k.lifetime, k.concurrent, round, final, crashedAtDereg.Load())
			}
			if k.concurrent && !e.crashed("b") {
				t.Fatalf("round %d: the other session the process holds did not fail", round)
			}
		}
	}
}

// spec: 4.7.1 (The registry critical section), 4.7.10 (Runtime supervisor,
// Crash; Runtime supervisor, Liveness)
// diagnosis: a start whose open sequence ran after the generation's end was
// handled was still recorded on that generation, so it neither failed as a
// not-live start nor had its stream failed with runtime_crash.
func TestSupervisedStartRecordAgainstExitedRace_spec_4_7_10(t *testing.T) {
	checkNoGoroutineLeak(t)
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	var recorded, refused int
	for _, lifetime := range []RuntimeLifetime{LifetimeKeep, LifetimeRestart} {
		for round := 0; round < 20; round++ {
			e := newExitsFixture(t, lifetime, LaunchSession)
			claim := claimForRecordTest(t, e.s, "b")
			if err := e.s.Runtime.Start(startCtx(t, 5*time.Second), "b"); err != nil {
				t.Fatalf("Start: %v", err)
			}
			var confirmed bool
			var openErr error
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				jitter(r)
				confirmed, openErr = e.s.openRuntimeSession(startCtx(t, 5*time.Second), "b", claim, manifestInputs{}, false)
			}()
			go func() {
				defer wg.Done()
				jitter(r)
				e.sup.writeLine(`{"type":"exited","gen":1,"signal":9}`)
			}()
			wg.Wait()
			requireNotLive(t, e.rt, CauseRuntimeExit)
			switch {
			case confirmed && openErr == nil:
				recorded++
				waitFor(t, "the recorded session to fail with runtime_crash", func() bool { return e.crashed("b") })
			case !confirmed && errors.Is(openErr, errRuntimeNotLive):
				refused++
				if recordedOn(e.s, "b") || e.crashed("b") {
					t.Fatalf("%s round %d: a refused start was recorded or crashed", lifetime, round)
				}
			default:
				t.Fatalf("%s round %d: openRuntimeSession = %v, %v; want recorded or not-live", lifetime, round, confirmed, openErr)
			}
			_ = e.rt.CloseListener()
			e.sup.close()
		}
	}
	t.Logf("recorded then crashed: %d, refused as not live: %d", recorded, refused)
}

// spec: 4.7.10 (Runtime supervisor, Runtime exits), 10.1 (Hold state timeout)
// diagnosis: once the hold timeout's close ran, the end did not take the
// holdTimeout row: a session failed with runtime_crash, or the not-live
// cause was not no-connection.
func TestSupervisedExitedAgainstHoldTimeoutRace_spec_4_7_10(t *testing.T) {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	for round := 0; round < 30; round++ {
		e := newExitsFixture(t, LifetimeKeep, LaunchSession)
		e.hold("a")
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); jitter(r); e.s.endRuntimeForHoldTimeout() }()
		go func() {
			defer wg.Done()
			jitter(r)
			if rand.Intn(2) == 0 {
				e.sup.writeLine(`{"type":"exited","gen":1,"signal":9}`)
			} else {
				e.sup.killProcesses()
			}
		}()
		wg.Wait()
		waitFor(t, "not live", func() bool { nl, _ := e.rt.NotLive(); return nl })
		_, cause := e.rt.NotLive()
		switch cause {
		case CauseNoConnection:
			if e.crashed("a") {
				t.Fatalf("round %d: the holdTimeout row failed a session", round)
			}
		case CauseRuntimeExit:
			if !e.crashed("a") {
				t.Fatalf("round %d: the none row did not fail the held session", round)
			}
		default:
			t.Fatalf("round %d: cause %s", round, cause)
		}
	}
}

// spec: 28.5.3 (CH-SUPERVISE, Degradation)
// diagnosis: closing the link while a request was outstanding hung the
// request or leaked a goroutine.
func TestSupervisorLinkCloseAgainstOutstandingRequest_spec_28_5_3(t *testing.T) {
	checkNoGoroutineLeak(t)
	for round := 0; round < 20; round++ {
		f := newSupervisedFixture(t, LifetimeKeep, LaunchSession, nil)
		f.sup.react(superviseSweep, fakeReaction{delay: 2 * time.Millisecond})
		errc := make(chan error, 1)
		go func() { errc <- f.rt.Sweep(context.Background()) }()
		time.Sleep(time.Duration(round%3) * time.Millisecond)
		_ = f.rt.CloseListener()
		select {
		case err := <-errc:
			if err != nil && !errors.Is(err, ErrSuperviseLinkFailed) {
				t.Fatalf("round %d: Sweep = %v", round, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("round %d: the outstanding request hung after the close", round)
		}
		f.sup.close()
	}
}

// spec: 4.7.9 (step 4), 4.7.10 (Runtime supervisor, Launch; Runtime
// supervisor, Liveness), 6.1 (Adapter SIGTERM behavior during sdk_connecting)
// diagnosis: a signal racing a pre-READY gate or a service-mode cause after
// READY produced zero or two exits, or an exit status other than the owning
// path's.
func TestPreReadySignalAgainstGatesRace_spec_4_7_10(t *testing.T) {
	checkNoGoroutineLeak(t)
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	for round := 0; round < 200; round++ {
		var mu sync.Mutex
		var exits []int
		var ends atomic.Int32
		sig := make(chan os.Signal, 1)
		stop := make(chan struct{})
		var stopOnce sync.Once
		stopFn := func() { stopOnce.Do(func() { close(stop) }) }
		var observe func(LivenessCause)
		gateFails := rand.Intn(3) == 0
		cfg := PreReadyConfig{
			Signals:     sig,
			ServiceMode: true,
			AwaitSupervisor: func(ctx context.Context) error {
				jitter(r)
				if gateFails {
					return errors.New("gate failed")
				}
				return nil
			},
			WarmUp:   func(ctx context.Context) error { jitter(r); return ctx.Err() },
			OnSignal: func() { ends.Add(1); stopFn() },
			Listen:   func() (net.Listener, error) { return &fakeListener{}, nil },
			Serve: func(net.Listener) error {
				go func() { jitter(r); observe(CauseRuntimeExit) }()
				<-stop
				return nil
			},
			Stop:     stopFn,
			Exit:     func(c int) { mu.Lock(); exits = append(exits, c); mu.Unlock() },
			Liveness: func(fn func(LivenessCause)) { observe = fn },
			Logf:     func(string, ...any) {},
		}
		go func() { jitter(r); sig <- syscall.SIGTERM }()
		done := make(chan struct{})
		go func() { defer close(done); RunPreReady(context.Background(), cfg) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("round %d: RunPreReady did not return", round)
		}
		mu.Lock()
		got := append([]int(nil), exits...)
		mu.Unlock()
		if len(got) != 1 {
			t.Fatalf("round %d: exits = %v, want exactly one", round, got)
		}
		// The signal path exits 0 after its end; every other owner exits 1
		// and sends no end.
		if (got[0] == 0) != (ends.Load() == 1) {
			t.Fatalf("round %d: exit %d with %d ends", round, got[0], ends.Load())
		}
	}
}

// spec: 4.7.10 (Runtime supervisor, Launch), 5.2 (Supervised scrub sequence)
// diagnosis: on a restart pod a start racing the whole-pod scrub launched a
// process before the boundary's end was attested, so a process of the
// earlier generation could run beside the new one.
func TestSupervisedLaunchAfterScrubOrdering_spec_4_7_10(t *testing.T) {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	for round := 0; round < 30; round++ {
		f := newSupervisedFixture(t, LifetimeRestart, LaunchSession, nil)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := f.rt.Start(ctx, "a"); err != nil {
			cancel()
			t.Fatal(err)
		}
		cancel()
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			jitter(r)
			_ = f.rt.EndAndSweep(context.Background())
			_ = f.rt.RebindListeners()
		}()
		go func() {
			defer wg.Done()
			jitter(r)
			c, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = f.rt.Start(c, "b")
		}()
		wg.Wait()
		f.sup.mu.Lock()
		violation := f.sup.launchViolation
		f.sup.mu.Unlock()
		if violation {
			t.Fatalf("round %d: a launch was sent before the earlier generation's end was attested", round)
		}
	}
}

// spec: 4.7.9 (step 4), 4.7.10 (Runtime supervisor, Launch; Runtime
// supervisor, End), 6.1 (Adapter SIGTERM behavior during sdk_connecting)
// diagnosis: a SIGTERM racing the return of the supervisor gate or the
// warm-up launch left a launched process that no end reached: either the
// warm-up launch was sent after ExitOnSignal decided nothing was launched,
// or ExitOnSignal's end reached the supervisor before the launch it targets.
func TestPreReadySignalAgainstWarmUpLaunchRace_spec_6_1(t *testing.T) {
	checkNoGoroutineLeak(t)
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	for _, mode := range []LaunchMode{LaunchService, LaunchPreconnectSDKWarm} {
		for round := 0; round < 60; round++ {
			t.Run(string(mode), func(t *testing.T) {
				runSignalAgainstWarmUpRound(t, r, mode, round)
			})
		}
	}
}

// runSignalAgainstWarmUpRound runs one RunPreReady on a supervised runtime
// with a jittered SIGTERM, and asserts that every launch the supervisor
// received was followed by an end.
func runSignalAgainstWarmUpRound(t *testing.T, r *rand.Rand, mode LaunchMode, round int) {
	f := newSupervisedFixture(t, LifetimeKeep, mode, nil)
	if round%2 == 0 {
		// Widen the window between the generation increment and the
		// launch's reply.
		f.sup.react(superviseLaunch, fakeReaction{delay: 5 * time.Millisecond})
	}
	s := New("test")
	s.ManifestDir = t.TempDir()
	s.UseSupervisedRuntime(f.rt)
	sig := make(chan os.Signal, 1)
	stop := make(chan struct{})
	var stopOnce sync.Once
	stopFn := func() { stopOnce.Do(func() { close(stop) }) }
	warm := f.rt.WarmUp
	if mode == LaunchPreconnectSDKWarm {
		warm = s.PreConnect
	}
	cfg := PreReadyConfig{
		Signals:         sig,
		ServiceMode:     mode == LaunchService,
		AwaitSupervisor: func(context.Context) error { jitter(r); return nil },
		WarmUp: func(ctx context.Context) error {
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			return warm(ctx)
		},
		OnSignal: func() { s.ExitOnSignal(2*time.Second, nil); stopFn() },
		Listen:   func() (net.Listener, error) { return &fakeListener{}, nil },
		Serve:    func(net.Listener) error { <-stop; return nil },
		Stop:     stopFn,
		Exit:     func(int) {},
		Liveness: f.rt.SetOnNotLive,
		Logf:     func(string, ...any) {},
	}
	go func() { jitter(r); sig <- syscall.SIGTERM }()
	done := make(chan struct{})
	go func() { defer close(done); RunPreReady(context.Background(), cfg) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("round %d: RunPreReady did not return", round)
	}
	f.sup.mu.Lock()
	live, violation := f.sup.liveGen, f.sup.launchViolation
	f.sup.mu.Unlock()
	if live != 0 {
		t.Fatalf("round %d: generation %d was launched and no end reached it (requests %v)", round, live, f.sup.sent())
	}
	if violation {
		t.Fatalf("round %d: a launch was sent while an earlier generation was live", round)
	}
}

// spec: 4.7.10 (Runtime supervisor, Runtime exits; Runtime supervisor,
// Signals)
// diagnosis: a warm-up launch in flight at the pod's termination reset the
// deleting disposition to none after the termination set it, so the
// adapter's own end during termination took the none row and set the
// runtime_exit not-live cause.
func TestServiceTerminationAgainstWarmUpLaunchRace_spec_4_7_10(t *testing.T) {
	checkNoGoroutineLeak(t)
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	for round := 0; round < 60; round++ {
		func() {
			f := newSupervisedFixture(t, LifetimeKeep, LaunchService, nil)
			if round%2 == 0 {
				f.sup.react(superviseLaunch, fakeReaction{delay: time.Millisecond})
			}
			s := New("test")
			s.UseSupervisedRuntime(f.rt)
			// The signal handler cancels the warm-up context before it runs
			// ExitOnSignal, so only a launch already past its context check
			// is in flight at the termination.
			warmCtx, cancelWarm := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancelWarm()
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				jitter(r)
				_ = f.rt.WarmUp(warmCtx)
			}()
			go func() {
				defer wg.Done()
				jitter(r)
				cancelWarm()
				s.ExitOnSignal(2*time.Second, nil)
			}()
			wg.Wait()
			if nl, cause := f.rt.NotLive(); nl && cause == CauseRuntimeExit {
				t.Fatalf("round %d: not-live cause %s set by the termination's own end (requests %v)", round, cause, f.sup.sent())
			}
			if d := f.rt.EndDisposition(); d != EndDeleting {
				t.Fatalf("round %d: disposition = %s, want deleting", round, d)
			}
			_ = f.link.Close()
		}()
	}
}
