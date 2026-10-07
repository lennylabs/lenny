// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration tests for the gateway's handling of a failed CH-ATTACH
// stream on a pod-backed session.
//
// Each case runs the real adapter over an in-memory gRPC connection with its
// heartbeat monitor enabled, the real pod executor holding the session's
// Attach stream, and a real session server behind an http.Server, so each
// delivering request ends when its handler returns, as in production. The
// executor's stream-failure handler is the session server's report, wired as
// cmd/lenny-gateway wires it.
//
// When the runtime stops answering heartbeats the adapter ends the session's
// stream with DEADLINE_EXCEEDED. The coordinating replica reports that end as
// runtime_crash whether or not a message is outstanding, the §7.3 classifier
// selects the next state, and the replica releases the session's binding with
// the failed disposition. A stream that closes when the runtime's output ends
// is discarded without a report. A POST /resume served by a replica that does
// not hold the session's coordination lease answers a retryable 503 and holds
// the row in awaiting_client_action, so the coordinating replica's own resume
// still succeeds.
//
// spec: §28.5.1 (Gateway-to-pod, CH-ATTACH Degradation.), §7.3 (Retry and
// Resume), §6.2 (Pod State Machine), §7.2 (Interactive Session Model).
package tier4_integration_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
	"github.com/lennylabs/lenny/pkg/gateway/storage/leasestore"
	"github.com/lennylabs/lenny/pkg/runtimekit/echocore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

const (
	// streamFailureHeartbeat and streamFailureAckTimeout shorten the §28.5.3
	// heartbeat cadence and ack window so an escalation lands in well under a
	// second.
	streamFailureHeartbeat  = 40 * time.Millisecond
	streamFailureAckTimeout = 120 * time.Millisecond
	// streamFailureWait bounds every wait for an asynchronous outcome.
	streamFailureWait = 10 * time.Second
	// streamFailureHang is the message text on which heartbeatRuntime stops
	// acking heartbeats and leaves the turn unanswered.
	streamFailureHang = "hang"
)

// heartbeatRuntime is an adapter.RuntimeProcess that answers heartbeats until
// it is told to stop, echoes every message as "echo:<text>", and records each
// session_end frame the adapter writes. A message whose text is
// streamFailureHang stops the heartbeat acks and is not answered, which is a
// runtime that hangs mid-turn.
type heartbeatRuntime struct {
	out     chan []byte
	silent  atomic.Bool
	mu      sync.Mutex
	endings []string
}

func newHeartbeatRuntime() *heartbeatRuntime {
	return &heartbeatRuntime{out: make(chan []byte, 256)}
}

// stopAcking makes the runtime stop answering heartbeats, as a runtime whose
// event loop hangs between turns.
func (r *heartbeatRuntime) stopAcking() { r.silent.Store(true) }

func (r *heartbeatRuntime) sessionEnds() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.endings...)
}

func (r *heartbeatRuntime) Start(context.Context, string) error { return nil }

func (r *heartbeatRuntime) WriteEnvelope(sessionID string, envelope []byte) error {
	var in struct {
		Type      string `json:"type"`
		SessionID string `json:"sessionId"`
		Input     []struct {
			Inline string `json:"inline"`
		} `json:"input"`
	}
	if err := json.Unmarshal(envelope, &in); err != nil {
		return nil //nolint:nilerr // A frame the fixture cannot parse is not answered.
	}
	switch in.Type {
	case "heartbeat":
		if !r.silent.Load() {
			r.emit(map[string]any{"type": "heartbeat_ack"})
		}
	case "session_end":
		r.mu.Lock()
		r.endings = append(r.endings, in.SessionID)
		r.mu.Unlock()
	case "message":
		text := ""
		if len(in.Input) > 0 {
			text = in.Input[0].Inline
		}
		if text == streamFailureHang {
			r.stopAcking()
			return nil
		}
		if in.SessionID == "" {
			in.SessionID = sessionID
		}
		r.emit(map[string]any{"type": "response", "sessionId": in.SessionID, "text": "echo:" + text})
	}
	return nil
}

func (r *heartbeatRuntime) emit(frame map[string]any) {
	b, _ := json.Marshal(frame)
	select {
	case r.out <- b:
	default:
	}
}

func (r *heartbeatRuntime) Output(context.Context, string) (<-chan []byte, error) {
	return r.out, nil
}
func (r *heartbeatRuntime) Interrupt(context.Context, string, bool) error { return nil }
func (r *heartbeatRuntime) Close(context.Context, string) error           { return nil }

// attachCounter counts the adapter's Attach RPCs that are still running.
type attachCounter struct{ active atomic.Int64 }

