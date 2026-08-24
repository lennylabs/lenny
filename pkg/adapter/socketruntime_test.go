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

// spec: §5.2 (recycle lifecycle: a scrubbed pod serves the next session),
// §4.7 (sidecar runtime transport)
//
// The listener is bound once at construction, before any session exists,
// and its address is released when the adapter process exits with the
// pod, so it outlives every session on the pod. A runtime that re-dials
// after the last session's Close (the developer-loop spawn path, where
// Start execs the runtime binary again) therefore finds the address still
// bound and the next session's Start accepts its connection.
//
// diagnosis: a failure here means the runtime socket is unbound by the
// last session's teardown, so the first session on a recycled pod fails
// its accept with "use of closed network connection". The gateway reads
// that as a failed start, retries onto another pod, and the recycled pod
// is retired rather than reused, which is the whole of what
// recycle.enabled buys.
func TestSocketRuntimeProcessAcceptsTheNextSessionAfterTheLastCloses_spec_5_2(t *testing.T) {
	socket := runtimeSocketAddr(t)
	sp, err := adapter.NewSocketRuntimeProcess(socket)
	if err != nil {
		t.Fatalf("NewSocketRuntimeProcess: %v", err)
	}
	defer sp.Close(context.Background(), "sess-b")

	firstCh := make(chan net.Conn, 1)
	go func() { firstCh <- dialRuntimeSocket(t, sp.SocketPath()) }()
	if err := sp.Start(context.Background(), "sess-a"); err != nil {
		t.Fatalf("Start(sess-a): %v", err)
	}
	first := <-firstCh
	defer first.Close()

	// The pod's only session ends. The runtime observes EOF and re-dials
	// the still-bound address below.
	if err := sp.Close(context.Background(), "sess-a"); err != nil {
		t.Fatalf("Close(sess-a): %v", err)
	}

	secondCh := make(chan net.Conn, 1)
	go func() { secondCh <- dialRuntimeSocket(t, sp.SocketPath()) }()
	if err := sp.Start(context.Background(), "sess-b"); err != nil {
		t.Fatalf("Start(sess-b) on the recycled pod: %v; the next session must accept a fresh "+
			"connection on the pod's still-bound runtime socket", err)
	}
	second := <-secondCh
	defer second.Close()

	// The recycled pod's session writes over the new connection and the
	// re-dialled runtime reads it.
	reader := bufio.NewReader(second)
	frame := `{"type":"message","sessionId":"sess-b"}`
	if err := sp.WriteEnvelope("sess-b", []byte(frame)); err != nil {
		t.Fatalf("WriteEnvelope on the recycled pod: %v", err)
	}
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("re-dialled runtime read on the recycled pod: %v", err)
	}
	if strings.TrimSpace(line) != frame {
		t.Errorf("re-dialled runtime received %q, want %q", strings.TrimSpace(line), frame)
	}
}

// spec: §5.2 (a scrubbed pod serves the next session), §4.7 (sidecar
// runtime transport)
//
// The listener is bound for the pod's lifetime, so a Start whose accept
// times out parks its pending accept rather than abandoning the
// goroutine that holds it. A second Accept per attempt would race the
// first for the runtime's connection and hand it to a Start that had
// already given up, so the runtime would be connected and every later
// Start would still time out.
//
// diagnosis: a failure here means each timed-out Start leaves a goroutine
// parked on the pod's listener. The next session's Start times out even
// though the runtime has connected, and the pod serves nothing more.
func TestSocketRuntimeProcessReusesThePendingAcceptAfterATimedOutStart_spec_5_2(t *testing.T) {
	socket := runtimeSocketAddr(t)
	sp, err := adapter.NewSocketRuntimeProcess(socket)
	if err != nil {
		t.Fatalf("NewSocketRuntimeProcess: %v", err)
	}
	sp.AcceptTimeout = 200 * time.Millisecond
	defer sp.Close(context.Background(), "sess-b")

	// Nothing dials, so the first session's start gives up on its accept.
	if err := sp.Start(context.Background(), "sess-a"); err == nil {
		t.Fatal("Start(sess-a) succeeded with no runtime connected")
	}

	// The runtime connects late. The next session's start must be the one
	// that takes the connection.
	conn := dialRuntimeSocket(t, sp.SocketPath())
	defer conn.Close()

	sp.AcceptTimeout = 5 * time.Second
	if err := sp.Start(context.Background(), "sess-b"); err != nil {
		t.Fatalf("Start(sess-b) after a timed-out start: %v; the pending accept must carry the "+
			"runtime's connection to the next start", err)
	}
	reader := bufio.NewReader(conn)
	frame := `{"type":"message","sessionId":"sess-b"}`
	if err := sp.WriteEnvelope("sess-b", []byte(frame)); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("runtime read: %v", err)
	}
	if strings.TrimSpace(line) != frame {
		t.Errorf("runtime received %q, want %q", strings.TrimSpace(line), frame)
	}
}

