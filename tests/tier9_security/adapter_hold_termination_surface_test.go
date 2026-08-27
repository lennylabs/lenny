// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 §10.1 coordinator-lost termination against the §9.1 / §15.4.3
// intra-pod MCP surface, driven on the real adapter.Server.
//
// The adapter enters hold state when the coordinating gateway's control
// stream drops with a session still live, and self-terminates that session
// when no new coordinator fences within the hold timeout. The termination
// closes the session on the pod's one shared runtime process. The pod-wide
// platform MCP socket outlives that close, and every call on it is
// forwarded to the gateway under a session identifier the gateway installs
// as the authenticated principal. A surface that still named the
// terminated session would execute tool calls under the user and the
// delegation budget of a session the platform has already ended, and no
// gateway-side check catches it.
//
// spec: §10.1; §9.1; §15.4.3; §13.1.
package tier9_security_test

import (
	"context"
	"encoding/json"
	"net"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/adapter"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// holdTerminationPod starts one session on an adapter wired to fwd and a
// live platform MCP socket, with a short §10.1 hold timeout, and returns
// the server together with the manifest a conforming runtime reads.
func holdTerminationPod(t *testing.T, fwd *recordingForwarder, sessionID string) (*adapter.Server, *adapter.Manifest) {
	t.Helper()
	s := adapter.New("test")
	s.WorkspaceBase = t.TempDir()
	s.Runtime = noopRuntime{}
	s.ManifestDir = t.TempDir()
	s.MCPSocket = shortMCPSocket(t)
	s.PlatformForwarder = fwd
	s.ConnectorForwarder = fwd
	s.CoordinatorHoldTimeout = 20 * time.Millisecond
	if _, err := s.StartSession(context.Background(), &adapterv1.StartSessionRequest{
		SessionId: &adapterv1.SessionId{Value: sessionID},
		Runtime:   "echo",
	}); err != nil {
		t.Fatalf("StartSession(%s): %v", sessionID, err)
	}
	return s, readProbeManifest(t, s)
}

// serveAdapterOverBufconn serves s on an in-memory listener and returns a
// connected client, so a test can open and drop the gateway control stream
// the way a crashed coordinating replica does.
func serveAdapterOverBufconn(t *testing.T, s *adapter.Server) adapterv1.AdapterClient {
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
		t.Fatalf("dial bufconn: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return adapterv1.NewAdapterClient(conn)
}

// dropCoordinatorStream opens the gateway control stream, waits until the
// adapter has attached it (an event the adapter emits arrives only once the
// stream is the pod's sink), and then drops it, which is the §10.1
// coordinator-loss signal.
func dropCoordinatorStream(t *testing.T, s *adapter.Server, client adapterv1.AdapterClient) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := client.AdapterEvents(ctx)
	if err != nil {
		cancel()
		t.Fatalf("open the gateway control stream: %v", err)
	}
	probing := make(chan struct{})
	go func() {
		for {
			select {
			case <-probing:
				return
			default:
			}
			s.EmitRateLimited("hold-probe")
			time.Sleep(2 * time.Millisecond)
		}
	}()
	if _, err := stream.Recv(); err != nil {
		close(probing)
		cancel()
		t.Fatalf("the adapter never attached the control stream: %v", err)
	}
	close(probing)
	cancel()
}

// spec: 10.1 (coordinator-lost self-termination), 9.1 (platform tool
// surface), 15.4.3 (intra-pod MCP), 13.1 (isolation boundaries)
//
// diagnosis: a failure means the pod's intra-pod MCP surface still names a
// session the coordinator-lost hold already terminated. Every tools/call a
// runtime makes after that termination executes under the ended session's
// user and delegation budget, which is a fail-open in the resolution that
// exists to fail closed.
func TestSharedPlatformMCPRefusesAfterCoordinatorLostTermination_spec_10_1(t *testing.T) {
	fwd := &recordingForwarder{}
	s, m := holdTerminationPod(t, fwd, "sess-alice")
	client := serveAdapterOverBufconn(t, s)

	dropCoordinatorStream(t, s, client)

	// The hold timeout fires and terminates the session; the pod's shared
	// runtime process is then serving nobody.
	deadline := time.Now().Add(10 * time.Second)
	for s.SoleSessionID() != "" {
		if time.Now().After(deadline) {
			t.Fatalf("the coordinator-lost termination left %q named as the runtime's sole session",
				s.SoleSessionID())
		}
		time.Sleep(5 * time.Millisecond)
	}

	// The pod-surface cancellation is the release step after the close, so
	// the sole-session wait above can return while it is still to run. Give
	// it a bounded window to clear the arming before probing, and probe
	// anyway when it does not, so the assertions below report the surviving
	// surface rather than a timeout.
	for deadline = time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		if armed, _ := s.PodMCPArming(); armed == "" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	connectorSocket := soleConnectorSocket(t, m)

	// The termination ended the pod's occupancy, so it runs the same
	// pod-wide MCP teardown a Shutdown release runs: the terminated
	// session's manifest nonce must open neither the platform socket nor
	// the connector socket. The hold path is the one path on which no
	// coordinator is left to reclaim the pod, so a surviving surface
	// persists for the life of the pod rather than until the next claim.
	reachable, authenticated, dispatched := platformToolCallOutcome(t, m.PlatformMcpServer.Socket, m.MCPNonce)
	if authenticated || dispatched {
		t.Errorf("after the coordinator-lost termination the platform socket authenticated=%v dispatched=%v for the ended session's nonce, want both false",
			authenticated, dispatched)
	}
	if reachable && authenticated {
		t.Error("the terminated session's manifest nonce still opens the pod's platform tool surface")
	}
	reachable, authenticated, dispatched = connectorToolCallOutcome(t, connectorSocket, m.MCPNonce)
	if authenticated || dispatched {
		t.Errorf("after the coordinator-lost termination the connector socket authenticated=%v dispatched=%v for the ended session's nonce, want both false",
			authenticated, dispatched)
	}
	if reachable && authenticated {
		t.Error("the terminated session's manifest nonce still opens the pod's connector tool surface")
	}
	if got := fwd.platformCallCount(); got != 0 {
		t.Errorf("the surface forwarded %d platform call(s) after the terminated session's close, want 0", got)
	}
	if got := fwd.connectorCallCount(); got != 0 {
		t.Errorf("the surface forwarded %d connector call(s) after the terminated session's close, want 0", got)
	}
}

// holdGateRuntime parks the first §10.1.4 hold-termination close so a case
// can drive the intra-pod MCP surface in the window between the two
// members' Runtime.Close calls. The window is real because a non-last
// close returns without touching the shared connection or the child, the
// hold-state interceptor covers the adapter's gRPC surface alone, and the
// timeout clears the hold before either pass runs.
type holdGateRuntime struct {
	mu      sync.Mutex
	entered chan string
	release chan struct{}
	seen    int
}

func newHoldGateRuntime() *holdGateRuntime {
	return &holdGateRuntime{entered: make(chan string, 1), release: make(chan struct{})}
}

func (r *holdGateRuntime) Start(context.Context, string) error { return nil }
func (r *holdGateRuntime) WriteEnvelope(string, []byte) error  { return nil }
func (r *holdGateRuntime) Output(context.Context, string) (<-chan []byte, error) {
	ch := make(chan []byte)
	close(ch)
	return ch, nil
}
func (r *holdGateRuntime) Interrupt(context.Context, string, bool) error { return nil }

func (r *holdGateRuntime) Close(_ context.Context, sessionID string) error {
	r.mu.Lock()
	r.seen++
	first := r.seen == 1
	r.mu.Unlock()
	if first {
		r.entered <- sessionID
		<-r.release
	}
	return nil
}

// spec: 10.1 (coordinator-lost self-termination), 10.1.4 (the per-member
// hold termination), 9.1 (platform tool surface), 15.4.3 (intra-pod MCP),
// 13.1 (isolation boundaries)
//
// The hold timeout terminates every started session on the pod, one member
// at a time. Between two members' closes the pod's shared runtime process
// is still resident and the pod-wide platform MCP socket is still
// listening, so a tool call arriving there names no one session. The
// surface must refuse rather than forward under either user's principal.
//
// diagnosis: a failure means the intra-pod surface forwards a call it
// cannot attribute during the pod's own self-termination. On a pod holding
// two users' sessions that executes one user's tool call under the other
// user's identity and delegation budget, which no gateway-side check
// catches.
func TestSharedPlatformMCPRefusesBetweenHoldTerminatedMembers_spec_10_1(t *testing.T) {
	fwd := &recordingForwarder{}
	rt := newHoldGateRuntime()
	s := adapter.New("test")
	s.WorkspaceBase = t.TempDir()
	s.Runtime = rt
	s.ManifestDir = t.TempDir()
	s.MCPSocket = shortMCPSocket(t)
	s.PlatformForwarder = fwd
	s.ConnectorForwarder = fwd
	s.CoordinatorHoldTimeout = 20 * time.Millisecond
	var m *adapter.Manifest
	for _, sessionID := range []string{"sess-alice", "sess-bob"} {
		if _, err := s.StartSession(context.Background(), &adapterv1.StartSessionRequest{
			SessionId: &adapterv1.SessionId{Value: sessionID},
			Runtime:   "echo",
		}); err != nil {
			t.Fatalf("StartSession(%s): %v", sessionID, err)
		}
		if m == nil {
			// The first claim takes the once-per-pod MCP start, and the
			// live servers authenticate the nonce it was armed with. A
			// later start republishes the manifest without re-arming them.
			m = readProbeManifest(t, s)
		}
	}
	client := serveAdapterOverBufconn(t, s)

	dropCoordinatorStream(t, s, client)

	select {
	case <-rt.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the hold timeout never reached the first member's runtime close")
	}
	defer close(rt.release)

	conn, err := net.Dial("unix", m.PlatformMcpServer.Socket)
	if err != nil {
		t.Fatalf("dial platform MCP socket: %v", err)
	}
	defer conn.Close()
	enc, dec := json.NewEncoder(conn), json.NewDecoder(conn)

	if err := enc.Encode(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{"_lennyNonce": m.MCPNonce, "protocolVersion": "2025-03-26"},
	}); err != nil {
		t.Fatalf("send initialize: %v", err)
	}
	var initResp map[string]json.RawMessage
	if err := dec.Decode(&initResp); err != nil {
		t.Fatalf("read initialize response: %v", err)
	}
	if _, isErr := initResp["error"]; isErr {
		t.Fatalf("nonce-bearing initialize errored: %s", initResp["error"])
	}

	if err := enc.Encode(map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": privilegedPlatformTool, "arguments": map[string]any{}},
	}); err != nil {
		t.Fatalf("send tools/call: %v", err)
	}
	var callResp map[string]json.RawMessage
	if err := dec.Decode(&callResp); err != nil {
		t.Fatalf("read tools/call response: %v", err)
	}
	if _, isErr := callResp["error"]; !isErr {
		t.Error("tools/call between two hold-terminated members succeeded; the surface must refuse " +
			"rather than forward under one of the two users' principals")
	}
	if got := fwd.platformCallCount(); got != 0 {
		t.Errorf("the surface forwarded %d platform call(s) mid-termination, want 0", got)
	}
}
