// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local timing coverage for the adapter's SIGTERM exit path.
//
// The kubelet SIGTERMs the adapter container when an agent pod terminates,
// directly or through the preStop drain hook, and SIGKILLs it at the pod's
// terminationGracePeriodSeconds (default 120s). The adapter's exit path runs
// the bounded DemoteSDK teardown, closes CH-RUNTIMEOPS, and gracefully stops
// the gRPC server; once the server stops serving, main's deferred pod-scope
// teardown closes the runtime's CH-MSGSOCK connection and unlinks its
// listener, and the trace provider flushes. A graceful stop waits for every
// pending RPC, and a session's CH-ATTACH stream is a pending RPC the gateway
// holds open for the binding's life. Whether that wait can outlast the grace
// deadline cannot be settled by reading the code, because it depends on
// which party ends the stream first, so these cases build and run the real
// cmd/lenny-adapter binary with a real runtime on its socket, put the server
// in each pending-RPC state the exit path can meet, send SIGTERM, and time
// the exit.
//
// The test-side budget is graceBudget. The specification fixes only the
// outer bound, the agent pod's terminationGracePeriodSeconds, which the
// preStop drain waits on less a reaping margin; it fixes no adapter-internal
// bound. A process that exits by itself does so within a few seconds, and one
// that waits on a stream nothing ends never exits, so any budget between the
// two separates them; the default stays inside the tier's per-scenario
// wall-clock budget, and LENNY_TIER7A_ADAPTER_GRACE_BUDGET overrides it.
//
// The cases that hold an established CH-ATTACH stream open while the runtime
// stays up do not exit: the graceful stop waits on the stream, the stream
// ends only when the gateway closes it or the runtime's output ends, and the
// pod-scope teardown that would end the runtime's output is deferred until
// after the server stops. Measured with the budget raised to 115s, the
// process was still running 120s after SIGTERM, past the pod's default
// grace period. Those cases skip, blocked on BUILD-GAPS F-4.6.31, and
// LENNY_TIER7A_ADAPTER_GRACE_RUN_BLOCKED=run runs them.
//
// spec: §4.6.1 (Warm Pool Controller (Pod Lifecycle), "Disruption protection
// for agent pods"), §4.7.10 (Deployment Model, "Runtime process lifetime"),
// §28.5.1 (Gateway-to-pod, CH-ATTACH), §28.5.3 (Intra-pod, CH-MSGSOCK,
// Session frame writes: Pod exit), §6.1 (Adapter SIGTERM behavior during
// sdk_connecting).
package tier7a_load_local_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	"github.com/lennylabs/lenny/pkg/runtimekit"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// graceBudgetEnv overrides graceBudget with a Go duration string.
const graceBudgetEnv = "LENNY_TIER7A_ADAPTER_GRACE_BUDGET"

// defaultGraceBudget is the time the test allows the adapter process from
// SIGTERM to exit. See the package comment for why it is smaller than the
// pod's grace period.
const defaultGraceBudget = 10 * time.Second

// graceObserveSlack is how long past the budget the test keeps watching an
// overrunning process before it SIGKILLs it, so the report states how far
// the exit overran rather than only that it did.
const graceObserveSlack = 5 * time.Second

// graceStartupWait bounds the adapter's startup and each session setup step.
const graceStartupWait = 30 * time.Second

// graceBudget resolves the exit budget from graceBudgetEnv.
func graceBudget(t *testing.T) time.Duration {
	t.Helper()
	v := os.Getenv(graceBudgetEnv)
	if v == "" {
		return defaultGraceBudget
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		t.Fatalf("%s=%q: want a positive Go duration", graceBudgetEnv, v)
	}
	return d
}

// graceBinaries holds the binaries every case runs, built once per test.
type graceBinaries struct {
	adapter string
	runtime string
}

// graceBuild compiles the repository main package pkg into dir.
func graceBuild(t *testing.T, dir, pkg string) string {
	t.Helper()
	bin := filepath.Join(dir, filepath.Base(pkg))
	cmd := exec.Command("go", "build", "-o", bin, "./"+pkg)
	cmd.Dir = schematest.RepoRoot(t)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("build %s: %v", pkg, err)
	}
	return bin
}

