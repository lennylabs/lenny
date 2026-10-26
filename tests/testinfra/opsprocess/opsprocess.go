// SPDX-License-Identifier: MIT

// Package opsprocess is the testinfra helper that boots cmd/lenny-ops
// as a subprocess for tier-4 integration tests. It builds the binary
// into a temp dir on first call, then launches it bound to a random
// free port. Provides BaseURL() so tests can issue HTTP against the
// real lenny-ops process (the §25.4 operability surface) rather than
// an httptest handler wired to stubs.
//
// It mirrors the tests/testinfra/gateway and tests/testinfra/tokenservice
// helpers. Without a cluster connection the subprocess runs in the §25.4
// single-process degraded mode (leader election skipped) while still
// serving the full HTTP surface against the real Postgres and Redis passed
// via flags. A test that needs the Kubernetes API, such as one that relies
// on the permissive /healthz either-dependency result, passes a KUBECONFIG
// entry in Options.Env. Every process runs on the local KMS master key of
// the test's stack (tests/testinfra/stackkey).
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
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
	"github.com/lennylabs/lenny/tests/testinfra/stackkey"
)

// Process represents a running lenny-ops subprocess.
type Process struct {
	cmd        *exec.Cmd
	baseURL    string
	stderr     *os.File
	stderrPath string
	stopOnce   sync.Once
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
// returns once /readyz answers. The subprocess runs with its
// working directory at the repo root so the default --runbook-dir
// (docs/runbooks) resolves; a missing dir is non-fatal.
//
// The binary is started outside production posture, so the operability
// surface is unauthenticated and honours the X-Lenny-* dev headers for
// identity and role — the same convention the gateway integration
// harness uses.
func StartWith(t testing.TB, extraArgs ...string) *Process {
	t.Helper()
	return StartOpts(t, Options{Args: extraArgs})
}

// Options configures StartOpts.
type Options struct {
	// Args are cmd/lenny-ops flags appended after the bind address. A
	// later value of a flag wins over an earlier one.
	Args []string
	// Env entries are appended after the harness defaults, so an entry
	// here overrides a default of the same name. A test gives lenny-ops
	// a Kubernetes API through a KUBECONFIG entry.
	Env []string
	// Binary is a cmd/lenny-ops binary Build returned. Empty builds one.
	// A test that restarts lenny-ops passes one binary to every start so
	// it builds once.
	Binary string
}

// StartOpts builds and spawns cmd/lenny-ops with opts and returns once
// /readyz answers. /readyz can answer before the first §25.4 revocation
// set read has succeeded, so a test that asserts a Lenny-issued token
// outcome waits with WaitAccepted first.
//
// spec: §10.2 (Authentication) "Verifier key set and rotation", §25.4
// (The lenny-ops Service) — every lenny-ops a test starts runs on the
// local KMS provider over the master key of the test's stack
// (tests/testinfra/stackkey), so it verifies the tokens the gateways and
// the Token Service of the same test sign.
func StartOpts(t testing.TB, opts Options) *Process {
	t.Helper()
	binary := opts.Binary
	if binary == "" {
		binary = Build(t)
	}
	tmp := filepath.Dir(binary)
	root := schematest.RepoRoot(t)

	port, err := freePort()
	if err != nil {
		t.Fatalf("freePort: %v", err)
	}
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	// Each process gets its own log, because processes started from one
	// binary share its directory.
	stderrFile, err := os.CreateTemp(tmp, "stderr-*.log")
	if err != nil {
		t.Fatalf("create stderr log: %v", err)
	}
	stderrPath := stderrFile.Name()

	baseArgs := []string{"--addr", addr}
	cmd := exec.Command(binary, append(baseArgs, opts.Args...)...)
	cmd.Dir = root
	cmd.Stdout = stderrFile
	cmd.Stderr = stderrFile
	// §12.4: lenny-ops fail-closes at startup when --redis-url points at an
	// unauthenticated, plaintext Redis without the insecure opt-out. The
	// integration harness boots against a dev Redis container that runs
	// auth-less and plaintext (the dev/local posture the opt-out exists
	// for), so default the env flag; a caller that passes an explicit
	// --redis-allow-insecure flag is unaffected because the later value wins.
	cmd.Env = append(append(os.Environ(), "LENNY_REDIS_ALLOW_INSECURE=true", stackkey.Env(t)), opts.Env...)
	// WaitDelay backstops the cleanup path: if SIGINT does not cause the
	// process to exit within this window, the runtime sends SIGKILL and
	// closes the inherited stdio pipes so cmd.Wait can return.
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Start(); err != nil {
		t.Fatalf("start lenny-ops: %v", err)
	}
	p := &Process{cmd: cmd, baseURL: "http://" + addr, stderr: stderrFile, stderrPath: stderrPath}

	t.Cleanup(func() { p.stop(t) })

	if err := p.waitReady(15 * time.Second); err != nil {
		b, _ := os.ReadFile(stderrPath)
		t.Fatalf("lenny-ops not ready: %v\nlogs:\n%s", err, b)
	}
	return p
}

// Build compiles cmd/lenny-ops into a temporary directory removed at test
// cleanup and returns the binary path, for a test that runs the binary with
// its own flags and environment, such as a startup refusal it expects the
// process to exit on.
func Build(t testing.TB) string {
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
	build := exec.Command("go", "build", "-o", binary, "./cmd/lenny-ops")
	build.Dir = schematest.RepoRoot(t)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build lenny-ops: %v\n%s", err, out)
	}
	return binary
}

