// SPDX-License-Identifier: MIT

package podsession_test

import (
	"context"
	"net"
	"strings"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
)

const negotiateVersionMethod = "/lenny.adapter.v1.Adapter/NegotiateVersion"

// readoptRecorder records every gRPC method a test adapter serves and, once
// held, rejects the §15.5 NegotiateVersion handshake with UNAVAILABLE +
// coordinator_hold exactly as a §10.1 hold-state pod does, while still
// admitting CoordinatorFence. It models the pod a crash-takeover replica
// re-adopts: the prior coordinator's connection was lost, so the pod sits in
// hold state and rejects every inbound RPC except the fence.
type readoptRecorder struct {
	mu      sync.Mutex
	methods []string
	held    bool
}

func (r *readoptRecorder) enterHold() {
	r.mu.Lock()
	r.held = true
	r.mu.Unlock()
}

func (r *readoptRecorder) record(method string) {
	r.mu.Lock()
	r.methods = append(r.methods, method)
	r.mu.Unlock()
}

// callsSince returns the methods recorded at or after index n, copied so the
// caller reads them without holding the lock the server goroutines write under.
func (r *readoptRecorder) callsSince(n int) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.methods))
	if n < len(r.methods) {
		out = append(out, r.methods[n:]...)
	}
	return out
}

func (r *readoptRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.methods)
}

func (r *readoptRecorder) rejectsHandshake(method string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.held && method == negotiateVersionMethod
}

func (r *readoptRecorder) unary(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	r.record(info.FullMethod)
	if r.rejectsHandshake(info.FullMethod) {
		return nil, status.Errorf(codes.Unavailable,
			"coordinator_hold: adapter awaits a new coordinator; %s rejected", info.FullMethod)
	}
	return handler(ctx, req)
}

func (r *readoptRecorder) stream(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	r.record(info.FullMethod)
	return handler(srv, ss)
}

// recordingAdapterDialer serves srv over an in-memory connection behind rec's
// recording interceptors and returns a DialAdapter func wired to it.
func recordingAdapterDialer(t *testing.T, srv *adapter.Server, rec *readoptRecorder) func(string) (*adapterclient.Client, error) {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	gs := adapter.NewGRPCServer(srv,
		grpc.ChainUnaryInterceptor(rec.unary),
		grpc.ChainStreamInterceptor(rec.stream))
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	return func(string) (*adapterclient.Client, error) {
		return adapterclient.Dial("passthrough:///bufnet",
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
				return lis.DialContext(ctx)
			}),
			grpc.WithTransportCredentials(insecure.NewCredentials()))
	}
}

