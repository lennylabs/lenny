// SPDX-License-Identifier: MIT

// Package gateway is the testinfra helper that boots cmd/lenny-gateway
// as a subprocess for tier-4 integration tests. Builds the binary
// into a temp dir on first call, then launches it bound to a random
// free port. Provides BaseURL() so tests can issue HTTP against the
// real gateway process (not just the http.Handler).
//
// Tests must call t.Cleanup-equivalent Stop() to terminate the
// subprocess; the helper does so automatically via the supplied
// testing.TB.
package gateway

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Process represents a running lenny-gateway subprocess.
type Process struct {
	cmd     *exec.Cmd
	baseURL string
	stderr  *os.File
}

// Start builds + spawns cmd/lenny-gateway on a random free port and
// returns when /v1/sessions is responsive. Skips the test when go is
// not on PATH (CI environments without Go).
func Start(t testing.TB) *Process {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go not on PATH: %v", err)
	}

	tmp, err := os.MkdirTemp("", "lenny-gateway-it-*")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmp) })

	binary := filepath.Join(tmp, "lenny-gateway")
	root := repoRoot(t)
	build := exec.Command("go", "build", "-o", binary, "./cmd/lenny-gateway")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build lenny-gateway: %v\n%s", err, out)
	}

	port, err := freePort()
	if err != nil {
		t.Fatalf("freePort: %v", err)
	}
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	stderrPath := filepath.Join(tmp, "stderr.log")
	stderrFile, err := os.Create(stderrPath)
	if err != nil {
		t.Fatalf("create stderr log: %v", err)
	}

	cmd := exec.Command(binary, "--addr", addr)
	cmd.Stdout = stderrFile
	cmd.Stderr = stderrFile
	// WaitDelay backstops the cleanup path: if SIGINT does not cause
	// the gateway to exit within this window, the runtime sends
	// SIGKILL and closes the inherited stdio pipes so cmd.Wait can
	// return. Without it, a hung subprocess deadlocks t.Cleanup.
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Start(); err != nil {
		t.Fatalf("start gateway: %v", err)
	}
	p := &Process{cmd: cmd, baseURL: "http://" + addr, stderr: stderrFile}

	t.Cleanup(func() {
		defer func() { _ = stderrFile.Close() }()

		// Buffered so the goroutine never blocks if cleanup gives up
		// waiting and returns.
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()

		_ = cmd.Process.Signal(os.Interrupt)

		select {
		case <-done:
			return
		case <-time.After(3 * time.Second):
		}

		// SIGINT did not take. Force-kill; WaitDelay still bounds the
		// final wait if the kernel takes its time reaping.
		_ = cmd.Process.Kill()
		select {
		case <-done:
		case <-time.After(cmd.WaitDelay + time.Second):
			t.Logf("gateway subprocess did not exit after SIGKILL; goroutine leaked")
		}
	})

	if err := p.waitReady(5 * time.Second); err != nil {
		// Surface the gateway log to the test on failure.
		b, _ := os.ReadFile(stderrPath)
		t.Fatalf("gateway not ready: %v\nlogs:\n%s", err, b)
	}
	return p
}

// BaseURL returns the http://host:port the gateway is listening on.
func (p *Process) BaseURL() string { return p.baseURL }

// waitReady polls /v1/sessions until the gateway responds (or until
// the deadline expires). Any 2xx-5xx response means the listener is
// up; only connection refused is treated as "not ready".
func (p *Process) waitReady(d time.Duration) error {
	deadline := time.Now().Add(d)
	client := &http.Client{Timeout: 500 * time.Millisecond}
	var lastErr error
	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, p.baseURL+"/v1/sessions", nil)
		req.Header.Set("X-Lenny-Tenant-ID", "ready-probe")
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			time.Sleep(50 * time.Millisecond)
			continue
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("ready probe timed out")
	}
	return lastErr
}

// freePort asks the kernel for a port and immediately releases it.
// Brief race with concurrent processes is acceptable for tier-4
// tests; production wires a deterministic allocator.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port, nil
}

// repoRoot walks upward from the test's working dir until it finds
// go.mod. testinfra packages are nested at varying depths so the
// caller can't bake in a fixed relative path.
func repoRoot(t testing.TB) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("repoRoot: walked past filesystem root looking for go.mod")
		}
		dir = parent
	}
}
