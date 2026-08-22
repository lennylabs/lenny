// SPDX-License-Identifier: MIT

//go:build component

// Tier-2 component coverage for the second teardown a §11.4 full revoke
// leaves behind on a concurrent-session pod.
//
// A revoked session is torn down twice. The revocation sends the adapter a
// Shutdown carrying USER_REVOKED, and the session's terminal release sends
// another for the same session when the gateway lets the pod go. The
// adapter's Shutdown handler is idempotent for exactly that reason: the
// second request names a session the slot registry no longer holds, and it
// reports a clean exit rather than an error.
//
// The outcome is not a return value. It is the pod's Redis slot counter
// and its per-pod claim: a release that reads the second Shutdown as a
// failure marks the slot leaked, and a leaked slot is never decremented,
// so every revoked session on a concurrent pool permanently consumes one
// slot of the pod's capacity and the pod never reaches occupancy zero.
// Both live outside the process under test, so the case runs against a
// real kube-apiserver and a real Redis counter.
//
// The §10.1 coordinator-lost hold is the second producer of that pair. Its
// timeout terminates the session the adapter started on the pod, and the
// gateway's terminal release follows with a Shutdown for a session already
// torn down, over a different code path than the revoke takes. The last
// case here drives that route to the same single decrement.
//
// spec: §5.2 (per-pod slot counter, leaked slots stay counted), §6.2
// (release disposition), §10.1 (coordinator-lost self-termination), §11.4
// (full revoke propagation).
package slotrelease_test

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/admission/ownership"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podclaim"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/storage/slotcounter"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

const (
	releaseNS   = "lenny-agents"
	releasePool = "claude-worker"
	releasePod  = "sbx-1"
	// releaseMaxConcurrent is the pool's maxConcurrentSessions. Four is
	// enough to hold the revoked session beside a co-tenant, which is what
	// makes the occupancy-zero edge a separate event from the revocation.
	releaseMaxConcurrent = 4
	// userTerminateDeadline is the §11.4 step-3 graceful window the
	// gateway pins at full revoke.
	userTerminateDeadline = 10 * time.Second
)

// slotRuntime is the pod's runtime process. closeErr, when set, makes
// every Runtime.Close fail, which is what the adapter reports as an
// unclean exit and the release reads as a leaked slot.
type slotRuntime struct{ closeErr error }

func (r *slotRuntime) Start(context.Context, string) error           { return nil }
func (r *slotRuntime) WriteEnvelope(string, []byte) error            { return nil }
func (r *slotRuntime) Interrupt(context.Context, string, bool) error { return nil }
func (r *slotRuntime) Close(context.Context, string) error           { return r.closeErr }
func (r *slotRuntime) Output(context.Context, string) (<-chan []byte, error) {
	ch := make(chan []byte)
	close(ch)
	return ch, nil
}

// releaseFixture is one pod on a concurrent-session pool, its adapter, its
// binder, and the Redis counter the pod's occupancy is kept in.
type releaseFixture struct {
	binder   *podsession.Binder
	registry *podsession.Registry
	exec     *executor.PodExecutor
	kube     client.Client
	redis    *miniredis.Miniredis
	// srv is the pod's adapter and rawClient a direct gRPC client onto it,
	// so a case can drive the §10.1 coordinator control stream the binder
	// does not open.
	srv       *adapter.Server
	rawClient adapterv1.AdapterClient
}

// newReleaseFixture brings up a real kube-apiserver holding one idle
// Sandbox, a miniredis-backed slot counter, and an adapter served over an
// in-memory connection.
func newReleaseFixture(t *testing.T, rt adapter.RuntimeProcess, configure ...func(*adapter.Server)) *releaseFixture {
	t.Helper()
	kube := releaseKubeClient(t)
	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rc.Close() })

	srv := adapter.New("test")
	srv.WorkspaceBase = t.TempDir()
	srv.SessionsRoot = t.TempDir()
	srv.ArtifactsRoot = t.TempDir()
	srv.ManifestDir = t.TempDir()
	srv.Runtime = rt
	for _, c := range configure {
		c(srv)
	}

	lis := releaseListener(t, srv)
	binder := &podsession.Binder{
		Client:           kube,
		Namespace:        releaseNS,
		AdapterPort:      50051,
		AcceptedVersions: []string{adapter.ProtocolVersionV1},
		DialAdapter:      releaseDialer(lis),
		SlotCounter:      slotcounter.New(rc),
	}
	registry := podsession.NewRegistry()
	return &releaseFixture{
		binder:    binder,
		registry:  registry,
		exec:      executor.NewPodExecutor(registry, binder),
		kube:      kube,
		redis:     mr,
		srv:       srv,
		rawClient: releaseRawClient(t, lis),
	}
}

