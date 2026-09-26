// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 functional integration flow for the concurrent-workspace
// per-slot execution path. A pool whose sessionPolicy.maxConcurrentSessions
// is above 1 multiplexes simultaneous sessions onto one pod, each in its
// own slot, over a single runtime process. This test exercises that path
// end to end across the gateway->adapter->runtime envelope route:
//
//   - A single real adapter.Server stands in for the pod's sidecar adapter,
//     driven over its real gRPC contract — the same surface the gateway's
//     adapterclient speaks.
//   - A single real SocketRuntimeProcess binds the pod's abstract runtime
//     socket and spawns the real cmd/runtimes/echo-concurrent binary, the
//     one reference runtime that implements the sessionId dispatch loop. One
//     runtime process per pod serves every slot, multiplexed on sessionId over
//     the single connection.
//   - Two sessions land on the pod in distinct slots, each with an isolated
//     per-slot workspace at /workspace/slots/{sessionId}/current/.
//
// The flow asserts the two properties the proposal names. First, workspace
// distinctness: each slot's FinalizeWorkspace materializes its own content
// into its own per-slot tree, and neither slot's file leaks into the other's
// workspace or into a pod-global /workspace/current. Second, per-slot
// response sessionId tagging: a message dispatched on one slot's Attach stream
// comes back tagged with that slot's sessionId, and each Attach stream receives
// only its slot's responses, proving the adapter stamps the inbound sessionId
// and demultiplexes the runtime's interleaved output by sessionId.
//
// The pool configuration the path requires (maxConcurrentSessions > 1 with
// acknowledgeProcessLevelIsolation: true) is the deployer contract the
// gateway enforces before it ever mints a slot. On the adapter every
// session is bound to a slot on every pod, so the concurrent-pool claim
// this flow drives differs from an exclusive one in the pod's slot count
// rather than in the path a StartSession takes.
//
// The file also carries the abandoned-bind flow: a bind the gateway abandons
// on the shared pod is compensated by the gateway's real binder, and the
// incumbent session, a later co-tenant, and the abandoned session's own retry
// are unaffected by the residue the pre-compensation gateway left.
//
// spec: §5.2 (concurrent sessions: sessionId multiplexing over stdin, dispatch
// loop keyed on sessionId, acknowledgeProcessLevelIsolation), §6.4 (per-slot
// filesystem layout /workspace/slots/{sessionId}/, per-slot cwd), §28.5.3
// (single stdin channel carrying sessionId on every pod), §7.1 (normal flow),
// §4.7.1 (role and gateway RPC contract).
package tier4_integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// concurrentPool names the deployer pool contract this flow stands up: a
// session-mode pool with sessionPolicy.maxConcurrentSessions above 1 and
// acknowledgeProcessLevelIsolation accepted. The values are asserted as a
// gate so a future change that lowered maxConcurrentSessions to 1 or
// dropped the acknowledgment would no longer exercise the per-slot path.
//
// spec: §5.2 — acknowledgeProcessLevelIsolation is required to
// configure maxConcurrentSessions > 1.
type concurrentPool struct {
	maxConcurrentSessions          int
	acknowledgeProcessLevelIsolate bool
}

// slot is one session's binding to a pod slot. The gateway uses the session
// id as the slot id (1:1 per pod), so the test mirrors that derivation.
type slot struct {
	sessionID string
	slotID    string
}

