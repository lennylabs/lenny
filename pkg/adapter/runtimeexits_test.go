// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// exitsFixture is a Server holding a supervised runtime whose first
// generation is launched and connected.
type exitsFixture struct {
	*supervisedFixture
	s    *Server
	logs *syncBuffer
}

func newExitsFixture(t *testing.T, lifetime RuntimeLifetime, mode LaunchMode) *exitsFixture {
	t.Helper()
	f := newSupervisedFixture(t, lifetime, mode, nil)
	logs := &syncBuffer{}
	f.rt.logger = slog.New(slog.NewTextHandler(logs, nil))
	s := New("test")
	s.UseSupervisedRuntime(f.rt)
	if err := f.rt.Start(startCtx(t, 5*time.Second), "a"); err != nil {
		t.Fatal(err)
	}
	return &exitsFixture{supervisedFixture: f, s: s, logs: logs}
}

// hold registers the named sessions and takes each one's rule-8 record.
func (e *exitsFixture) hold(ids ...string) {
	e.s.mu.Lock()
	defer e.s.mu.Unlock()
	if e.s.slots == nil {
		e.s.slots = map[string]*slotState{}
	}
	for _, id := range ids {
		e.s.slots[id] = &slotState{sessionID: id, started: true}
		e.s.noteRuntimeStartedLocked(id)
	}
}

// register adds an entry with no rule-8 record.
func (e *exitsFixture) register(id string) {
	e.s.mu.Lock()
	defer e.s.mu.Unlock()
	if e.s.slots == nil {
		e.s.slots = map[string]*slotState{}
	}
	e.s.slots[id] = &slotState{sessionID: id, started: true}
}

// deregister removes the entry, as Shutdown's locked step does.
func (e *exitsFixture) deregister(id string) {
	e.s.mu.Lock()
	defer e.s.mu.Unlock()
	delete(e.s.slots, id)
}

// crashed reports whether the session's stream was failed with
// runtime_crash.
func (e *exitsFixture) crashed(id string) bool {
	e.s.mu.Lock()
	defer e.s.mu.Unlock()
	st, ok := e.s.slots[id]
	if !ok || st.crash == nil {
		return false
	}
	select {
	case <-st.crash:
		return true
	default:
		return false
	}
}

// endBy delivers one end event of generation 1 and waits for its handling.
func (e *exitsFixture) endBy(t *testing.T, kind string) {
	t.Helper()
	switch kind {
	case "exited 0":
		e.sup.writeLine(`{"type":"exited","gen":1,"status":0}`)
	case "exited 2":
		e.sup.writeLine(`{"type":"exited","gen":1,"status":2}`)
	case "exited by signal":
		e.sup.writeLine(`{"type":"exited","gen":1,"signal":9}`)
	case "connection":
		e.sup.killProcesses()
	case "ended":
		if err := e.rt.EndProcess(context.Background(), 0); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unknown event %s", kind)
	}
	waitFor(t, "the end to be handled", func() bool {
		l := e.logs.String()
		return strings.Contains(l, "runtime_process_ended") || strings.Contains(l, "runtime_shutdown_exit_nonzero")
	})
}

