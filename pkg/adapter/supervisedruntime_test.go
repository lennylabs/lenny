// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter/scrub"
)

func startCtx(t *testing.T, d time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	t.Cleanup(cancel)
	return ctx
}

func requireNotLive(t *testing.T, rt *SupervisedRuntime, want LivenessCause) {
	t.Helper()
	waitFor(t, "the not-live state", func() bool { nl, _ := rt.NotLive(); return nl })
	if _, got := rt.NotLive(); got != want {
		t.Fatalf("not-live cause = %s, want %s", got, want)
	}
}

func requireLive(t *testing.T, rt *SupervisedRuntime) {
	t.Helper()
	if nl, cause := rt.NotLive(); nl {
		t.Fatalf("not-live state set (%s), want clear", cause)
	}
}

// spec: 4.7.10 (Runtime supervisor, Launch), 4.7.9 (step 7)
func TestSupervisedRuntimeConcurrentFirstStartsSendOneLaunch_spec_4_7_10(t *testing.T) {
	f := newSupervisedFixture(t, LifetimeKeep, LaunchSession, nil)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- f.rt.Start(startCtx(t, 5*time.Second), "s")
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
	}
	if n := f.sup.count(superviseLaunch); n != 1 {
		t.Fatalf("launch sent %d times, want 1", n)
	}
	if f.rt.RuntimeGeneration() != 1 {
		t.Fatalf("generation = %d, want 1", f.rt.RuntimeGeneration())
	}
	if got := f.sup.request(superviseLaunch, 0)["gen"].(float64); got != 1 {
		t.Fatalf("launch gen = %v, want 1", got)
	}
}

// cancelLaunchingStart runs a Start under ctx, waits for its launch, cancels
// it, and requires the cancellation back.
func cancelLaunchingStart(t *testing.T, f *supervisedFixture, ctx context.Context, cancel context.CancelFunc) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- f.rt.Start(ctx, "a") }()
	waitFor(t, "the launch", func() bool { return f.sup.count(superviseLaunch) == 1 })
	time.Sleep(20 * time.Millisecond)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Start = %v, want context.Canceled", err)
	}
}

// A launched process stays held to the deadline of the start that launched
// it after that start is cancelled: a process that has not connected when
// the deadline passes is the no-connection cause, and a process that
// connected before it is not.
// spec: 4.7.10 (Runtime supervisor, Liveness; Runtime supervisor, Launch),
// 5.2 (Runtime not live)
func TestSupervisedRuntimeCancelledLaunchingStartKeepsConnectDeadline_spec_4_7_10(t *testing.T) {
	t.Run("an unconnected process is not live once the original deadline passes", func(t *testing.T) {
		f := newSupervisedFixture(t, LifetimeKeep, LaunchSession, nil)
		f.sup.react(superviseLaunch, fakeReaction{noProcess: true})
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		deadline, _ := ctx.Deadline()
		cancelLaunchingStart(t, f, ctx, cancel)
		requireLive(t, f.rt)
		requireNotLive(t, f.rt, CauseNoConnection)
		if time.Now().Before(deadline) {
			t.Fatal("no-connection cause set before the launching start's deadline")
		}
		if err := f.rt.Start(startCtx(t, time.Second), "b"); !errors.Is(err, errRuntimeNotLive) {
			t.Fatalf("Start after the deadline = %v, want errRuntimeNotLive", err)
		}
		if n := f.sup.count(superviseLaunch); n != 1 {
			t.Fatalf("launches = %d, want 1", n)
		}
	})
	t.Run("a joining start waiting when the original deadline passes", func(t *testing.T) {
		f := newSupervisedFixture(t, LifetimeKeep, LaunchSession, nil)
		f.sup.react(superviseLaunch, fakeReaction{noProcess: true})
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		cancelLaunchingStart(t, f, ctx, cancel)
		if err := f.rt.Start(startCtx(t, time.Second), "b"); err == nil {
			t.Fatal("joining Start succeeded with no connection")
		}
		requireNotLive(t, f.rt, CauseNoConnection)
	})
	t.Run("a start without a deadline holds the process to the accept bound", func(t *testing.T) {
		f := newSupervisedFixture(t, LifetimeKeep, LaunchSession, nil)
		f.sup.react(superviseLaunch, fakeReaction{noProcess: true})
		f.rt.mu.Lock()
		tr := f.rt.transport
		f.rt.mu.Unlock()
		tr.mu.Lock()
		tr.AcceptTimeout = 300 * time.Millisecond
		tr.mu.Unlock()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		cancelLaunchingStart(t, f, ctx, cancel)
		requireLive(t, f.rt)
		requireNotLive(t, f.rt, CauseNoConnection)
	})
	t.Run("a process that connects before the original deadline stays live", func(t *testing.T) {
		f := newSupervisedFixture(t, LifetimeKeep, LaunchSession, nil)
		f.sup.react(superviseLaunch, fakeReaction{noProcess: true})
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		cancelLaunchingStart(t, f, ctx, cancel)
		f.sup.startProcess()
		waitFor(t, "the connection", func() bool {
			f.rt.mu.Lock()
			tr := f.rt.transport
			f.rt.mu.Unlock()
			return tr.hasConnected()
		})
		time.Sleep(400 * time.Millisecond)
		requireLive(t, f.rt)
		if err := f.rt.Start(startCtx(t, 5*time.Second), "b"); err != nil {
			t.Fatalf("Start after the connection: %v", err)
		}
	})
}