func (a *attachCounter) interceptor() grpc.ServerOption {
	return grpc.ChainStreamInterceptor(func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, h grpc.StreamHandler) error {
		if strings.HasSuffix(info.FullMethod, "/Attach") {
			a.active.Add(1)
			defer a.active.Add(-1)
		}
		return h(srv, ss)
	})
}

// recordingPodExecutor is the pod executor with every Release disposition
// recorded, so a case can assert the failure funnel released the binding
// with the failed disposition.
type recordingPodExecutor struct {
	*executor.PodExecutor

	mu       sync.Mutex
	released []executor.Disposition
}

func (e *recordingPodExecutor) Release(ctx context.Context, sessionID string, d executor.Disposition) error {
	e.mu.Lock()
	e.released = append(e.released, d)
	e.mu.Unlock()
	return e.PodExecutor.Release(ctx, sessionID, d)
}

func (e *recordingPodExecutor) releases() []executor.Disposition {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]executor.Disposition(nil), e.released...)
}

// streamFailureFixture is one replica serving one bound pod-backed session.
type streamFailureFixture struct {
	id       string
	sandbox  string
	store    sessionstore.Store
	registry *podsession.Registry
	exec     *recordingPodExecutor
	attaches *attachCounter
	srv      *sessionserver.Server
	base     string
}

// streamFailureReplica configures the session server of one replica.
type streamFailureReplica struct {
	store     sessionstore.Store
	leases    leasestore.LeaseStore
	replicaID string
	binder    *podsession.Binder
}

// newStreamFailureFixture serves rt over an adapter with its heartbeat monitor
// enabled, starts session id on it, publishes the binding at coordination
// generation 1, and commits the session row in state st with retries already
// used. The replica's executor reports a failed stream to its session server.
func newStreamFailureFixture(t *testing.T, rt adapter.RuntimeProcess, id string, st session.State, retries int64, rep streamFailureReplica) *streamFailureFixture {
	t.Helper()
	f := &streamFailureFixture{id: id, sandbox: "sbx-" + id, registry: podsession.NewRegistry(), attaches: &attachCounter{}}
	if rep.store == nil {
		rep.store = memstore.New()
	}
	f.store = rep.store
	cl := streamFailureAdapter(t, rt, f.attaches)
	if err := cl.StartSession(context.Background(), adapterclient.StartSessionParams{SessionID: id, Runtime: "echo"}); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	f.registry.Put(&podsession.BindResult{
		SessionID: id, TenantID: "acme", SandboxName: f.sandbox, Adapter: cl, CoordinationGeneration: 1,
	})
	binder := rep.binder
	if binder == nil {
		binder = &podsession.Binder{Client: streamFailureFakeCluster(t), Namespace: crossEnvNS}
	}
	f.exec = &recordingPodExecutor{PodExecutor: executor.NewPodExecutor(f.registry, binder)}
	t.Cleanup(func() { f.exec.EvictStream(id) })
	f.srv = sessionserver.New(f.store, sessionserver.Options{
		Executor:                f.exec,
		PodRegistry:             f.registry,
		PodBinder:               rep.binder,
		AgentNamespace:          crossEnvNS,
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		Transcripts:             transcriptstore.NewMemory(),
		CoordinationLeaseStore:  rep.leases,
		ReplicaID:               rep.replicaID,
		IDFunc:                  func() string { return id },
	})
	f.exec.SetStreamFailureHandler(f.srv.ReportAttachStreamFailure)
	hs := httptest.NewServer(f.srv.Handler())
	t.Cleanup(hs.Close)
	f.base = hs.URL
	streamFailureSeed(t, f.store, id, f.sandbox, st, retries)
	return f
}

// streamFailureAdapter serves an adapter over rt with the shortened heartbeat
// and returns a client dialled to it.
func streamFailureAdapter(t *testing.T, rt adapter.RuntimeProcess, attaches *attachCounter) *adapterclient.Client {
	t.Helper()
	srv := adapter.New("stream-failure-test")
	srv.WorkspaceBase = t.TempDir()
	srv.Runtime = rt
	srv.HeartbeatInterval = streamFailureHeartbeat
	srv.HeartbeatAckTimeout = streamFailureAckTimeout
	lis := bufconn.Listen(1 << 20)
	gs := adapter.NewGRPCServer(srv, attaches.interceptor())
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	cl, err := adapterclient.Dial("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial adapter: %v", err)
	}
	t.Cleanup(func() { _ = cl.Close() })
	return cl
}