// bindSlot places one session on the pod and registers the binding the
// executor's release reads.
func (f *releaseFixture) bindSlot(t *testing.T, sessionID string) *podsession.BindResult {
	t.Helper()
	result, err := f.binder.BindSlot(context.Background(), podsession.SlotBindRequest{
		Pool:                  releasePool,
		SessionID:             sessionID,
		TenantID:              "acme",
		Runtime:               "echo",
		MaxConcurrentSessions: releaseMaxConcurrent,
		Plan:                  &adapterv1.WorkspacePlan{},
	})
	if err != nil {
		t.Fatalf("bind a slot for %s: %v", sessionID, err)
	}
	f.registry.Put(result)
	return result
}

// activeSlots reads the pod's §5.2 occupancy counter. A missing key is
// zero, which is the state the counter reaches when the last slot
// releases.
func (f *releaseFixture) activeSlots(t *testing.T) int {
	t.Helper()
	v, err := f.redis.Get("lenny:pod:" + releasePod + ":active_slots")
	if err != nil {
		return 0
	}
	n, cerr := strconv.Atoi(v)
	if cerr != nil {
		t.Fatalf("pod occupancy counter is not a number: %q", v)
	}
	return n
}

// claimExists reports whether the per-pod occupancy claim is still there.
// It is the §5.2 occupancy authority: the claim spans the whole occupancy
// episode and is disposed of when the pod's last slot releases.
func (f *releaseFixture) claimExists(t *testing.T) bool {
	t.Helper()
	var claim lennyv1.SandboxClaim
	err := f.kube.Get(context.Background(), client.ObjectKey{
		Namespace: releaseNS, Name: podclaim.ClaimName(releasePod),
	}, &claim)
	if err == nil {
		return true
	}
	if apierrors.IsNotFound(err) {
		return false
	}
	t.Fatalf("get the per-pod claim: %v", err)
	return false
}

// spec: 5.2 (per-pod slot counter; a leaked slot stays counted), 6.2
// (release disposition), 11.4 (full revoke propagation)
// diagnosis: a revoked session's slot was decremented the wrong number of
// times. A count that never reaches zero means the terminal release read
// the second Shutdown as a failure and marked the slot leaked, so the
// revoked session holds one of the pod's slots for the life of the pod and
// the pod never recycles or retires. A count that goes below the number of
// live slots means a teardown decremented twice and the gateway will
// over-assign past maxConcurrentSessions.
func TestRevokedSessionDecrementsItsSlotExactlyOnce_spec_11_4(t *testing.T) {
	f := newReleaseFixture(t, &slotRuntime{})
	revoked := f.bindSlot(t, "sess-revoked")
	coTenant := f.bindSlot(t, "sess-cotenant")
	if got := f.activeSlots(t); got != 2 {
		t.Fatalf("pod occupancy after two binds = %d, want 2", got)
	}

	// §11.4 step 2: the revocation tears the session down over the
	// still-open connection its bind holds.
	cleanly, err := revoked.Adapter.Shutdown(context.Background(),
		"sess-revoked", "USER_REVOKED", userTerminateDeadline)
	if err != nil {
		t.Fatalf("revoke teardown: %v", err)
	}
	if !cleanly {
		t.Fatal("the revoke teardown reported an unclean exit")
	}
	if got := f.activeSlots(t); got != 2 {
		t.Fatalf("pod occupancy after the revoke teardown = %d, want 2; the revocation does not touch the counter", got)
	}

	// The terminal release sends the second Shutdown for the same session.
	// The adapter no longer holds its entry, so the request is the
	// idempotent no-op and reports a clean exit.
	if err := f.exec.Release(context.Background(), "sess-revoked", ""); err != nil {
		t.Fatalf("release the revoked session's slot: %v", err)
	}
	if got := f.activeSlots(t); got != 1 {
		t.Errorf("pod occupancy after the revoke and the release = %d, want 1; "+
			"the pair decremented the slot other than exactly once", got)
	}
	if !f.claimExists(t) {
		t.Error("the per-pod claim was disposed of while a co-tenant slot is still live")
	}

	// The pod reaches occupancy zero and takes its claim disposition once
	// its remaining slot releases, which a leaked revoked slot would
	// prevent for the life of the pod.
	if err := f.exec.Release(context.Background(), "sess-cotenant", ""); err != nil {
		t.Fatalf("release the co-tenant's slot: %v", err)
	}
	_ = coTenant
	if got := f.activeSlots(t); got != 0 {
		t.Errorf("pod occupancy after every slot released = %d, want 0", got)
	}
	if f.claimExists(t) {
		t.Error("the per-pod claim survived occupancy zero; the pod neither recycles nor retires")
	}
}

