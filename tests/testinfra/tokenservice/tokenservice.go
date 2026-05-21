// SPDX-License-Identifier: MIT

// Package tokenservice is the testinfra helper that boots
// cmd/lenny-token-service as a subprocess for tier-4 integration
// tests. Builds the binary into a temp dir on first call, then
// launches it bound to a random free gRPC port. Provides GRPCAddr()
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
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	tokensv1 "github.com/lennylabs/lenny/pkg/proto/tokenservice/v1"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// Process represents a running lenny-token-service subprocess.
type Process struct {
	cmd      *exec.Cmd
	grpcAddr string
	httpAddr string
	stderr   *os.File
}

// SkipUnlessAvailable t.Skips when the Go toolchain is missing.
func SkipUnlessAvailable(t testing.TB) {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("tokenservice.SkipUnlessAvailable: go not on PATH: %v", err)
	}
}

// Start builds + spawns cmd/lenny-token-service on a random free
// gRPC and HTTP port. Returns when the gRPC listener is responsive.
// Skips the test when go is not on PATH (CI environments without Go).
func Start(t testing.TB) *Process {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go not on PATH: %v", err)
	}

	tmp, err := os.MkdirTemp("", "lenny-token-service-it-*")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmp) })

	binary := filepath.Join(tmp, "lenny-token-service")
	root := schematest.RepoRoot(t)
	build := exec.Command("go", "build", "-o", binary, "./cmd/lenny-token-service")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build lenny-token-service: %v\n%s", err, out)
	}

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

	stderrPath := filepath.Join(tmp, "stderr.log")
	stderrFile, err := os.Create(stderrPath)
	if err != nil {
		t.Fatalf("create stderr log: %v", err)
	}

	cmd := exec.Command(binary,
		"--addr", httpAddr,
		"--grpc-addr", grpcAddr,
	)
	cmd.Stdout = stderrFile
	cmd.Stderr = stderrFile
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Start(); err != nil {
		t.Fatalf("start token-service: %v", err)
	}
	p := &Process{cmd: cmd, grpcAddr: grpcAddr, httpAddr: httpAddr, stderr: stderrFile}

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
			t.Logf("token-service subprocess did not exit after SIGKILL; goroutine leaked")
		}
	})

	if err := p.waitReady(5 * time.Second); err != nil {
		b, _ := os.ReadFile(stderrPath)
		t.Fatalf("token-service not ready: %v\nlogs:\n%s", err, b)
	}
	return p
}

// GRPCAddr returns the host:port the gRPC TokenService listens on.
func (p *Process) GRPCAddr() string { return p.grpcAddr }

// HTTPAddr returns the host:port the HTTP token-exchange listens on.
func (p *Process) HTTPAddr() string { return p.httpAddr }

// waitReady polls the gRPC port until a TCP dial succeeds.
func (p *Process) waitReady(d time.Duration) error {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", p.grpcAddr, 500*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("token-service gRPC %s did not accept connections within %s", p.grpcAddr, d)
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