// spec: 4.7.10 (Runtime supervisor, Launch; Runtime supervisor, Liveness),
// 5.2 (Runtime not live)
func TestSupervisedRuntimeLaunchPermitConditions_spec_4_7_10(t *testing.T) {
	t.Run("no launch after a crash: the start fails and sends nothing", func(t *testing.T) {
		f := newSupervisedFixture(t, LifetimeKeep, LaunchSession, nil)
		if err := f.rt.Start(startCtx(t, 5*time.Second), "a"); err != nil {
			t.Fatal(err)
		}
		f.sup.writeLine(`{"type":"exited","gen":1,"signal":9}`)
		requireNotLive(t, f.rt, CauseRuntimeExit)
		if err := f.rt.Start(startCtx(t, time.Second), "b"); !errors.Is(err, errRuntimeNotLive) {
			t.Fatalf("Start after crash = %v, want errRuntimeNotLive", err)
		}
		if f.sup.count(superviseLaunch) != 1 {
			t.Fatal("a start after a crash sent a launch")
		}
	})
	t.Run("not live refuses a start that would join a live process", func(t *testing.T) {
		f := newSupervisedFixture(t, LifetimeKeep, LaunchSession, nil)
		if err := f.rt.Start(startCtx(t, 5*time.Second), "a"); err != nil {
			t.Fatal(err)
		}
		f.rt.MarkNotLive(CauseHang)
		if err := f.rt.Start(startCtx(t, time.Second), "b"); !errors.Is(err, errRuntimeNotLive) {
			t.Fatalf("Start = %v, want errRuntimeNotLive", err)
		}
	})
	t.Run("a launch is permitted again after an attested end", func(t *testing.T) {
		f := newSupervisedFixture(t, LifetimeRestart, LaunchSession, nil)
		if err := f.rt.Start(startCtx(t, 5*time.Second), "a"); err != nil {
			t.Fatal(err)
		}
		if err := f.rt.EndAndSweep(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := f.rt.RebindListeners(); err != nil {
			t.Fatal(err)
		}
		if err := f.rt.Start(startCtx(t, 5*time.Second), "b"); err != nil {
			t.Fatal(err)
		}
		if n := f.sup.count(superviseLaunch); n != 2 || f.rt.RuntimeGeneration() != 2 {
			t.Fatalf("launches = %d gen = %d, want 2 and 2", n, f.rt.RuntimeGeneration())
		}
		requireLive(t, f.rt)
	})
	t.Run("a launch whose reply carries an error fails the start and is not live", func(t *testing.T) {
		f := newSupervisedFixture(t, LifetimeKeep, LaunchSession, nil)
		f.sup.react(superviseLaunch, fakeReaction{errCode: SuperviseErrExecFailed})
		if err := f.rt.Start(startCtx(t, 5*time.Second), "a"); !errors.Is(err, errLaunchRefused) {
			t.Fatalf("Start = %v, want errLaunchRefused", err)
		}
		requireNotLive(t, f.rt, CauseReplyError)
	})
	for name, r := range map[string]fakeReaction{"no reply": {silent: true}, "closed connection": {closeConn: true}} {
		t.Run("launch with "+name+" is a link failure", func(t *testing.T) {
			f := newSupervisedFixture(t, LifetimeKeep, LaunchSession, nil)
			f.sup.react(superviseLaunch, r)
			if err := f.rt.Start(startCtx(t, 300*time.Millisecond), "a"); !errors.Is(err, ErrSuperviseLinkFailed) {
				t.Fatalf("Start = %v, want a link failure", err)
			}
			requireNotLive(t, f.rt, CauseLinkFailure)
		})
	}
	t.Run("a launched process that does not connect within the start's deadline", func(t *testing.T) {
		f := newSupervisedFixture(t, LifetimeKeep, LaunchSession, nil)
		f.sup.react(superviseLaunch, fakeReaction{noProcess: true})
		if err := f.rt.Start(startCtx(t, 150*time.Millisecond), "a"); err == nil {
			t.Fatal("Start succeeded with no connection")
		}
		requireNotLive(t, f.rt, CauseNoConnection)
	})
	t.Run("a start cancelled before its deadline leaves the not-live state clear", func(t *testing.T) {
		f := newSupervisedFixture(t, LifetimeKeep, LaunchSession, nil)
		f.sup.react(superviseLaunch, fakeReaction{noProcess: true})
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- f.rt.Start(ctx, "a") }()
		waitFor(t, "the launch", func() bool { return f.sup.count(superviseLaunch) == 1 })
		time.Sleep(50 * time.Millisecond)
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled Start = %v, want context.Canceled", err)
		}
		requireLive(t, f.rt)
		// The launched process connects after the cancelled start returned;
		// the next start joins it rather than launching again.
		go f.sup.startProcess()
		if err := f.rt.Start(startCtx(t, 5*time.Second), "b"); err != nil {
			t.Fatalf("Start after a cancelled start: %v", err)
		}
		if n := f.sup.count(superviseLaunch); n != 1 || f.rt.RuntimeGeneration() != 1 {
			t.Fatalf("launches = %d gen = %d, want 1 and 1", n, f.rt.RuntimeGeneration())
		}
		requireLive(t, f.rt)
	})
	t.Run("a launched process that exits before it holds its session", func(t *testing.T) {
		f := newSupervisedFixture(t, LifetimeKeep, LaunchSession, nil)
		code := 1
		f.sup.react(superviseLaunch, fakeReaction{noProcess: true, exitAfterLaunch: &code})
		if err := f.rt.Start(startCtx(t, 300*time.Millisecond), "a"); err == nil {
			t.Fatal("Start succeeded although the process exited")
		}
		if nl, _ := f.rt.NotLive(); !nl {
			t.Fatal("not-live state clear after the launched process exited")
		}
	})
}