// streamFailureFakeCluster is a cluster holding no Sandbox, so the failed
// release's pod drain finds nothing to delete after it has sent the adapter
// its Shutdown. The release error is logged by the failure funnel.
func streamFailureFakeCluster(t *testing.T) client.Client {
	t.Helper()
	s := k8sruntime.NewScheme()
	if err := lennyv1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	return fake.NewClientBuilder().WithScheme(s).Build()
}

// streamFailureSeed commits the session row bound to sandbox at coordination
// generation 1, with retries already used out of a budget of one.
func streamFailureSeed(t *testing.T, store sessionstore.Store, id, sandbox string, st session.State, retries int64) {
	t.Helper()
	now := time.Now().UTC()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: id, TenantID: "acme", State: st, RuntimeRef: matOKRuntime,
		IsolationProfile: isolation.ProfileSandboxed, PodAssignment: sandbox,
		CoordinationGeneration: 1, RetryCount: retries,
		RetryPolicy:       &session.RetryPolicy{MaxRetries: 1},
		WorkspaceSnapshot: &sessionstore.WorkspaceSnapshot{Ref: "ckpt-1", Source: sessionstore.WorkspaceSnapshotCheckpoint},
		CreatedAt:         now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

func (f *streamFailureFixture) row(t *testing.T) sessionstore.Session {
	t.Helper()
	row, err := f.store.Get(context.Background(), "acme", f.id)
	if err != nil {
		t.Fatalf("get %s: %v", f.id, err)
	}
	return row
}

// awaitState waits for the session row to reach want.
func (f *streamFailureFixture) awaitState(t *testing.T, want session.State) sessionstore.Session {
	t.Helper()
	deadline := time.Now().Add(streamFailureWait)
	for {
		row := f.row(t)
		if row.State == want {
			return row
		}
		if time.Now().After(deadline) {
			t.Fatalf("session %s state = %s after %s, want %s", f.id, row.State, streamFailureWait, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// awaitNoAttach waits for every Attach RPC on the adapter to end.
func (f *streamFailureFixture) awaitNoAttach(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(streamFailureWait)
	for f.attaches.active.Load() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("%d Attach stream(s) still open on the adapter, want none", f.attaches.active.Load())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// post sends one REST message and returns the HTTP status and decoded body.
func (f *streamFailureFixture) post(t *testing.T, content string) (int, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"messages": []any{map[string]any{"role": "user", "content": content}}})
	return multiTurnPost(t, f.base+"/v1/sessions/"+f.id+"/messages", string(body))
}

// assertReportedAndReleased asserts the outcome of one runtime_crash report on
// a session with retry budget left: the row is resume_pending with the
// failure reason and one retry counted, the binding is released once with the
// failed disposition, the adapter wrote session_end, the stream is gone from
// the adapter, and a later delivery finds no binding and no cached stream.
func (f *streamFailureFixture) assertReportedAndReleased(t *testing.T, rt *heartbeatRuntime) {
	t.Helper()
	row := f.awaitState(t, session.StateResumePending)
	if row.FailureReason != string(session.FailureRuntimeCrash) || row.RetryCount != 1 {
		t.Errorf("row = %q/retries %d, want runtime_crash/1", row.FailureReason, row.RetryCount)
	}
	if got := f.exec.releases(); len(got) != 1 || got[0] != executor.DispositionFailed {
		t.Errorf("releases = %v, want one release with the failed disposition", got)
	}
	if _, ok := f.registry.Get(f.id); ok {
		t.Error("the binding survived the failure report")
	}
	if ends := rt.sessionEnds(); len(ends) != 1 || ends[0] != f.id {
		t.Errorf("runtime received session_end for %v, want exactly %s", ends, f.id)
	}
	f.awaitNoAttach(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := f.exec.Send(ctx, f.id, []executor.Message{{Role: "user", Content: "after"}}); err == nil ||
		!strings.Contains(err.Error(), "not bound") {
		t.Errorf("Send after the report = %v, want a not-bound error: no cached stream outlives the release", err)
	}
}

// spec: 28.5.1 (Gateway-to-pod), 7.3 (Retry and Resume), 6.2 (Pod State
// Machine)
// diagnosis: a runtime that stopped answering heartbeats between turns, on a
// stream REST direct delivery opened, was not reported as runtime_crash, or
// the report did not release the session's binding with the failed
// disposition and evict the stream. A row left running means the gateway
// ignored the adapter's DEADLINE_EXCEEDED end; a missing failed release or a
// surviving binding means the session kept its pod after its runtime hung.
func TestStreamFailureBetweenTurnsReportsAndReleases_spec_28_5_1(t *testing.T) {
	rt := newHeartbeatRuntime()
	f := newStreamFailureFixture(t, rt, "sess-between", session.StateRunning, 0, streamFailureReplica{})

	if code, resp := f.post(t, "turn-1"); code != http.StatusOK {
		t.Fatalf("first message: status %d, body %v", code, resp)
	}
	if st := f.row(t).State; st != session.StateRunning {
		t.Fatalf("state after the first message = %s, want running", st)
	}
	rt.stopAcking()

	f.assertReportedAndReleased(t, rt)
}

// spec: 28.5.1 (Gateway-to-pod), 7.3 (Retry and Resume), 6.2 (Pod State
// Machine)
// diagnosis: a runtime that hung mid-turn left the in-flight delivery
// waiting, or the stream's DEADLINE_EXCEEDED end was not reported. The
// request carrying the turn must fail once the adapter ends the stream, and
// the same runtime_crash report must move the session off its pod.
func TestStreamFailureMidTurnFailsTheSendAndReports_spec_28_5_1(t *testing.T) {
	rt := newHeartbeatRuntime()
	f := newStreamFailureFixture(t, rt, "sess-midturn", session.StateRunning, 0, streamFailureReplica{})

	if code, resp := f.post(t, "turn-1"); code != http.StatusOK {
		t.Fatalf("first message: status %d, body %v", code, resp)
	}
	start := time.Now()
	code, resp := f.post(t, streamFailureHang)
	if code == http.StatusOK {
		t.Fatalf("hung turn: status 200, body %v; want the delivery to fail when the stream ends", resp)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("hung turn returned after %s, want it to end with the stream", elapsed)
	}

	f.assertReportedAndReleased(t, rt)
}

// executorReaders counts the live goroutines running the pod executor's
// stream reader, which exits only after the stream's end handling has run.
func executorReaders() int {
	buf := make([]byte, 1<<22)
	buf = buf[:runtime.Stack(buf, true)]
	return strings.Count(string(buf), "executor.(*PodExecutor).readConn")
}

// embeddedEchoLoop is the echocore loop the embedded reference runtime runs
// in-process.
func embeddedEchoLoop(ctx context.Context, in io.Reader, out io.Writer) error {
	return echocore.Run(ctx, in, out, io.Discard)
}

// spec: 28.5.1 (Gateway-to-pod), 7.2 (Interactive Session Model)
// diagnosis: interrupting an embedded-runtime session ended its runtime loop,
// and the Attach stream that closed with the loop's output was reported as a
// stream failure. A stream that closes when the runtime's output ends is
// discarded without a report, so the interrupted session stays suspended,
// keeps its retry count, and holds its binding. The assertions run after the
// executor's reader has handled the end, so they exercise the discard.
func TestEmbeddedInterruptEndsTheStreamWithoutAReport_spec_28_5_1(t *testing.T) {
	rt := adapter.NewInProcessRuntime(embeddedEchoLoop)
	f := newStreamFailureFixture(t, rt, "sess-embedded", session.StateRunning, 0, streamFailureReplica{})

	readers := executorReaders()
	if code, resp := f.post(t, "turn-1"); code != http.StatusOK {
		t.Fatalf("first message: status %d, body %v", code, resp)
	}
	if got := executorReaders(); got != readers+1 {
		t.Fatalf("executor readers = %d after the first message, want %d", got, readers+1)
	}

	code, resp := multiTurnPost(t, f.base+"/v1/sessions/"+f.id+"/interrupt", `{}`)
	if code != http.StatusOK {
		t.Fatalf("interrupt: status %d, body %v", code, resp)
	}
	f.awaitNoAttach(t)
	deadline := time.Now().Add(streamFailureWait)
	for executorReaders() > readers {
		if time.Now().After(deadline) {
			t.Fatal("the executor's reader did not handle the stream's end")
		}
		time.Sleep(5 * time.Millisecond)
	}

	row := f.row(t)
	if row.State != session.StateSuspended || row.RetryCount != 0 || row.FailureReason != "" {
		t.Errorf("row = %s/retries %d/%q, want suspended/0 with no failure reason", row.State, row.RetryCount, row.FailureReason)
	}
	if _, ok := f.registry.Get(f.id); !ok {
		t.Error("the interrupted session lost its binding")
	}
	if got := f.exec.releases(); len(got) != 0 {
		t.Errorf("releases = %v, want none", got)
	}
}

// streamFailureLeases returns a coordination lease store over an in-memory
// Redis that both replicas share.
func streamFailureLeases(t *testing.T) *leasestore.Store {
	t.Helper()
	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rc.Close() })
	return leasestore.New(rc)
}

// streamFailureResumeBinder is a pod binder over the shared cluster whose
// claimed pod is a fresh adapter, so a POST /resume reaches the bind.
func streamFailureResumeBinder(t *testing.T, cluster client.Client) *podsession.Binder {
	t.Helper()
	srv := adapter.New("stream-failure-resume")
	srv.WorkspaceBase = t.TempDir()
	srv.Runtime = &eagerRuntime{}
	return &podsession.Binder{
		Client:           cluster,
		Namespace:        crossEnvNS,
		AdapterPort:      50051,
		AcceptedVersions: []string{adapter.ProtocolVersionV1},
		DialAdapter:      eagerAdapterDialer(t, srv),
	}
}

// resumeOn posts POST /v1/sessions/{id}/resume to base and returns the
// response.
func resumeOn(t *testing.T, base, id string) *http.Response {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/sessions/"+id+"/resume", nil)
	if err != nil {
		t.Fatalf("build resume: %v", err)
	}
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST resume: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// spec: 28.5.1 (Gateway-to-pod), 7.3 (Retry and Resume), 7.2 (Interactive
// Session Model)
// diagnosis: after a stream-failure report moved a session with exhausted
// retries to awaiting_client_action on its coordinating replica, a POST
// /resume served by another replica demoted the row or bound a pod against
// the coordinator's lease. The second replica must answer the retryable 503
// RESUME_FAILED with Retry-After, publish no binding, and hold the row in
// awaiting_client_action; a row left in failed means the client's retry meets
// a terminal row. The coordinating replica's own resume must then reach
// running.
func TestCrossReplicaResumeAfterStreamFailureHoldsTheRow_spec_7_3(t *testing.T) {
	envtest.SkipUnlessAvailable(t)
	cluster := materializeCluster(t)
	leases := streamFailureLeases(t)
	store := memstore.New()
	const id = "sess-cross"

	rt := newHeartbeatRuntime()
	first := newStreamFailureFixture(t, rt, id, session.StateRunning, 1, streamFailureReplica{
		store: store, leases: leases, replicaID: "rep-1", binder: streamFailureResumeBinder(t, cluster),
	})
	if _, err := leases.Acquire(context.Background(), "acme", id, "rep-1", time.Minute); err != nil {
		t.Fatalf("rep-1 acquires the coordination lease: %v", err)
	}
	secondRegistry := podsession.NewRegistry()
	second := sessionserver.New(store, sessionserver.Options{
		Executor:                executor.NewPodExecutor(secondRegistry, nil),
		PodRegistry:             secondRegistry,
		PodBinder:               streamFailureResumeBinder(t, cluster),
		AgentNamespace:          crossEnvNS,
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		Transcripts:             transcriptstore.NewMemory(),
		CoordinationLeaseStore:  leases,
		ReplicaID:               "rep-2",
		IDFunc:                  func() string { return id },
	})
	secondHS := httptest.NewServer(second.Handler())
	t.Cleanup(secondHS.Close)

	if code, resp := first.post(t, "turn-1"); code != http.StatusOK {
		t.Fatalf("first message: status %d, body %v", code, resp)
	}
	rt.stopAcking()
	first.awaitState(t, session.StateAwaitingClientAction)
	if lease, err := leases.Get(context.Background(), "acme", id); err != nil || lease.Holder != "rep-1" {
		t.Fatalf("lease after the report = %+v (err %v), want rep-1 still holding it", lease, err)
	}

	resp := resumeOn(t, secondHS.URL, id)
	var env struct {
		Error struct {
			Code      string `json:"code"`
			Retryable bool   `json:"retryable"`
		} `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&env)
	if resp.StatusCode != http.StatusServiceUnavailable || env.Error.Code != "RESUME_FAILED" || !env.Error.Retryable {
		t.Fatalf("resume on rep-2 = %d %+v, want 503 retryable RESUME_FAILED", resp.StatusCode, env.Error)
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Error("resume on rep-2 carries no Retry-After")
	}
	if st := first.row(t).State; st != session.StateAwaitingClientAction {
		t.Fatalf("state after rep-2's resume = %s, want awaiting_client_action", st)
	}
	if _, ok := secondRegistry.Get(id); ok {
		t.Error("rep-2 published a binding against rep-1's coordination lease")
	}

	resp = resumeOn(t, first.base, id)
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("resume on rep-1 = %d %s, want 200", resp.StatusCode, body)
	}
	if st := first.row(t).State; st != session.StateRunning {
		t.Fatalf("state after rep-1's resume = %s, want running", st)
	}
	if _, ok := first.registry.Get(id); !ok {
		t.Error("rep-1 published no binding for the resumed session")
	}
}
