// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 flows for the lifetime of the pod's runtime socket (CH-MSGSOCK).
// The adapter binds that address once when it starts, and the address
// outlives every session the pod serves: a session teardown closes the
// session's runtime connection and leaves the listener bound, and the
// adapter process releases the listener when it exits.
//
// The file carries two flows. The first drives a real adapter.Server over
// its gRPC contract through two complete sessions in sequence on one
// SocketRuntimeProcess, so the second session's runtime has to dial the same
// address the first session's runtime used. The second builds and runs the
// real cmd/lenny-adapter binary with a filesystem-path runtime socket,
// stops it with SIGTERM, and asserts that the socket file is gone, which is
// what lets a restarted adapter bind the same path.
//
// spec: §5.2 (Pool Configuration and Execution Modes), §15.4.3 (Runtime
// Integration Levels), §4.7.10 (Deployment Model), §28.5.3 (Intra-pod).
package tier4_integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/lennylabs/lenny/pkg/adapter"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// spec: §15.4.3 (Runtime Integration Levels), §5.2 (Pool Configuration and
// Execution Modes), §4.7.10 (Deployment Model).
// diagnosis: a failure means the pod lost its runtime ingress at the first
// session's end: the first session's teardown unbound the pod's runtime
// socket address, so the second session's runtime had nothing to dial and no
// recycling pod can serve a second session.
func TestAdapterServesTwoSequentialSessionsOverOneRuntimeSocket_spec_15_4_3(t *testing.T) {
	echoBin := buildRepoBinary(t, "cmd/runtimes/echo")

	base := t.TempDir()
	srv := adapter.New("tier4-sequential-sessions")
	srv.WorkspaceBase = filepath.Join(base, "workspace")
	srv.SessionsRoot = filepath.Join(base, "sessions")
	srv.ArtifactsRoot = filepath.Join(base, "artifacts")
	srv.CredentialsDir = filepath.Join(base, "run", "lenny")

	rt, err := adapter.NewSocketRuntimeProcess(concurrentSocketAddr(t))
	if err != nil {
		t.Fatalf("bind pod runtime socket: %v", err)
	}
	// The listener is pod-scoped and outlives every session Close, so it is
	// released separately. Registered first, this cleanup runs last.
	t.Cleanup(func() { _ = rt.CloseListener() })
	// SpawnPath is the test-only spawn hook: each session whose start finds
	// no live runtime connection execs the echo runtime, which dials the
	// pod's address. The flow therefore asserts the adapter half alone.
	rt.SpawnPath = echoBin
	rt.AcceptTimeout = 15 * time.Second
	srv.Runtime = rt
	addr := rt.SocketPath()

	client := concurrentAdapterClient(t, srv)
	for _, sessionID := range []string{"sess-alice", "sess-bob"} {
		runOneSession(t, client, sessionID)
		if got := rt.SocketPath(); got != addr {
			t.Fatalf("runtime socket address after %s = %q, want the boot-time address %q", sessionID, got, addr)
		}
	}
}

// runOneSession drives one complete session on the shared adapter:
// StartSession, one message round trip on an Attach stream, and an
// unconditional Shutdown. A failure to start or to receive the response is
// fatal, because it means the pod cannot serve this session.
func runOneSession(t *testing.T, client adapterv1.AdapterClient, sessionID string) {
	t.Helper()
	ctx := context.Background()
	if _, err := client.StartSession(ctx, &adapterv1.StartSessionRequest{
		SessionId: &adapterv1.SessionId{Value: sessionID},
		Runtime:   "echo",
	}); err != nil {
		t.Fatalf("StartSession(%s): %v", sessionID, err)
	}

	streamCtx, cancel := context.WithCancel(ctx)
	stream, err := client.Attach(streamCtx)
	if err != nil {
		cancel()
		t.Fatalf("Attach(%s): %v", sessionID, err)
	}
	text := "ping-" + sessionID
	body, err := json.Marshal(map[string]any{
		"type":  "message",
		"id":    "m_" + sessionID,
		"input": []map[string]any{{"type": "text", "inline": text}},
	})
	if err != nil {
		cancel()
		t.Fatalf("encode message for %s: %v", sessionID, err)
	}
	if err := stream.Send(&adapterv1.AttachRequest{
		SessionId:    &adapterv1.SessionId{Value: sessionID},
		EnvelopeJson: body,
	}); err != nil {
		cancel()
		t.Fatalf("Send message(%s): %v", sessionID, err)
	}
	resp := recvResponse(t, stream)
	cancel()
	if len(resp.Output) != 1 || !strings.Contains(resp.Output[0].Inline, text) {
		t.Fatalf("%s response output = %+v, want the echoed %q", sessionID, resp.Output, text)
	}

	if _, err := client.Shutdown(ctx, &adapterv1.ShutdownRequest{
		SessionId:             &adapterv1.SessionId{Value: sessionID},
		UnconditionalTeardown: true,
	}); err != nil {
		t.Fatalf("Shutdown(%s): %v", sessionID, err)
	}
}