// spec: 4.7.10 (Runtime supervisor, End), 5.2 (Supervised scrub sequence)
func TestSupervisedRuntimeEndAndSweepReplies_spec_4_7_10(t *testing.T) {
	type tc struct {
		typ      string
		reaction fakeReaction
		cause    LivenessCause
	}
	cases := map[string]tc{
		"end error":                {superviseEnd, fakeReaction{errCode: SuperviseErrEndDeadline}, CauseReplyError},
		"end no reply":             {superviseEnd, fakeReaction{silent: true}, CauseLinkFailure},
		"end closed connection":    {superviseEnd, fakeReaction{closeConn: true}, CauseLinkFailure},
		"sweep error":              {superviseSweep, fakeReaction{errCode: SuperviseErrSweepFailed}, CauseReplyError},
		"sweep walk reached bound": {superviseSweep, fakeReaction{errCode: SuperviseErrSweepBound}, CauseReplyError},
		"sweep no reply":           {superviseSweep, fakeReaction{silent: true}, CauseLinkFailure},
		"sweep closed connection":  {superviseSweep, fakeReaction{closeConn: true}, CauseLinkFailure},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := newSupervisedFixture(t, LifetimeRestart, LaunchSession, nil)
			if err := f.rt.Start(startCtx(t, 5*time.Second), "a"); err != nil {
				t.Fatal(err)
			}
			f.sup.react(c.typ, c.reaction)
			err := f.rt.EndAndSweep(startCtx(t, 300*time.Millisecond))
			if !errors.Is(err, scrub.ErrNotLive) {
				t.Fatalf("EndAndSweep = %v, want an ErrNotLive error", err)
			}
			requireNotLive(t, f.rt, c.cause)
			if c.cause == CauseLinkFailure {
				// After a link failure no further request is written.
				n := len(f.sup.sent())
				_ = f.rt.Sweep(context.Background())
				if len(f.sup.sent()) != n {
					t.Fatal("a request was sent after the link failure")
				}
			}
		})
	}
	t.Run("an attested end permits a launch and closes the composed listener", func(t *testing.T) {
		f := newSupervisedFixture(t, LifetimeRestart, LaunchSession, nil)
		if err := f.rt.Start(startCtx(t, 5*time.Second), "a"); err != nil {
			t.Fatal(err)
		}
		f.rt.SetEndDisposition(EndScrub)
		if err := f.rt.EndProcess(context.Background(), 0); err != nil {
			t.Fatal(err)
		}
		if got := f.sup.request(superviseEnd, 0)["graceMs"].(float64); got != 0 {
			t.Fatalf("scrub end graceMs = %v, want 0", got)
		}
		if f.rt.HasLaunched() {
			t.Fatal("HasLaunched after an attested end")
		}
		if c, err := net.Dial("unix", f.runtime); err == nil {
			_ = c.Close()
			t.Fatal("the ended process's listener still accepts")
		}
		requireLive(t, f.rt)
	})
}