// graceLog collects a child process's output; the exec package writes it
// from its own goroutine while the test reads it.
type graceLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// Write appends p to the log.
func (l *graceLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

// String returns the output collected so far.
func (l *graceLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// graceProc is a started child process. exited closes when the process has
// exited, after which waitErr holds what cmd.Wait returned; closing rather
// than sending lets every waiter, including the cleanup, observe the exit.
type graceProc struct {
	cmd     *exec.Cmd
	log     *graceLog
	exited  chan struct{}
	waitErr error
}

// startGraceProc starts cmd, collects its output, and kills it at cleanup.
func startGraceProc(t *testing.T, cmd *exec.Cmd) *graceProc {
	t.Helper()
	p := &graceProc{cmd: cmd, log: &graceLog{}, exited: make(chan struct{})}
	cmd.Stdout = p.log
	cmd.Stderr = p.log
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %s: %v", cmd.Path, err)
	}
	go func() {
		p.waitErr = cmd.Wait()
		close(p.exited)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		select {
		case <-p.exited:
		case <-time.After(5 * time.Second):
		}
	})
	return p
}

// graceAdapter is one running adapter process and the runtime on its socket.
type graceAdapter struct {
	proc     *graceProc
	runtime  *graceProc
	sockPath string
	credDir  string
	client   adapterv1.AdapterClient
}

// startGraceAdapter starts the adapter binary on a loopback port with a
// filesystem-path runtime socket and the agent UID set to the test's own, so
// the runtime the test starts passes the SO_PEERCRED peer check. When
// dialRuntime is true it also starts the echo-concurrent runtime, which
// waits for the manifest the first session start publishes and then dials
// the socket with that manifest's nonce.
func startGraceAdapter(t *testing.T, bins graceBinaries, dialRuntime bool) *graceAdapter {
	t.Helper()
	root := t.TempDir()
	// The Unix sun_path field holds about 104 bytes, which t.TempDir() can
	// exceed, so the socket lives under a short temporary directory.
	sockDir, err := os.MkdirTemp("", "lgb")
	if err != nil {
		t.Fatalf("socket temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	sockPath := filepath.Join(sockDir, "rt.sock")
	credDir := filepath.Join(root, "run-lenny")
	addr := graceLoopbackAddr(t)
	dir := func(name string) string { return filepath.Join(root, name) }
	args := []string{
		"--addr", addr,
		"--runtime-socket", sockPath,
		"--runtime-uid", strconv.Itoa(os.Getuid()),
		"--workspace-base", dir("workspace"),
		"--sessions-root", dir("sessions"),
		"--artifacts-root", dir("artifacts"),
		"--staging-dir", filepath.Join(dir("workspace"), ".staging"),
		"--credentials-dir", credDir,
		"--shared-assets-dir", filepath.Join(dir("workspace"), "shared"),
		"--heartbeat-interval-seconds", "0",
	}
	a := &graceAdapter{sockPath: sockPath, credDir: credDir}
	a.proc = startGraceProc(t, exec.Command(bins.adapter, args...))
	graceWaitServing(t, addr, a.proc)

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial adapter %s: %v", addr, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	a.client = adapterv1.NewAdapterClient(conn)

	if dialRuntime {
		a.runtime = a.startRuntime(t, bins)
	}
	return a
}

// startRuntime starts the echo-concurrent runtime against the adapter's
// socket and manifest.
func (a *graceAdapter) startRuntime(t *testing.T, bins graceBinaries) *graceProc {
	t.Helper()
	cmd := exec.Command(bins.runtime)
	cmd.Env = append(
		os.Environ(),
		runtimekit.SocketEnvVar+"="+a.sockPath,
		runtimekit.ManifestEnvVar+"="+filepath.Join(a.credDir, adapter.ManifestFilename),
	)
	return startGraceProc(t, cmd)
}

// graceLoopbackAddr returns a loopback host:port that was free when asked.
func graceLoopbackAddr(t *testing.T) string {
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

// graceWaitServing polls the adapter's health service until it reports
// SERVING. The SIGTERM handler is installed before the server serves, so a
// SERVING answer also means the handler is in place.
func graceWaitServing(t *testing.T, addr string, p *graceProc) {
	t.Helper()
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial adapter %s: %v", addr, err)
	}
	defer func() { _ = conn.Close() }()
	hc := healthv1.NewHealthClient(conn)
	deadline := time.Now().Add(graceStartupWait)
	for time.Now().Before(deadline) {
		select {
		case <-p.exited:
			t.Fatalf("lenny-adapter exited before serving: %v; log:\n%s", p.waitErr, p.log.String())
		default:
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		resp, err := hc.Check(ctx, &healthv1.HealthCheckRequest{})
		cancel()
		if err == nil && resp.GetStatus() == healthv1.HealthCheckResponse_SERVING {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("lenny-adapter never reported SERVING on %s; log:\n%s", addr, p.log.String())
}

// startGraceSession starts sessionID on the adapter.
func startGraceSession(t *testing.T, a *graceAdapter, sessionID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), graceStartupWait)
	defer cancel()
	if _, err := a.client.StartSession(ctx, &adapterv1.StartSessionRequest{
		SessionId: &adapterv1.SessionId{Value: sessionID},
		Runtime:   "echo-concurrent",
	}); err != nil {
		t.Fatalf("StartSession(%s): %v; adapter log:\n%s", sessionID, err, a.proc.log.String())
	}
}

// graceMessage encodes one message envelope for sessionID carrying text.
func graceMessage(t *testing.T, sessionID, id, text string) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"type":      "message",
		"id":        id,
		"sessionId": sessionID,
		"input":     []map[string]any{{"type": "text", "inline": text}},
	})
	if err != nil {
		t.Fatalf("encode message: %v", err)
	}
	return body
}