// spec: §5.2,
// §5.2, §6.4.
// diagnosis: a failure means concurrent-workspace per-slot execution
// regressed end to end across the gateway->adapter->runtime path. Either two
// sessions on a maxConcurrentSessions > 1 pod did not land in distinct slots
// with isolated workspaces (one slot's file leaked into the other's tree or
// into a pod-global /workspace/current), or the per-slot response was not
// tagged with its originating sessionId (the adapter failed to stamp the
// inbound sessionId or to demultiplex the single runtime connection's
// interleaved output by sessionId).
func TestConcurrentWorkspacePerSlotExecution_spec_5_2(t *testing.T) {
	pool := concurrentPool{maxConcurrentSessions: 2, acknowledgeProcessLevelIsolate: true}
	// The deployer contract that admits the per-slot path: the gateway
	// rejects a maxConcurrentSessions > 1 pool that omits the isolation
	// acknowledgment (spec/05:511), so this flow only stands up against a
	// pool that carries both.
	if pool.maxConcurrentSessions <= 1 || !pool.acknowledgeProcessLevelIsolate {
		t.Fatalf("the concurrent-workspace flow requires maxConcurrentSessions > 1 with "+
			"acknowledgeProcessLevelIsolation: true, got %+v", pool)
	}

	echoConcurrentBin := buildConcurrentRuntime(t)

	base := t.TempDir()
	srv := adapter.New("tier4-concurrent")
	srv.WorkspaceBase = filepath.Join(base, "workspace")
	srv.SessionsRoot = filepath.Join(base, "sessions")
	srv.ArtifactsRoot = filepath.Join(base, "artifacts")
	srv.CredentialsDir = filepath.Join(base, "run", "lenny")

	// One real runtime process per pod, the §4.7 sidecar transport: the
	// adapter binds the abstract socket and spawns the real echo-concurrent
	// binary, which dials back and runs its sessionId dispatch loop over the one
	// connection. Every slot rides this single connection (spec/05:509).
	rt, err := adapter.NewSocketRuntimeProcess(concurrentSocketAddr(t))
	if err != nil {
		t.Fatalf("bind pod runtime socket: %v", err)
	}
	rt.SpawnPath = echoConcurrentBin
	rt.AcceptTimeout = 15 * time.Second
	srv.Runtime = rt
	t.Cleanup(func() { _ = rt.Close(context.Background(), "pod-teardown") })

	client := concurrentAdapterClient(t, srv)
	ctx := context.Background()

	slots := []slot{
		{sessionID: "sess-alice", slotID: "sess-alice"},
		{sessionID: "sess-bob", slotID: "sess-bob"},
	}
	if len(slots) > pool.maxConcurrentSessions {
		t.Fatalf("flow drives %d slots, more than the pool bound %d", len(slots), pool.maxConcurrentSessions)
	}

	// Two sessions land on the one pod, each claiming its own slot. The
	// second claim is admitted rather than rejected with "pod is not idle":
	// the single pod-global runtime multiplexes both slots on sessionId.
	for _, sl := range slots {
		if _, err := client.StartSession(ctx, &adapterv1.StartSessionRequest{
			SessionId: &adapterv1.SessionId{Value: sl.sessionID},
			Runtime:   "echo-concurrent",
		}); err != nil {
			t.Fatalf("StartSession(%s on %s): %v", sl.sessionID, sl.slotID, err)
		}
	}

	assertWorkspaceDistinctness(t, ctx, client, srv, slots)
	assertPerSlotResponseTagging(t, client, slots)
}