// spec: §10.1 (a crash-takeover pod is in hold state and rejects every inbound
// RPC except CoordinatorFence), §15.5 (the version handshake runs after the
// fence, not before) — ReadoptConnect re-opens the §4.7 adapter connection to a
// still-running pod without the §15.5 NegotiateVersion handshake, so the
// takeover replica sends CoordinatorFence as the first RPC over it. reconnect
// runs NegotiateVersion before any fence, which a hold-state pod rejects, so it
// cannot re-adopt the pod; ReadoptConnect is the fence-first entry point.
// diagnosis: a failure means the crash-takeover re-adopt issues a pre-fence
// version handshake that the §10.1 hold-state pod rejects, so the reacquired
// pod is never fenced and self-terminates at the hold timeout.
func TestReadoptConnectSkipsVersionHandshakeSoFenceIsFirst_spec_10_1(t *testing.T) {
	rt := &fakeRuntime{}
	srv := adapter.New("adapter-test")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = rt

	rec := &readoptRecorder{}
	c := k8sClient(t, idleSandbox("sbx-1", "10.244.1.7"))
	binder := newBinder(c, recordingAdapterDialer(t, srv, rec))

	// A session is started on the pod, so it is a still-running pod a crash
	// takeover re-adopts. The initial bind runs the §15.5 handshake normally.
	if _, err := binder.Bind(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
	}); err != nil {
		t.Fatalf("Bind: %v", err)
	}

	// The prior coordinator's connection is lost: the pod enters §10.1 hold
	// state and rejects the pre-fence version handshake.
	rec.enterHold()
	before := rec.count()

	sb, cl, err := binder.ReadoptConnect(context.Background(), "sbx-1")
	if err != nil {
		t.Fatalf("ReadoptConnect against a hold-state pod: %v", err)
	}
	defer cl.Close()
	if sb.Name != "sbx-1" {
		t.Errorf("ReadoptConnect resolved sandbox %q, want sbx-1", sb.Name)
	}

	// Opening the connection issued no RPC: the takeover's first RPC is the
	// caller's fence, not a version handshake.
	if got := rec.callsSince(before); len(got) != 0 {
		t.Fatalf("ReadoptConnect issued RPCs before the fence: %v", got)
	}

	// The caller sends CoordinatorFence as the first RPC; the hold-state pod
	// accepts it because it is the fence.
	res, err := cl.CoordinatorFence(context.Background(), "sess-1", 2)
	if err != nil {
		t.Fatalf("CoordinatorFence as the first RPC on the re-adopted connection: %v", err)
	}
	if !res.Accepted {
		t.Fatalf("hold-state pod rejected the fence: %+v", res)
	}

	post := rec.callsSince(before)
	if len(post) == 0 || !strings.Contains(post[0], "CoordinatorFence") {
		t.Fatalf("first RPC after re-adopt = %v, want CoordinatorFence first", post)
	}
	for _, m := range post {
		if strings.Contains(m, "NegotiateVersion") {
			t.Errorf("re-adopt issued %s; the hold-state pod would reject the version handshake", m)
		}
		if strings.Contains(m, "Attach") {
			t.Errorf("re-adopt opened an Attach stream (%s); the content stream must stay lazy", m)
		}
	}

	// The premise ReadoptConnect exists for: a negotiate-first dial against
	// this same hold-state pod is rejected, so reconnect cannot re-adopt it.
	handshakeCl, err := recordingAdapterDialer(t, srv, rec)("")
	if err != nil {
		t.Fatalf("dial for handshake premise: %v", err)
	}
	defer handshakeCl.Close()
	if _, err := handshakeCl.NegotiateVersion(context.Background(), []string{adapter.ProtocolVersionV1}); status.Code(err) != codes.Unavailable {
		t.Errorf("version handshake against a hold-state pod = %v, want UNAVAILABLE coordinator_hold", err)
	}
}

// spec: §10.1 — ReadoptConnect fails closed when the session carries no
// persisted pod binding: with no sandbox name it cannot name the pod to
// re-adopt, so it rejects rather than dialing an empty address.
// diagnosis: a failure means a handoff with a lost binding dials nothing and
// misreports a connection, so a peer never re-adopts the actual pod.
func TestReadoptConnectFailsClosedOnEmptyBinding_spec_10_1(t *testing.T) {
	rec := &readoptRecorder{}
	srv := adapter.New("adapter-test")
	c := k8sClient(t)
	binder := newBinder(c, recordingAdapterDialer(t, srv, rec))

	sb, cl, err := binder.ReadoptConnect(context.Background(), "")
	if err == nil {
		if cl != nil {
			cl.Close()
		}
		t.Fatalf("ReadoptConnect with an empty binding = (%v, %v, nil), want an error", sb, cl)
	}
	if rec.count() != 0 {
		t.Errorf("ReadoptConnect dialed on an empty binding: recorded %d RPCs", rec.count())
	}
}

// spec: §10.1 — ReadoptConnect surfaces the resolve failure when the bound pod
// no longer exists (drained or gone since the binding was persisted), so the
// takeover fails rather than fencing a phantom pod.
// diagnosis: a failure means a re-adopt against a gone pod is not surfaced, so
// the takeover reports success for a pod it never reached.
func TestReadoptConnectFailsWhenBoundPodIsGone_spec_10_1(t *testing.T) {
	rec := &readoptRecorder{}
	srv := adapter.New("adapter-test")
	// No Sandbox is seeded, so resolveSandbox cannot find the bound pod.
	c := k8sClient(t)
	binder := newBinder(c, recordingAdapterDialer(t, srv, rec))

	if _, _, err := binder.ReadoptConnect(context.Background(), "sbx-gone"); err == nil {
		t.Fatal("ReadoptConnect against a gone pod succeeded, want a resolve error")
	}
	if rec.count() != 0 {
		t.Errorf("ReadoptConnect dialed a gone pod: recorded %d RPCs", rec.count())
	}
}