// spec: §4.7.10 (Deployment Model), §5.2 (Pool Configuration and Execution
// Modes).
// diagnosis: a failure means the adapter process exits without releasing its
// runtime socket, so a filesystem-path socket file survives the process and
// blocks a restarted adapter from binding the same path. If the process did
// not exit cleanly, read the captured adapter log for the failing step.
func TestAdapterProcessUnlinksItsRuntimeSocketOnSIGTERM_spec_5_2(t *testing.T) {
	adapterBin := buildRepoBinary(t, "cmd/lenny-adapter")

	// The Unix sun_path field holds about 104 bytes, and t.TempDir() can
	// exceed that, so the socket lives under a short temp directory.
	sockDir, err := os.MkdirTemp("", "lsk")
	if err != nil {
		t.Fatalf("socket temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	sockPath := filepath.Join(sockDir, "rt.sock")

	grpcAddr := reserveLoopbackAddr(t)
	cmd := exec.Command(adapterBin, adapterArgsOutsideAPod(t.TempDir(), grpcAddr, sockPath)...)
	logs := &processLog{}
	cmd.Stdout = logs
	cmd.Stderr = logs
	if err := cmd.Start(); err != nil {
		t.Fatalf("start lenny-adapter: %v", err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		select {
		case <-exited:
		case <-time.After(5 * time.Second):
		}
	})

	// The socket file appears before main installs its SIGTERM handler, so
	// the health check is the readiness signal.
	waitForHealthServing(t, grpcAddr, exited, logs)
	if _, err := os.Stat(sockPath); err != nil {
		t.Fatalf("runtime socket %s absent while the adapter serves: %v", sockPath, err)
	}

	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal SIGTERM: %v", err)
	}
	select {
	case err := <-exited:
		if err != nil {
			t.Fatalf("lenny-adapter exit after SIGTERM = %v, want a clean exit; log:\n%s", err, logs.String())
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("lenny-adapter did not exit within 30s of SIGTERM; log:\n%s", logs.String())
	}
	if _, err := os.Stat(sockPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("runtime socket %s after exit: stat err = %v, want it removed", sockPath, err)
	}
}

// adapterArgsOutsideAPod returns the lenny-adapter flags that let the binary
// start on a developer host: every pod-mounted directory points under root,
// the gRPC server binds grpcAddr, and the sidecar runtime transport binds
// sockPath.
func adapterArgsOutsideAPod(root, grpcAddr, sockPath string) []string {
	dir := func(name string) string { return filepath.Join(root, name) }
	return []string{
		"--addr", grpcAddr,
		"--runtime-socket", sockPath,
		"--workspace-base", dir("workspace"),
		"--sessions-root", dir("sessions"),
		"--artifacts-root", dir("artifacts"),
		"--staging-dir", filepath.Join(dir("workspace"), ".staging"),
		"--credentials-dir", dir("run-lenny"),
		"--shared-assets-dir", filepath.Join(dir("workspace"), "shared"),
		"--heartbeat-interval-seconds", "0",
	}
}

// reserveLoopbackAddr returns a loopback host:port that was free when the
// test asked the kernel for one.
func reserveLoopbackAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve loopback port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	if err := l.Close(); err != nil {
		t.Fatalf("release reserved port: %v", err)
	}
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
}

// waitForHealthServing polls the grpc.health.v1 service on addr until it
// reports SERVING, failing the test if the process exits first or the
// deadline passes.
func waitForHealthServing(t *testing.T, addr string, exited <-chan error, logs *processLog) {
	t.Helper()
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial adapter %s: %v", addr, err)
	}
	defer func() { _ = conn.Close() }()
	hc := healthv1.NewHealthClient(conn)
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-exited:
			t.Fatalf("lenny-adapter exited before serving: %v; log:\n%s", err, logs.String())
		default:
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		resp, err := hc.Check(ctx, &healthv1.HealthCheckRequest{})
		cancel()
		if err == nil && resp.GetStatus() == healthv1.HealthCheckResponse_SERVING {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("lenny-adapter health never reported SERVING on %s; log:\n%s", addr, logs.String())
}

// processLog collects a child process's output. The exec package copies
// the output on its own goroutine, and the test reads the log while the
// process may still be running, so access is serialized.
type processLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// Write appends p to the log.
func (l *processLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

// String returns the output collected so far.
func (l *processLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// buildRepoBinary compiles the repository's main package pkg into a temp
// path and returns the binary's path.
func buildRepoBinary(t *testing.T, pkg string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), filepath.Base(pkg))
	cmd := exec.Command("go", "build", "-o", bin, "./"+pkg)
	cmd.Dir = schematest.RepoRoot(t)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("build %s: %v", pkg, err)
	}
	return bin
}
