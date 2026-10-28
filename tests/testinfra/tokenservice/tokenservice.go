// SPDX-License-Identifier: MIT

// Package tokenservice is the testinfra helper that boots
// cmd/lenny-token-service as a subprocess for tier-4 integration
// tests. Start builds the binary into a temp dir, then launches it
// bound to a random free gRPC port; Build returns the built binary for
// a test that runs it with its own flags. Provides GRPCAddr()
// so tests can pass it to the gateway harness as the
// --token-service-grpc-addr flag value.
package tokenservice

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	tokensv1 "github.com/lennylabs/lenny/pkg/proto/tokenservice/v1"
	"github.com/lennylabs/lenny/tests/testinfra/binbuild"
	"github.com/lennylabs/lenny/tests/testinfra/stackkey"
)

// Process represents a running lenny-token-service subprocess.
type Process struct {
	// done receives the result of cmd.Wait once the process exits.
	done chan error
	// stopOnce makes stop signal the process at most once.
	stopOnce   sync.Once
	cmd        *exec.Cmd
	grpcAddr   string
	httpAddr   string
	stderr     *os.File
	stderrPath string
}

// SkipUnlessAvailable t.Skips when the Go toolchain is missing.
func SkipUnlessAvailable(t testing.TB) {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("tokenservice.SkipUnlessAvailable: go not on PATH: %v", err)
	}
}

// Start builds + spawns cmd/lenny-token-service on a random free
// gRPC and HTTP port. Returns when both listeners are responsive.
// Skips the test when go is not on PATH (CI environments without Go).
func Start(t testing.TB) *Process {
	t.Helper()
	return StartOpts(t, Options{})
}

// StartWith is Start with additional cmd/lenny-token-service flags
// appended after the listen addresses, such as --postgres-dsn.
func StartWith(t testing.TB, extraArgs ...string) *Process {
	t.Helper()
	return StartOpts(t, Options{Args: extraArgs})
}

// Options configures StartOpts.
type Options struct {
	// Args are cmd/lenny-token-service flags appended after the listen
	// addresses. A later value of a flag wins over an earlier one.
	Args []string
	// Env entries are appended after the harness defaults, so an entry
	// here overrides a default of the same name.
	Env []string
	// Binary is a cmd/lenny-token-service binary Build returned. Empty
	// builds one. A test that restarts the Token Service passes one
	// binary to every start so it builds once.
	Binary string
}

// StartOpts builds and spawns cmd/lenny-token-service with opts and
// returns once its gRPC and HTTP listeners accept connections.
//
// spec: §10.2 (Authentication) "Verifier key set and rotation", §4.3 —
// every Token Service a test starts runs on the local KMS provider over
// the master key of the test's stack (tests/testinfra/stackkey), so it
// shares one key set with the gateways and lenny-ops the test starts.
func StartOpts(t testing.TB, opts Options) *Process {
	t.Helper()
	binary := opts.Binary
	if binary == "" {
		binary = Build(t)
	}
	tmp := filepath.Dir(binary)

	grpcPort, err := freePort()
	if err != nil {
		t.Fatalf("freePort (grpc): %v", err)
	}
	httpPort, err := freePort()
	if err != nil {
		t.Fatalf("freePort (http): %v", err)
	}
	grpcAddr := fmt.Sprintf("127.0.0.1:%d", grpcPort)
	httpAddr := fmt.Sprintf("127.0.0.1:%d", httpPort)

	// Each process gets its own log, because processes started from one
	// binary share its directory.
	stderrFile, err := os.CreateTemp(tmp, "stderr-*.log")
	if err != nil {
		t.Fatalf("create stderr log: %v", err)
	}
	stderrPath := stderrFile.Name()

	args := append([]string{"--addr", httpAddr, "--grpc-addr", grpcAddr}, opts.Args...)
	cmd := exec.Command(binary, args...)
	cmd.Stdout = stderrFile
	cmd.Stderr = stderrFile
	cmd.Env = append(append(os.Environ(), stackkey.Env(t)), opts.Env...)
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Start(); err != nil {
		t.Fatalf("start token-service: %v", err)
	}
	p := &Process{cmd: cmd, grpcAddr: grpcAddr, httpAddr: httpAddr, stderr: stderrFile, stderrPath: stderrPath, done: make(chan error, 1)}
	// Buffered so the goroutine never blocks once nothing waits on it.
	go func() { p.done <- cmd.Wait() }()

	t.Cleanup(func() {
		defer func() { _ = stderrFile.Close() }()
		p.stop(t)
	})

	if err := p.waitReady(5 * time.Second); err != nil {
		b, _ := os.ReadFile(stderrPath)
		t.Fatalf("token-service not ready: %v\nlogs:\n%s", err, b)
	}
	return p
}