// spec: 4.7.10 (Runtime supervisor, Cleanup), 5.2 (Supervised scrub
// sequence), 28.5.3 (CH-SUPERVISE, Timing)
func TestSupervisedRuntimeRunCleanupOutcomes_spec_4_7_10(t *testing.T) {
	req := scrub.CleanupRequest{Commands: []string{"/bin/true  a b"}, Timeout: 50 * time.Millisecond, PrevProvider: "p", PrevLeaseID: "l"}
	t.Run("success sends the argv lists and the metadata", func(t *testing.T) {
		f := newSupervisedFixture(t, LifetimeKeep, LaunchSession, nil)
		if err := f.rt.RunCleanup(context.Background(), req); err != nil {
			t.Fatal(err)
		}
		got := f.sup.request(superviseCleanup, 0)
		cmds := got["commands"].([]any)
		if len(cmds) != 1 || len(cmds[0].([]any)) != 3 || got["timeoutMs"].(float64) != 50 || got["prevProvider"] != "p" || got["prevLeaseId"] != "l" {
			t.Fatalf("cleanup request = %v", got)
		}
	})
	for _, code := range []string{SuperviseErrCleanupFailed, SuperviseErrCleanupTimeout} {
		t.Run("a cleaned with "+code+" fails the scrub and is not a link failure", func(t *testing.T) {
			f := newSupervisedFixture(t, LifetimeKeep, LaunchSession, nil)
			// The reply arrives only after timeoutMs, inside the request's
			// own bound, which exceeds the cleanupTimeoutSeconds deadline.
			f.sup.react(superviseCleanup, fakeReaction{errCode: code, delay: 120 * time.Millisecond})
			err := f.rt.RunCleanup(context.Background(), req)
			if err == nil || errors.Is(err, scrub.ErrNotLive) {
				t.Fatalf("RunCleanup = %v, want a scrub failure", err)
			}
			requireLive(t, f.rt)
			if f.link.Failed() {
				t.Fatal("a late cleaned failed the link")
			}
		})
	}
	t.Run("no reply is a link failure", func(t *testing.T) {
		f := newSupervisedFixture(t, LifetimeKeep, LaunchSession, nil)
		f.sup.react(superviseCleanup, fakeReaction{silent: true})
		if err := f.rt.RunCleanup(startCtx(t, 200*time.Millisecond), req); !errors.Is(err, scrub.ErrNotLive) {
			t.Fatalf("RunCleanup = %v, want ErrNotLive", err)
		}
		requireNotLive(t, f.rt, CauseLinkFailure)
	})
	t.Run("an empty command is refused before anything is sent", func(t *testing.T) {
		f := newSupervisedFixture(t, LifetimeKeep, LaunchSession, nil)
		if err := f.rt.RunCleanup(context.Background(), scrub.CleanupRequest{Commands: []string{"  "}}); err == nil {
			t.Fatal("RunCleanup accepted an empty command")
		}
		if f.sup.count(superviseCleanup) != 0 {
			t.Fatal("cleanup sent for an empty command")
		}
	})
}