// openGraceAttach opens a CH-ATTACH stream for sessionID, completes one
// message round trip so the stream is established end to end, and leaves
// it open, as the gateway's executor holds a session's stream for the
// binding's life. The stream's context is never cancelled by the test
// before the adapter exits.
func openGraceAttach(t *testing.T, a *graceAdapter, sessionID string) adapterv1.Adapter_AttachClient {
	t.Helper()
	stream, err := a.client.Attach(context.Background())
	if err != nil {
		t.Fatalf("Attach(%s): %v", sessionID, err)
	}
	text := "ping-" + sessionID
	if err := stream.Send(&adapterv1.AttachRequest{
		SessionId:    &adapterv1.SessionId{Value: sessionID},
		EnvelopeJson: graceMessage(t, sessionID, "m0_"+sessionID, text),
	}); err != nil {
		t.Fatalf("Send(%s): %v", sessionID, err)
	}
	got := make(chan error, 1)
	go func() {
		for {
			resp, err := stream.Recv()
			if err != nil {
				got <- err
				return
			}
			if bytes.Contains(resp.GetEnvelopeJson(), []byte(text)) {
				got <- nil
				return
			}
		}
	}()
	select {
	case err := <-got:
		if err != nil {
			t.Fatalf("Recv(%s): %v; adapter log:\n%s", sessionID, err, a.proc.log.String())
		}
	case <-time.After(graceStartupWait):
		t.Fatalf("no response on the %s stream within %s", sessionID, graceStartupWait)
	}
	return stream
}

// graceExit is the measured outcome of one SIGTERM.
type graceExit struct {
	elapsed time.Duration
	exited  bool
	err     error
}

// sigtermAndTime sends SIGTERM to the adapter and waits for its exit.
func sigtermAndTime(t *testing.T, a *graceAdapter, budget time.Duration) graceExit {
	t.Helper()
	return awaitGraceExit(a, sigtermAdapter(t, a), budget)
}

// sigtermAdapter sends SIGTERM to the adapter and returns when it was sent.
func sigtermAdapter(t *testing.T, a *graceAdapter) time.Time {
	t.Helper()
	begin := time.Now()
	if err := a.proc.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal SIGTERM: %v", err)
	}
	return begin
}

// awaitGraceExit waits until budget plus graceObserveSlack has passed since
// begin for the adapter to exit, and SIGKILLs it after that window. It makes
// no test assertion, so it may run on any goroutine.
func awaitGraceExit(a *graceAdapter, begin time.Time, budget time.Duration) graceExit {
	select {
	case <-a.proc.exited:
		return graceExit{elapsed: time.Since(begin), exited: true, err: a.proc.waitErr}
	case <-time.After(budget + graceObserveSlack - time.Since(begin)):
		elapsed := time.Since(begin)
		_ = a.proc.cmd.Process.Kill()
		<-a.proc.exited
		return graceExit{elapsed: elapsed}
	}
}

// graceBlockedRunEnv, set to "run", runs the cases blocked on F-4.6.31
// instead of skipping them, so the change that fixes the hang can show them
// failing before it and passing after it.
const graceBlockedRunEnv = "LENNY_TIER7A_ADAPTER_GRACE_RUN_BLOCKED"

// skipGraceBlocked skips a case whose exit the open CH-ATTACH stream holds
// past the budget, unless graceBlockedRunEnv asks for it to run.
func skipGraceBlocked(t *testing.T) {
	t.Helper()
	if os.Getenv(graceBlockedRunEnv) == "run" {
		return
	}
	t.Skip("blocked: F-4.6.31 (owner: R16) the adapter's SIGTERM path waits in GracefulStop on an open CH-ATTACH stream that only the gateway or the runtime can end, and the pod-scope teardown that would end it runs only after the server stops")
}