// spec: 5.2 (a leaked slot remains counted), 6.2 (release disposition)
// diagnosis: the retained meaning of a leaked slot was lost. A teardown
// whose adapter call failed, or which reported an unclean exit, must leave
// the slot counted and the pod's claim undisposed: the session's resources
// are not reclaimed until the pod terminates, and freeing the occupancy
// would let the gateway place a new session into a slot the adapter may
// still hold. A failure here means the release now treats every outcome as
// clean, which is the over-correction the idempotent second Shutdown
// invites.
func TestUncleanSlotTeardownStaysCounted_spec_5_2(t *testing.T) {
	t.Run("unclean_exit", func(t *testing.T) {
		f := newReleaseFixture(t, &slotRuntime{closeErr: context.DeadlineExceeded})
		f.bindSlot(t, "sess-unclean")
		if err := f.exec.Release(context.Background(), "sess-unclean", ""); err != nil {
			t.Fatalf("release the slot: %v", err)
		}
		if got := f.activeSlots(t); got != 1 {
			t.Errorf("pod occupancy after an unclean teardown = %d, want 1 (the slot stays counted)", got)
		}
		if !f.claimExists(t) {
			t.Error("the per-pod claim was disposed of while a leaked slot is still counted")
		}
	})

	t.Run("transport_error", func(t *testing.T) {
		f := newReleaseFixture(t, &slotRuntime{})
		result := f.bindSlot(t, "sess-transport")
		// The adapter connection is gone before the release runs, which is
		// the pod-unreachable case: on doubt the slot stays counted.
		result.Adapter.Close()
		if err := f.exec.Release(context.Background(), "sess-transport", ""); err != nil {
			t.Fatalf("release the slot: %v", err)
		}
		if got := f.activeSlots(t); got != 1 {
			t.Errorf("pod occupancy after a transport failure = %d, want 1 (the slot stays counted)", got)
		}
		if !f.claimExists(t) {
			t.Error("the per-pod claim was disposed of after a transport failure")
		}
	})
}