// spec: 4.7.10 (Runtime supervisor, End), 5.2 (Supervised scrub sequence,
// Listener rebind)
func TestSupervisedRuntimeRebindDiscardsABackloggedConnection_spec_4_7_10(t *testing.T) {
	ops, err := newTestRuntimeOps(t, uniqueSocket("ops"), SocketPeerAuth{ExpectedUID: currentUID()})
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = ops.Run(context.Background()) }()
	t.Cleanup(func() { _ = ops.Close() })
	f := newSupervisedFixture(t, LifetimeRestart, LaunchSession, ops)
	if err := f.rt.Start(startCtx(t, 5*time.Second), "a"); err != nil {
		t.Fatal(err)
	}
	if err := f.rt.EndAndSweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := f.rt.RebindListeners(); err != nil {
		t.Fatal(err)
	}
	// A connection the ended generation left in the new listener's backlog
	// before the next launch is discarded by the next rebind.
	stale := dialUntil(t, f.runtime)
	staleOps := dialUntil(t, ops.SocketPath())
	if err := f.rt.RebindListeners(); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]net.Conn{"CH-MSGSOCK": stale, "CH-RUNTIMEOPS": staleOps} {
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
		if _, err := c.Read(make([]byte, 1)); err == nil {
			t.Fatalf("%s backlogged connection still open after the rebind", name)
		}
		_ = c.Close()
	}
	if err := f.rt.Start(startCtx(t, 5*time.Second), "b"); err != nil {
		t.Fatalf("Start after rebind: %v", err)
	}
	requireLive(t, f.rt)
}

// spec: 4.7.10 (Runtime supervisor, Liveness), 5.2 (Runtime not live)
func TestSupervisedRuntimeServesNextSession_spec_4_7_10(t *testing.T) {
	f := newSupervisedFixture(t, LifetimeKeep, LaunchSession, nil)
	if !f.rt.ServesNextSession() {
		t.Fatal("a keep pod with a launch permitted must serve the next session")
	}
	if err := f.rt.Start(startCtx(t, 5*time.Second), "a"); err != nil {
		t.Fatal(err)
	}
	if !f.rt.ServesNextSession() {
		t.Fatal("a keep pod with a live connection must serve")
	}
	f.sup.killProcesses()
	waitFor(t, "the connection end", func() bool { return !f.rt.ServesNextSession() })
	r := newSupervisedFixture(t, LifetimeRestart, LaunchSession, nil)
	if !r.rt.ServesNextSession() {
		t.Fatal("a restart pod with the not-live state clear must serve")
	}
	r.rt.MarkNotLive(CauseReplyError)
	if r.rt.ServesNextSession() {
		t.Fatal("not live must report not able to serve")
	}
}

// spec: 4.7.10 (Runtime supervisor, Runtime exits)
func TestEndDispositionPrecedence_spec_4_7_10(t *testing.T) {
	order := []EndDisposition{EndDemote, EndScrub, EndShutdown, EndHoldTimeout, EndDeleting, EndNone}
	for i, first := range order {
		for j, second := range order {
			rt := &SupervisedRuntime{disposition: EndNone}
			rt.SetEndDisposition(first)
			rt.SetEndDisposition(second)
			want := first
			if j < i {
				want = second
			}
			if got := rt.EndDisposition(); got != want {
				t.Errorf("set %s then %s = %s, want %s", first, second, got, want)
			}
		}
	}
	// A launch resets the disposition to none.
	f := newSupervisedFixture(t, LifetimeKeep, LaunchSession, nil)
	f.rt.SetEndDisposition(EndDeleting)
	f.rt.mu.Lock()
	f.rt.launchPermitted = true
	f.rt.mu.Unlock()
	if err := f.rt.Start(startCtx(t, 5*time.Second), "a"); err != nil {
		t.Fatal(err)
	}
	if got := f.rt.EndDisposition(); got != EndNone {
		t.Fatalf("disposition after launch = %s, want none", got)
	}
}

