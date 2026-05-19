// SPDX-License-Identifier: MIT

package kind

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// PortForward starts `kubectl port-forward` for an in-cluster target
// (a Service or pod, e.g. "svc/lenny-gateway") and returns a loopback
// base URL the host can reach plus a stop function.
//
// target is the kubectl port-forward target argument: "svc/<name>" for
// a Service, "pod/<name>" for a pod. namespace is the target's
// namespace. remotePort is the port the target listens on.
//
// The local port is OS-assigned per call so concurrent tests that each
// forward the same Service do not collide. PortForward blocks until
// kubectl reports the forward is established or a short deadline
// elapses; a forward that never establishes fails the test, because a
// later request would otherwise misreport a connection refusal as a
// gateway result.
//
// The returned stop function kills the kubectl process and waits for
// it to exit. PortForward also registers a t.Cleanup that calls stop,
// so callers may ignore the returned stop when teardown at test end is
// sufficient. Callers that forward several targets in one test, or
// that need the forward to end before a later step, call stop
// explicitly.
func (c *Cluster) PortForward(t testing.TB, target, namespace string, remotePort int) (baseURL string, stop func()) {
	t.Helper()

	localPort := freeLocalPort(t)
	cmd := exec.Command(
		"kubectl", "--kubeconfig", c.KubeconfigPath,
		"port-forward", target,
		"-n", namespace,
		fmt.Sprintf("%d:%d", localPort, remotePort),
	)
	// kubectl writes "Forwarding from 127.0.0.1:<port> -> <remote>" to
	// stdout once the forward is live. Read it to gate readiness rather
	// than sleeping a fixed interval.
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("kind.PortForward: stdout pipe: %v", err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("kind.PortForward: start kubectl port-forward %s: %v", target, err)
	}

	var stopped bool
	stop = func() {
		if stopped {
			return
		}
		stopped = true
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}
	t.Cleanup(stop)

	ready := make(chan struct{})
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			if strings.Contains(sc.Text(), "Forwarding from") {
				close(ready)
				// Drain the rest so the pipe never blocks kubectl.
				_, _ = io.Copy(io.Discard, stdout)
				return
			}
		}
	}()

	baseURL = fmt.Sprintf("http://127.0.0.1:%d", localPort)
	select {
	case <-ready:
	case <-time.After(15 * time.Second):
		stop()
		t.Fatalf("kind.PortForward: %s did not establish within 15s\nstderr: %s",
			target, stderr.String())
	}

	// kubectl prints the readiness line before the listener fully
	// accepts; poll a TCP dial so the first request does not race the
	// listener's bind.
	if err := waitDialable(net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", localPort)), 10*time.Second); err != nil {
		stop()
		t.Fatalf("kind.PortForward: %s local port %d not dialable: %v\nstderr: %s",
			target, localPort, err, stderr.String())
	}
	return baseURL, stop
}

// freeLocalPort asks the kernel for an ephemeral TCP port on loopback,
// closes the listener, and returns the port number. The window between
// close and the kubectl bind is racy in principle; kubectl's bind
// failure surfaces as a PortForward readiness timeout, which fails the
// test with a diagnostic rather than hanging.
func freeLocalPort(t testing.TB) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("kind.PortForward: reserve local port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

// waitDialable polls a TCP dial against addr until it connects or the
// deadline elapses.
func waitDialable(addr string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var d net.Dialer
	for {
		conn, err := d.DialContext(ctx, "tcp", addr)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}