// assertWorkspaceDistinctness materializes a distinct marker file into each
// slot's per-slot workspace and asserts that each slot's tree holds only its
// own content, that no slot's file leaked into a sibling's tree, and that no
// pod-global /workspace/current appears. This pins the §6.4 per-slot layout:
// a session's cwd is /workspace/slots/{sessionId}/current/ on every pod
// whatever the pool's concurrency, and the pod-global path exists nowhere.
//
// spec: §6.4 — per-slot workspace /workspace/slots/{sessionId}/;
// the runtime MUST NOT assume a global /workspace/current.
func assertWorkspaceDistinctness(t *testing.T, ctx context.Context, client adapterv1.AdapterClient, srv *adapter.Server, slots []slot) {
	t.Helper()
	content := map[string]string{
		slots[0].slotID: "workspace-of-" + slots[0].sessionID,
		slots[1].slotID: "workspace-of-" + slots[1].sessionID,
	}
	for _, sl := range slots {
		// Both sessions have started, so the finalize is the §7.4 mid-session
		// form, which §4.7.1 rule 6 exempts and rule 1 pairs with no bind
		// attempt token.
		if _, err := client.FinalizeWorkspace(ctx, &adapterv1.FinalizeWorkspaceRequest{
			SessionId:  &adapterv1.SessionId{Value: sl.sessionID},
			MidSession: true,
			WorkspacePlan: &adapterv1.WorkspacePlan{
				SchemaVersion: 1,
				Sources: []*adapterv1.WorkspaceSource{
					{Type: "inlineFile", Path: "marker.txt", Content: content[sl.slotID], Mode: "0644"},
				},
			},
		}); err != nil {
			t.Fatalf("FinalizeWorkspace(%s): %v", sl.slotID, err)
		}
	}

	for _, sl := range slots {
		got, err := os.ReadFile(filepath.Join(srv.WorkspaceBase, "slots", sl.slotID, "current", "marker.txt"))
		if err != nil {
			t.Fatalf("read %s per-slot workspace marker: %v", sl.slotID, err)
		}
		if string(got) != content[sl.slotID] {
			t.Errorf("%s workspace marker = %q, want %q; per-slot workspaces are not isolated",
				sl.slotID, got, content[sl.slotID])
		}
	}
	// The pod-global /workspace/current is retired on every pool class, so
	// the directory itself must be absent rather than merely unwritten. The
	// property it protects, that a materialization never lands outside the
	// requesting session's own tree, outlives the concurrency condition the
	// assertion was first written under.
	if _, err := os.Stat(filepath.Join(srv.WorkspaceBase, "current")); !os.IsNotExist(err) {
		t.Errorf("pod-global /workspace/current exists (err=%v); §6.4 retires the path on every pod", err)
	}
}

// assertPerSlotResponseTagging opens a per-slot Attach stream for each slot,
// dispatches a message on it, and asserts the response comes back tagged with
// that slot's sessionId. Each stream receives only its own slot's response,
// proving the adapter stamps the inbound sessionId onto the envelope it writes
// to the single runtime connection and demultiplexes the runtime's
// interleaved output by sessionId. The real echo-concurrent runtime stamps the
// originating sessionId onto every response, so the tag the stream observes is
// the runtime's, carried back across the adapter unchanged.
//
// spec: §28.5.3 — single stdin channel, dispatch loop keyed on
// sessionId; §6.4 — adapter sets the slot's cwd when dispatching.
func assertPerSlotResponseTagging(t *testing.T, client adapterv1.AdapterClient, slots []slot) {
	t.Helper()
	for _, sl := range slots {
		sl := sl
		t.Run(sl.slotID, func(t *testing.T) {
			streamCtx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			stream, err := client.Attach(streamCtx)
			if err != nil {
				t.Fatalf("Attach(%s): %v", sl.slotID, err)
			}
			// The first AttachRequest binds the slot and carries a message
			// envelope with no sessionId in its body: the adapter stamps the
			// slot's sessionId before the envelope reaches the shared runtime.
			msg := map[string]any{
				"type":  "message",
				"id":    "m_" + sl.sessionID,
				"input": []map[string]any{{"type": "text", "inline": "ping-" + sl.sessionID}},
			}
			body, err := json.Marshal(msg)
			if err != nil {
				t.Fatalf("encode message for %s: %v", sl.slotID, err)
			}
			if err := stream.Send(&adapterv1.AttachRequest{
				SessionId:    &adapterv1.SessionId{Value: sl.sessionID},
				EnvelopeJson: body,
			}); err != nil {
				t.Fatalf("Send bind+message(%s): %v", sl.slotID, err)
			}

			resp := recvResponse(t, stream)
			if resp.SessionID != sl.slotID {
				t.Errorf("%s response carried sessionId %q, want %q; per-slot response tagging regressed",
					sl.slotID, resp.SessionID, sl.slotID)
			}
			if len(resp.Output) != 1 || resp.Output[0].Inline == "" {
				t.Errorf("%s response output = %+v, want the echoed input", sl.slotID, resp.Output)
			}
			// echocore wraps the echoed input with a per-slot sequence
			// prefix, so the response contains this slot's input rather than
			// equalling it. The assertion catches cross-slot misrouting: each
			// slot's response must echo its own input, never a sibling's.
			if want := "ping-" + sl.sessionID; len(resp.Output) == 1 && !strings.Contains(resp.Output[0].Inline, want) {
				t.Errorf("%s echoed %q, want it to contain %q; a sibling slot's input was misrouted",
					sl.slotID, resp.Output[0].Inline, want)
			}
		})
	}
}

