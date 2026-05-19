// SPDX-License-Identifier: MIT

package runtimekit_test

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

	"github.com/lennylabs/lenny/pkg/runtimekit"
)

func TestOpenWithoutSocketEnvUsesStdinStdout(t *testing.T) {
	t.Setenv(runtimekit.SocketEnvVar, "")
	tr, err := runtimekit.Open(context.Background())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer tr.Close()
	if tr.Socket {
		t.Error("Open must not report a socket transport when the env var is unset")
	}
	if tr.Reader != os.Stdin || tr.Writer != os.Stdout {
		t.Error("Open must return stdin/stdout when the socket env var is unset")
	}
}

func TestOpenWithSocketEnvDialsTheSocket(t *testing.T) {
	socket := transportSocketAddr(t)
	// A listener stands in for the adapter.
	addr := socket
	if strings.HasPrefix(socket, "@") {
		addr = "\x00" + socket[1:]
	}
	ln, err := net.Listen("unix", addr)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	accepted := make(chan net.Conn, 1)
	go func() {
		c, aerr := ln.Accept()
		if aerr == nil {
			accepted <- c
		}
	}()

	t.Setenv(runtimekit.SocketEnvVar, socket)
	tr, err := runtimekit.Open(context.Background())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer tr.Close()
	if !tr.Socket {
		t.Error("Open must report a socket transport when the env var names a socket")
	}

	adapterConn := <-accepted
	defer adapterConn.Close()

	// The runtime writes a frame; the adapter side reads it.
	if _, err := tr.Writer.Write([]byte(`{"type":"response"}` + "\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	line, err := bufio.NewReader(adapterConn).ReadString('\n')
	if err != nil {
		t.Fatalf("adapter read: %v", err)
	}
	if strings.TrimSpace(line) != `{"type":"response"}` {
		t.Errorf("adapter received %q", line)
	}
}

func TestDialSocketFailsFastWhenNoListener(t *testing.T) {
	socket := transportSocketAddr(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	_, err := runtimekit.DialSocket(ctx, socket)
	if err == nil {
		t.Fatal("DialSocket should fail when no adapter listens")
	}
	// The retry window is bounded; the dial must not hang.
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("DialSocket took %s, want a bounded retry window", elapsed)
	}
}

// transportSocketAddr returns a socket address: a Linux abstract
// address or, elsewhere, a short filesystem path. The path is kept
// short because the Unix sun_path field is limited to ~104 bytes on
// darwin and t.TempDir() alone can exceed that.
func transportSocketAddr(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "linux" {
		return fmt.Sprintf("@lenny-test-tr-%d-%s", os.Getpid(), strings.ReplaceAll(t.Name(), "/", "_"))
	}
	f, err := os.CreateTemp("", "tr-*.sock")
	if err != nil {
		t.Fatalf("temp socket path: %v", err)
	}
	path := f.Name()
	_ = f.Close()
	_ = os.Remove(path)
	t.Cleanup(func() { _ = os.Remove(path) })
	return path
}