// requireGraceExit asserts the adapter exited cleanly within budget, and
// that the steps the exit path runs after the server stops ran: the
// pod-scope teardown unlinked the runtime socket and, when a runtime was
// connected, ended its connection, so the runtime read end of file and
// exited.
func requireGraceExit(t *testing.T, a *graceAdapter, got graceExit, budget time.Duration) {
	t.Helper()
	t.Logf("adapter exit after SIGTERM: exited=%t elapsed=%s budget=%s", got.exited, got.elapsed.Round(time.Millisecond), budget)
	if !got.exited {
		t.Fatalf("lenny-adapter still running %s after SIGTERM, past the %s budget; SIGKILLed. log:\n%s",
			got.elapsed.Round(time.Millisecond), budget, a.proc.log.String())
	}
	if got.elapsed > budget {
		t.Errorf("lenny-adapter exited %s after SIGTERM, past the %s budget", got.elapsed.Round(time.Millisecond), budget)
	}
	if got.err != nil {
		t.Errorf("lenny-adapter exit = %v, want a clean exit that ran the deferred teardown; log:\n%s", got.err, a.proc.log.String())
	}
	if _, err := os.Stat(a.sockPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("runtime socket %s after exit: stat err = %v, want it removed by the pod-scope teardown", a.sockPath, err)
	}
	if a.runtime == nil {
		return
	}
	select {
	case <-a.runtime.exited:
	case <-time.After(5 * time.Second):
		t.Errorf("runtime still running 5s after the adapter exited: the pod-scope teardown did not end its connection; runtime log:\n%s",
			a.runtime.log.String())
	}
}

// spec: §4.6.1 (Warm Pool Controller (Pod Lifecycle), "Disruption protection for agent pods"), §4.7.10 (Deployment Model, "Runtime process lifetime"), §28.5.1 (Gateway-to-pod, CH-ATTACH), §28.5.3 (Intra-pod, Session frame writes: Pod exit)
// diagnosis: a failure means the adapter process does not exit within the
// grace budget after SIGTERM, so the kubelet SIGKILLs it at the pod's grace
// deadline and the steps main defers past the gRPC server's stop (the
// runtime connection's pod-scope teardown, the socket unlink, and the trace
// flush) never run. The failing case names the pending-RPC state that held
// the graceful stop open; an open CH-ATTACH stream that only the gateway or
// the runtime can end is the hang BUILD-GAPS F-4.6.31 records.
func TestAdapterExitsWithinGraceBudget(t *testing.T) {
	budget := graceBudget(t)
	binDir := t.TempDir()
	bins := graceBinaries{
		adapter: graceBuild(t, binDir, "cmd/lenny-adapter"),
		runtime: graceBuild(t, binDir, "cmd/runtimes/echo-concurrent"),
	}

	t.Run("no_open_streams", func(t *testing.T) {
		a := startGraceAdapter(t, bins, false)
		requireGraceExit(t, a, sigtermAndTime(t, a, budget), budget)
	})

	t.Run("idle_session_no_stream", func(t *testing.T) {
		a := startGraceAdapter(t, bins, true)
		startGraceSession(t, a, "sess-alice")
		requireGraceExit(t, a, sigtermAndTime(t, a, budget), budget)
	})

	t.Run("in_flight_unary_rpc", func(t *testing.T) {
		graceInFlightUnary(t, bins, budget)
	})

	t.Run("one_open_attach_stream", func(t *testing.T) {
		skipGraceBlocked(t)
		graceOpenStreamsCase(t, bins, budget, 1)
	})

	t.Run("concurrent_open_attach_streams", func(t *testing.T) {
		skipGraceBlocked(t)
		graceOpenStreamsCase(t, bins, budget, 4)
	})

	t.Run("attach_stream_mid_send", func(t *testing.T) {
		skipGraceBlocked(t)
		graceMidSendCase(t, bins, budget)
	})

	t.Run("open_attach_stream_runtime_exits_with_pod", func(t *testing.T) {
		graceRuntimeExitsCase(t, bins, budget)
	})
}