// slotResponse is the subset of a §28.5.3 outbound `response` frame this
// flow asserts on: the discriminator, the sessionId the runtime stamps and the
// adapter carries back, and the echoed text parts.
type slotResponse struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	Output    []struct {
		Inline string `json:"inline"`
	} `json:"output"`
}

// recvResponse reads from the Attach stream until it observes a `response`
// frame, skipping any protocol-level frames the runtime may interleave. It
// bounds the wait so a missing response fails the test rather than hanging.
func recvResponse(t *testing.T, stream adapterv1.Adapter_AttachClient) slotResponse {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		got, err := stream.Recv()
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		var resp slotResponse
		if err := json.Unmarshal(got.GetEnvelopeJson(), &resp); err != nil {
			t.Fatalf("decode response frame %q: %v", got.GetEnvelopeJson(), err)
		}
		if resp.Type == "response" {
			return resp
		}
	}
	t.Fatal("the echo-concurrent runtime produced no response within the deadline")
	return slotResponse{}
}

// buildConcurrentRuntime compiles the real cmd/runtimes/echo-concurrent
// binary into a temp path. It is the reference runtime that implements the
// sessionId dispatch loop, so the flow exercises the production multiplexing
// path rather than a fake.
func buildConcurrentRuntime(t *testing.T) string {
	t.Helper()
	root := schematest.RepoRoot(t)
	bin := filepath.Join(t.TempDir(), "echo-concurrent")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/runtimes/echo-concurrent")
	cmd.Dir = root
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("build echo-concurrent: %v", err)
	}
	return bin
}

// concurrentSocketAddr returns the abstract Unix socket the adapter binds
// for the pod's runtime. On Linux it is an abstract address (no filesystem
// cleanup); elsewhere it is a short filesystem path, since the Unix sun_path
// field is limited to ~104 bytes and t.TempDir() alone can exceed that.
func concurrentSocketAddr(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "linux" {
		return "@lenny-tier4-cworkspace-" + sanitizeName(t.Name())
	}
	f, err := os.CreateTemp("", "cws-*.sock")
	if err != nil {
		t.Fatalf("temp socket path: %v", err)
	}
	path := f.Name()
	_ = f.Close()
	_ = os.Remove(path)
	t.Cleanup(func() { _ = os.Remove(path) })
	return path
}

// sanitizeName makes a test name usable as an abstract socket suffix by
// replacing the path separators Go inserts for subtests.
func sanitizeName(name string) string {
	out := make([]rune, 0, len(name))
	for _, r := range name {
		if r == '/' || r == ' ' {
			out = append(out, '-')
			continue
		}
		out = append(out, r)
	}
	return string(out)
}

// concurrentAdapterClient boots the adapter's real gRPC server over an
// in-memory bufconn listener and returns a connected Adapter client. This is
// the exact contract the gateway's adapterclient speaks, so driving the flow
// through it exercises the gateway->adapter wire path without a Kubernetes
// cluster.
func concurrentAdapterClient(t *testing.T, s *adapter.Server) adapterv1.AdapterClient {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	gs := adapter.NewGRPCServer(s)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial adapter bufconn: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return adapterv1.NewAdapterClient(conn)
}

