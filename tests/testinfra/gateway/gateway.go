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
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/binbuild"
	"github.com/lennylabs/lenny/tests/testinfra/ports"
	"github.com/lennylabs/lenny/tests/testinfra/stackkey"
)

// Process represents a running lenny-gateway subprocess.
type Process struct {
	// exited is closed once cmd.Wait returns; waitErr holds its result.
	exited  chan struct{}
	waitErr error
	// stopOnce makes stop signal the process at most once.
	stopOnce sync.Once
	cmd      *exec.Cmd
	baseURL  string
	// forwardAddr is the CH-HOLDERFORWARD listen and advertise address.
	forwardAddr string
	stderr      *os.File
	stderrPath  string
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
	return StartOpts(t, Options{Args: extraArgs})
}

// Options configures StartOpts.
type Options struct {
	// Args are cmd/lenny-gateway flags appended after the bind address.
	// A later value of a flag wins over an earlier one.
	Args []string
	// Env entries are appended after the harness defaults, so an entry
	// here overrides a default of the same name.
	Env []string
	// Binary is a cmd/lenny-gateway binary Build returned. Empty builds
	// one. A test that restarts replicas passes one binary to every
	// start so it builds once.
	Binary string
	// Reserve chooses each loopback address the process is given. Empty
	// uses ports.Reserve. A test of the start retry passes one that
	// returns an address another process holds first.
	Reserve func(testing.TB) string
}

// StartOpts builds and spawns cmd/lenny-gateway with opts and returns
// once the gateway answers HTTP.
//
// spec: §10.2 (Authentication) "Verifier key set and rotation" — every
// gateway a test starts runs on the local KMS provider over the master
// key of the test's stack (tests/testinfra/stackkey), so peer replicas,
// the Token Service, and lenny-ops in one test share one key set and a
// restarted replica keeps it.
func StartOpts(t testing.TB, opts Options) *Process {
	t.Helper()
	binary := opts.Binary
	if binary == "" {
		binary = Build(t)
	}
	// cmd/lenny-gateway cannot bind port 0 and report the port it got, so
	// the harness chooses the addresses, and starts again on fresh ones when
	// another process takes one first (TEST-GAPS T-17.4.38).
	return ports.StartRetrying(t, "lenny-gateway", func() (*Process, error) {
		return startOnce(t, binary, opts)
	})
}

// startOnce starts one gateway process on freshly chosen addresses and waits
// for it to serve. It returns an error wrapping ports.ErrAddrInUse when the
// process lost an address to another process, so StartOpts retries.
func startOnce(t testing.TB, binary string, opts Options) (*Process, error) {
	t.Helper()
	tmp := filepath.Dir(binary)
	reserve := opts.Reserve
	if reserve == nil {
		reserve = ports.Reserve
	}

	addr := reserve(t)
	// §10.1.1 Replica address: a gateway with a lease store (Redis) refuses
	// to start without an advertise address, so every process gets a
	// second free port for its forward listener and advertises it.
	forwardAddr := reserve(t)

	// Each process gets its own log, because processes started from one
	// binary share its directory.
	stderrFile, err := os.CreateTemp(tmp, "stderr-*.log")
	if err != nil {
		t.Fatalf("create stderr log: %v", err)
	}
	stderrPath := stderrFile.Name()

	// §10.6: the gateway fail-closes without an explicit
	// environment-variable policy. The harness defaults to deny-all;
	// a test may override it by passing --no-environment-policy in
	// opts.Args, where the later value wins.
	baseArgs := []string{"--addr", addr, "--no-environment-policy", "deny-all"}
	cmd := exec.Command(binary, append(baseArgs, opts.Args...)...)
	cmd.Stdout = stderrFile
	cmd.Stderr = stderrFile
	// §17.4: the harness emulates the production edge, where an
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
		"LENNY_HOLDER_FORWARD_LISTEN_ADDR="+forwardAddr,
		"LENNY_HOLDER_FORWARD_ADVERTISE_ADDR="+forwardAddr,
		stackkey.Env(t),
	)
	cmd.Env = append(cmd.Env, opts.Env...)
	// WaitDelay backstops the cleanup path: if SIGINT does not cause
	// the gateway to exit within this window, the runtime sends
	// SIGKILL and closes the inherited stdio pipes so cmd.Wait can
	// return. Without it, a hung subprocess deadlocks t.Cleanup.
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Start(); err != nil {
		t.Fatalf("start gateway: %v", err)
	}
	p := &Process{cmd: cmd, baseURL: "http://" + addr, forwardAddr: forwardAddr, stderr: stderrFile, stderrPath: stderrPath, exited: make(chan struct{})}
	go func() {
		p.waitErr = cmd.Wait()
		close(p.exited)
	}()

	t.Cleanup(func() {
		defer func() { _ = stderrFile.Close() }()
		p.stop(t, os.Interrupt)
	})

	if err := p.waitReady(5 * time.Second); err != nil {
		// A process that lost the forward address still runs; end it now
		// rather than at cleanup. Surface the gateway log to the test.
		p.stop(t, os.Interrupt)
		return nil, fmt.Errorf("gateway not ready: %w\nlogs:\n%s", err, p.Logs())
	}
	return p, nil
}

