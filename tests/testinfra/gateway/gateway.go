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

	tmp, err := os.MkdirTemp("", "lenny-gateway-it-*")
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
