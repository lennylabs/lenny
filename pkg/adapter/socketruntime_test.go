// SPDX-License-Identifier: MIT

package adapter_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
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

// spec: §5.2, §4.7.10 (Runtime process lifetime) — a per-slot Close on one
// slot while a sibling slot is still active must not tear the shared runtime
// connection down: the sibling keeps writing and reading over it. The last
// slot's Close leaves it up too, because the connection lives as long as the
// pod.
//
// diagnosis: a failure here means a normal completion of one slot destroys
// the shared connection siblings or later sessions are still using, so
// per-slot multiplexing over the single connection regressed to a whole-pod
// teardown.
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

	// The last slot's Close leaves the connection up: the runtime reads a
	// further frame rather than EOF.
	if err := sp.Close(context.Background(), "sess-b"); err != nil {
		t.Fatalf("Close(sess-b): %v", err)
	}
	requireRuntimeReadsFrame(t, sp, runtimeConn, reader, "after the last slot's Close")
}

// requireRuntimeReadsFrame writes one envelope and fails the test unless the
// runtime reads it over runtimeConn, which shows the connection is still up.
func requireRuntimeReadsFrame(t *testing.T, sp *adapter.SocketRuntimeProcess, runtimeConn net.Conn, reader *bufio.Reader, when string) {
	t.Helper()
	frame := `{"type":"message","sessionId":"sess-next"}`
	if err := sp.WriteEnvelope("sess-next", []byte(frame)); err != nil {
		t.Fatalf("WriteEnvelope %s: %v", when, err)
	}
	_ = runtimeConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("runtime read %s: %v, want the frame (the connection was closed)", when, err)
	}
	if strings.TrimSpace(line) != frame {
		t.Errorf("runtime received %q %s, want %q", strings.TrimSpace(line), when, frame)
	}
}

// spec: §5.2, §4.7.10 (Runtime process lifetime) — a clean Interrupt (the
// §28.5.3 heartbeat-hung interrupt) on one slot while a sibling is active
// must not close the shared connection, and neither does the last active
// slot's Interrupt, because the connection lives as long as the pod.
//
// diagnosis: a failure here means one slot's heartbeat timeout kills the
// shared connection every sibling and later session depends on, regressing
// slot independence over the single connection.
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

	// Interrupting the last active slot leaves the shared connection up.
	if err := sp.Interrupt(context.Background(), "sess-b", false); err != nil {
		t.Fatalf("Interrupt(sess-b): %v", err)
	}
	requireRuntimeReadsFrame(t, sp, runtimeConn, reader, "after the last slot's Interrupt")
}

// dialRuntimeSocketErr dials the socket the way dialRuntimeSocket does but
// returns the error, so a test can report a refused dial with a diagnosable
// message rather than a helper fatal.
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

// startGeneration dials the runtime socket and runs the pod's first Start,
// for sessionID, returning the runtime side of the accepted connection. A
// refused dial or a failed Start fails the test with the error text. Later
// sessions on the same pod call Start directly: they ride the accepted
// connection and dial nothing.
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
		t.Fatalf("runtime dial for %s: %v", sessionID, d.err)
	}
	if startErr != nil {
		_ = d.conn.Close()
		t.Fatalf("Start(%s): %v", sessionID, startErr)
	}
	return d.conn
}

// roundTrip writes one adapter-to-runtime envelope and one runtime-to-adapter
// frame over the pod's runtime connection and asserts both arrive.
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

