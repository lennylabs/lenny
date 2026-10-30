// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

// supervisedServer wires a fresh supervised fixture into a Server, with no
// start run yet.
func supervisedServer(t *testing.T) (*Server, *supervisedFixture) {
	t.Helper()
	f := newSupervisedFixture(t, LifetimeKeep, LaunchSession, nil)
	s := New("test")
	s.UseSupervisedRuntime(f.rt)
	return s, f
}

// requireNotLiveNow requires the not-live state with the no-connection cause
// at the moment of the call, without waiting for it.
func requireNotLiveNow(t *testing.T, rt *SupervisedRuntime) {
	t.Helper()
	nl, cause := rt.NotLive()
	if !nl || cause != CauseNoConnection {
		t.Fatalf("not-live state when the hold-timeout close returned = %v (%s), want set with %s",
			nl, cause, CauseNoConnection)
	}
}

// requireLaterStartRefused requires that a start after the close is refused as
// not live and sends no further launch.
func requireLaterStartRefused(t *testing.T, s *Server, f *supervisedFixture, launches int) {
	t.Helper()
	if err := s.Runtime.Start(startCtx(t, time.Second), "later"); !errors.Is(err, errRuntimeNotLive) {
		t.Fatalf("Start after the hold-timeout close = %v, want errRuntimeNotLive", err)
	}
	if n := f.sup.count(superviseLaunch); n != launches {
		t.Fatalf("launches = %d, want %d", n, launches)
	}
}

// requireListenerClosed requires that the runtime socket refuses a new
// connection, so no later process can connect and serve.
func requireListenerClosed(t *testing.T, f *supervisedFixture) {
	t.Helper()
	c, err := net.DialTimeout("unix", f.runtime, 200*time.Millisecond)
	if err == nil {
		_ = c.Close()
		t.Fatal("the runtime socket accepted a connection after the hold-timeout close")
	}
}

// launchUnconnected sends the first launch with no process dialing and
// cancels the launching start well before its deadline, so the connection
// watch cannot set the no-connection cause during the test.
func launchUnconnected(t *testing.T, f *supervisedFixture) {
	t.Helper()
	f.sup.react(superviseLaunch, fakeReaction{noProcess: true})
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cancelLaunchingStart(t, f, ctx, cancel)
}

// The coordinator hold timeout's close of the runtime connection applies the
// holdTimeout row before it returns: the current generation is ended, the
// not-live state is set with the no-connection cause, and every later start
// is refused. It does so whatever the transport's state: a live connection
// with its reader, a launch whose process has not connected, a connection the
// accept loop installed but no start has read, and no transport at all
// between an attested end and the rebind.
// spec: 4.7.10 (Runtime supervisor, Runtime exits; Runtime supervisor,
// Liveness), 10.1 (Hold state timeout)
// diagnosis: the close only closed the installed connection, and the end and
// the not-live state were left to the connection's reader, so they were set
// after onHoldTimeout's registry pass, or never when no connection was
// installed or no reader had started; a start in that window joined a
// connection being closed and stayed recorded with no coordinator.
func TestSupervisedHoldTimeoutCloseEndsTheGenerationBeforeItReturns_spec_4_7_10(t *testing.T) {
	t.Run("a live connection with its reader", func(t *testing.T) {
		e := newExitsFixture(t, LifetimeKeep, LaunchSession)
		e.hold("a")
		// A start that joined the live process before the close.
		if err := e.s.Runtime.Start(startCtx(t, time.Second), "b"); err != nil {
			t.Fatalf("joining Start: %v", err)
		}
		e.s.endRuntimeForHoldTimeout()
		requireNotLiveNow(t, e.rt)
		if disp, handled := e.rt.handledEnd(1); !handled || disp != EndHoldTimeout {
			t.Fatalf("generation 1 end handled = %v (%s), want handled on %s", handled, disp, EndHoldTimeout)
		}
		if err := e.rt.confirmStart("b"); !errors.Is(err, errRuntimeNotLive) {
			t.Fatalf("confirmStart of the joined start = %v, want errRuntimeNotLive", err)
		}
		if e.crashed("a") {
			t.Fatal("the holdTimeout row failed a session with runtime_crash")
		}
		requireLaterStartRefused(t, e.s, e.supervisedFixture, 1)
		requireListenerClosed(t, e.supervisedFixture)
	})
	t.Run("no launch and no connection", func(t *testing.T) {
		s, f := supervisedServer(t)
		s.endRuntimeForHoldTimeout()
		requireNotLiveNow(t, f.rt)
		requireLaterStartRefused(t, s, f, 0)
		requireListenerClosed(t, f)
	})
	t.Run("a launch whose process has not connected", func(t *testing.T) {
		s, f := supervisedServer(t)
		launchUnconnected(t, f)
		s.endRuntimeForHoldTimeout()
		requireNotLiveNow(t, f.rt)
		if _, handled := f.rt.handledEnd(1); !handled {
			t.Fatal("generation 1 end not handled when the hold-timeout close returned")
		}
		requireListenerClosed(t, f)
		requireLaterStartRefused(t, s, f, 1)
	})
	t.Run("a connection installed with no reader started", func(t *testing.T) {
		s, f := supervisedServer(t)
		launchUnconnected(t, f)
		f.sup.startProcess()
		tr, err := f.rt.current()
		if err != nil {
			t.Fatal(err)
		}
		waitFor(t, "the late connection", tr.hasConnected)
		s.endRuntimeForHoldTimeout()
		requireNotLiveNow(t, f.rt)
		if _, handled := f.rt.handledEnd(1); !handled {
			t.Fatal("generation 1 end not handled when the hold-timeout close returned")
		}
		requireLaterStartRefused(t, s, f, 1)
	})
	t.Run("between an attested end and the rebind", func(t *testing.T) {
		s, f := supervisedServer(t)
		if err := s.Runtime.Start(startCtx(t, 5*time.Second), "a"); err != nil {
			t.Fatal(err)
		}
		f.rt.SetEndDisposition(EndScrub)
		if err := f.rt.EndProcess(context.Background(), 0); err != nil {
			t.Fatal(err)
		}
		requireLive(t, f.rt)
		s.endRuntimeForHoldTimeout()
		requireNotLiveNow(t, f.rt)
		requireLaterStartRefused(t, s, f, 1)
	})
}