// spec: §5.2 (recycle lifecycle: a scrubbed pod serves the next session),
// §15.4 (a runtime's clean exit closes the transport), §4.7
//
// The runtime may end its own connection between sessions: the §15.4
// clean exit, or a crash. The pod's transport state follows the reader,
// so the next session's Start accepts a fresh connection instead of
// returning early on a connection that is gone.
//
// diagnosis: a failure here means the pod holds a dead socket open in its
// bookkeeping. Every later Start returns success without a transport and
// every write on that session goes nowhere, so the pod cannot recover in
// place and each session on it fails silently.
func TestSocketRuntimeProcessReconnectsAfterTheRuntimeEndsTheConnection_spec_5_2(t *testing.T) {
	socket := runtimeSocketAddr(t)
	sp, err := adapter.NewSocketRuntimeProcess(socket)
	if err != nil {
		t.Fatalf("NewSocketRuntimeProcess: %v", err)
	}
	sp.AcceptTimeout = 5 * time.Second
	defer sp.Close(context.Background(), "sess-b")

	firstCh := make(chan net.Conn, 1)
	go func() { firstCh <- dialRuntimeSocket(t, sp.SocketPath()) }()
	if err := sp.Start(context.Background(), "sess-a"); err != nil {
		t.Fatalf("Start(sess-a): %v", err)
	}
	first := <-firstCh

	// The subscriber's channel closes when the fan-out reader exits, which
	// is after it has recorded the connection as gone.
	out, err := sp.Output(context.Background(), "sess-a")
	if err != nil {
		t.Fatalf("Output(sess-a): %v", err)
	}

	// The runtime exits and its connection ends.
	first.Close()
	select {
	case <-out:
	case <-time.After(5 * time.Second):
		t.Fatal("the fan-out reader did not observe the runtime's end of the connection")
	}

	secondCh := make(chan net.Conn, 1)
	go func() { secondCh <- dialRuntimeSocket(t, sp.SocketPath()) }()
	if err := sp.Start(context.Background(), "sess-b"); err != nil {
		t.Fatalf("Start(sess-b) after the runtime exited: %v", err)
	}
	second := <-secondCh
	defer second.Close()

	reader := bufio.NewReader(second)
	frame := `{"type":"message","sessionId":"sess-b"}`
	if err := sp.WriteEnvelope("sess-b", []byte(frame)); err != nil {
		t.Fatalf("WriteEnvelope after the runtime re-dialled: %v", err)
	}
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("re-dialled runtime read: %v", err)
	}
	if strings.TrimSpace(line) != frame {
		t.Errorf("re-dialled runtime received %q, want %q", strings.TrimSpace(line), frame)
	}
}

// spec: §5.2 (per-slot teardown and release; the shared runtime
// connection), §4.7 (sidecar runtime transport)
//
// The fan-out reader records the connection as gone whenever the runtime's
// end of it disappears, so a session can reach Close with no live
// connection. Close must still release that session from the active set:
// a session left registered there is a permanent phantom sibling, and
// every later session on the pod then finds the set non-empty and skips
// the shared-connection teardown.
//
// diagnosis: a failure here means a runtime crash strands its session in
// the adapter's active set. From that point on no session's Close tears
// the runtime connection down, the §15.4 clean-exit signal never reaches
// the runtime again, and the spawned-child grace wait never runs.
func TestSocketRuntimeProcessCloseReleasesTheSessionAfterTheRuntimeDied_spec_5_2(t *testing.T) {
	socket := runtimeSocketAddr(t)
	sp, err := adapter.NewSocketRuntimeProcess(socket)
	if err != nil {
		t.Fatalf("NewSocketRuntimeProcess: %v", err)
	}
	sp.AcceptTimeout = 5 * time.Second
	defer sp.Close(context.Background(), "sess-b")

	firstCh := make(chan net.Conn, 1)
	go func() { firstCh <- dialRuntimeSocket(t, sp.SocketPath()) }()
	if err := sp.Start(context.Background(), "sess-a"); err != nil {
		t.Fatalf("Start(sess-a): %v", err)
	}
	first := <-firstCh

	// The subscriber's channel closes once the reader has recorded the
	// connection as gone, which is the state Close has to handle.
	out, err := sp.Output(context.Background(), "sess-a")
	if err != nil {
		t.Fatalf("Output(sess-a): %v", err)
	}

	// The runtime crashes: its end of the connection goes away without the
	// adapter having closed it.
	first.Close()
	select {
	case <-out:
	case <-time.After(5 * time.Second):
		t.Fatal("the fan-out reader did not observe the runtime's end of the connection")
	}

	if err := sp.Close(context.Background(), "sess-a"); err != nil {
		t.Fatalf("Close(sess-a) after the runtime died: %v", err)
	}

	secondCh := make(chan net.Conn, 1)
	go func() { secondCh <- dialRuntimeSocket(t, sp.SocketPath()) }()
	if err := sp.Start(context.Background(), "sess-b"); err != nil {
		t.Fatalf("Start(sess-b) after the runtime re-dialled: %v", err)
	}
	second := <-secondCh
	defer second.Close()

	// sess-b is the pod's only active session, so its Close closes the
	// shared connection and the runtime reads EOF.
	if err := sp.Close(context.Background(), "sess-b"); err != nil {
		t.Fatalf("Close(sess-b): %v", err)
	}
	if err := second.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	buf := make([]byte, 1)
	if _, err := second.Read(buf); !errors.Is(err, io.EOF) {
		t.Errorf("the re-dialled runtime read %v, want EOF; the crashed session was left in the "+
			"active set, so sess-b's Close never tore the shared connection down", err)
	}
}

