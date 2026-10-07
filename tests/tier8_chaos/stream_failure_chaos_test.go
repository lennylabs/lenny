// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 chaos coverage for the gateway's handling of a CH-ATTACH stream that
// ends because a link under it failed.
//
// Two failures are injected against an in-process adapter and the real pod
// executor and session server. The first kills the runtime's CH-MSGSOCK
// connection to the adapter, mid-turn or between turns. The adapter's stream
// then closes with the runtime's output, which the gateway discards without a
// report; the runtime has exited, and the next message delivery's Attach ends
// with INTERNAL, which is reported once as runtime_crash. The second
// partitions LNK-POD-GRPC between the gateway and the adapter. The gateway's
// keepalive probe fails, the session's stream ends with UNAVAILABLE, and the
// gateway discards the stream without a report, so the session stays running
// and keeps its binding.
//
// spec: §28.5.1 (Gateway-to-pod, CH-ATTACH Timing. and Degradation.), §7.3
// (Retry and Resume), §11.3 (Timeouts and Cancellation).
package tier8_chaos_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/api/v1/session"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/environment/transcriptstore"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

const (
	// streamChaosWait bounds every wait for an asynchronous outcome.
	streamChaosWait = 10 * time.Second
	// streamChaosNonce is the manifest nonce the in-process runtime presents
	// in its connection handshake.
	streamChaosNonce = "nonce-stream-chaos"
	// streamChaosHang is the message text the runtimes leave unanswered.
	streamChaosHang = "hang"
	// podKeepaliveTime and podKeepaliveTimeout are the §11.3 gateway-to-pod
	// keepalive interval and timeout.
	podKeepaliveTime    = 10 * time.Second
	podKeepaliveTimeout = 5 * time.Second
)

// streamChaosGateway is one replica's pod executor and session server for one
// bound session, with every stream-failure report counted.
type streamChaosGateway struct {
	id       string
	store    sessionstore.Store
	registry *podsession.Registry
	exec     *executor.PodExecutor
	reports  atomic.Int64
	base     string
}

// newStreamChaosGateway binds id to cl, commits a running row at coordination
// generation 1, and serves the session server over a real http.Server.
func newStreamChaosGateway(t *testing.T, id string, cl *adapterclient.Client) *streamChaosGateway {
	t.Helper()
	g := &streamChaosGateway{id: id, store: memstore.New(), registry: podsession.NewRegistry()}
	sandbox := "sbx-" + id
	g.registry.Put(&podsession.BindResult{SessionID: id, TenantID: "acme", SandboxName: sandbox, Adapter: cl, CoordinationGeneration: 1})
	scheme := k8sruntime.NewScheme()
	if err := lennyv1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	g.exec = executor.NewPodExecutor(g.registry, &podsession.Binder{
		Client: fake.NewClientBuilder().WithScheme(scheme).Build(), Namespace: "lenny-agents",
	})
	t.Cleanup(func() { g.exec.EvictStream(id) })
	srv := sessionserver.New(g.store, sessionserver.Options{
		Executor: g.exec, PodRegistry: g.registry, Transcripts: transcriptstore.NewMemory(),
	})
	g.exec.SetStreamFailureHandler(func(tenantID, sessionID, sandboxName string, cause error) {
		g.reports.Add(1)
		srv.ReportAttachStreamFailure(tenantID, sessionID, sandboxName, cause)
	})
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(hs.Close)
	g.base = hs.URL
	now := time.Now().UTC()
	if err := g.store.Create(context.Background(), sessionstore.Session{
		ID: id, TenantID: "acme", State: session.StateRunning, RuntimeRef: "echo",
		PodAssignment: sandbox, CoordinationGeneration: 1, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
	return g
}

// deliver posts one REST message and returns the HTTP status and the
// delivery receipt's status.
func (g *streamChaosGateway) deliver(t *testing.T, content string) (int, string) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"messages": []any{map[string]any{"role": "user", "content": content}}})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.base+"/v1/sessions/"+g.id+"/messages", strings.NewReader(string(body)))
	if err != nil {
		t.Errorf("build request: %v", err)
		return 0, ""
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Errorf("POST %q: %v", content, err)
		return 0, ""
	}
	defer resp.Body.Close()
	var out struct {
		DeliveryReceipt struct {
			Status string `json:"status"`
		} `json:"deliveryReceipt"`
	}
	raw, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out.DeliveryReceipt.Status
}