// spec: §4.7.10 (Runtime process lifetime), §28.5.3 (intra-pod), §5.2 (pool configuration and execution modes)
//
// CloseListener is the transport's pod-scope teardown. It ends the live
// runtime connection, so the runtime reads EOF and the session's output
// stream closes, it makes the address refuse a new dial, and a repeated call
// returns nil, because test cleanups and the adapter's exit path may both
// reach it.
//
// diagnosis: a failure here means the pod-scope teardown left the runtime's
// connection up (a kept runtime goes on holding a terminated session's
// credentials), left the address accepting dials, or failed on a second
// call.
func TestSocketRuntimeCloseListenerEndsTheConnectionAndIsIdempotent_spec_4_7_10(t *testing.T) {
	sp, err := adapter.NewSocketRuntimeProcess(runtimeSocketAddr(t))
	if err != nil {
		t.Fatalf("NewSocketRuntimeProcess: %v", err)
	}
	t.Cleanup(func() { _ = sp.CloseListener() })
	addr := sp.SocketPath()

	conn := startGeneration(t, sp, "sess-a")
	defer conn.Close()
	roundTrip(t, sp, "sess-a", conn)
	out, err := sp.Output(context.Background(), "sess-a")
	if err != nil {
		t.Fatalf("Output(sess-a): %v", err)
	}

	if err := sp.CloseListener(); err != nil {
		t.Fatalf("CloseListener() = %v, want nil", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := bufio.NewReader(conn).ReadString('\n'); !errors.Is(err, io.EOF) {
		t.Fatalf("runtime read after CloseListener = %v, want io.EOF", err)
	}
	requireOutputCloses(t, out, "after CloseListener")

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

// requireOutputCloses fails the test unless out closes within 2 seconds,
// discarding any frame still buffered ahead of the close.
func requireOutputCloses(t *testing.T, out <-chan []byte, when string) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-out:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatalf("Output channel did not close %s", when)
		}
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
// A clean Interrupt of the last active session ends nothing: it leaves the
// shared connection up and the pod's runtime listener bound, the same as the
// last session's Close. Only the pod-scope teardown unbinds the address.
//
// diagnosis: a failure here means the last-slot Interrupt path unbinds the
// pod's runtime socket, which only the pod-scope teardown may do.
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
// it. Across two sequential sessions on one pod's connection, each
// last-session Close returns nil, a repeated Close for a released session
// returns nil, and a Close on a process that never accepted a connection
// returns nil and leaves the address bound.
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

	conn := startGeneration(t, sp, "sess-a")
	defer conn.Close()
	for _, sessionID := range []string{"sess-a", "sess-b"} {
		if err := sp.Start(context.Background(), sessionID); err != nil {
			t.Fatalf("Start(%s): %v", sessionID, err)
		}
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
// fails at once with the ended error instead of waiting out the accept
// timeout, and a second CloseListener returns nil.
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
	if err := sp.Start(context.Background(), "sess-b"); err != nil {
		t.Fatalf("Start(sess-b): %v", err)
	}
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
	if !errors.Is(err, adapter.ErrRuntimeConnectionEnded) {
		t.Fatalf("Start after CloseListener = %v, want the runtime-connection-ended error", err)
	}
	if elapsed := time.Since(began); elapsed >= sp.AcceptTimeout/2 {
		t.Fatalf("Start after CloseListener returned after %s, want it to fail at once rather than wait out the %s accept timeout", elapsed, sp.AcceptTimeout)
	}

	if err := sp.CloseListener(); err != nil {
		t.Fatalf("second CloseListener() = %v, want nil", err)
	}
}

// spec: §4.7.10 (Runtime process lifetime), §5.2 (pool configuration and execution modes)
//
// The connection accepted at the pod's first session start serves the pod's
// later sessions across occupancy zero. alice starts and closes, then bob
// starts: bob's Start returns without a second dial, bob's envelope arrives
// on alice's runtime connection, and the runtime reads no EOF.
//
// diagnosis: the adapter ended the runtime at occupancy zero, and the
// recycled pod cannot serve its next session.
func TestSocketRuntimeProcessKeepsTheConnectionAcrossOccupancyZero_spec_4_7_10(t *testing.T) {
	sp, err := adapter.NewSocketRuntimeProcess(runtimeSocketAddr(t))
	if err != nil {
		t.Fatalf("NewSocketRuntimeProcess: %v", err)
	}
	t.Cleanup(func() { _ = sp.CloseListener() })
	sp.AcceptTimeout = 30 * time.Second

	conn := startGeneration(t, sp, "alice")
	defer conn.Close()
	roundTrip(t, sp, "alice", conn)
	if err := sp.Close(context.Background(), "alice"); err != nil {
		t.Fatalf("Close(alice): %v", err)
	}

	// No runtime dials again. A Start that waited for an accept would run
	// to the 30s bound; one on the kept connection returns at once.
	began := time.Now()
	if err := sp.Start(context.Background(), "bob"); err != nil {
		t.Fatalf("Start(bob) after alice's Close = %v, want nil on the kept connection", err)
	}
	if elapsed := time.Since(began); elapsed > time.Second {
		t.Fatalf("Start(bob) took %s, want it to return on the kept connection without an accept", elapsed)
	}
	roundTrip(t, sp, "bob", conn)

	// The runtime reads no EOF: its read deadline expires instead.
	_ = conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	_, err = bufio.NewReader(conn).ReadString('\n')
	var ne net.Error
	if !errors.As(err, &ne) || !ne.Timeout() {
		t.Fatalf("runtime read after bob's round trip = %v, want a read-deadline timeout (no EOF)", err)
	}
}

// spec: §4.7.10 (Runtime process lifetime), §5.2 (Runtime not live)
//
// When the runtime closes its end, the transport records a sticky ended
// state: Start and Output return the ended error at once, with a 30s accept
// bound that a Start waiting for a reconnect would run to.
//
// diagnosis: a later session's start waits out the accept bound on a pod
// whose runtime has exited, or starts over a dead connection.
func TestSocketRuntimeProcessStartFailsFastAfterTheRuntimeExits_spec_4_7_10(t *testing.T) {
	sp, err := adapter.NewSocketRuntimeProcess(runtimeSocketAddr(t))
	if err != nil {
		t.Fatalf("NewSocketRuntimeProcess: %v", err)
	}
	t.Cleanup(func() { _ = sp.CloseListener() })
	sp.AcceptTimeout = 30 * time.Second

	conn := startGeneration(t, sp, "alice")
	out, err := sp.Output(context.Background(), "alice")
	if err != nil {
		t.Fatalf("Output(alice): %v", err)
	}
	_ = conn.Close()
	requireOutputCloses(t, out, "after the runtime closed its end")

	began := time.Now()
	if err := sp.Start(context.Background(), "bob"); !errors.Is(err, adapter.ErrRuntimeConnectionEnded) {
		t.Fatalf("Start(bob) after the runtime exited = %v, want the runtime-connection-ended error", err)
	}
	if _, err := sp.Output(context.Background(), "bob"); !errors.Is(err, adapter.ErrRuntimeConnectionEnded) {
		t.Fatalf("Output(bob) after the runtime exited = %v, want the runtime-connection-ended error", err)
	}
	if elapsed := time.Since(began); elapsed > time.Second {
		t.Fatalf("Start and Output took %s after the runtime exited, want under 1s", elapsed)
	}

	// A runtime that dials again is not accepted: the ended state is sticky.
	if c, err := dialRuntimeSocketErr(sp.SocketPath()); err == nil {
		defer c.Close()
	}
	if err := sp.Start(context.Background(), "carol"); !errors.Is(err, adapter.ErrRuntimeConnectionEnded) {
		t.Fatalf("Start(carol) after a redial = %v, want the runtime-connection-ended error", err)
	}
}

// spec: §5.2 (Runtime not live), §4.7.10 (Runtime process lifetime)
//
// ServesNextSession is false before the first accept, true once the runtime
// has connected, still true after the last session's Close, false once the
// runtime closes its end, and false for the rest of the pod's life.
//
// diagnosis: the whole-pod scrub report misstates whether the runtime can
// serve the next session, so the gateway reuses a pod whose runtime is gone
// or retires one whose runtime is live.
func TestSocketRuntimeProcessServesNextSession_spec_5_2(t *testing.T) {
	sp, err := adapter.NewSocketRuntimeProcess(runtimeSocketAddr(t))
	if err != nil {
		t.Fatalf("NewSocketRuntimeProcess: %v", err)
	}
	t.Cleanup(func() { _ = sp.CloseListener() })

	if sp.ServesNextSession() {
		t.Fatal("ServesNextSession() = true before any runtime connected, want false")
	}
	conn := startGeneration(t, sp, "alice")
	if !sp.ServesNextSession() {
		t.Fatal("ServesNextSession() = false after the accept, want true")
	}
	out, err := sp.Output(context.Background(), "alice")
	if err != nil {
		t.Fatalf("Output(alice): %v", err)
	}
	if err := sp.Close(context.Background(), "alice"); err != nil {
		t.Fatalf("Close(alice): %v", err)
	}
	if !sp.ServesNextSession() {
		t.Fatal("ServesNextSession() = false after the last session's Close, want true")
	}
	_ = conn.Close()
	requireOutputCloses(t, out, "after the runtime closed its end")
	if sp.ServesNextSession() {
		t.Fatal("ServesNextSession() = true after the runtime closed its end, want false")
	}
	if err := sp.Start(context.Background(), "bob"); err == nil {
		t.Fatal("Start(bob) after the runtime closed its end = nil, want an error")
	}
	if sp.ServesNextSession() {
		t.Fatal("ServesNextSession() = true after a refused Start, want false for the rest of the pod's life")
	}
}