// injectingRuntime wraps the pod's shared runtime process so a test can put
// a frame on every open Attach stream's runtime output, as the runtime would.
// echo-concurrent stamps a sessionId on every frame it writes, so an
// unaddressed session-scoped frame, the one §28.5.3 resolves against the
// pod's slot count, can only be produced this way.
type injectingRuntime struct {
	adapter.RuntimeProcess
	mu   sync.Mutex
	subs []chan []byte
}

func (r *injectingRuntime) Output(ctx context.Context, sessionID string) (<-chan []byte, error) {
	in, err := r.RuntimeProcess.Output(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	inj := make(chan []byte, 4)
	r.mu.Lock()
	r.subs = append(r.subs, inj)
	r.mu.Unlock()
	out := make(chan []byte)
	go func() {
		defer close(out)
		for {
			var line []byte
			select {
			case l, ok := <-in:
				if !ok {
					return
				}
				line = l
			case line = <-inj:
			case <-ctx.Done():
				return
			}
			select {
			case out <- line:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

// subscribers reports how many Attach streams have opened the output.
func (r *injectingRuntime) subscribers() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.subs)
}

// inject delivers line to every subscribed output without blocking.
func (r *injectingRuntime) inject(line []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range r.subs {
		select {
		case s <- line:
		default:
		}
	}
}

// bindFaults is a unary client interceptor on the gateway's adapter
// connection. It fails a named session's StartSession the way an abandoned
// bind meets it, and it can withhold a compensating Shutdown so the test
// delivers it later, as a reclaim that arrives late or is lost.
type bindFaults struct {
	mu sync.Mutex
	// startFault maps a session to the next StartSession's fault: "drop"
	// answers Unavailable without reaching the adapter, and "expire" waits
	// for the caller's context to expire, delivers the start on a detached
	// context so the adapter starts the session, and answers the context's
	// error.
	startFault map[string]string
	// holdReclaim withholds the next compensating Shutdown for a session:
	// the request is kept in held and answered Unavailable.
	holdReclaim map[string]bool
	held        []*adapterv1.ShutdownRequest
	// reclaims records every compensating Shutdown that reached the adapter.
	reclaims []*adapterv1.ShutdownRequest
}

func newBindFaults() *bindFaults {
	return &bindFaults{startFault: map[string]string{}, holdReclaim: map[string]bool{}}
}

func (f *bindFaults) intercept(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	switch r := req.(type) {
	case *adapterv1.StartSessionRequest:
		f.mu.Lock()
		fault := f.startFault[r.GetSessionId().GetValue()]
		delete(f.startFault, r.GetSessionId().GetValue())
		f.mu.Unlock()
		switch fault {
		case "drop":
			return status.Error(codes.Unavailable, "start lost in transit")
		case "expire":
			<-ctx.Done()
			if err := invoker(context.WithoutCancel(ctx), method, req, reply, cc, opts...); err != nil {
				return err
			}
			return status.FromContextError(ctx.Err()).Err()
		}
	case *adapterv1.ShutdownRequest:
		if r.GetBindAttempt() != "" {
			f.mu.Lock()
			sid := r.GetSessionId().GetValue()
			if f.holdReclaim[sid] {
				delete(f.holdReclaim, sid)
				f.held = append(f.held, r)
				f.mu.Unlock()
				return status.Error(codes.Unavailable, "reclaim lost with the gateway")
			}
			f.reclaims = append(f.reclaims, r)
			f.mu.Unlock()
		}
	}
	return invoker(ctx, method, req, reply, cc, opts...)
}

// abandonFixture is a concurrent pool's shared pod: a real adapter.Server
// with a real SocketRuntimeProcess running echo-concurrent, reached by the
// gateway's real podsession.Binder over a connection bindFaults intercepts,
// and by a raw adapter client the test drives Attach and late reclaims on.
type abandonFixture struct {
	srv    *adapter.Server
	rt     *injectingRuntime
	binder *podsession.Binder
	faults *bindFaults
	raw    adapterv1.AdapterClient
}

func newAbandonFixture(t *testing.T) *abandonFixture {
	t.Helper()
	c := recycleCluster(t)
	echoConcurrentBin := buildConcurrentRuntime(t)
	base := t.TempDir()
	srv := adapter.New("tier4-abandoned-bind")
	srv.WorkspaceBase = filepath.Join(base, "workspace")
	srv.SessionsRoot = filepath.Join(base, "sessions")
	srv.ArtifactsRoot = filepath.Join(base, "artifacts")
	srv.CredentialsDir = filepath.Join(base, "run", "lenny")
	// The subtest names outgrow the abstract socket name limit, so the
	// address is a short unique one on Linux.
	addr := concurrentSocketAddr(t)
	if runtime.GOOS == "linux" {
		addr = fmt.Sprintf("@lenny-t4-abandon-%d", time.Now().UnixNano())
	}
	proc, err := adapter.NewSocketRuntimeProcess(addr)
	if err != nil {
		t.Fatalf("bind pod runtime socket: %v", err)
	}
	proc.SpawnPath = echoConcurrentBin
	proc.AcceptTimeout = 15 * time.Second
	rt := &injectingRuntime{RuntimeProcess: proc}
	srv.Runtime = rt
	t.Cleanup(func() { _ = proc.Close(context.Background(), "pod-teardown") })

	lis := bufconn.Listen(1 << 20)
	gs := adapter.NewGRPCServer(srv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	dialer := grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
		return lis.DialContext(ctx)
	})
	faults := newBindFaults()
	binder, _ := recycleSlotBinder(t, c, func(string) (*adapterclient.Client, error) {
		return adapterclient.Dial("passthrough:///bufnet", dialer,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithUnaryInterceptor(faults.intercept))
	})
	conn, err := grpc.NewClient("passthrough:///bufnet", dialer, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial adapter bufconn: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return &abandonFixture{srv: srv, rt: rt, binder: binder, faults: faults, raw: adapterv1.NewAdapterClient(conn)}
}

// slotReq is the concurrent-pool bind request for one session.
func (f *abandonFixture) slotReq(sessionID string, maxConcurrent int32) podsession.SlotBindRequest {
	return podsession.SlotBindRequest{
		Pool: "recycle-pool", SessionID: sessionID, TenantID: "acme", Runtime: "echo-concurrent",
		MaxConcurrentSessions: maxConcurrent, Plan: &adapterv1.WorkspacePlan{},
	}
}

// bind binds and starts sessionID through the gateway binder and fails the
// test on any error.
func (f *abandonFixture) bind(t *testing.T, sessionID string, maxConcurrent int32) *podsession.BindResult {
	t.Helper()
	res, err := f.binder.BindSlot(context.Background(), f.slotReq(sessionID, maxConcurrent))
	if err != nil {
		t.Fatalf("BindSlot(%s): %v", sessionID, err)
	}
	t.Cleanup(func() { _ = res.Adapter.Close() })
	return res
}

// failBind runs a bind the fault rules make fail, releases its reservation
// with the disposition the binder reports, as the §5.2 retry policy does, and
// returns the bind error.
func (f *abandonFixture) failBind(t *testing.T, ctx context.Context, sessionID string, maxConcurrent int32) *podsession.SlotBindError {
	t.Helper()
	_, err := f.binder.BindSlot(ctx, f.slotReq(sessionID, maxConcurrent))
	var sbe *podsession.SlotBindError
	if !errors.As(err, &sbe) {
		t.Fatalf("BindSlot(%s) error = %v, want a *SlotBindError", sessionID, err)
	}
	if err := f.binder.ReleaseSlotReservation(context.Background(), sbe.Pod, sbe.SlotID, sbe.Leaked); err != nil {
		t.Fatalf("release %s reservation: %v", sessionID, err)
	}
	return sbe
}

// assertEchoes opens an Attach stream for sessionID and asserts the shared
// runtime answers a message with a response tagged for that session.
func (f *abandonFixture) assertEchoes(t *testing.T, sessionID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	stream, err := f.raw.Attach(ctx)
	if err != nil {
		t.Fatalf("Attach(%s): %v", sessionID, err)
	}
	body, _ := json.Marshal(map[string]any{
		"type": "message", "id": "m_" + sessionID,
		"input": []map[string]any{{"type": "text", "inline": "ping-" + sessionID}},
	})
	if err := stream.Send(&adapterv1.AttachRequest{SessionId: &adapterv1.SessionId{Value: sessionID}, EnvelopeJson: body}); err != nil {
		t.Fatalf("Send(%s): %v", sessionID, err)
	}
	if resp := recvResponse(t, stream); resp.SessionID != sessionID {
		t.Errorf("%s response tagged %q, want %q", sessionID, resp.SessionID, sessionID)
	}
}

// assertUnaddressedFrameRelays opens an Attach stream for sessionID and puts
// an unaddressed session-scoped frame on the runtime output. §28.5.3 relays it
// only while the pod's slot registry holds at most one entry, so the frame
// arriving is the observable that the registry holds sessionID alone.
func (f *abandonFixture) assertUnaddressedFrameRelays(t *testing.T, sessionID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	before := f.rt.subscribers()
	stream, err := f.raw.Attach(ctx)
	if err != nil {
		t.Fatalf("Attach(%s): %v", sessionID, err)
	}
	if err := stream.Send(&adapterv1.AttachRequest{SessionId: &adapterv1.SessionId{Value: sessionID}}); err != nil {
		t.Fatalf("Send(%s): %v", sessionID, err)
	}
	for f.rt.subscribers() == before {
		if ctx.Err() != nil {
			t.Fatalf("the %s Attach stream never opened the runtime output", sessionID)
		}
		time.Sleep(10 * time.Millisecond)
	}
	f.rt.inject([]byte(`{"type":"response","output":[{"type":"text","inline":"unaddressed-probe"}]}`))
	for {
		got, err := stream.Recv()
		if err != nil {
			t.Fatalf("the unaddressed frame never reached %s's stream (%v): the pod still counts a residue entry", sessionID, err)
		}
		if strings.Contains(string(got.GetEnvelopeJson()), "unaddressed-probe") {
			return
		}
	}
}

// sendHeldReclaim delivers the compensating Shutdown bindFaults withheld,
// as the late or recovered reclaim of the abandoned attempt.
func (f *abandonFixture) sendHeldReclaim(t *testing.T) *adapterv1.ShutdownResponse {
	t.Helper()
	f.faults.mu.Lock()
	held := f.faults.held
	f.faults.held = nil
	f.faults.mu.Unlock()
	if len(held) != 1 {
		t.Fatalf("held reclaims = %d, want one", len(held))
	}
	resp, err := f.raw.Shutdown(context.Background(), held[0])
	if err != nil {
		t.Fatalf("deliver held reclaim: %v", err)
	}
	return resp
}

// spec: §7.1 (normal flow); §5.2 (pool configuration and execution modes);
// §4.7.1 (role and gateway RPC contract)
// diagnosis: a failure means a bind the gateway abandoned left residue on the
// shared pod. If the unaddressed frame does not reach alice's stream, the
// abandoned attempt's slot registry entry survived and holds the §28.5.3 slot
// count above one. If carol cannot start or echo, the reclaim took the shared
// runtime's connection or listener with it. In the late-reclaim arm, a
// failure means a compensation for an earlier attempt destroyed the retried
// session. In the refused-retry arm, a failure means a retry was admitted onto
// the previous attempt's entry, or the recovered reclaim did not free the slot.
func TestAbandonedBindLeavesNoSlotResidueOnTheSharedPod_spec_7_1(t *testing.T) {
	envtest.SkipUnlessAvailable(t)

	t.Run("abandoned_at_session_start", func(t *testing.T) {
		f := newAbandonFixture(t)
		f.bind(t, "sess-alice", 2)
		f.faults.startFault["sess-bob"] = "drop"
		sbe := f.failBind(t, context.Background(), "sess-bob", 2)
		if sbe.Leaked {
			t.Fatalf("bob's reclaim was not acknowledged clean: %v", sbe)
		}
		if len(f.faults.reclaims) != 1 || f.faults.reclaims[0].GetSessionId().GetValue() != "sess-bob" {
			t.Fatalf("compensating reclaims = %v, want one for sess-bob", f.faults.reclaims)
		}
		if _, err := os.Stat(filepath.Join(f.srv.WorkspaceBase, "slots", "sess-bob")); !os.IsNotExist(err) {
			t.Errorf("bob's slot tree survived the reclaim (stat err=%v)", err)
		}
		f.assertUnaddressedFrameRelays(t, "sess-alice")
		f.bind(t, "sess-carol", 2)
		f.assertEchoes(t, "sess-carol")
		f.assertEchoes(t, "sess-alice")
	})

	t.Run("late_reclaim_is_superseded", func(t *testing.T) {
		f := newAbandonFixture(t)
		f.bind(t, "sess-alice", 2)
		f.faults.startFault["sess-bob"] = "expire"
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = f.failBind(t, ctx, "sess-bob", 2)
		if len(f.faults.reclaims) != 1 {
			t.Fatalf("compensating reclaims = %d, want one", len(f.faults.reclaims))
		}
		first := f.faults.reclaims[0]
		f.bind(t, "sess-bob", 2)
		// The first attempt's compensation arrives again after the retry
		// bound and started: it names a token the entry does not carry.
		resp, err := f.raw.Shutdown(context.Background(), first)
		if err != nil {
			t.Fatalf("late reclaim: %v", err)
		}
		if resp.GetSlotReclaim() != adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_SUPERSEDED {
			t.Errorf("late reclaim outcome = %v, want SUPERSEDED", resp.GetSlotReclaim())
		}
		if _, err := os.Stat(filepath.Join(f.srv.WorkspaceBase, "slots", "sess-bob", "current")); err != nil {
			t.Errorf("the retried session's tree did not survive the late reclaim: %v", err)
		}
		f.assertEchoes(t, "sess-bob")
		f.assertEchoes(t, "sess-alice")
	})

	t.Run("retry_refused_until_the_reclaim_lands", func(t *testing.T) {
		f := newAbandonFixture(t)
		f.bind(t, "sess-alice", 4)
		f.faults.startFault["sess-bob"] = "drop"
		f.faults.holdReclaim["sess-bob"] = true
		if sbe := f.failBind(t, context.Background(), "sess-bob", 4); !sbe.Leaked {
			t.Fatal("an unanswered reclaim was booked not leaked")
		}
		// The retry lands on the entry the lost reclaim left and is refused
		// on the identity gate before it touches anything.
		retry := f.failBind(t, context.Background(), "sess-bob", 4)
		if !errors.Is(retry, adapterclient.ErrSlotBindAttemptSuperseded) {
			t.Fatalf("retry error = %v, want the superseded refusal", retry)
		}
		if resp := f.sendHeldReclaim(t); resp.GetSlotReclaim() != adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_RECLAIMED {
			t.Fatalf("recovered reclaim outcome = %v, want RECLAIMED", resp.GetSlotReclaim())
		}
		f.bind(t, "sess-bob", 4)
		f.assertEchoes(t, "sess-bob")
	})
}