// Build returns the path of a cmd/lenny-gateway binary in a fresh
// temporary directory, which t's cleanup removes. A test that
// runs the binary to exit with its own flags and environment, such as a
// startup-refusal case, uses it directly; Start and StartWith use it
// before they launch the process. It skips the test when go is not on
// PATH.
func Build(t testing.TB) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go not on PATH: %v", err)
	}
	// binbuild links the command once per test process and hands this
	// test a copy in a directory of its own (TEST-GAPS T-17.4.33).
	return binbuild.Build(t, "./cmd/lenny-gateway", "lenny-gateway")
}

// stop signals the gateway with sig and waits for it to exit, force-killing
// it when sig does not take within 3 seconds. Only the first call signals;
// later calls return at once.
func (p *Process) stop(t testing.TB, sig os.Signal) {
	p.stopOnce.Do(func() {
		_ = p.cmd.Process.Signal(sig)
		select {
		case <-p.exited:
			return
		case <-time.After(3 * time.Second):
		}
		// The signal did not take. Force-kill; WaitDelay still bounds the
		// final wait if the kernel takes its time reaping.
		_ = p.cmd.Process.Kill()
		select {
		case <-p.exited:
		case <-time.After(p.cmd.WaitDelay + time.Second):
			t.Logf("gateway subprocess did not exit after SIGKILL; goroutine leaked")
		}
	})
}

// Stop ends the gateway with SIGINT, as a rollout ends a replica, and
// waits for it to exit. A test uses it to restart a replica with new
// flags or to take one replica away. The cleanup StartOpts registers
// tolerates a prior Stop.
func (p *Process) Stop(t testing.TB) {
	t.Helper()
	p.stop(t, os.Interrupt)
}

// Kill ends the gateway with SIGKILL and waits for it to exit, as a crash
// of the replica would: no shutdown path runs. A test uses it to hand work
// the replica held to another replica.
func (p *Process) Kill(t testing.TB) {
	t.Helper()
	p.stop(t, os.Kill)
}

// BaseURL returns the http://host:port the gateway is listening on.
func (p *Process) BaseURL() string { return p.baseURL }

// ForwardAddr returns the host:port the gateway's CH-HOLDERFORWARD listener
// binds and advertises in its coordination holder value. The listener runs
// only when the gateway has a lease store and the gateway replica
// certificate material; the harness runs in dev mode without it.
func (p *Process) ForwardAddr() string { return p.forwardAddr }

// Logs returns the gateway's combined stdout and stderr written so far,
// so a test can include the gateway's own account of a failure in its
// message. It returns an empty string when the log cannot be read.
func (p *Process) Logs() string {
	b, err := os.ReadFile(p.stderrPath)
	if err != nil {
		return ""
	}
	return string(b)
}

// waitReady polls /v1/sessions until the gateway responds (or until
// the deadline expires). Any 2xx-5xx response means the listener is
// up; only connection refused is treated as "not ready". A response
// counts only once this process holds the REST listener, because another
// process that took the port would answer the probe while this one exits
// on its bind refusal. A process that exits ends the wait with
// ports.ErrAddrInUse when its log records a bind refusal. The wait also
// returns ports.ErrAddrInUse when another process holds the forward
// address: the gateway binds it only when it has replica certificate
// material, but it advertises it to its peers either way.
func (p *Process) waitReady(d time.Duration) error {
	deadline := time.Now().Add(d)
	client := &http.Client{Timeout: 500 * time.Millisecond}
	addr := strings.TrimPrefix(p.baseURL, "http://")
	var lastErr error
	for time.Now().Before(deadline) {
		if err := p.exitErr(); err != nil {
			return err
		}
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
		if ports.Held(p.cmd.Process.Pid, p.forwardAddr) == ports.HolderOther {
			return fmt.Errorf("another process holds the forward address %s: %w", p.forwardAddr, ports.ErrAddrInUse)
		}
		if !ports.Ready(p.cmd.Process.Pid, addr) {
			lastErr = fmt.Errorf("%s answered, but not from this process", addr)
			time.Sleep(50 * time.Millisecond)
			continue
		}
		return nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("ready probe timed out")
	}
	return lastErr
}

// exitErr returns nil while the process runs. Once it has exited, it returns
// an error wrapping ports.ErrAddrInUse when the log records a bind refusal,
// and the exit status otherwise.
func (p *Process) exitErr() error {
	select {
	case <-p.exited:
	default:
		return nil
	}
	if ports.LogsAddrInUse([]byte(p.Logs())) {
		return fmt.Errorf("gateway exited (%v): %w", p.waitErr, ports.ErrAddrInUse)
	}
	return fmt.Errorf("gateway exited before it served: %v", p.waitErr)
}