// spec: §5.2 ("Other slots continue unaffected"; the pod reuses its
// runtime socket for the next session), §28.5.3 (JSONL framing)
//
// Each connection owns the subscribers registered against it. The reader
// of a connection the adapter has already closed can still be running when
// the next session's Start installs a fresh connection and its Attach
// stream subscribes, and it must close only its own connection's
// subscribers on the way out.
//
// diagnosis: a failure here means a departing reader closes the next
// session's Attach subscribers. That session's stream ends at an EOF the
// runtime never sent, so its first message produces no output.
func TestSocketRuntimeProcessDepartingReaderLeavesTheNextSessionsSubscriber_spec_5_2(t *testing.T) {
	socket := runtimeSocketAddr(t)
	sp, err := adapter.NewSocketRuntimeProcess(socket)
	if err != nil {
		t.Fatalf("NewSocketRuntimeProcess: %v", err)
	}
	sp.AcceptTimeout = 5 * time.Second
	defer sp.Close(context.Background(), "sess-b")

	firstCh := make(chan net.Conn, 1)
	go func() { firstCh <- dialRuntimeSocket(t, sp.SocketPath()) }()
	if err := sp.Start(context.Background(), "sess-a"); err != nil {
		t.Fatalf("Start(sess-a): %v", err)
	}
	first := <-firstCh

	// sess-a's Attach stream subscribes and never reads. Enough frames to
	// fill its intake park the fan-out reader inside the delivery, so the
	// reader is still alive when the next session starts below.
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	defer cancelFirst()
	if _, err := sp.Output(firstCtx, "sess-a"); err != nil {
		t.Fatalf("Output(sess-a): %v", err)
	}
	for i := 0; i < 256; i++ {
		if _, err := fmt.Fprintf(first, "{\"type\":\"status\",\"sessionId\":\"sess-a\",\"n\":%d}\n", i); err != nil {
			t.Fatalf("write frame %d from the runtime: %v", i, err)
		}
	}
	time.Sleep(200 * time.Millisecond)

	// The pod's only session ends and the runtime re-dials for the next
	// one, which registers its own Attach subscriber.
	if err := sp.Close(context.Background(), "sess-a"); err != nil {
		t.Fatalf("Close(sess-a): %v", err)
	}
	first.Close()

	secondCh := make(chan net.Conn, 1)
	go func() { secondCh <- dialRuntimeSocket(t, sp.SocketPath()) }()
	if err := sp.Start(context.Background(), "sess-b"); err != nil {
		t.Fatalf("Start(sess-b): %v", err)
	}
	second := <-secondCh
	defer second.Close()

	outB, err := sp.Output(context.Background(), "sess-b")
	if err != nil {
		t.Fatalf("Output(sess-b): %v", err)
	}

	// Release the parked reader of the previous connection. It exits and
	// tears down its own subscribers.
	cancelFirst()
	time.Sleep(200 * time.Millisecond)

	frame := `{"type":"status","sessionId":"sess-b"}`
	if _, err := fmt.Fprintln(second, frame); err != nil {
		t.Fatalf("write the next session's frame from the runtime: %v", err)
	}
	select {
	case line, ok := <-outB:
		if !ok {
			t.Fatal("the next session's Attach stream closed; the previous connection's reader " +
				"closed a subscriber that belongs to the connection after it")
		}
		if strings.TrimSpace(string(line)) != frame {
			t.Errorf("the next session's stream carried %q, want %q", line, frame)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the next session's Attach stream carried no frame")
	}
}
