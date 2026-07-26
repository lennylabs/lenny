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
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// Process represents a running lenny-gateway subprocess.
type Process struct {
	cmd     *exec.Cmd
	baseURL string
	stderr  *os.File
}

// SkipUnlessAvailable t.Skips when the prerequisites for booting
// the gateway (the `go` toolchain) are missing. Matches the
// chaos / envtest / kind / compose / load convention so callers
// can guard the test in one line:
//
//	gateway.SkipUnlessAvailable(t)
//	gw := gateway.Start(t)
func SkipUnlessAvailable(t testing.TB) {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("gateway.SkipUnlessAvailable: go not on PATH: %v", err)
	}
}

// Build compiles cmd/lenny-gateway into a fresh temp binary and returns
// its path. Shared by StartWith (which boots the binary as a
// long-running process) and RunToExit (which runs it to completion to
// assert a startup-fatal exit). A build failure fails the test.
func Build(t testing.TB) string {
	t.Helper()
	tmp, err := os.MkdirTemp("", "lenny-gateway-build-*")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmp) })

	binary := filepath.Join(tmp, "lenny-gateway")
	root := schematest.RepoRoot(t)
	build := exec.Command("go", "build", "-o", binary, "./cmd/lenny-gateway")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build lenny-gateway: %v\n%s", err, out)
	}
	return binary
}

// Start builds + spawns cmd/lenny-gateway on a random free port and
// returns when /v1/sessions is responsive. Skips the test when go is
// not on PATH (CI environments without Go).
func Start(t testing.TB) *Process {
	return StartWith(t)
}

// StartWith is Start with additional cmd/lenny-gateway flags appended
// after the bind address — e.g. StartWith(t, "--dev-mode") so a test
// can exercise the dev-header RBAC path.
func StartWith(t testing.TB, extraArgs ...string) *Process {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go not on PATH: %v", err)
	}

	binary := Build(t)

	tmp, err := os.MkdirTemp("", "lenny-gateway-run-*")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmp) })

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

	// §10.6: the gateway fail-closes without an explicit
	// environment-variable policy. The harness defaults to deny-all;
	// a test may override it by passing --no-environment-policy in
	// extraArgs, where the later value wins.
	baseArgs := []string{"--addr", addr, "--no-environment-policy", "deny-all"}
	cmd := exec.Command(binary, append(baseArgs, extraArgs...)...)
	cmd.Stdout = stderrFile
	cmd.Stderr = stderrFile
	// §17.4 line 268: the harness emulates the production edge, where an
	// ingress terminates TLS in front of the gateway's plain-HTTP
	// listener. Acknowledge it so the dev-mode hard startup assertion
	// passes for both the dev-mode and the production-posture tests. A
	// test that wants the gate to fail sets LENNY_DEV_MODE/upstream
	// itself. F-17.4.5.
	//
	// §10.3 (F-10.3.14): outside dev mode the gateway fail-closes at
	// startup on the required platform keys auth.oidc.issuerUrl and
	// auth.oidc.clientId. Every integration test drives the gateway via
	// the X-Lenny-* dev headers rather than a real OIDC bearer, so the
	// harness defaults LENNY_DEV_MODE=true. This is the default for the
	// --dev-mode flag (cmd/lenny-gateway reads envFlag("LENNY_DEV_MODE")),
	// so a StartWith(t, "--dev-mode") caller is unaffected; the env value
	// only supplies the default for callers that pass no flag.
	//
	// §12.4 (F-12.4): the gateway fail-closes at startup when a
	// --redis-url points at an unauthenticated, plaintext Redis without
	// --redis-allow-insecure. The integration harness boots against a
	// dev Redis container (tests/testinfra/containers.StartRedis) that
	// runs auth-less and plaintext, which is the dev/local posture the
	// --redis-allow-insecure opt-out exists for. Default the env flag so
	// a StartWith(t, "--redis-url=...") caller dials the dev Redis the
	// same way a dev deployment does; a test that wants to exercise the
	// §12.4 AUTH-and-TLS guard sets LENNY_REDIS_ALLOW_INSECURE=false (or
	// passes the flag) itself.
	cmd.Env = append(
		os.Environ(),
		"LENNY_TLS_TERMINATED_UPSTREAM=true",
		"LENNY_DEV_MODE=true",
		"LENNY_REDIS_ALLOW_INSECURE=true",
	)
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

// ExitResult is the outcome of running cmd/lenny-gateway to completion
// via RunToExit, rather than the long-running Start/StartWith flow.
type ExitResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// RunToExit builds cmd/lenny-gateway (or reuses a binary already built
// by Build/StartWith in the same test) and runs it with args to
// completion, waiting for the process to exit rather than for
// readiness. It exists for the §27.2 layer-3 startup-fatal backstop: a
// malformed playground config (or another startup-gate violation) makes
// the real process log.Fatalf and exit non-zero before ever binding a
// listener, so StartWith's readiness wait would just time out and fail
// the test instead of letting the caller assert on the exit.
//
// A timeout or a process-start failure fails the test; a non-zero exit
// is returned in ExitCode so the caller can assert on it and on the
// captured stderr/stdout.
func RunToExit(t testing.TB, timeout time.Duration, args ...string) ExitResult {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go not on PATH: %v", err)
	}

	binary := Build(t)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	res := ExitResult{Stdout: stdout.String(), Stderr: stderr.String()}
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("lenny-gateway %s: did not exit within %s (expected a startup-fatal exit)\nstdout:\n%s\nstderr:\n%s",
			strings.Join(args, " "), timeout, res.Stdout, res.Stderr)
	}
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			res.ExitCode = ee.ExitCode()
		} else {
			t.Fatalf("lenny-gateway %s: %v", strings.Join(args, " "), err)
		}
	}
	return res
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