// Stop ends the Token Service with SIGINT and waits for it to exit,
// force-killing it when the signal does not take within 3 seconds. A
// test uses it to restart the signer. Only the first call signals; the
// cleanup StartOpts registers tolerates a prior Stop.
func (p *Process) Stop(t testing.TB) {
	t.Helper()
	p.stop(t)
}

// stop implements Stop.
func (p *Process) stop(t testing.TB) {
	p.stopOnce.Do(func() {
		_ = p.cmd.Process.Signal(os.Interrupt)
		select {
		case <-p.done:
			return
		case <-time.After(3 * time.Second):
		}
		_ = p.cmd.Process.Kill()
		select {
		case <-p.done:
		case <-time.After(p.cmd.WaitDelay + time.Second):
			t.Logf("token-service subprocess did not exit after SIGKILL; goroutine leaked")
		}
	})
}

// Logs returns the Token Service's combined output written so far, or
// an empty string when the log cannot be read.
func (p *Process) Logs() string {
	b, err := os.ReadFile(p.stderrPath)
	if err != nil {
		return ""
	}
	return string(b)
}

// Build returns the path of a cmd/lenny-token-service binary in a fresh
// temporary directory, which t's cleanup removes. A
// test that runs the binary to exit with its own flags and environment,
// such as a startup-refusal case, uses it directly; Start uses it before
// it launches the process. It skips the test when go is not on PATH.
func Build(t testing.TB) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go not on PATH: %v", err)
	}
	// binbuild links the command once per test process and hands this
	// test a copy in a directory of its own (TEST-GAPS T-17.4.33).
	return binbuild.Build(t, "./cmd/lenny-token-service", "lenny-token-service")
}

// GRPCAddr returns the host:port the gRPC TokenService listens on.
func (p *Process) GRPCAddr() string { return p.grpcAddr }

// HTTPAddr returns the host:port the HTTP token-exchange listens on.
func (p *Process) HTTPAddr() string { return p.httpAddr }

// BaseURL returns the http://host:port of the HTTP token-exchange
// listener, which serves POST /v1/oauth/token.
func (p *Process) BaseURL() string { return "http://" + p.httpAddr }

// waitReady polls the gRPC and HTTP ports until a TCP dial to each
// succeeds.
func (p *Process) waitReady(d time.Duration) error {
	deadline := time.Now().Add(d)
	for _, addr := range []string{p.grpcAddr, p.httpAddr} {
		if err := waitDial(addr, deadline); err != nil {
			return fmt.Errorf("token-service %s did not accept connections within %s: %w", addr, d, err)
		}
	}
	return nil
}

// waitDial polls addr until a TCP dial succeeds or deadline passes.
func waitDial(addr string, deadline time.Time) error {
	var lastErr error
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		lastErr = err
		time.Sleep(100 * time.Millisecond)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("deadline passed")
	}
	return lastErr
}

// DialGRPC returns a TokenServiceClient connected to this process's
// gRPC listener using insecure transport. Tier-4 tests use it to
// drive the server directly when they want to verify it responds; the
// gateway uses its own dial path in production.
func (p *Process) DialGRPC(t testing.TB) tokensv1.TokenServiceClient {
	t.Helper()
	conn, err := grpc.NewClient(p.grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial token-service grpc: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return tokensv1.NewTokenServiceClient(conn)
}

// freePort returns an open TCP port the OS assigned on 127.0.0.1.
func freePort() (int, error) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer lis.Close()
	return lis.Addr().(*net.TCPAddr).Port, nil
}

// Ping issues a no-op AssignCredentials with a deliberately bogus pool
// to confirm the server responds (rejects with a clear error). It is a
// liveness probe for tier-4 tests that need the server up but do not
// have credential pools registered.
func (p *Process) Ping(t testing.TB) error {
	t.Helper()
	client := p.DialGRPC(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := client.AssignCredentials(ctx, &tokensv1.AssignCredentialsRequest{
		TenantId:  "ping",
		SessionId: "ping",
		PoolIds:   []string{"nonexistent"},
	})
	return err
}