// spec: 4.7.10 (Runtime supervisor, Runtime exits; Runtime supervisor, Crash;
// Runtime supervisor, Liveness), 28.5.3 (Exit Codes)
func TestRuntimeExitsRowPerDispositionEventAndPodKind_spec_4_7_10(t *testing.T) {
	kinds := []struct {
		name     string
		lifetime RuntimeLifetime
		mode     LaunchMode
	}{
		{"keep", LifetimeKeep, LaunchSession},
		{"restart", LifetimeRestart, LaunchSession},
		{"service", LifetimeKeep, LaunchService},
		{"preconnect", LifetimeKeep, LaunchPreconnectPodWarm},
	}
	dispositions := []EndDisposition{EndDemote, EndScrub, EndShutdown, EndHoldTimeout, EndDeleting, EndNone}
	events := []string{"exited 0", "exited 2", "exited by signal", "connection", "ended"}
	for _, k := range kinds {
		for _, d := range dispositions {
			for _, ev := range events {
				for _, held := range []bool{true, false} {
					name := fmt.Sprintf("%s/%s/%s/held=%v", k.name, d, ev, held)
					t.Run(name, func(t *testing.T) {
						e := newExitsFixture(t, k.lifetime, k.mode)
						if held {
							e.hold("a")
						}
						var observed []LivenessCause
						var mu sync.Mutex
						e.rt.SetOnNotLive(func(c LivenessCause) { mu.Lock(); observed = append(observed, c); mu.Unlock() })
						e.rt.SetEndDisposition(d)
						e.endBy(t, ev)
						row := runtimeExitRow(d)
						if got := e.crashed("a"); got != (row.crash && held) {
							t.Fatalf("session failed with runtime_crash = %v, want %v", got, row.crash && held)
						}
						nl, cause := e.rt.NotLive()
						if nl != (row.notLive != "") || (nl && cause != row.notLive) {
							t.Fatalf("not-live = %v (%s), want cause %q", nl, cause, row.notLive)
						}
						mu.Lock()
						n := len(observed)
						mu.Unlock()
						if (row.notLive != "") != (n == 1) {
							t.Fatalf("not-live observer ran %d times, want %v", n, row.notLive != "")
						}
						warned := strings.Contains(e.logs.String(), "runtime_shutdown_exit_nonzero")
						wantWarn := d == EndShutdown && (ev == "exited 2" || ev == "exited by signal")
						if warned != wantWarn {
							t.Fatalf("Exit Codes warning logged = %v, want %v", warned, wantWarn)
						}
					})
				}
			}
		}
	}
}

// spec: 4.7.10 (Runtime supervisor, Runtime exits)
func TestRuntimeExitsHandlesEachGenerationOnce_spec_4_7_10(t *testing.T) {
	orders := [][]string{
		{"exited by signal", "connection"},
		{"connection", "exited by signal"},
	}
	for _, order := range orders {
		t.Run(strings.Join(order, " then "), func(t *testing.T) {
			e := newExitsFixture(t, LifetimeKeep, LaunchSession)
			e.hold("a")
			e.endBy(t, order[0])
			// A disposition change after the first handled event changes
			// nothing for this generation.
			e.rt.SetEndDisposition(EndScrub)
			switch order[1] {
			case "connection":
				e.sup.killProcesses()
			default:
				e.sup.writeLine(`{"type":"exited","gen":1,"signal":9}`)
			}
			time.Sleep(30 * time.Millisecond)
			if !e.crashed("a") {
				t.Fatal("the first event's none row did not fail the session")
			}
			if _, cause := e.rt.NotLive(); cause != CauseRuntimeExit {
				t.Fatalf("cause = %s, want runtime_exit", cause)
			}
			if c := strings.Count(e.logs.String(), "runtime_process_ended"); c != 1 {
				t.Fatalf("end handled %d times, want once", c)
			}
		})
	}
	t.Run("a scrub end then a late connection end", func(t *testing.T) {
		e := newExitsFixture(t, LifetimeRestart, LaunchSession)
		e.rt.SetEndDisposition(EndScrub)
		e.endBy(t, "ended")
		e.sup.writeLine(`{"type":"exited","gen":1,"status":0}`)
		time.Sleep(30 * time.Millisecond)
		requireLive(t, e.rt)
	})
}

// A runtime that exits after the shutdown write closes its CH-MSGSOCK socket
// and the supervisor reports its exited, in either order. When the connection
// end or the ended reply is handled first, the later exited still records the
// Exit Codes warning with its status and applies no second row.
// spec: 4.7.10 (Runtime supervisor, Runtime exits), 28.5.3 (Exit Codes)
func TestShutdownRowWarnsOnALateNonZeroExited_spec_28_5_3(t *testing.T) {
	cases := []struct {
		name   string
		first  string
		exited string
		disp   EndDisposition
		warn   bool
	}{
		{"connection then exited status 2", "connection", `{"type":"exited","gen":1,"status":2}`, EndShutdown, true},
		{"connection then exited by signal", "connection", `{"type":"exited","gen":1,"signal":9}`, EndShutdown, true},
		{"ended then exited status 2", "ended", `{"type":"exited","gen":1,"status":2}`, EndShutdown, true},
		{"connection then exited status 0", "connection", `{"type":"exited","gen":1,"status":0}`, EndShutdown, false},
		{"connection then exited status 2 on the scrub row", "connection", `{"type":"exited","gen":1,"status":2}`, EndScrub, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newExitsFixture(t, LifetimeKeep, LaunchSession)
			e.hold("a")
			e.rt.SetEndDisposition(tc.disp)
			e.endBy(t, tc.first)
			// A disposition change after the first handled event does not
			// change which row the late exited is checked against.
			e.rt.SetEndDisposition(EndNone)
			e.sup.writeLine(tc.exited)
			if tc.warn {
				waitFor(t, "the late Exit Codes warning", func() bool {
					return strings.Contains(e.logs.String(), "runtime_shutdown_exit_nonzero")
				})
			} else {
				time.Sleep(50 * time.Millisecond)
			}
			logs := e.logs.String()
			want := 0
			if tc.warn {
				want = 1
			}
			if c := strings.Count(logs, "runtime_shutdown_exit_nonzero"); c != want {
				t.Fatalf("Exit Codes warnings = %d, want %d; logs:\n%s", c, want, logs)
			}
			if tc.warn && !strings.Contains(logs, "exit=") {
				t.Fatalf("the warning does not carry the status; logs:\n%s", logs)
			}
			if c := strings.Count(logs, "runtime_process_ended"); c != 1 {
				t.Fatalf("row applied %d times, want once", c)
			}
			if e.crashed("a") {
				t.Fatal("the late exited applied a crash row")
			}
			requireLive(t, e.rt)
		})
	}
}