// dropCoordinatorStream opens the §10.1 gateway control stream, waits
// until the adapter has attached it, and then drops it, which is the
// coordinator-loss signal a crashed coordinating replica produces.
func (f *releaseFixture) dropCoordinatorStream(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := f.rawClient.AdapterEvents(ctx)
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
			f.srv.EmitRateLimited("hold-probe")
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

// waitForHoldTermination blocks until the §10.1 hold timeout has taken the
// pod's started session off the shared runtime process, which is the point
// at which the adapter has terminated it.
func (f *releaseFixture) waitForHoldTermination(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if f.srv.SoleSessionID() == "" {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("the coordinator hold never terminated the pod's session")
}

// spec: 10.1 (coordinator-lost self-termination), 5.2 (per-pod slot
// counter; a leaked slot stays counted), 6.2 (release disposition)
// diagnosis: a session the coordinator-lost hold terminated did not
// release its slot exactly once. The hold timeout is the second producer
// of a terminal release for a session the adapter has already torn down,
// and it reaches that teardown through its own path rather than through a
// gateway Shutdown. A count that never reaches zero means the release read
// the hold-terminated session's Shutdown as a failure and marked the slot
// leaked, so every hold-terminated session consumes one of the pod's slots
// for the life of the pod and the pod never recycles or retires. A count
// below the number of live slots means the pair decremented twice and the
// gateway will over-assign past maxConcurrentSessions.
func TestHoldTerminatedSessionDecrementsItsSlotExactlyOnce_spec_10_1(t *testing.T) {
	f := newReleaseFixture(t, &slotRuntime{}, func(s *adapter.Server) {
		s.CoordinatorHoldTimeout = 20 * time.Millisecond
	})
	f.bindSlot(t, "sess-held")
	if got := f.activeSlots(t); got != 1 {
		t.Fatalf("pod occupancy after the first bind = %d, want 1", got)
	}

	// The coordinating gateway drops, no replacement fences, and the hold
	// times out and terminates the session the adapter had started.
	f.dropCoordinatorStream(t)
	f.waitForHoldTermination(t)
	if got := f.activeSlots(t); got != 1 {
		t.Fatalf("pod occupancy after the hold termination = %d, want 1; the termination does not touch the counter", got)
	}

	// A co-tenant takes a second slot, so the pod reaching occupancy zero
	// is a separate event from the terminated session's release.
	f.bindSlot(t, "sess-cotenant")
	if got := f.activeSlots(t); got != 2 {
		t.Fatalf("pod occupancy after the co-tenant bound = %d, want 2", got)
	}

	// The gateway's terminal release for the terminated session is the
	// second teardown, and it decrements the slot exactly once.
	if err := f.exec.Release(context.Background(), "sess-held", ""); err != nil {
		t.Fatalf("release the hold-terminated session's slot: %v", err)
	}
	if got := f.activeSlots(t); got != 1 {
		t.Errorf("pod occupancy after the hold termination and the release = %d, want 1; "+
			"the pair decremented the slot other than exactly once", got)
	}
	if !f.claimExists(t) {
		t.Error("the per-pod claim was disposed of while a co-tenant slot is still live")
	}

	if err := f.exec.Release(context.Background(), "sess-cotenant", ""); err != nil {
		t.Fatalf("release the co-tenant's slot: %v", err)
	}
	if got := f.activeSlots(t); got != 0 {
		t.Errorf("pod occupancy after every slot released = %d, want 0", got)
	}
	if f.claimExists(t) {
		t.Error("the per-pod claim survived occupancy zero; the pod neither recycles nor retires")
	}
}

// releaseKubeClient starts an envtest kube-apiserver holding one idle
// Sandbox in the pool. The gateway's slot claim uses SSA Apply, which the
// controller-runtime fake client does not implement, so the case needs a
// real apiserver.
func releaseKubeClient(t *testing.T) client.Client {
	t.Helper()
	env := envtest.Start(t)
	scheme := runtime.NewScheme()
	if err := lennyv1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme lenny: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme corev1: %v", err)
	}
	c, err := client.New(env.RESTConfig(), client.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	ctx := context.Background()
	if err := c.Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: releaseNS},
	}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create namespace: %v", err)
	}
	sb := &lennyv1.Sandbox{ObjectMeta: metav1.ObjectMeta{
		Name:      releasePod,
		Namespace: releaseNS,
		Labels:    map[string]string{warmpool.LabelPool: releasePool},
	}}
	if err := c.Create(ctx, sb); err != nil {
		t.Fatalf("create Sandbox: %v", err)
	}
	// §4.6.3 field ownership: the WarmPoolController owns the phase and
	// the pod address, so the seed is applied under its field manager.
	u := &unstructured.Unstructured{}
	u.SetAPIVersion(lennyv1.GroupVersion.String())
	u.SetKind("Sandbox")
	u.SetName(releasePod)
	u.SetNamespace(releaseNS)
	if err := unstructured.SetNestedField(u.Object,
		map[string]interface{}{"phase": "idle", "podIP": "10.244.2.5"}, "status"); err != nil {
		t.Fatalf("build Sandbox status seed: %v", err)
	}
	if err := c.Status().Patch(ctx, u, client.Apply,
		client.FieldOwner(string(ownership.WarmPoolController))); err != nil {
		t.Fatalf("seed Sandbox status: %v", err)
	}
	return c
}

// releaseListener serves the adapter over an in-memory connection.
func releaseListener(t *testing.T, srv *adapter.Server) *bufconn.Listener {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	gs := adapter.NewGRPCServer(srv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	return lis
}

// bufconnDialOptions dials the in-memory listener.
func bufconnDialOptions(lis *bufconn.Listener) []grpc.DialOption {
	return []grpc.DialOption{
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}
}

// releaseDialer is the binder's adapter dialer onto the in-memory listener.
func releaseDialer(lis *bufconn.Listener) func(string) (*adapterclient.Client, error) {
	return func(string) (*adapterclient.Client, error) {
		return adapterclient.Dial("passthrough:///bufnet", bufconnDialOptions(lis)...)
	}
}

// releaseRawClient returns a plain adapter gRPC client onto the same
// listener, which a case uses to open and drop the §10.1 gateway control
// stream the binder itself never opens.
func releaseRawClient(t *testing.T, lis *bufconn.Listener) adapterv1.AdapterClient {
	t.Helper()
	conn, err := grpc.NewClient("passthrough:///bufnet", bufconnDialOptions(lis)...)
	if err != nil {
		t.Fatalf("dial the adapter: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return adapterv1.NewAdapterClient(conn)
}