// spec: 4.7.10 (Runtime supervisor, Launch; Runtime supervisor, SDK-warm),
// 4.7.9 (step 4)
func TestSupervisedWarmUpLaunch_spec_4_7_10(t *testing.T) {
	t.Run("writes the warm-up manifest and waits for the connection", func(t *testing.T) {
		f := newSupervisedFixture(t, LifetimeKeep, LaunchService, nil)
		s := New("test")
		s.ManifestDir = t.TempDir()
		s.UseSupervisedRuntime(f.rt)
		if s.SupervisedRuntimeOrNil() != f.rt {
			t.Fatal("the Server does not hold the supervised runtime")
		}
		if err := f.rt.WarmUp(startCtx(t, 5*time.Second)); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadManifest(s.ManifestDir); err != nil {
			t.Fatalf("no warm-up manifest: %v", err)
		}
		if n := f.sup.count(superviseLaunch); n != 1 {
			t.Fatalf("launches = %d, want 1", n)
		}
		// A session start joins the warm-up process.
		if err := f.rt.Start(startCtx(t, 5*time.Second), "a"); err != nil {
			t.Fatal(err)
		}
		if n := f.sup.count(superviseLaunch); n != 1 {
			t.Fatalf("a start after warm-up launched again (%d launches)", n)
		}
	})
	t.Run("a process that does not connect is a warm-up failure", func(t *testing.T) {
		f := newSupervisedFixture(t, LifetimeKeep, LaunchService, nil)
		f.sup.react(superviseLaunch, fakeReaction{noProcess: true})
		if err := f.rt.WarmUp(startCtx(t, 150*time.Millisecond)); err == nil {
			t.Fatal("WarmUp succeeded with no connection")
		}
		requireNotLive(t, f.rt, CauseWarmUpFailure)
	})
	t.Run("a failed manifest write is a warm-up failure", func(t *testing.T) {
		f := newSupervisedFixture(t, LifetimeKeep, LaunchService, nil)
		f.rt.SetWarmUpPreparer(func() error { return errors.New("disk full") })
		if err := f.rt.WarmUp(startCtx(t, time.Second)); err == nil {
			t.Fatal("WarmUp succeeded without a manifest")
		}
		if f.sup.count(superviseLaunch) != 0 {
			t.Fatal("launched before the manifest existed")
		}
		requireNotLive(t, f.rt, CauseWarmUpFailure)
	})
}

// spec: 4.7.10 (Runtime supervisor, End; Runtime supervisor, Launch), 6.1
// (Adapter SIGTERM behavior during sdk_connecting)
func TestSupervisedEndIsOrderedAfterTheLaunchInFlight_spec_4_7_10(t *testing.T) {
	f := newSupervisedFixture(t, LifetimeKeep, LaunchService, nil)
	entered := make(chan struct{})
	release := make(chan struct{})
	// The hook runs after the generation increment and before the launch
	// request, which is the window an end must not overtake.
	f.rt.setLaunchHook(func() { close(entered); <-release })
	warmDone := make(chan error, 1)
	go func() { warmDone <- f.rt.WarmUp(startCtx(t, 5*time.Second)) }()
	<-entered
	endDone := make(chan error, 1)
	go func() {
		if !f.rt.HasLaunched() {
			endDone <- errors.New("HasLaunched reported no launch while one was in flight")
			return
		}
		endDone <- f.rt.EndProcess(startCtx(t, 5*time.Second), 0)
	}()
	time.Sleep(100 * time.Millisecond)
	if n := f.sup.count(superviseEnd); n != 0 {
		t.Fatalf("an end reached the supervisor before the launch it targets (requests %v)", f.sup.sent())
	}
	close(release)
	// The end may complete before the ended process's connection is
	// accepted, so the warm-up's own outcome is not asserted here.
	<-warmDone
	if err := <-endDone; err != nil {
		t.Fatalf("end: %v", err)
	}
	if got := f.sup.sent(); len(got) != 2 || got[0] != superviseLaunch || got[1] != superviseEnd {
		t.Fatalf("requests = %v, want [launch end]", got)
	}
	f.sup.mu.Lock()
	live := f.sup.liveGen
	f.sup.mu.Unlock()
	if live != 0 {
		t.Fatalf("generation %d is still live after the end", live)
	}
}
