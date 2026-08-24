// SPDX-License-Identifier: MIT

package runtimekit_test

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/runtimekit"
)

// listenAdapterSocket binds the socket the way the §4.7 adapter binds it
// and returns the listener.
func listenAdapterSocket(t *testing.T, socket string) net.Listener {
	t.Helper()
	addr := socket
	if strings.HasPrefix(socket, "@") {
		addr = "\x00" + socket[1:]
	}
	ln, err := net.Listen("unix", addr)
	if err != nil {
		t.Fatalf("listen on %q: %v", socket, err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return ln
}

// TestServeRunsOneLoopOverStdinStdout pins the §15.4 contract transport:
// with no adapter socket in the environment Serve runs the loop once over
// the process's standard streams and returns when it ends.
//
// spec: §15.4 (stdin/stdout contract transport), §28.5.3.
func TestServeRunsOneLoopOverStdinStdout(t *testing.T) {
	t.Setenv(runtimekit.SocketEnvVar, "")
	runs := 0
	err := runtimekit.Serve(context.Background(), func(context.Context, io.Reader, io.Writer) error {
		runs++
		return nil
	})
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if runs != 1 {
		t.Errorf("loop ran %d times over stdin/stdout, want 1", runs)
	}
}

// TestServeReconnectsForTheNextSessionAfterTheAdapterClosesTheConnection
// pins the §5.2 recycle boundary in the §4.7 sidecar model: the adapter
// closes the ending session's connection, the pod keeps the runtime
// process alive and reuses it, so Serve redials and runs a fresh loop for
// the next session. Serve returns once the adapter's listener is gone,
// which is the pod's own teardown.
//
// spec: §5.2 (recycle lifecycle), §4.7 (sidecar deployment model).
func TestServeReconnectsForTheNextSessionAfterTheAdapterClosesTheConnection(t *testing.T) {
	socket := transportSocketAddr(t)
	ln := listenAdapterSocket(t, socket)
	t.Setenv(runtimekit.SocketEnvVar, socket)

	// The adapter's half: accept two successive connections, closing each
	// one the way the adapter closes an ending session's runtime, then
	// unbind the listener with the pod.
	served := make(chan struct{}, 2)
	go func() {
		for i := 0; i < 2; i++ {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			served <- struct{}{}
			_ = conn.Close()
		}
		_ = ln.Close()
	}()

	var mu sync.Mutex
	loops := 0
	done := make(chan error, 1)
	go func() {
		done <- runtimekit.Serve(context.Background(), func(_ context.Context, in io.Reader, _ io.Writer) error {
			mu.Lock()
			loops++
			mu.Unlock()
			_, _ = io.Copy(io.Discard, in)
			return nil
		})
	}()

	for i := 0; i < 2; i++ {
		select {
		case <-served:
		case <-time.After(15 * time.Second):
			t.Fatalf("the runtime made no connection %d; it did not survive the previous session", i+1)
		}
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Serve did not return after the adapter's listener went away")
	}
	mu.Lock()
	defer mu.Unlock()
	if loops != 2 {
		t.Errorf("the runtime ran %d session loops, want 2 (one per adapter connection)", loops)
	}
}

// TestServeReturnsTheLoopFailure pins the §15.4 exit-code path: a loop
// that fails is returned unchanged rather than retried on a fresh
// connection, so the runtime's exit code still reports the failure.
//
// spec: §15.4 (runtime exit codes).
func TestServeReturnsTheLoopFailure(t *testing.T) {
	socket := transportSocketAddr(t)
	ln := listenAdapterSocket(t, socket)
	t.Setenv(runtimekit.SocketEnvVar, socket)

	go func() {
		conn, err := ln.Accept()
		if err == nil {
			_ = conn.Close()
		}
	}()

	want := errors.New("protocol failure")
	err := runtimekit.Serve(context.Background(), func(context.Context, io.Reader, io.Writer) error {
		return want
	})
	if !errors.Is(err, want) {
		t.Fatalf("Serve error = %v, want the loop's own failure %v", err, want)
	}
}

// TestServeReportsAFirstDialFailure pins the startup path: the runtime
// container that cannot reach the adapter at all fails rather than
// exiting cleanly, so the pod surfaces the §4.7 transport failure.
//
// spec: §4.7 (sidecar deployment model).
func TestServeReportsAFirstDialFailure(t *testing.T) {
	t.Setenv(runtimekit.SocketEnvVar, transportSocketAddr(t))
	err := runtimekit.Serve(context.Background(), func(context.Context, io.Reader, io.Writer) error {
		t.Error("the loop ran with no adapter connection")
		return nil
	})
	if err == nil {
		t.Fatal("Serve returned nil with no adapter listening, want the dial failure")
	}
}
