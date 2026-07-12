// SPDX-License-Identifier: MIT

// Package opsprocess is the testinfra helper that boots cmd/lenny-ops
// as a subprocess for tier-4 integration tests. It builds the binary
// into a temp dir on first call, then launches it bound to a random
// free port. Provides BaseURL() so tests can issue HTTP against the
// real lenny-ops process (the §25.4 operability surface) rather than
// an httptest handler wired to stubs.
//
// It mirrors the tests/testinfra/gateway and tests/testinfra/tokenservice
// helpers: the subprocess runs in the §25.4 single-process degraded mode
// (no cluster connection, leader election skipped) while still serving the
// full HTTP surface against the real Postgres and Redis passed via flags.
//
// Tests do not manage the subprocess lifecycle directly; the helper
// registers a t.Cleanup that terminates it.
package opsprocess

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

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// Process represents a running lenny-ops subprocess.
type Process struct {
	cmd     *exec.Cmd
	baseURL string
	stderr  *os.File
}

// SkipUnlessAvailable t.Skips when the Go toolchain needed to build
// cmd/lenny-ops is missing, matching the gateway / tokenservice
// convention so a caller can guard the test in one line.
func SkipUnlessAvailable(t testing.TB) {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("opsprocess.SkipUnlessAvailable: go not on PATH: %v", err)
	}
}

// StartWith builds cmd/lenny-ops and spawns it on a random free port
// with the supplied extra flags appended after the bind address. It
// returns once /healthz is responsive. The subprocess runs with its
// working directory at the repo root so the default --runbook-dir
// (docs/runbooks) resolves; a missing dir is non-fatal.
//
// The binary is started outside production posture, so the operability
// surface is unauthenticated and honours the X-Lenny-* dev headers for
// identity and role — the same convention the gateway integration
// harness uses.
func StartWith(t testing.TB, extraArgs ...string) *Process {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go not on PATH: %v", err)
	}

	tmp, err := os.MkdirTemp("", "lenny-ops-it-*")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmp) })

	binary := filepath.Join(tmp, "lenny-ops")
	root := schematest.RepoRoot(t)
	build := exec.Command("go", "build", "-o", binary, "./cmd/lenny-ops")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build lenny-ops: %v\n%s", err, out)
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

	baseArgs := []string{"--addr", addr}
	cmd := exec.Command(binary, append(baseArgs, extraArgs...)...)
	cmd.Dir = root
	cmd.Stdout = stderrFile
	cmd.Stderr = stderrFile
	// §12.4: lenny-ops fail-closes at startup when --redis-url points at an
	// unauthenticated, plaintext Redis without the insecure opt-out. The
	// integration harness boots against a dev Redis container that runs
	// auth-less and plaintext (the dev/local posture the opt-out exists
	// for), so default the env flag; a caller that passes an explicit
	// --redis-allow-insecure flag is unaffected because the later value wins.
	cmd.Env = append(os.Environ(), "LENNY_REDIS_ALLOW_INSECURE=true")
	// WaitDelay backstops the cleanup path: if SIGINT does not cause the
	// process to exit within this window, the runtime sends SIGKILL and
	// closes the inherited stdio pipes so cmd.Wait can return.
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Start(); err != nil {
		t.Fatalf("start lenny-ops: %v", err)
	}
	p := &Process{cmd: cmd, baseURL: "http://" + addr, stderr: stderrFile}

	t.Cleanup(func() {
		defer func() { _ = stderrFile.Close() }()

		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()

		_ = cmd.Process.Signal(os.Interrupt)

		select {
		case <-done:
			return
		case <-time.After(3 * time.Second):
		}

		_ = cmd.Process.Kill()
		select {
		case <-done:
		case <-time.After(cmd.WaitDelay + time.Second):
			t.Logf("lenny-ops subprocess did not exit after SIGKILL; goroutine leaked")
		}
	})

	if err := p.waitReady(15 * time.Second); err != nil {
		b, _ := os.ReadFile(stderrPath)
		t.Fatalf("lenny-ops not ready: %v\nlogs:\n%s", err, b)
	}
	return p
}

// BaseURL returns the http://host:port the lenny-ops surface listens on.
func (p *Process) BaseURL() string { return p.baseURL }

// waitReady polls /healthz until the process responds (or the deadline
// expires). Any HTTP response means the listener is up; only a connection
// error is treated as "not ready". /healthz is unauthenticated per §25.4.
func (p *Process) waitReady(d time.Duration) error {
	deadline := time.Now().Add(d)
	client := &http.Client{Timeout: 500 * time.Millisecond}
	var lastErr error
	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, p.baseURL+"/healthz", nil)
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

// freePort asks the kernel for a port and immediately releases it. The
// brief race with concurrent processes is acceptable for tier-4 tests.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port, nil
}
