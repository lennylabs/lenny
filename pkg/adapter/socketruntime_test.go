// SPDX-License-Identifier: MIT

package adapter_test

import (
	"bufio"
	"context"
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

func TestSocketRuntimeProcessRejectsWrongSession(t *testing.T) {
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
	defer (<-connCh).Close()

	if err := sp.WriteEnvelope("other", []byte(`{}`)); err == nil {
		t.Error("WriteEnvelope must reject a session the socket is not bound to")
	}
	if _, err := sp.Output(context.Background(), "other"); err == nil {
		t.Error("Output must reject a session the socket is not bound to")
	}
}