// graceInFlightUnary SIGTERMs the adapter while a StartSession is blocked
// waiting for the runtime to dial, then lets the runtime dial. A graceful
// stop lets the in-flight unary RPC finish rather than cutting it off, and
// the process then exits.
func graceInFlightUnary(t *testing.T, bins graceBinaries, budget time.Duration) {
	a := startGraceAdapter(t, bins, false)
	startErr := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), graceStartupWait)
		defer cancel()
		_, err := a.client.StartSession(ctx, &adapterv1.StartSessionRequest{
			SessionId: &adapterv1.SessionId{Value: "sess-alice"},
			Runtime:   "echo-concurrent",
		})
		startErr <- err
	}()
	// The StartSession publishes the manifest and then waits for the
	// runtime's dial, so once the manifest file exists the RPC is in flight.
	graceWaitFile(t, filepath.Join(a.credDir, adapter.ManifestFilename))
	begin := sigtermAdapter(t, a)
	exitCh := make(chan graceExit, 1)
	go func() { exitCh <- awaitGraceExit(a, begin, budget) }()
	time.Sleep(500 * time.Millisecond)
	a.runtime = a.startRuntime(t, bins)
	select {
	case err := <-startErr:
		if err != nil {
			t.Errorf("in-flight StartSession = %v, want it to complete under the graceful stop", err)
		}
	case <-time.After(graceStartupWait):
		t.Errorf("in-flight StartSession did not return within %s", graceStartupWait)
	}
	requireGraceExit(t, a, <-exitCh, budget)
}

// graceWaitFile waits for path to exist.
func graceWaitFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(graceStartupWait)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s never appeared", path)
}

// graceOpenStreamsCase starts n sessions, holds one established CH-ATTACH
// stream open on each, and SIGTERMs the adapter while the runtime stays up.
func graceOpenStreamsCase(t *testing.T, bins graceBinaries, budget time.Duration, n int) {
	a := startGraceAdapter(t, bins, true)
	streams := make([]adapterv1.Adapter_AttachClient, 0, n)
	for i := 0; i < n; i++ {
		sessionID := fmt.Sprintf("sess-%02d", i)
		startGraceSession(t, a, sessionID)
		streams = append(streams, openGraceAttach(t, a, sessionID))
	}
	got := sigtermAndTime(t, a, budget)
	for i, s := range streams {
		graceDrainStream(t, i, s)
	}
	requireGraceExit(t, a, got, budget)
}

// graceDrainStream reads a stream to its end after the adapter exited, and
// reports how it ended, so the log records whether the server ended it.
func graceDrainStream(t *testing.T, i int, s adapterv1.Adapter_AttachClient) {
	t.Helper()
	for {
		if _, err := s.Recv(); err != nil {
			if !errors.Is(err, io.EOF) {
				t.Logf("stream %d ended: %v", i, err)
			}
			return
		}
	}
}

// graceMidSendFrames and graceMidSendBytes size the burst that leaves the
// adapter blocked in stream.Send: the test never reads the echoes, so the
// stream's flow-control window fills and the handler's send blocks.
const (
	graceMidSendFrames = 128
	graceMidSendBytes  = 32 << 10
)

// graceMidSendCase holds a stream whose handler is blocked sending runtime
// output the client is not reading when SIGTERM arrives.
func graceMidSendCase(t *testing.T, bins graceBinaries, budget time.Duration) {
	a := startGraceAdapter(t, bins, true)
	startGraceSession(t, a, "sess-alice")
	stream := openGraceAttach(t, a, "sess-alice")
	payload := strings.Repeat("x", graceMidSendBytes)
	sent := make(chan int, 1)
	go func() {
		n := 0
		for ; n < graceMidSendFrames; n++ {
			if err := stream.Send(&adapterv1.AttachRequest{
				SessionId:    &adapterv1.SessionId{Value: "sess-alice"},
				EnvelopeJson: graceMessage(t, "sess-alice", fmt.Sprintf("m%d", n+1), payload),
			}); err != nil {
				break
			}
		}
		sent <- n
	}()
	// Let the burst fill the window in both directions before the signal.
	time.Sleep(time.Second)
	got := sigtermAndTime(t, a, budget)
	select {
	case n := <-sent:
		t.Logf("client sent %d of %d frames before the stream ended", n, graceMidSendFrames)
	case <-time.After(5 * time.Second):
		t.Logf("client send still blocked after the adapter exit")
	}
	requireGraceExit(t, a, got, budget)
}

// graceRuntimeExitsCase holds an open stream and SIGTERMs the runtime with
// the adapter, as the kubelet does to every container of a terminating pod.
// The runtime's exit ends its connection, which ends the stream's runtime
// output, so the handler returns and the graceful stop completes.
func graceRuntimeExitsCase(t *testing.T, bins graceBinaries, budget time.Duration) {
	a := startGraceAdapter(t, bins, true)
	startGraceSession(t, a, "sess-alice")
	stream := openGraceAttach(t, a, "sess-alice")
	if err := a.runtime.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal the runtime: %v", err)
	}
	got := sigtermAndTime(t, a, budget)
	graceDrainStream(t, 0, stream)
	requireGraceExit(t, a, got, budget)
}
