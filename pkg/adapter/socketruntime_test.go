// SPDX-License-Identifier: MIT

package adapter_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter"
)

// runtimeSocketAddr returns a socket address the test binds: a Linux
// abstract address (no filesystem cleanup) or, elsewhere, a short
// filesystem path. The path is kept short because the Unix sun_path
// field is limited to ~104 bytes on darwin and t.TempDir() alone can
// exceed that.
func runtimeSocketAddr(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "linux" {
		return fmt.Sprintf("@lenny-test-rt-%d-%s", os.Getpid(), t.Name())
	}
	return filesystemSocketPath(t)
}

// filesystemSocketPath returns a short, unused filesystem path for a Unix
// socket, removed at test end. A case that asserts on the socket file itself
// uses it on every platform, because an abstract address leaves no file.
func filesystemSocketPath(t *testing.T) string {
	t.Helper()
	f, err := os.CreateTemp("", "rt-*.sock")
	if err != nil {
		t.Fatalf("temp socket path: %v", err)
	}
	path := f.Name()
	_ = f.Close()
	_ = os.Remove(path)
	t.Cleanup(func() { _ = os.Remove(path) })
	return path
}

// dialRuntimeSocket dials the socket the way runtimekit does: an
// "@"-prefixed Linux abstract address maps to a leading NUL.
func dialRuntimeSocket(t *testing.T, socket string) net.Conn {
	t.Helper()
	addr := socket
	if strings.HasPrefix(socket, "@") {
		addr = "\x00" + socket[1:]
	}
	var d net.Dialer
	conn, err := d.DialContext(context.Background(), "unix", addr)
	if err != nil {
		t.Fatalf("dial runtime socket %q: %v", socket, err)
	}
	return conn
}