func (g *streamChaosGateway) row(t *testing.T) sessionstore.Session {
	t.Helper()
	row, err := g.store.Get(context.Background(), "acme", g.id)
	if err != nil {
		t.Fatalf("get %s: %v", g.id, err)
	}
	return row
}

// assertUnreported asserts the session is still running with no retry
// counted, holds its binding, and produced no stream-failure report.
func (g *streamChaosGateway) assertUnreported(t *testing.T, when string) {
	t.Helper()
	if n := g.reports.Load(); n != 0 {
		t.Errorf("%s: stream-failure reports = %d, want none", when, n)
	}
	if row := g.row(t); row.State != session.StateRunning || row.RetryCount != 0 {
		t.Errorf("%s: row = %s/retries %d, want running/0", when, row.State, row.RetryCount)
	}
	if _, ok := g.registry.Get(g.id); !ok {
		t.Errorf("%s: the session lost its binding", when)
	}
}

// executorReaders counts the live goroutines running the pod executor's
// stream reader, which exits only after the stream's end handling has run.
func executorReaders() int {
	buf := make([]byte, 1<<22)
	buf = buf[:runtime.Stack(buf, true)]
	return strings.Count(string(buf), "executor.(*PodExecutor).readConn")
}

// awaitReaders waits for the executor's reader count to fall to want.
func awaitReaders(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(streamChaosWait)
	for executorReaders() > want {
		if time.Now().After(deadline) {
			t.Fatalf("executor readers = %d, want %d: the stream's end was not handled", executorReaders(), want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// socketRuntime is a runtime process that dials the adapter's CH-MSGSOCK
// socket, passes the connection handshake, acks heartbeats, and echoes every
// message except streamChaosHang. kill closes its connection, as a runtime
// process that crashed.
type socketRuntime struct {
	conn     net.Conn
	gotHang  chan struct{}
	hangOnce sync.Once
}

// dialSocketRuntime connects to the adapter's runtime socket and serves it on
// a goroutine.
func dialSocketRuntime(t *testing.T, socket string) *socketRuntime {
	t.Helper()
	conn, err := net.Dial("unix", socket)
	if err != nil {
		t.Fatalf("dial runtime socket %s: %v", socket, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if _, err := fmt.Fprintf(conn, "{\"_lennyNonce\":%q}\n", streamChaosNonce); err != nil {
		t.Fatalf("write runtime handshake: %v", err)
	}
	r := &socketRuntime{conn: conn, gotHang: make(chan struct{})}
	go r.serve()
	return r
}

func (r *socketRuntime) kill() { _ = r.conn.Close() }

func (r *socketRuntime) serve() {
	sc := bufio.NewScanner(r.conn)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		var in struct {
			Type      string `json:"type"`
			SessionID string `json:"sessionId"`
			Input     []struct {
				Inline string `json:"inline"`
			} `json:"input"`
		}
		if json.Unmarshal(sc.Bytes(), &in) != nil {
			continue
		}
		var out any
		switch in.Type {
		case "heartbeat":
			out = map[string]any{"type": "heartbeat_ack"}
		case "message":
			text := ""
			if len(in.Input) > 0 {
				text = in.Input[0].Inline
			}
			if text == streamChaosHang {
				r.hangOnce.Do(func() { close(r.gotHang) })
				continue
			}
			out = map[string]any{"type": "response", "sessionId": in.SessionID, "text": "echo:" + text}
		default:
			continue
		}
		b, _ := json.Marshal(out)
		if _, err := r.conn.Write(append(b, '\n')); err != nil {
			return
		}
	}
}

// attachEnds counts the adapter's Attach RPCs that are still running.
type attachEnds struct{ active atomic.Int64 }

func (a *attachEnds) option() grpc.ServerOption {
	return grpc.ChainStreamInterceptor(func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, h grpc.StreamHandler) error {
		if strings.HasSuffix(info.FullMethod, "/Attach") {
			a.active.Add(1)
			defer a.active.Add(-1)
		}
		return h(srv, ss)
	})
}

func (a *attachEnds) await(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(streamChaosWait)
	for a.active.Load() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("%d Attach stream(s) still open on the adapter", a.active.Load())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// socketAdapter serves an adapter whose runtime is a SocketRuntimeProcess,
// starts sessionID on it with a dialled runtime, and returns the runtime, the
// adapter client, and the adapter's Attach counter.
func socketAdapter(t *testing.T, sessionID string) (*socketRuntime, *adapterclient.Client, *attachEnds) {
	t.Helper()
	socket := fmt.Sprintf("@lenny-t8-stream-%d-%s", os.Getpid(), strings.ReplaceAll(t.Name(), "/", "-"))
	sp, err := adapter.NewSocketRuntimeProcess(socket,
		adapter.SocketPeerAuth{ExpectedUID: uint32(os.Getuid())}, func() string { return streamChaosNonce })
	if err != nil {
		t.Fatalf("NewSocketRuntimeProcess: %v", err)
	}
	t.Cleanup(func() { _ = sp.CloseListener() })
	srv := adapter.New("stream-chaos")
	srv.WorkspaceBase = t.TempDir()
	srv.Runtime = sp
	ends := &attachEnds{}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	gs := adapter.NewGRPCServer(srv, ends.option())
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	cl, err := adapterclient.Dial(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial adapter: %v", err)
	}
	t.Cleanup(func() { _ = cl.Close() })
	rt := dialSocketRuntime(t, sp.SocketPath())
	if err := cl.StartSession(context.Background(), adapterclient.StartSessionParams{SessionID: sessionID, Runtime: "echo"}); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	return rt, cl, ends
}

// spec: 28.5.1 (Gateway-to-pod), 7.3 (Retry and Resume)
// diagnosis: killing the runtime's connection to the adapter was reported as a
// stream failure while the stream closed with the runtime's output, or the
// exited runtime was never reported. A stream that closes when the runtime's
// output ends is discarded without a report, mid-turn or between turns; the
// next message delivery's Attach ends with INTERNAL and is reported once as
// runtime_crash, which moves the session to resume_pending. A report before
// that delivery, or more than one, is the failure.
func TestRuntimeConnectionKillReportsOnTheNextDelivery_spec_28_5_1(t *testing.T) {
	for _, midTurn := range []bool{false, true} {
		name := "between turns"
		if midTurn {
			name = "mid-turn"
		}
		t.Run(name, func(t *testing.T) {
			id := "sess-kill-" + strings.ReplaceAll(name, " ", "-")
			rt, cl, ends := socketAdapter(t, id)
			g := newStreamChaosGateway(t, id, cl)
			readers := executorReaders()

			if code, _ := g.deliver(t, "turn-1"); code != http.StatusOK {
				t.Fatalf("first message: status %d", code)
			}
			hung := make(chan int, 1)
			if midTurn {
				go func() {
					code, _ := g.deliver(t, streamChaosHang)
					hung <- code
				}()
				select {
				case <-rt.gotHang:
				case <-time.After(streamChaosWait):
					t.Fatal("the runtime never received the hung turn")
				}
			}
			rt.kill()
			ends.await(t)
			awaitReaders(t, readers)
			if midTurn {
				if code := <-hung; code == http.StatusOK {
					t.Error("the turn in flight when the runtime died was delivered")
				}
			}
			g.assertUnreported(t, "after the runtime's connection ended")

			if code, _ := g.deliver(t, "turn-2"); code == http.StatusOK {
				t.Fatal("a message to the exited runtime was delivered")
			}
			if n := g.reports.Load(); n != 1 {
				t.Fatalf("stream-failure reports after the next delivery = %d, want 1", n)
			}
			row := g.row(t)
			if row.State != session.StateResumePending || row.FailureReason != string(session.FailureRuntimeCrash) || row.RetryCount != 1 {
				t.Errorf("row = %s/%q/retries %d, want resume_pending/runtime_crash/1", row.State, row.FailureReason, row.RetryCount)
			}
			// The released session is resume_pending, so a later message is
			// held for the resume rather than delivered, and opens no stream.
			if _, receipt := g.deliver(t, "turn-3"); receipt == string(session.DeliveryStatusDelivered) {
				t.Error("a message after the report was delivered to the pod")
			}
			if n := g.reports.Load(); n != 1 {
				t.Errorf("stream-failure reports after a delivery to the released session = %d, want still 1", n)
			}
		})
	}
}

// blackholeProxy forwards TCP between the gateway and the adapter until it is
// partitioned, after which it reads and drops every byte in both directions
// and keeps both connections open, as a network partition does.
type blackholeProxy struct {
	ln          net.Listener
	upstream    string
	partitioned atomic.Bool
}

func newBlackholeProxy(t *testing.T, upstream string) *blackholeProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen proxy: %v", err)
	}
	p := &blackholeProxy{ln: ln, upstream: upstream}
	t.Cleanup(func() { _ = ln.Close() })
	go p.serve(t)
	return p
}

func (p *blackholeProxy) serve(t *testing.T) {
	for {
		down, err := p.ln.Accept()
		if err != nil {
			return
		}
		up, err := net.Dial("tcp", p.upstream)
		if err != nil {
			_ = down.Close()
			continue
		}
		t.Cleanup(func() { _ = down.Close(); _ = up.Close() })
		go p.pipe(up, down)
		go p.pipe(down, up)
	}
}

func (p *blackholeProxy) pipe(dst, src net.Conn) {
	buf := make([]byte, 32*1024)
	for {
		n, err := src.Read(buf)
		if err != nil {
			_ = dst.Close()
			return
		}
		if p.partitioned.Load() {
			continue
		}
		if _, err := dst.Write(buf[:n]); err != nil {
			_ = src.Close()
			return
		}
	}
}

// echoRuntime is an in-process runtime that echoes every message.
type echoRuntime struct{ out chan []byte }

func (r *echoRuntime) Start(context.Context, string) error { return nil }
func (r *echoRuntime) WriteEnvelope(sessionID string, envelope []byte) error {
	var in struct {
		Type      string `json:"type"`
		SessionID string `json:"sessionId"`
		Input     []struct {
			Inline string `json:"inline"`
		} `json:"input"`
	}
	if json.Unmarshal(envelope, &in) != nil || in.Type != "message" {
		return nil
	}
	text := ""
	if len(in.Input) > 0 {
		text = in.Input[0].Inline
	}
	if in.SessionID == "" {
		in.SessionID = sessionID
	}
	b, _ := json.Marshal(map[string]any{"type": "response", "sessionId": in.SessionID, "text": "echo:" + text})
	r.out <- b
	return nil
}
func (r *echoRuntime) Output(context.Context, string) (<-chan []byte, error) { return r.out, nil }
func (r *echoRuntime) Interrupt(context.Context, string, bool) error         { return nil }
func (r *echoRuntime) Close(context.Context, string) error                   { return nil }

// attachEndRecorder records the status the gateway's Attach stream ended with,
// as the client's RecvMsg returned it.
type attachEndRecorder struct {
	once sync.Once
	end  chan error
}

func (a *attachEndRecorder) interceptor() grpc.DialOption {
	return grpc.WithChainStreamInterceptor(func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn,
		method string, streamer grpc.Streamer, opts ...grpc.CallOption,
	) (grpc.ClientStream, error) {
		cs, err := streamer(ctx, desc, cc, method, opts...)
		if err != nil || !strings.HasSuffix(method, "/Attach") {
			return cs, err
		}
		return &recordingClientStream{ClientStream: cs, rec: a}, nil
	})
}

type recordingClientStream struct {
	grpc.ClientStream
	rec *attachEndRecorder
}

func (s *recordingClientStream) RecvMsg(m any) error {
	err := s.ClientStream.RecvMsg(m)
	if err != nil {
		s.rec.once.Do(func() { s.rec.end <- err })
	}
	return err
}

// spec: 28.5.1 (Gateway-to-pod), 11.3 (Timeouts and Cancellation), 7.3 (Retry
// and Resume)
// diagnosis: a partition of the gateway-to-pod connection ended the session's
// Attach stream with a status other than UNAVAILABLE, or the UNAVAILABLE end
// was reported as runtime_crash. The gateway's keepalive probe detects the
// partition within the keepalive time plus timeout, the stream ends with
// UNAVAILABLE, and the gateway discards it without a report, so the session
// stays running and keeps its binding. A row that left running means a
// transport blip pushed a live session toward resume and drained a pod the
// coordination Sweeper owns.
func TestPodGRPCPartitionEvictsWithUnavailableAndKeepsTheRow_spec_28_5_1(t *testing.T) {
	const id = "sess-partition"
	srv := adapter.New("stream-chaos-partition")
	srv.WorkspaceBase = t.TempDir()
	srv.Runtime = &echoRuntime{out: make(chan []byte, 16)}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	// The adapter's keepalive enforcement mirrors cmd/lenny-adapter, so the
	// gateway's §11.3 pings are accepted while the link is up.
	gs := adapter.NewGRPCServer(srv,
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{MinTime: podKeepaliveTime / 2, PermitWithoutStream: true}),
		grpc.KeepaliveParams(keepalive.ServerParameters{Time: podKeepaliveTime, Timeout: podKeepaliveTimeout}))
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	proxy := newBlackholeProxy(t, lis.Addr().String())
	ends := &attachEndRecorder{end: make(chan error, 1)}
	cl, err := adapterclient.Dial(proxy.ln.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{Time: podKeepaliveTime, Timeout: podKeepaliveTimeout, PermitWithoutStream: true}),
		ends.interceptor())
	if err != nil {
		t.Fatalf("dial adapter through the proxy: %v", err)
	}
	t.Cleanup(func() { _ = cl.Close() })
	if err := cl.StartSession(context.Background(), adapterclient.StartSessionParams{SessionID: id, Runtime: "echo"}); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	g := newStreamChaosGateway(t, id, cl)
	readers := executorReaders()

	if code, _ := g.deliver(t, "turn-1"); code != http.StatusOK {
		t.Fatalf("first message: status %d", code)
	}
	proxy.partitioned.Store(true)
	partitionedAt := time.Now()

	var end error
	select {
	case end = <-ends.end:
	case <-time.After(podKeepaliveTime + podKeepaliveTimeout + 15*time.Second):
		t.Fatal("the session's Attach stream did not end after the partition")
	}
	if code := status.Code(end); code != codes.Unavailable {
		t.Fatalf("Attach stream ended with %v (%v), want UNAVAILABLE from the failed keepalive probe", code, end)
	}
	t.Logf("Attach stream ended %s after the partition: %v", time.Since(partitionedAt).Round(time.Millisecond), end)
	awaitReaders(t, readers)
	if wait := podKeepaliveTime + podKeepaliveTimeout - time.Since(partitionedAt); wait > 0 {
		time.Sleep(wait)
	}

	g.assertUnreported(t, "after the UNAVAILABLE end")
}