// A hold-timeout close that meets a launch in flight waits for that launch's
// reply, and the generation the launch started ends on the holdTimeout row:
// no session fails with runtime_crash, the not-live cause is no-connection,
// and no launch reaches the supervisor after the close has returned.
// spec: 4.7.10 (Runtime supervisor, Runtime exits; Runtime supervisor,
// Launch), 10.1 (Hold state timeout)
// diagnosis: the close set the disposition and read the generation in two
// critical sections with no ordering against a launch, so a launch could
// increment the generation and reset the disposition to none between them,
// and that generation's end took the none row; and a launch that had passed
// its not-live check was sent after the close returned, starting a process
// that could never connect.
func TestSupervisedHoldTimeoutCloseWaitsForTheLaunchInFlight_spec_4_7_10(t *testing.T) {
	s, f := supervisedServer(t)
	f.sup.react(superviseLaunch, fakeReaction{noProcess: true})
	entered := make(chan struct{})
	release := make(chan struct{})
	// The hook runs after the generation increment and before the launch
	// request, which is the window the close must not overtake.
	f.rt.setLaunchHook(func() { close(entered); <-release })
	startDone := make(chan error, 1)
	go func() { startDone <- s.Runtime.Start(startCtx(t, 5*time.Second), "b") }()
	<-entered
	closeDone := make(chan struct{})
	go func() { s.endRuntimeForHoldTimeout(); close(closeDone) }()
	select {
	case <-closeDone:
		t.Fatal("the hold-timeout close returned while a launch was in flight, before its request was sent")
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	<-closeDone
	launchesAtReturn := f.sup.count(superviseLaunch)
	if err := <-startDone; err == nil {
		t.Fatal("the launching start succeeded on a runtime the hold timeout closed")
	}
	requireNotLive(t, f.rt, CauseNoConnection)
	if disp, handled := f.rt.handledEnd(1); !handled || disp != EndHoldTimeout {
		t.Fatalf("generation 1 end handled = %v (%s), want handled on %s", handled, disp, EndHoldTimeout)
	}
	if recordedOn(s, "b") {
		t.Fatal("the launching start was recorded on the generation the close ended")
	}
	requireLaterStartRefused(t, s, f, launchesAtReturn)
	if launchesAtReturn != 1 {
		t.Fatalf("launches when the close returned = %d, want 1", launchesAtReturn)
	}
	requireListenerClosed(t, f)
}