func TestSocketRuntimeProcessBridgesJSONLFrames(t *testing.T) {
	socket := runtimeSocketAddr(t)
	sp, err := adapter.NewSocketRuntimeProcess(socket)
	if err != nil {
		t.Fatalf("NewSocketRuntimeProcess: %v", err)
	}
	t.Cleanup(func() { _ = sp.CloseListener() })
	defer sp.Close(context.Background(), "s1")

	// The runtime side connects, then Start accepts it.
	connCh := make(chan net.Conn, 1)
	go func() { connCh <- dialRuntimeSocket(t, sp.SocketPath()) }()

	if err := sp.Start(context.Background(), "s1"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	runtimeConn := <-connCh
	defer runtimeConn.Close()

	// The adapter writes an inbound envelope; the runtime side reads it.
	if err := sp.WriteEnvelope("s1", []byte(`{"type":"message","id":"m1"}`)); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}
	line, err := bufio.NewReader(runtimeConn).ReadString('\n')
	if err != nil {
		t.Fatalf("runtime read: %v", err)
	}
	if strings.TrimSpace(line) != `{"type":"message","id":"m1"}` {
		t.Errorf("runtime received %q", line)
	}

	// The runtime writes a response; Output streams it to the adapter.
	out, err := sp.Output(context.Background(), "s1")
	if err != nil {
		t.Fatalf("Output: %v", err)
	}
	if _, err := runtimeConn.Write([]byte(`{"type":"response"}` + "\n")); err != nil {
		t.Fatalf("runtime write: %v", err)
	}
	select {
	case got := <-out:
		if string(got) != `{"type":"response"}` {
			t.Errorf("adapter received %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Output did not deliver the runtime frame")
	}
}

func TestSocketRuntimeProcessStartTimesOutWithoutAConnection(t *testing.T) {
	socket := runtimeSocketAddr(t)
	sp, err := adapter.NewSocketRuntimeProcess(socket)
	if err != nil {
		t.Fatalf("NewSocketRuntimeProcess: %v", err)
	}
	t.Cleanup(func() { _ = sp.CloseListener() })
	defer sp.Close(context.Background(), "s1")
	sp.AcceptTimeout = 150 * time.Millisecond

	// No runtime connects: Start must time out rather than block forever.
	err = sp.Start(context.Background(), "s1")
	if err == nil {
		t.Fatal("Start should fail when no runtime connects")
	}
	if !strings.Contains(err.Error(), "did not connect") {
		t.Errorf("Start error = %v, want an accept-timeout", err)
	}
}

func TestSocketRuntimeProcessOutputClosesOnRuntimeDisconnect(t *testing.T) {
	socket := runtimeSocketAddr(t)
	sp, err := adapter.NewSocketRuntimeProcess(socket)
	if err != nil {
		t.Fatalf("NewSocketRuntimeProcess: %v", err)
	}
	t.Cleanup(func() { _ = sp.CloseListener() })
	defer sp.Close(context.Background(), "s1")

	connCh := make(chan net.Conn, 1)
	go func() { connCh <- dialRuntimeSocket(t, sp.SocketPath()) }()
	if err := sp.Start(context.Background(), "s1"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	runtimeConn := <-connCh

	out, err := sp.Output(context.Background(), "s1")
	if err != nil {
		t.Fatalf("Output: %v", err)
	}
	// §15.4: closing the runtime side is the clean-exit signal; Output
	// must close its channel.
	_ = runtimeConn.Close()
	select {
	case _, ok := <-out:
		if ok {
			t.Error("Output channel should close on runtime disconnect")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Output channel did not close after runtime disconnect")
	}
}

// spec: §5.2, §28.5.3 — one runtime process per pod
// serves every slot over the single connection, so a second Start for a
// sibling slot's session reuses the live connection rather than accepting
// a new one, and WriteEnvelope writes any slot's session over it.
func TestSocketRuntimeProcessStartIsIdempotentAcrossSlots_spec_5_2(t *testing.T) {
	socket := runtimeSocketAddr(t)
	sp, err := adapter.NewSocketRuntimeProcess(socket)
	if err != nil {
		t.Fatalf("NewSocketRuntimeProcess: %v", err)
	}
	t.Cleanup(func() { _ = sp.CloseListener() })
	defer sp.Close(context.Background(), "s1")

	connCh := make(chan net.Conn, 1)
	go func() { connCh <- dialRuntimeSocket(t, sp.SocketPath()) }()
	if err := sp.Start(context.Background(), "sess-a"); err != nil {
		t.Fatalf("Start(sess-a): %v", err)
	}
	runtimeConn := <-connCh
	defer runtimeConn.Close()

	// A second Start, for a sibling slot's session, reuses the connection.
	if err := sp.Start(context.Background(), "sess-b"); err != nil {
		t.Fatalf("Start(sess-b) must reuse the live connection: %v", err)
	}

	// Each session writes over the one connection; the runtime reads both.
	reader := bufio.NewReader(runtimeConn)
	for _, frame := range []string{`{"type":"message","sessionId":"sess-a"}`, `{"type":"message","sessionId":"sess-b"}`} {
		if err := sp.WriteEnvelope("ignored", []byte(frame)); err != nil {
			t.Fatalf("WriteEnvelope(%s): %v", frame, err)
		}
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("runtime read: %v", err)
		}
		if strings.TrimSpace(line) != frame {
			t.Errorf("runtime received %q, want %q", strings.TrimSpace(line), frame)
		}
	}
}

// spec: §28.5.3 — the single runtime connection fans every frame
// out to all Output subscribers, so two concurrent per-slot Attach streams
// each receive the runtime's full output and demultiplex by sessionId. A
// subscriber that arrives after Start still sees frames written after it
// subscribes.
func TestSocketRuntimeProcessFansOutToConcurrentSubscribers_spec_15_4(t *testing.T) {
	socket := runtimeSocketAddr(t)
	sp, err := adapter.NewSocketRuntimeProcess(socket)
	if err != nil {
		t.Fatalf("NewSocketRuntimeProcess: %v", err)
	}
	t.Cleanup(func() { _ = sp.CloseListener() })
	defer sp.Close(context.Background(), "s1")

	connCh := make(chan net.Conn, 1)
	go func() { connCh <- dialRuntimeSocket(t, sp.SocketPath()) }()
	if err := sp.Start(context.Background(), "sess-a"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	runtimeConn := <-connCh
	defer runtimeConn.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	outA, err := sp.Output(ctx, "sess-a")
	if err != nil {
		t.Fatalf("Output(sess-a): %v", err)
	}
	outB, err := sp.Output(ctx, "sess-b")
	if err != nil {
		t.Fatalf("Output(sess-b): %v", err)
	}

	frame := `{"type":"response","sessionId":"sess-a"}`
	if _, err := runtimeConn.Write([]byte(frame + "\n")); err != nil {
		t.Fatalf("runtime write: %v", err)
	}
	for name, out := range map[string]<-chan []byte{"sess-a": outA, "sess-b": outB} {
		select {
		case got := <-out:
			if string(got) != frame {
				t.Errorf("%s subscriber received %q, want %q", name, got, frame)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%s subscriber did not receive the fanned-out frame", name)
		}
	}
}

// spec: §5.2 — a
// per-slot Close on one slot while a sibling slot is still active must not
// tear the shared runtime connection down: the sibling keeps writing and
// reading over it. Only the last slot's Close closes the connection.
//
// diagnosis: a failure here means a normal completion of one slot destroys
// the shared connection siblings are still using, so per-slot multiplexing
// over the single connection regressed to a whole-pod teardown.
func TestSocketRuntimeProcessCloseScopedToSlot_spec_5_2(t *testing.T) {
	socket := runtimeSocketAddr(t)
	sp, err := adapter.NewSocketRuntimeProcess(socket)
	if err != nil {
		t.Fatalf("NewSocketRuntimeProcess: %v", err)
	}
	t.Cleanup(func() { _ = sp.CloseListener() })
	defer sp.Close(context.Background(), "sess-b")

	connCh := make(chan net.Conn, 1)
	go func() { connCh <- dialRuntimeSocket(t, sp.SocketPath()) }()
	if err := sp.Start(context.Background(), "sess-a"); err != nil {
		t.Fatalf("Start(sess-a): %v", err)
	}
	runtimeConn := <-connCh
	defer runtimeConn.Close()
	if err := sp.Start(context.Background(), "sess-b"); err != nil {
		t.Fatalf("Start(sess-b): %v", err)
	}

	// sess-a completes normally. The shared connection must survive because
	// sess-b is still active.
	if err := sp.Close(context.Background(), "sess-a"); err != nil {
		t.Fatalf("Close(sess-a): %v", err)
	}

	// sess-b can still write over the shared connection and the runtime
	// reads it: the per-slot Close did not EOF sess-b's transport.
	reader := bufio.NewReader(runtimeConn)
	frame := `{"type":"message","sessionId":"sess-b"}`
	if err := sp.WriteEnvelope("sess-b", []byte(frame)); err != nil {
		t.Fatalf("WriteEnvelope after sibling Close: %v", err)
	}
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("runtime read after sibling Close: %v", err)
	}
	if strings.TrimSpace(line) != frame {
		t.Errorf("runtime received %q after sibling Close, want %q", strings.TrimSpace(line), frame)
	}

	// The last slot's Close tears the shared connection down: the runtime
	// observes EOF.
	if err := sp.Close(context.Background(), "sess-b"); err != nil {
		t.Fatalf("Close(sess-b): %v", err)
	}
	if _, err := reader.ReadString('\n'); err == nil {
		t.Error("runtime should observe EOF after the last slot's Close")
	}
}

// spec: §5.2 — a clean Interrupt (the §28.5.3
// heartbeat-hung SIGTERM) on one slot while a sibling is active must not
// close the shared connection: only the last active slot's Interrupt EOFs
// the runtime.
//
// diagnosis: a failure here means one slot's heartbeat timeout kills the
// shared connection every sibling depends on, regressing slot independence
// over the single connection.
func TestSocketRuntimeProcessInterruptScopedToSlot_spec_5_2(t *testing.T) {
	socket := runtimeSocketAddr(t)
	sp, err := adapter.NewSocketRuntimeProcess(socket)
	if err != nil {
		t.Fatalf("NewSocketRuntimeProcess: %v", err)
	}
	t.Cleanup(func() { _ = sp.CloseListener() })
	defer sp.Close(context.Background(), "sess-b")

	connCh := make(chan net.Conn, 1)
	go func() { connCh <- dialRuntimeSocket(t, sp.SocketPath()) }()
	if err := sp.Start(context.Background(), "sess-a"); err != nil {
		t.Fatalf("Start(sess-a): %v", err)
	}
	runtimeConn := <-connCh
	defer runtimeConn.Close()
	if err := sp.Start(context.Background(), "sess-b"); err != nil {
		t.Fatalf("Start(sess-b): %v", err)
	}

	// sess-a's heartbeat hangs: the Attach loop sends the clean Interrupt.
	// The shared connection must survive because sess-b is still active.
	if err := sp.Interrupt(context.Background(), "sess-a", false); err != nil {
		t.Fatalf("Interrupt(sess-a): %v", err)
	}

	reader := bufio.NewReader(runtimeConn)
	frame := `{"type":"message","sessionId":"sess-b"}`
	if err := sp.WriteEnvelope("sess-b", []byte(frame)); err != nil {
		t.Fatalf("WriteEnvelope after sibling Interrupt: %v", err)
	}
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("runtime read after sibling Interrupt: %v", err)
	}
	if strings.TrimSpace(line) != frame {
		t.Errorf("runtime received %q after sibling Interrupt, want %q", strings.TrimSpace(line), frame)
	}

	// Interrupting the last active slot closes the shared connection.
	if err := sp.Interrupt(context.Background(), "sess-b", false); err != nil {
		t.Fatalf("Interrupt(sess-b): %v", err)
	}
	if _, err := reader.ReadString('\n'); err == nil {
		t.Error("runtime should observe EOF after the last slot's Interrupt")
	}
}

// dialRuntimeSocketErr dials the socket the way dialRuntimeSocket does but
// returns the error, so a test can report a refused second-generation dial
// with a diagnosable message rather than a helper fatal.
func dialRuntimeSocketErr(socket string) (net.Conn, error) {
	addr := socket
	if strings.HasPrefix(socket, "@") {
		addr = "\x00" + socket[1:]
	}
	var d net.Dialer
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return d.DialContext(ctx, "unix", addr)
}

// startGeneration dials the runtime socket and runs Start for sessionID,
// returning the runtime side of the accepted connection. A refused dial or a
// failed Start fails the test with the error text, which names a closed
// listener when a session teardown unbound the pod's address.
func startGeneration(t *testing.T, sp *adapter.SocketRuntimeProcess, sessionID string) net.Conn {
	t.Helper()
	type dialResult struct {
		conn net.Conn
		err  error
	}
	dialed := make(chan dialResult, 1)
	go func() {
		c, err := dialRuntimeSocketErr(sp.SocketPath())
		dialed <- dialResult{conn: c, err: err}
	}()
	startErr := sp.Start(context.Background(), sessionID)
	d := <-dialed
	if d.err != nil {
		t.Fatalf("runtime dial for %s after an earlier session ended: %v (the pod's listener was unbound by a session teardown)", sessionID, d.err)
	}
	if startErr != nil {
		_ = d.conn.Close()
		t.Fatalf("Start(%s) after an earlier session ended: %v (a 'use of closed network connection' error means a session teardown closed the pod's listener)", sessionID, startErr)
	}
	return d.conn
}

// roundTrip writes one adapter-to-runtime envelope and one runtime-to-adapter
// frame over a generation's connection and asserts both arrive.
func roundTrip(t *testing.T, sp *adapter.SocketRuntimeProcess, sessionID string, runtimeConn net.Conn) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out, err := sp.Output(ctx, sessionID)
	if err != nil {
		t.Fatalf("Output(%s): %v", sessionID, err)
	}
	if err := sp.WriteEnvelope(sessionID, []byte(`{"type":"message","sessionId":"`+sessionID+`"}`)); err != nil {
		t.Fatalf("WriteEnvelope(%s): %v", sessionID, err)
	}
	_ = runtimeConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	got, err := bufio.NewReader(runtimeConn).ReadString('\n')
	if err != nil || !strings.Contains(got, sessionID) {
		t.Fatalf("runtime read for %s = %q, %v", sessionID, got, err)
	}
	want := `{"type":"response","sessionId":"` + sessionID + `"}`
	if _, err := runtimeConn.Write([]byte(want + "\n")); err != nil {
		t.Fatalf("runtime write for %s: %v", sessionID, err)
	}
	select {
	case line, ok := <-out:
		if !ok || string(line) != want {
			t.Fatalf("Output(%s) yielded %q (open=%v), want %q", sessionID, line, ok, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("Output(%s) yielded no frame within 5s", sessionID)
	}
}

// spec: §15.4.3 (runtime integration levels), §5.2 (pool configuration and execution modes), §4.7.9 (startup sequence for type: agent runtimes), §28.5.3 (intra-pod)
//
// The pod's runtime listener is bound once at adapter start and every
// session the pod serves is accepted on it, so the last session's Close
// releases the shared connection and leaves the address bound. A second
// session on the same pod then dials the same address and is accepted.
//
// diagnosis: a failure here means a session teardown unbound the pod's
// runtime socket, so no recycling pod can serve a second session.
func TestSocketRuntimeListenerOutlivesTheLastSessionAndAcceptsTheNext_spec_15_4_3(t *testing.T) {
	sp, err := adapter.NewSocketRuntimeProcess(runtimeSocketAddr(t))
	if err != nil {
		t.Fatalf("NewSocketRuntimeProcess: %v", err)
	}
	t.Cleanup(func() { _ = sp.CloseListener() })
	addr := sp.SocketPath()

	first := startGeneration(t, sp, "sess-a")
	defer first.Close()
	roundTrip(t, sp, "sess-a", first)
	if err := sp.Close(context.Background(), "sess-a"); err != nil {
		t.Fatalf("Close(sess-a) = %v, want nil: the last-session teardown leaves the listener bound", err)
	}

	second := startGeneration(t, sp, "sess-b")
	defer second.Close()
	roundTrip(t, sp, "sess-b", second)
	if got := sp.SocketPath(); got != addr {
		t.Errorf("SocketPath() = %q after the second session's Start, want the address bound at construction %q", got, addr)
	}
	if err := sp.Close(context.Background(), "sess-b"); err != nil {
		t.Fatalf("Close(sess-b): %v", err)
	}
}

// spec: §5.2 (pool configuration and execution modes), §28.5.3 (intra-pod), §15.4.3 (runtime integration levels)
//
// Each accepted connection's fan-out reader delivers to and closes only the
// subscribers registered against that connection. A first connection's
// reader is parked on a stalled subscriber when its session ends and the
// next connection is accepted; once released, the reader drains the frames
// it had already buffered and exits on the closed connection. None of
// those frames, and not its exit, may reach the second connection's
// subscriber.
//
// diagnosis: a failure here means a departing connection's reader broadcast
// into, or closed, the next session's output stream, so a recycled pod's
// second session receives the first runtime's frames or loses its stream.
func TestSocketRuntimeDepartingReaderLeavesTheNextConnectionsSubscribers_spec_5_2(t *testing.T) {
	sp, err := adapter.NewSocketRuntimeProcess(runtimeSocketAddr(t))
	if err != nil {
		t.Fatalf("NewSocketRuntimeProcess: %v", err)
	}
	t.Cleanup(func() { _ = sp.CloseListener() })

	first := startGeneration(t, sp, "sess-a")
	defer first.Close()
	ctxA, cancelA := context.WithCancel(context.Background())
	defer cancelA()
	if _, err := sp.Output(ctxA, "sess-a"); err != nil {
		t.Fatalf("Output(sess-a): %v", err)
	}
	// The subscriber's channel is never read, so its pump holds one frame
	// and its buffered intake fills; the remaining frames park the reader
	// with the rest already buffered in its scanner.
	const frames = 200
	var batch strings.Builder
	for i := 0; i < frames; i++ {
		fmt.Fprintf(&batch, `{"type":"response","sessionId":"sess-a","seq":%d}`+"\n", i)
	}
	if _, err := first.Write([]byte(batch.String())); err != nil {
		t.Fatalf("first runtime write: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	if err := sp.Close(context.Background(), "sess-a"); err != nil {
		t.Fatalf("Close(sess-a): %v", err)
	}
	second := startGeneration(t, sp, "sess-b")
	defer second.Close()
	ctxB, cancelB := context.WithCancel(context.Background())
	defer cancelB()
	outB, err := sp.Output(ctxB, "sess-b")
	if err != nil {
		t.Fatalf("Output(sess-b): %v", err)
	}

	// Release the parked reader of the first connection.
	cancelA()
	select {
	case line, ok := <-outB:
		if !ok {
			t.Fatal("sess-b's output closed when the first connection's reader exited")
		}
		t.Fatalf("sess-b's output received %q from the first connection's reader", line)
	case <-time.After(500 * time.Millisecond):
	}

	want := `{"type":"response","sessionId":"sess-b"}`
	if _, err := second.Write([]byte(want + "\n")); err != nil {
		t.Fatalf("second runtime write: %v", err)
	}
	select {
	case line, ok := <-outB:
		if !ok || string(line) != want {
			t.Fatalf("sess-b's first value = %q (open=%v), want %q", line, ok, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("sess-b's output yielded no frame within 5s")
	}
	_ = sp.Close(context.Background(), "sess-b")
}

// spec: §28.5.3 (intra-pod), §4.7.10 (deployment model), §5.2 (pool configuration and execution modes)
//
// CloseListener releases the pod-scoped runtime listener the adapter process
// owns. It leaves the live runtime connection serving its session, it makes
// the address refuse a new dial, and a repeated call returns nil, because
// test cleanups and the adapter's exit path may both reach it.
//
// diagnosis: a failure here means releasing the runtime socket at adapter
// exit either tore down the session's live connection, left the address
// accepting dials, or failed on a second release.
func TestSocketRuntimeCloseListenerReleasesOnlyTheListenerAndIsIdempotent_spec_28_5_3(t *testing.T) {
	sp, err := adapter.NewSocketRuntimeProcess(runtimeSocketAddr(t))
	if err != nil {
		t.Fatalf("NewSocketRuntimeProcess: %v", err)
	}
	t.Cleanup(func() { _ = sp.CloseListener() })
	addr := sp.SocketPath()

	conn := startGeneration(t, sp, "sess-a")
	defer conn.Close()

	if err := sp.CloseListener(); err != nil {
		t.Fatalf("CloseListener() = %v, want nil", err)
	}
	// The accepted connection is not the listener's: the session keeps
	// round-tripping frames after the listener is gone.
	roundTrip(t, sp, "sess-a", conn)

	if c, err := dialRuntimeSocketErr(addr); err == nil {
		_ = c.Close()
		t.Fatalf("dial %q after CloseListener succeeded, want the address unbound", addr)
	}

	if err := sp.CloseListener(); err != nil {
		t.Fatalf("second CloseListener() = %v, want nil", err)
	}
	if err := sp.Close(context.Background(), "sess-a"); err != nil {
		t.Fatalf("Close(sess-a) after CloseListener = %v, want nil", err)
	}
}

// assertDialable fails the test unless a fresh runtime dial to addr connects.
// The probe connection is closed at once; it stays queued in the listener's
// backlog, so a caller must not run another Start on the process after it.
func assertDialable(t *testing.T, addr, when string) {
	t.Helper()
	c, err := dialRuntimeSocketErr(addr)
	if err != nil {
		t.Fatalf("runtime dial to %q %s: %v (the pod's listener is no longer bound)", addr, when, err)
	}
	_ = c.Close()
}

// spec: §5.2 (pool configuration and execution modes), §15.4.3 (runtime integration levels), §28.5.3 (intra-pod)
//
// A clean Interrupt of the last active session closes the shared connection
// and leaves the pod's runtime listener bound, the same as the last
// session's Close, so the next session's runtime can still dial the address.
//
// diagnosis: a failure here means the last-slot Interrupt path unbinds the
// pod's runtime socket, so a pod whose last session ended on a heartbeat
// timeout cannot accept another runtime connection.
func TestSocketRuntimeInterruptLeavesTheListenerBound_spec_5_2(t *testing.T) {
	sp, err := adapter.NewSocketRuntimeProcess(runtimeSocketAddr(t))
	if err != nil {
		t.Fatalf("NewSocketRuntimeProcess: %v", err)
	}
	t.Cleanup(func() { _ = sp.CloseListener() })

	conn := startGeneration(t, sp, "sess-a")
	defer conn.Close()
	if err := sp.Interrupt(context.Background(), "sess-a", false); err != nil {
		t.Fatalf("Interrupt(sess-a) = %v, want nil", err)
	}
	assertDialable(t, sp.SocketPath(), "after the last session's Interrupt")
}

// spec: §5.2 (pool configuration and execution modes), §15.4.3 (runtime integration levels), §4.7 (runtime adapter)
//
// A session teardown never reports an error from the pod's listener, so a
// socket runtime's per-session scrub cannot reach the leaked outcome through
// it. Across two sessions on one pod, each last-session Close returns nil, a
// repeated Close for a released session returns nil, and a Close on a process
// that never accepted a connection returns nil and leaves the address bound.
//
// diagnosis: a failure here means a per-session Close surfaces a listener
// error or unbinds the address, so the session scrub reports a spurious leak
// and the pod loses its runtime ingress.
func TestSocketRuntimeCloseReportsNoListenerErrorAcrossGenerations_spec_5_2(t *testing.T) {
	sp, err := adapter.NewSocketRuntimeProcess(runtimeSocketAddr(t))
	if err != nil {
		t.Fatalf("NewSocketRuntimeProcess: %v", err)
	}
	t.Cleanup(func() { _ = sp.CloseListener() })

	for _, sessionID := range []string{"sess-a", "sess-b"} {
		conn := startGeneration(t, sp, sessionID)
		defer conn.Close()
		if err := sp.Close(context.Background(), sessionID); err != nil {
			t.Fatalf("last-session Close(%s) = %v, want nil", sessionID, err)
		}
		if err := sp.Close(context.Background(), sessionID); err != nil {
			t.Fatalf("repeated Close(%s) = %v, want nil", sessionID, err)
		}
	}

	idle, err := adapter.NewSocketRuntimeProcess(runtimeSocketAddr(t) + "-idle")
	if err != nil {
		t.Fatalf("NewSocketRuntimeProcess(idle): %v", err)
	}
	t.Cleanup(func() { _ = idle.CloseListener() })
	if err := idle.Close(context.Background(), "sess-c"); err != nil {
		t.Fatalf("Close(sess-c) on a never-connected process = %v, want nil", err)
	}
	assertDialable(t, idle.SocketPath(), "after a Close on a never-connected process")
}

// spec: §4.7.10 (deployment model), §5.2 (pool configuration and execution modes), §15.4.3 (runtime integration levels), §28.5.3 (intra-pod)
//
// The pod's runtime address is bound once at construction and released only
// by CloseListener. Session teardowns leave it bound and never rebind it, so
// SocketPath is the same address across sessions. CloseListener unbinds it:
// a dial is refused, the filesystem socket file is removed, a following Start
// fails at once with the closed-listener error instead of waiting out the
// accept timeout, and a second CloseListener returns nil.
//
// diagnosis: a failure here means the adapter's exit path no longer releases
// the runtime socket (a filesystem-path socket then blocks a restarted
// adapter from binding), or a session teardown rebinds or unbinds the
// address the runtime was told to dial.
func TestSocketRuntimeCloseListenerUnbindsTheAddress_spec_4_7(t *testing.T) {
	path := filesystemSocketPath(t)
	sp, err := adapter.NewSocketRuntimeProcess(path)
	if err != nil {
		t.Fatalf("NewSocketRuntimeProcess: %v", err)
	}
	t.Cleanup(func() { _ = sp.CloseListener() })
	addr := sp.SocketPath()
	assertSocketPath := func(when string) {
		t.Helper()
		if got := sp.SocketPath(); got != addr {
			t.Fatalf("SocketPath() = %q %s, want the address bound at construction %q", got, when, addr)
		}
	}

	first := startGeneration(t, sp, "sess-a")
	defer first.Close()
	if err := sp.Close(context.Background(), "sess-a"); err != nil {
		t.Fatalf("Close(sess-a): %v", err)
	}
	assertSocketPath("after the last session's Close")
	second := startGeneration(t, sp, "sess-b")
	defer second.Close()
	assertSocketPath("after the second session's Start")
	if err := sp.Close(context.Background(), "sess-b"); err != nil {
		t.Fatalf("Close(sess-b): %v", err)
	}

	assertDialable(t, addr, "after the last session's Close")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("socket file %q before CloseListener: %v, want it present", path, err)
	}

	if err := sp.CloseListener(); err != nil {
		t.Fatalf("CloseListener() = %v, want nil", err)
	}
	if c, err := dialRuntimeSocketErr(addr); err == nil {
		_ = c.Close()
		t.Fatalf("dial %q after CloseListener succeeded, want the address unbound", addr)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket file %q after CloseListener: stat error %v, want it removed", path, err)
	}

	sp.AcceptTimeout = 10 * time.Second
	began := time.Now()
	err = sp.Start(context.Background(), "sess-c")
	if !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Start after CloseListener = %v, want an error wrapping net.ErrClosed", err)
	}
	if elapsed := time.Since(began); elapsed >= sp.AcceptTimeout/2 {
		t.Fatalf("Start after CloseListener returned after %s, want it to fail at once rather than wait out the %s accept timeout", elapsed, sp.AcceptTimeout)
	}

	if err := sp.CloseListener(); err != nil {
		t.Fatalf("second CloseListener() = %v, want nil", err)
	}
}