// spec: 4.7.10 (Runtime supervisor, Crash), 4.7.1 (The registry critical
// section)
func TestRuntimeCrashFailsOnlyTheSessionsTheProcessHolds_spec_4_7_10(t *testing.T) {
	for _, lifetime := range []RuntimeLifetime{LifetimeKeep, LifetimeRestart} {
		t.Run(string(lifetime)+"/registered with no rule-8 record", func(t *testing.T) {
			e := newExitsFixture(t, lifetime, LaunchSession)
			e.register("a")
			e.endBy(t, "exited by signal")
			if e.crashed("a") {
				t.Fatal("a session the process does not hold failed with runtime_crash")
			}
			requireNotLive(t, e.rt, CauseRuntimeExit)
		})
		t.Run(string(lifetime)+"/before the last session's deregistration", func(t *testing.T) {
			e := newExitsFixture(t, lifetime, LaunchSession)
			e.hold("a")
			e.endBy(t, "exited by signal")
			if !e.crashed("a") {
				t.Fatal("the held session did not fail")
			}
		})
		t.Run(string(lifetime)+"/after the last session's deregistration", func(t *testing.T) {
			e := newExitsFixture(t, lifetime, LaunchSession)
			e.hold("a")
			e.deregister("a")
			e.endBy(t, "exited by signal")
			e.s.mu.Lock()
			n := len(e.s.slots)
			e.s.mu.Unlock()
			if n != 0 {
				t.Fatal("deregistered entry came back")
			}
			requireNotLive(t, e.rt, CauseRuntimeExit)
			if err := e.rt.Start(startCtx(t, time.Second), "b"); err == nil {
				t.Fatal("a start after the crash succeeded")
			}
			if e.sup.count(superviseLaunch) != 1 {
				t.Fatal("a refused start sent a launch")
			}
		})
		t.Run(string(lifetime)+"/concurrent pod between two deregistrations", func(t *testing.T) {
			e := newExitsFixture(t, lifetime, LaunchSession)
			e.hold("a", "b")
			e.deregister("a")
			e.endBy(t, "connection")
			if !e.crashed("b") {
				t.Fatal("the other session the process holds did not fail")
			}
		})
	}
}