// BaseURL returns the http://host:port the lenny-ops surface listens on.
func (p *Process) BaseURL() string { return p.baseURL }

// Logs returns the lenny-ops combined output written so far, or an empty
// string when the log cannot be read.
func (p *Process) Logs() string {
	b, err := os.ReadFile(p.stderrPath)
	if err != nil {
		return ""
	}
	return string(b)
}

// Stop terminates the lenny-ops subprocess mid-test so a caller can inject a
// genuine outage of this installation: the SSE connection an agent holds
// against it drops, and subsequent requests fail to connect rather than
// returning stale success. It is the subprocess analogue of the
// tests/testinfra/containers Postgres.Stop/Redis.Stop mid-test injection,
// used by the §25.17 multi-installation watchdog tests that take one
// installation down while asserting the loop against a surviving one is
// unaffected. The t.Cleanup registered by StartWith tolerates a prior Stop.
func (p *Process) Stop(t testing.TB) {
	t.Helper()
	p.stop(t)
}

// stop signals SIGINT, escalates to SIGKILL if the process has not exited
// within 3 seconds, and waits for it to exit. Guarded by sync.Once so it is
// safe to call from both an explicit Stop(t) and the t.Cleanup registered at
// startup; the second call is a no-op.
func (p *Process) stop(t testing.TB) {
	t.Helper()
	p.stopOnce.Do(func() {
		defer func() { _ = p.stderr.Close() }()

		done := make(chan error, 1)
		go func() { done <- p.cmd.Wait() }()

		_ = p.cmd.Process.Signal(os.Interrupt)

		select {
		case <-done:
			return
		case <-time.After(3 * time.Second):
		}

		_ = p.cmd.Process.Kill()
		select {
		case <-done:
		case <-time.After(p.cmd.WaitDelay + time.Second):
			t.Logf("lenny-ops subprocess did not exit after SIGKILL; goroutine leaked")
		}
	})
}

// readyRequestTimeout bounds one /readyz request in waitReady. /readyz
// runs the dependency probes bounded by the 2 s probe timeout of
// pkg/ops/opsserver, so the bound exceeds it: a /readyz answer delayed
// by a probe that hangs, rather than refuses, still ends the wait.
const readyRequestTimeout = 3 * time.Second

// waitReady polls /readyz until the process responds (or the deadline
// expires). Any HTTP response means the listener is up; only a connection
// error is treated as "not ready". The wait polls /readyz, which returns
// 200 while the process serves, rather than /healthz, which returns 503
// outside a cluster whenever Postgres is down. Both endpoints are
// unauthenticated per §25.4.
func (p *Process) waitReady(d time.Duration) error {
	deadline := time.Now().Add(d)
	client := &http.Client{Timeout: readyRequestTimeout}
	var lastErr error
	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, p.baseURL+"/readyz", nil)
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
