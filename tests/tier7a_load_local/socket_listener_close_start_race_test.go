// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local concurrency coverage for the pod-scoped runtime
// listener of the sidecar socket transport.
//
// The adapter binds its runtime socket once, when the pod boots, and every
// session the pod serves is accepted on that one listener. A session's
// teardown releases the session and, when it was the last active one, the
// shared runtime connection, but it leaves the listener bound. The window
// this case pins is a few microseconds wide: the last session's Close runs
// while a new session's Start is already in flight. The arriving Start
// either takes the runtime process's lock before the Close clears the
// connection and reuses it, or takes it afterwards and accepts its own
// connection on the still-bound listener. Neither ordering may surface a
// closed-listener error, which is what a teardown that unbinds the address
// produces on the second ordering.
//
// The case carries a stress budget:
//
//	lenny-test stress --test TestLastSessionCloseRacingAnArrivingStartKeepsTheListener_spec_5_2 --runs 50 --pkg ./tests/tier7a_load_local/... --tag load_local
//
// spec: §5.2 (Pool Configuration and Execution Modes), §15.4.3 (Runtime
// Integration Levels).
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

// listenerRaceAcceptTimeout bounds the arriving Start's accept. Its runtime
// dials before the rendezvous, so the connection is already queued in the
// listener's backlog when the Start reaches accept; the bound only stops a
// regression from hanging the case.
const listenerRaceAcceptTimeout = 5 * time.Second

// spec: §5.2 (Pool Configuration and Execution Modes), §15.4.3 (Runtime Integration Levels)
// diagnosis: a failure means the session boundary can still unbind the pod's
// runtime socket address under a concurrent start, so a session arriving as
// the last one departs finds no listener to be accepted on.
func TestLastSessionCloseRacingAnArrivingStartKeepsTheListener_spec_5_2(t *testing.T) {
	for attempt := range raceAttempts {
		t.Run(fmt.Sprintf("attempt_%d", attempt), func(t *testing.T) {
			closeStartListenerAttempt(t)
		})
	}
}

// closeStartListenerAttempt runs one race on a fresh runtime process: the
// last active session's Close against a new session's dial and Start,
// released from a common rendezvous. It then checks that the arriving
// session is served by exactly one live connection and that the address
// still accepts a later session.
func closeStartListenerAttempt(t *testing.T) {
	t.Helper()
	rt, socket := newListenerRaceRuntime(t)
	first := dialRuntimeSocket(t, socket)
	if err := rt.Start(context.Background(), "alice"); err != nil {
		t.Fatalf("start the departing session: %v", err)
	}

	// The arriving session's runtime dials before the rendezvous rather
	// than after it. A dial inside the race is a system call ahead of the
	// Start, long enough that the Close wins every attempt and the reuse
	// ordering is never reached; dialing first puts the Start and the Close
	// on the runtime process's lock at the same instant.
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
		t.Fatalf("the arriving Start = %v, want it to reuse the live connection or accept its own", startErr)
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
	rt, err := adapter.NewSocketRuntimeProcess(socket)
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
// arriving session and checks that it reaches exactly one of the two
// runtime-side connections. When the Start reused the first connection, the
// departing session's Close found a sibling still active and left that
// connection up, so the envelope arrives there. When the Start accepted its
// own connection, the Close tore the first connection down and the envelope
// arrives on the second.
func assertArrivingSessionServedByOneConnection(t *testing.T, rt *adapter.SocketRuntimeProcess, first, second net.Conn) {
	t.Helper()
	const envelope = `{"type":"message","sessionId":"bob"}`
	if err := rt.WriteEnvelope("bob", []byte(envelope)); err != nil {
		t.Fatalf("write the arriving session's envelope: %v", err)
	}
	got := make(chan bool, 2)
	for _, conn := range []net.Conn{first, second} {
		go func(conn net.Conn) { got <- readsEnvelope(conn, envelope) }(conn)
	}
	delivered := 0
	for range 2 {
		if <-got {
			delivered++
		}
	}
	if delivered != 1 {
		t.Errorf("the arriving session's envelope reached %d runtime connections, want exactly 1", delivered)
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
// address is still bound: a new runtime dial succeeds and a later session's
// Start completes on it.
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
