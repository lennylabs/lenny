// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local concurrency coverage for the pod-scoped runtime
// listener of the sidecar socket transport.
//
// The adapter binds its runtime socket once, when the pod boots, and accepts
// the runtime's connection at the pod's first session start. The runtime
// process lives as long as the pod, so a session's teardown, including the
// last one before occupancy zero, ends nothing: the connection and the
// listener both stay up, and only the pod-scope teardown closes them. The
// window this case pins is a few microseconds wide: the last session's Close
// runs while a new session's Start is already in flight. On every ordering
// the arriving Start returns on the kept connection without an accept, and
// neither call surfaces a closed-listener error. A second runtime dial sits
// unaccepted in the listener's backlog, because the transport accepts one
// connection for the pod's life.
//
// The case carries a stress budget:
//
//	lenny-test stress --test TestLastSessionCloseRacingAnArrivingStartKeepsTheListener_spec_5_2 --runs 50 --pkg ./tests/tier7a_load_local/... --tag load_local
//
// spec: §5.2 (Pool Configuration and Execution Modes), §15.4.3 (Runtime
// Integration Levels), §4.7.10 (Runtime process lifetime).
package tier7a_load_local_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter"
)

// listenerRaceAcceptTimeout bounds the first Start's accept. Its runtime
// dials before the Start, so the connection is already queued in the
// listener's backlog when the Start reaches accept; the bound only stops a
// regression from hanging the case.
const listenerRaceAcceptTimeout = 5 * time.Second

// spec: §5.2 (Pool Configuration and Execution Modes), §15.4.3 (Runtime Integration Levels), §4.7.10 (Runtime process lifetime)
// diagnosis: a failure means the session boundary can still end the pod's
// runtime connection or unbind its address under a concurrent start, so a
// session arriving as the last one departs is not served by the runtime
// process the pod kept.
func TestLastSessionCloseRacingAnArrivingStartKeepsTheListener_spec_5_2(t *testing.T) {
	for attempt := range raceAttempts {
		t.Run(fmt.Sprintf("attempt_%d", attempt), func(t *testing.T) {
			closeStartListenerAttempt(t)
		})
	}
}

// closeStartListenerAttempt runs one race on a fresh runtime process: the
// last active session's Close against a new session's Start, released from
// a common rendezvous. It then checks that the arriving session is served by
// the kept connection and that a later session still starts on it.
func closeStartListenerAttempt(t *testing.T) {
	t.Helper()
	rt, socket := newListenerRaceRuntime(t)
	first := dialRuntimeSocket(t, socket)
	if err := rt.Start(context.Background(), "alice"); err != nil {
		t.Fatalf("start the departing session: %v", err)
	}

	// A second runtime dial is queued before the rendezvous. The transport
	// never accepts it, because the arriving Start rides the kept
	// connection; it stands in for a runtime that redials, which must not
	// take the session over.
	second := dialRuntimeSocket(t, socket)
	var closeErr, startErr error
	rendezvous := newRaceStart(2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		rendezvous.arrive()
		closeErr = rt.Close(context.Background(), "alice")
	}()
	go func() {
		defer wg.Done()
		rendezvous.arrive()
		startErr = rt.Start(context.Background(), "bob")
	}()
	rendezvous.release(t)
	wg.Wait()

	if closeErr != nil {
		t.Errorf("Close of the last active session = %v, want nil", closeErr)
	}
	if startErr != nil {
		if errors.Is(startErr, net.ErrClosed) {
			t.Fatalf("the arriving Start hit a closed listener: %v", startErr)
		}
		t.Fatalf("the arriving Start = %v, want it to return on the kept connection", startErr)
	}
	assertArrivingSessionServedByOneConnection(t, rt, first, second)
	assertListenerStillAccepts(t, rt, socket)
}

// newListenerRaceRuntime binds a SocketRuntimeProcess on a fresh
// filesystem-path socket and registers the pod-exit release of its
// listener. The cleanup is registered first so it runs after every later
// cleanup, as the adapter process releases the listener only at exit.
func newListenerRaceRuntime(t *testing.T) (*adapter.SocketRuntimeProcess, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "lenny-rt-*")
	if err != nil {
		t.Fatalf("temp runtime socket dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "r.sock")
	rt, err := adapter.NewSocketRuntimeProcess(socket, adapter.SocketPeerAuth{ExpectedUID: uint32(os.Getuid())})
	if err != nil {
		t.Fatalf("NewSocketRuntimeProcess: %v", err)
	}
	t.Cleanup(func() { _ = rt.CloseListener() })
	rt.AcceptTimeout = listenerRaceAcceptTimeout
	return rt, socket
}

// dialRuntimeSocket stands in for the pod's runtime process connecting to
// the adapter's address.
func dialRuntimeSocket(t *testing.T, socket string) net.Conn {
	t.Helper()
	conn, err := net.Dial("unix", socket)
	if err != nil {
		t.Fatalf("dial runtime socket: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// assertArrivingSessionServedByOneConnection writes one envelope for the
// arriving session and checks that it reaches the first, kept runtime-side
// connection alone. The departing session's Close ends nothing, so the
// arriving Start rides the connection accepted at the pod's first session
// start, and the second dial is never accepted.
func assertArrivingSessionServedByOneConnection(t *testing.T, rt *adapter.SocketRuntimeProcess, first, second net.Conn) {
	t.Helper()
	const envelope = `{"type":"message","sessionId":"bob"}`
	if err := rt.WriteEnvelope("bob", []byte(envelope)); err != nil {
		t.Fatalf("write the arriving session's envelope: %v", err)
	}
	onFirst := make(chan bool, 1)
	onSecond := make(chan bool, 1)
	go func() { onFirst <- readsEnvelope(first, envelope) }()
	go func() { onSecond <- readsEnvelope(second, envelope) }()
	if !<-onFirst {
		t.Error("the arriving session's envelope did not reach the kept runtime connection")
	}
	if <-onSecond {
		t.Error("the arriving session's envelope reached a second runtime connection, want the kept one alone")
	}
}

// readsEnvelope reports whether the next line conn yields is envelope. A
// connection the adapter closed yields EOF, and a live connection that
// carries nothing hits the read deadline; both report false.
func readsEnvelope(conn net.Conn, envelope string) bool {
	_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return false
	}
	return strings.TrimSpace(line) == envelope
}

// assertListenerStillAccepts ends the arriving session and checks that the
// address is still bound and that a later session's Start completes on the
// kept connection.
func assertListenerStillAccepts(t *testing.T, rt *adapter.SocketRuntimeProcess, socket string) {
	t.Helper()
	if err := rt.Close(context.Background(), "bob"); err != nil {
		t.Errorf("Close of the arriving session = %v, want nil", err)
	}
	dialRuntimeSocket(t, socket)
	if err := rt.Start(context.Background(), "carol"); err != nil {
		t.Errorf("Start of a later session on the same address = %v, want nil", err)
	}
	if err := rt.Close(context.Background(), "carol"); err != nil {
		t.Errorf("Close of the later session = %v, want nil", err)
	}
}