// spec: 4.7.10 (Runtime supervisor, Runtime exits)
func TestAdapterExitAndRebindClosesSelectNoRow_spec_4_7_10(t *testing.T) {
	t.Run("the adapter's own exit close", func(t *testing.T) {
		e := newExitsFixture(t, LifetimeKeep, LaunchService)
		e.hold("a")
		if err := e.rt.transport.CloseListener(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(30 * time.Millisecond)
		if e.crashed("a") {
			t.Fatal("the exit close failed a session")
		}
		requireLive(t, e.rt)
	})
	t.Run("the rebind close", func(t *testing.T) {
		e := newExitsFixture(t, LifetimeKeep, LaunchService)
		e.hold("a")
		if err := e.rt.RebindListeners(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(30 * time.Millisecond)
		if e.crashed("a") {
			t.Fatal("the rebind close failed a session")
		}
		requireLive(t, e.rt)
	})
}

// spec: 4.7.10 (Runtime supervisor, Liveness), 28.5.3 (Inbound: shutdown)
func TestLivenessCausesOnTheServer_spec_4_7_10(t *testing.T) {
	t.Run("hang sets not live on a concurrent restart pod", func(t *testing.T) {
		e := newExitsFixture(t, LifetimeRestart, LaunchSession)
		e.hold("a", "b")
		e.s.onHeartbeatHung(context.Background(), "a", e.s.Runtime)
		requireNotLive(t, e.rt, CauseHang)
		if err := e.s.Runtime.Start(startCtx(t, time.Second), "c"); err == nil {
			t.Fatal("a start joined the hung process")
		}
	})
	t.Run("hold timeout closes through the end handler", func(t *testing.T) {
		e := newExitsFixture(t, LifetimeKeep, LaunchSession)
		e.hold("a")
		e.s.endRuntimeForHoldTimeout()
		requireNotLive(t, e.rt, CauseNoConnection)
		if e.crashed("a") {
			t.Fatal("the hold timeout failed a session with runtime_crash")
		}
	})
	t.Run("ReportPodScrub runtime_live follows the not-live state", func(t *testing.T) {
		e := newExitsFixture(t, LifetimeRestart, LaunchSession)
		if !e.s.runtimeServesNextSession() {
			t.Fatal("live runtime reported not able to serve")
		}
		e.rt.MarkNotLive(CauseLinkFailure)
		if e.s.runtimeServesNextSession() {
			t.Fatal("not-live runtime reported able to serve")
		}
	})
}

// spec: 4.7.10 (Runtime supervisor, Generation), 28.6 (sole-session cohort)
func TestCohortResetsAtEachNewGeneration_spec_4_7_10(t *testing.T) {
	e := newExitsFixture(t, LifetimeRestart, LaunchSession)
	e.hold("a")
	if got := e.s.soleSession(); got != "a" {
		t.Fatalf("soleSession = %q, want a", got)
	}
	e.hold("b")
	if got := e.s.soleSession(); got != "" {
		t.Fatalf("soleSession with two sessions of one generation = %q, want empty", got)
	}
	e.deregister("a")
	e.deregister("b")
	e.s.noteRuntimeClosed("a")
	e.s.noteRuntimeClosed("b")
	if err := e.rt.EndAndSweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := e.rt.RebindListeners(); err != nil {
		t.Fatal(err)
	}
	if err := e.rt.Start(startCtx(t, 5*time.Second), "c"); err != nil {
		t.Fatal(err)
	}
	e.hold("c")
	if got := e.s.soleSession(); got != "c" {
		t.Fatalf("first session of a new generation: soleSession = %q, want c", got)
	}
}

// spec: 6.1 (Adapter SIGTERM behavior during sdk_connecting), 4.7.10
// (Runtime supervisor, SDK-warm)
func TestSupervisedShutdownDemoteSDK_spec_6_1(t *testing.T) {
	t.Run("a registered session: no end, entry kept", func(t *testing.T) {
		e := newExitsFixture(t, LifetimeKeep, LaunchPreconnectPodWarm)
		e.hold("a")
		e.s.ShutdownDemoteSDK(time.Second)
		if n := e.sup.count(superviseEnd); n != 0 {
			t.Fatalf("end sent %d times with a session registered, want 0", n)
		}
		e.s.mu.Lock()
		_, ok := e.s.slots["a"]
		e.s.mu.Unlock()
		if !ok {
			t.Fatal("the registered session's entry was removed")
		}
	})
	t.Run("no session: demotes once with one end", func(t *testing.T) {
		e := newExitsFixture(t, LifetimeKeep, LaunchPreconnectPodWarm)
		if !e.s.SDKWarmReady() {
			t.Fatal("a launch on a preconnect pod must set sdkConnected")
		}
		e.s.ShutdownDemoteSDK(2 * time.Second)
		if n := e.sup.count(superviseEnd); n != 1 {
			t.Fatalf("end sent %d times, want 1", n)
		}
		if e.s.SDKWarmReady() {
			t.Fatal("sdkConnected not cleared")
		}
		if e.crashed("a") {
			t.Fatal("demotion failed a session")
		}
	})
	t.Run("ConfigureWorkspace with no live process launches once", func(t *testing.T) {
		f := newSupervisedFixture(t, LifetimeKeep, LaunchPreconnectPodWarm, nil)
		s := New("test")
		s.UseSupervisedRuntime(f.rt)
		sw, ok := s.sdkWarmRuntime()
		if !ok {
			t.Fatal("a preconnect supervised pod must serve SDK-warm RPCs")
		}
		if err := sw.ConfigureWorkspace(startCtx(t, 5*time.Second), "a", "/w"); err != nil {
			t.Fatal(err)
		}
		if n := f.sup.count(superviseLaunch); n != 1 {
			t.Fatalf("launch sent %d times, want 1", n)
		}
	})
	t.Run("a session or service pod serves no SDK-warm RPC", func(t *testing.T) {
		for _, mode := range []LaunchMode{LaunchSession, LaunchService} {
			f := newSupervisedFixture(t, LifetimeKeep, mode, nil)
			s := New("test")
			s.UseSupervisedRuntime(f.rt)
			if _, ok := s.sdkWarmRuntime(); ok {
				t.Fatalf("%s pod serves SDK-warm RPCs", mode)
			}
		}
	})
	t.Run("a failed demote end sets not live and returns the error", func(t *testing.T) {
		e := newExitsFixture(t, LifetimeKeep, LaunchPreconnectPodWarm)
		e.sup.react(superviseEnd, fakeReaction{errCode: SuperviseErrEndDeadline})
		sw, _ := e.s.sdkWarmRuntime()
		if err := sw.DemoteSDK(context.Background()); err == nil {
			t.Fatal("DemoteSDK with a failed end returned nil")
		}
		requireNotLive(t, e.rt, CauseReplyError)
		if e.rt.EndDisposition() != EndDemote {
			t.Fatalf("disposition = %s, want endDemote", e.rt.EndDisposition())
		}
	})
	t.Run("refused second confirmation sends one end and fails no session", func(t *testing.T) {
		e := newExitsFixture(t, LifetimeKeep, LaunchPreconnectPodWarm)
		e.hold("a")
		sw, _ := e.s.sdkWarmRuntime()
		err := e.s.refuseUnconfirmedSDKWarmStart(context.Background(), sw, "a")
		if err == nil {
			t.Fatal("refusal returned nil")
		}
		if n := e.sup.count(superviseEnd); n != 1 {
			t.Fatalf("end sent %d times, want 1", n)
		}
		if e.crashed("a") {
			t.Fatal("the refusal's end failed a session with runtime_crash")
		}
		requireLive(t, e.rt)
	})
	t.Run("ExitOnSignal on a service pod ends the process once", func(t *testing.T) {
		e := newExitsFixture(t, LifetimeKeep, LaunchService)
		e.s.ExitOnSignal(2*time.Second, nil)
		if n := e.sup.count(superviseEnd); n != 1 {
			t.Fatalf("end sent %d times, want 1", n)
		}
	})
	t.Run("ExitOnSignal on a session pod sends no end", func(t *testing.T) {
		e := newExitsFixture(t, LifetimeKeep, LaunchSession)
		e.s.ExitOnSignal(time.Second, nil)
		if n := e.sup.count(superviseEnd); n != 0 {
			t.Fatalf("end sent %d times, want 0", n)
		}
		if e.rt.EndDisposition() != EndDeleting {
			t.Fatalf("disposition = %s, want deleting", e.rt.EndDisposition())
		}
	})
}

// spec: 4.7.10 (Runtime supervisor, Crash), 7.3 (retry and resume), 28.5.1
// (CH-ATTACH, Degradation)
func TestAttachEndsWithRuntimeCrashWhenTheHeldProcessCrashes_spec_4_7_10(t *testing.T) {
	for _, ev := range []string{"exited by signal", "connection"} {
		t.Run(ev, func(t *testing.T) {
			e := newExitsFixture(t, LifetimeKeep, LaunchSession)
			e.hold("a")
			e.s.mu.Lock()
			e.s.slots["a"].paths.Current = t.TempDir()
			e.s.mu.Unlock()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			out := make(chan error, 1)
			go func() {
				out <- e.s.Attach(&firstFrameStream{ctx: ctx, first: &adapterv1.AttachRequest{SessionId: &adapterv1.SessionId{Value: "a"}}})
			}()
			time.Sleep(30 * time.Millisecond)
			e.endBy(t, ev)
			select {
			case err := <-out:
				if status.Code(err) != codes.Internal || !strings.Contains(err.Error(), "runtime_crash") {
					t.Fatalf("Attach = %v, want Internal runtime_crash", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("Attach did not end on the crash")
			}
		})
	}
}

// spec: 4.7.10 (Runtime supervisor, Crash)
func TestIsClosedReadsAChannelWithoutBlocking_spec_4_7_10(t *testing.T) {
	if isClosed(nil) {
		t.Error("a nil channel reported closed; a session with no crash channel has not crashed")
	}
	ch := make(chan struct{})
	if isClosed(ch) {
		t.Error("an open channel reported closed")
	}
	close(ch)
	if !isClosed(ch) {
		t.Error("a closed channel reported open")
	}
}

// spec: 4.7.10 (Runtime supervisor, Signals), 6.1 (Adapter SIGTERM behavior
// during sdk_connecting)
func TestServiceExitOnSignalEndsOnlyALaunchedProcess_spec_4_7_10(t *testing.T) {
	t.Run("nothing launched: no end", func(t *testing.T) {
		f := newSupervisedFixture(t, LifetimeKeep, LaunchService, nil)
		s := New("test")
		s.UseSupervisedRuntime(f.rt)
		s.ExitOnSignal(time.Second, nil)
		if n := f.sup.count(superviseEnd); n != 0 {
			t.Fatalf("end sent %d times with nothing launched, want 0", n)
		}
		if f.rt.EndDisposition() != EndDeleting {
			t.Fatalf("disposition = %s, want deleting", f.rt.EndDisposition())
		}
	})
	t.Run("zero timeout falls back to the default bound", func(t *testing.T) {
		e := newExitsFixture(t, LifetimeKeep, LaunchService)
		e.s.ExitOnSignal(0, nil)
		if n := e.sup.count(superviseEnd); n != 1 {
			t.Fatalf("end sent %d times, want 1", n)
		}
	})
	t.Run("a failed end is logged and the exit proceeds", func(t *testing.T) {
		e := newExitsFixture(t, LifetimeKeep, LaunchService)
		e.sup.react(superviseEnd, fakeReaction{errCode: SuperviseErrEndDeadline})
		stopped := &recordingStopper{}
		e.s.ExitOnSignal(time.Second, stopped)
		if n := e.sup.count(superviseEnd); n != 1 {
			t.Fatalf("end sent %d times, want 1", n)
		}
		if !stopped.stopped {
			t.Fatal("the server was not stopped after a failed end")
		}
	})
}

// spec: 4.7.10 (Runtime supervisor, Runtime exits; Runtime supervisor,
// Signals)
// diagnosis: the pod's termination set the deleting disposition before a
// warm-up launch in flight reset it to none, so the adapter's own end during
// termination took the none row and set the runtime_exit not-live cause.
func TestServiceTerminationDuringAnInFlightWarmUpLaunchEndsUnderDeleting_spec_4_7_10(t *testing.T) {
	f := newSupervisedFixture(t, LifetimeKeep, LaunchService, nil)
	s := New("test")
	s.UseSupervisedRuntime(f.rt)
	// Holding sendMu parks the warm-up launch after its permit check and
	// before it resets the disposition; the termination path then sets the
	// deleting value and queues behind it in SettleLaunch.
	f.rt.sendMu.Lock()
	warmDone := make(chan error, 1)
	go func() { warmDone <- f.rt.WarmUp(startCtx(t, 5*time.Second)) }()
	time.Sleep(20 * time.Millisecond)
	exitDone := make(chan struct{})
	go func() { defer close(exitDone); s.ExitOnSignal(2*time.Second, nil) }()
	waitFor(t, "the deleting disposition", func() bool { return f.rt.EndDisposition() == EndDeleting })
	time.Sleep(20 * time.Millisecond)
	f.rt.sendMu.Unlock()
	<-exitDone
	<-warmDone
	if got := f.sup.sent(); len(got) < 2 || got[0] != superviseLaunch || got[len(got)-1] != superviseEnd {
		t.Fatalf("requests = %v, want the warm-up launch first and the termination's end last", got)
	}
	if nl, cause := f.rt.NotLive(); nl && cause == CauseRuntimeExit {
		t.Fatalf("not-live cause %s set by the termination's own end, want the deleting row", cause)
	}
	if d := f.rt.EndDisposition(); d != EndDeleting {
		t.Fatalf("disposition = %s, want deleting", d)
	}
}
