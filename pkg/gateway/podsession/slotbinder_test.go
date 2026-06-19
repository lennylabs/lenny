// SPDX-License-Identifier: MIT

package podsession_test

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/adapter"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
	"github.com/lennylabs/lenny/pkg/gateway/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/podclaim"
	"github.com/lennylabs/lenny/pkg/gateway/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/slotcounter"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/pkg/sandbox/state"
)

// newSlotBinder is newBinder wired with a miniredis-backed slot counter.
// The §5.2 Redis counter (with its §12.4 Postgres fallback) is the
// intra-pod capacity gate, so the concurrent-session BindSlot path requires
// it; a binder with no Counter fails closed.
func newSlotBinder(t *testing.T, c client.Client, dial func(string) (*adapterclient.Client, error)) *podsession.Binder {
	t.Helper()
	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rc.Close() })
	b := newBinder(c, dial)
	b.SlotCounter = slotcounter.New(rc)
	return b
}

// podClaimExists reports whether the per-pod SandboxClaim (claim-<podName>)
// exists for sandboxName. The per-pod claim is the occupancy authority; the
// slot count lives in the Redis counter, not on Sandbox.status.
func podClaimExists(t *testing.T, c client.Client, sandboxName string) bool {
	t.Helper()
	err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: "claim-" + sandboxName}, &lennyv1.SandboxClaim{})
	if err == nil {
		return true
	}
	if apierrors.IsNotFound(err) {
		return false
	}
	t.Fatalf("get per-pod claim for %s: %v", sandboxName, err)
	return false
}

// concurrentAdapter is a gRPC adapter fake that models a concurrent-session
// pod (§5.2): unlike the session-mode adapter.Server, it accepts a
// StartSession for more than one session at a time — one per slot —
// keyed by session id. It implements only the RPCs the binder's
// BindSlot path drives; everything else is the embedded
// UnimplementedAdapterServer.
type concurrentAdapter struct {
	adapterv1.UnimplementedAdapterServer

	mu sync.Mutex
	// started records every session a slot was StartSession'd for.
	started map[string]bool
	// finalized records every session FinalizeWorkspace ran for, so a
	// test can assert that a concurrent-session slot finalizes its
	// per-slot workspace (§5.2).
	finalized map[string]bool
	// startErr, when non-nil, makes StartSession fail so a test can drive
	// the §5.2 slot-failure path.
	startErr error
}

func newConcurrentAdapter() *concurrentAdapter {
	return &concurrentAdapter{started: map[string]bool{}, finalized: map[string]bool{}}
}

func (a *concurrentAdapter) NegotiateVersion(_ context.Context, req *adapterv1.NegotiateVersionRequest) (*adapterv1.NegotiateVersionResponse, error) {
	sel := ""
	for _, v := range req.GetAcceptedProtocolVersions() {
		if v == adapter.ProtocolVersionV1 {
			sel = v
		}
	}
	return &adapterv1.NegotiateVersionResponse{
		SelectedProtocolVersion: sel,
		Capabilities:            []string{"concurrentWorkspace"},
		AdapterVersion:          "concurrent-fake",
		Incompatible:            sel == "",
	}, nil
}

func (a *concurrentAdapter) FinalizeWorkspace(_ context.Context, req *adapterv1.FinalizeWorkspaceRequest) (*adapterv1.FinalizeWorkspaceResponse, error) {
	a.mu.Lock()
	a.finalized[req.GetSessionId().GetValue()] = true
	a.mu.Unlock()
	return &adapterv1.FinalizeWorkspaceResponse{}, nil
}

func (a *concurrentAdapter) RunSetup(context.Context, *adapterv1.RunSetupRequest) (*adapterv1.RunSetupResponse, error) {
	return &adapterv1.RunSetupResponse{}, nil
}

func (a *concurrentAdapter) StartSession(_ context.Context, req *adapterv1.StartSessionRequest) (*adapterv1.StartSessionResponse, error) {
	a.mu.Lock()
	startErr := a.startErr
	if startErr == nil {
		a.started[req.GetSessionId().GetValue()] = true
	}
	a.mu.Unlock()
	if startErr != nil {
		return nil, startErr
	}
	return &adapterv1.StartSessionResponse{}, nil
}

func (a *concurrentAdapter) Shutdown(context.Context, *adapterv1.ShutdownRequest) (*adapterv1.ShutdownResponse, error) {
	return &adapterv1.ShutdownResponse{}, nil
}

func (a *concurrentAdapter) startedSet() map[string]bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := map[string]bool{}
	for s := range a.started {
		out[s] = true
	}
	return out
}

func (a *concurrentAdapter) finalizedSet() map[string]bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := map[string]bool{}
	for s := range a.finalized {
		out[s] = true
	}
	return out
}

// concurrentAdapterDialer serves the concurrent-adapter fake over an
// in-memory connection and returns a DialAdapter func wired to it.
func concurrentAdapterDialer(t *testing.T, a *concurrentAdapter) func(string) (*adapterclient.Client, error) {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer()
	adapterv1.RegisterAdapterServer(gs, a)
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

// concurrentIdleSandbox is an idle Sandbox in the test pool ready to
// host concurrent-session slots.
func concurrentIdleSandbox(name, podIP string) *lennyv1.Sandbox {
	return &lennyv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNS,
			Labels:    map[string]string{warmpool.LabelPool: testPool},
		},
		Status: lennyv1.SandboxStatus{Phase: "idle", PodIP: podIP},
	}
}

// spec: 5.2
// diagnosis: BindSlot did not place a workspace-concurrent session on a
// pod slot. §5.2: workspace-concurrent runs the §4.7 workspace-and-start
// sequence on a per-pod slot, FinalizeWorkspace materializes the slot's
// workspace, and the pod transitions to slot_active.
func TestBindSlotWorkspaceConcurrentStartsTheSlot(t *testing.T) {
	a := newConcurrentAdapter()
	c := k8sClient(t, concurrentIdleSandbox("sbx-1", "10.244.1.7"))
	binder := newSlotBinder(t, c, concurrentAdapterDialer(t, a))

	res, err := binder.BindSlot(context.Background(), podsession.SlotBindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
		MaxConcurrentSessions: 8,
		Plan:                  &adapterv1.WorkspacePlan{},
	})
	if err != nil {
		t.Fatalf("BindSlot: %v", err)
	}
	defer res.Adapter.Close()

	if res.SandboxName != "sbx-1" || res.PodIP != "10.244.1.7" {
		t.Errorf("result = %+v, want sbx-1 / 10.244.1.7", res)
	}
	if res.SlotID != "sess-1" {
		t.Errorf("SlotID = %q, want sess-1", res.SlotID)
	}
	if !a.startedSet()["sess-1"] {
		t.Error("the slot's runtime was not started")
	}
	// Workspace-concurrent materializes a per-slot workspace (§6.4).
	if !a.finalizedSet()["sess-1"] {
		t.Error("workspace-concurrent must finalize the slot's workspace")
	}

	// The pod is acquired: its per-pod occupancy claim exists. The gateway
	// no longer writes Sandbox.status; the WPC projects the phase.
	if !podClaimExists(t, c, "sbx-1") {
		t.Error("BindSlot did not create the per-pod occupancy claim for sbx-1")
	}
}

// spec: 5.2
// diagnosis: a second workspace-concurrent session did not share the
// pod. §5.2: the per-pod slot bound runs multiple sessions on one pod
// simultaneously; the second session lands on the same pod as the first
// while activeSlots < maxConcurrent.
func TestBindSlotSecondSessionSharesThePod(t *testing.T) {
	a := newConcurrentAdapter()
	// One idle pod and one spare; both sessions should fit on the first.
	c := k8sClient(
		t,
		concurrentIdleSandbox("sbx-1", "10.244.1.7"),
		concurrentIdleSandbox("sbx-2", "10.244.1.8"),
	)
	binder := newSlotBinder(t, c, concurrentAdapterDialer(t, a))

	r1, err := binder.BindSlot(context.Background(), podsession.SlotBindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
		MaxConcurrentSessions: 4, Plan: &adapterv1.WorkspacePlan{},
	})
	if err != nil {
		t.Fatalf("BindSlot sess-1: %v", err)
	}
	defer r1.Adapter.Close()

	r2, err := binder.BindSlot(context.Background(), podsession.SlotBindRequest{
		Pool: testPool, SessionID: "sess-2", TenantID: "acme", Runtime: "claude-code",
		MaxConcurrentSessions: 4, Plan: &adapterv1.WorkspacePlan{},
	})
	if err != nil {
		t.Fatalf("BindSlot sess-2: %v", err)
	}
	defer r2.Adapter.Close()

	if r1.SandboxName != r2.SandboxName {
		t.Errorf("the two slots landed on different pods (%s, %s); §5.2 packs them onto one pod",
			r1.SandboxName, r2.SandboxName)
	}

	// The shared pod carries a single per-pod occupancy claim; both slots
	// multiplex onto it. The spare pod is never acquired.
	if !podClaimExists(t, c, r1.SandboxName) {
		t.Errorf("shared pod %s has no per-pod claim", r1.SandboxName)
	}
	spare := "sbx-1"
	if r1.SandboxName == "sbx-1" {
		spare = "sbx-2"
	}
	if podClaimExists(t, c, spare) {
		t.Errorf("spare pod %s was acquired; both slots must share one pod", spare)
	}
	started := a.startedSet()
	if !started["sess-1"] || !started["sess-2"] {
		t.Errorf("both slots' runtimes must start; started=%v", started)
	}
}

// spec: §4.1 (proposal), §7.1 step 4, §5.2
// diagnosis: ClaimSlot did not reserve a §5.2 slot at create. The 0007
// eager-claim design claims a per-session slot at /create for a
// concurrent-workspace pool, so the §15.1 created-state pod-claim invariant
// holds uniformly; a failure means the create path admitted a `created`
// session with no claimed pod. ClaimSlot reserves the slot (the per-pod
// SandboxClaim plus the active_slots increment) and runs the handshake, but
// does not materialize the workspace or start the runtime.
func TestClaimSlotReservesSlotWithoutStarting_spec_5_2(t *testing.T) {
	a := newConcurrentAdapter()
	c := k8sClient(t, concurrentIdleSandbox("sbx-1", "10.244.1.7"))
	binder := newSlotBinder(t, c, concurrentAdapterDialer(t, a))

	claim, err := binder.ClaimSlot(context.Background(), podsession.SlotBindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
		MaxConcurrentSessions: 8, Plan: &adapterv1.WorkspacePlan{},
	})
	if err != nil {
		t.Fatalf("ClaimSlot: %v", err)
	}
	if claim.SandboxName != "sbx-1" || claim.Pool != testPool {
		t.Errorf("claim = %+v, want sbx-1 / %s", claim, testPool)
	}
	// SlotID == SessionID so the binding is reconstructable from the persisted
	// PodAssignment + PoolRef + the session id.
	if claim.SlotID != "sess-1" {
		t.Errorf("SlotID = %q, want sess-1 (== session id)", claim.SlotID)
	}
	// The slot is reserved: the per-pod occupancy claim exists.
	if !podClaimExists(t, c, "sbx-1") {
		t.Error("ClaimSlot did not create the per-pod occupancy claim")
	}
	// No materialization or start at create: the runtime is not started and
	// the per-slot workspace is not finalized (those run at BindReservedSlot).
	if a.startedSet()["sess-1"] {
		t.Error("ClaimSlot started the runtime; the runtime must launch at /start")
	}
	if a.finalizedSet()["sess-1"] {
		t.Error("ClaimSlot finalized the workspace; materialization must run at /finalize")
	}
}

// spec: §4.1 (proposal), §5.2 — a concurrent pool with no idle pod returns
// the ErrNoIdlePod exhaustion sentinel unwrapped, reserving no slot, so the
// create handler maps it to the §7.1 SESSION_CREATION_FAILED atomicity
// envelope before the client uploads.
func TestClaimSlotEmptyPoolReturnsExhaustion_spec_5_2(t *testing.T) {
	binder := newSlotBinder(t, k8sClient(t), concurrentAdapterDialer(t, newConcurrentAdapter()))
	_, err := binder.ClaimSlot(context.Background(), podsession.SlotBindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme", MaxConcurrentSessions: 8,
	})
	if !errors.Is(err, podclaim.ErrNoIdlePod) {
		t.Errorf("error = %v, want ErrNoIdlePod for an empty pool", err)
	}
}

// spec: §4.3, §4.4 (proposal), §5.2
// diagnosis: BindReservedSlot did not reconnect to a slot reserved at create
// and run the materialize-and-launch sequence. The 0007 decomposed lifecycle
// reserves the slot at /create (ClaimSlot) and reconnects at /start
// (BindReservedSlot) rather than re-reserving, so the create-time binding
// holds from create through start. A failure means /start re-reserved a fresh
// slot (double-counting active_slots) or could not launch on the reserved one.
func TestBindReservedSlotReconnectsAndStarts_spec_5_2(t *testing.T) {
	a := newConcurrentAdapter()
	c := k8sClient(t, concurrentIdleSandbox("sbx-1", "10.244.1.7"))
	binder := newSlotBinder(t, c, concurrentAdapterDialer(t, a))
	req := podsession.SlotBindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
		MaxConcurrentSessions: 8, Plan: &adapterv1.WorkspacePlan{},
	}

	claim, err := binder.ClaimSlot(context.Background(), req)
	if err != nil {
		t.Fatalf("ClaimSlot: %v", err)
	}
	res, err := binder.BindReservedSlot(context.Background(), req, claim.SandboxName, claim.SlotID)
	if err != nil {
		t.Fatalf("BindReservedSlot: %v", err)
	}
	defer res.Adapter.Close()

	if res.SandboxName != "sbx-1" || res.SlotID != "sess-1" {
		t.Errorf("result = %+v, want sbx-1 / sess-1", res)
	}
	if !a.startedSet()["sess-1"] {
		t.Error("BindReservedSlot did not start the reserved slot's runtime")
	}
	if !a.finalizedSet()["sess-1"] {
		t.Error("BindReservedSlot did not finalize the reserved slot's workspace")
	}
	// The reservation made at create is still the binding: the per-pod claim
	// exists and was never re-reserved (one slot, not two).
	if !podClaimExists(t, c, "sbx-1") {
		t.Error("BindReservedSlot lost the per-pod occupancy claim")
	}
}

// spec: §5.2, §6.2 (pre-attached reclaim)
// diagnosis: BindReservedSlot did not release the create-time slot
// reservation on a start failure, leaking the pod's active_slots. A start
// that cannot reach `running` must reclaim the slot the create-time
// reservation held, the slot analog of the exclusive Prepare/Launch reclaim;
// the release runs exactly once so the callers do not double-decrement.
func TestBindReservedSlotReleasesReservationOnFailure_spec_5_2(t *testing.T) {
	a := newConcurrentAdapter()
	a.startErr = errors.New("runtime refused to start")
	c := k8sClient(t, concurrentIdleSandbox("sbx-1", "10.244.1.7"))
	binder := newSlotBinder(t, c, concurrentAdapterDialer(t, a))
	req := podsession.SlotBindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
		MaxConcurrentSessions: 8, Plan: &adapterv1.WorkspacePlan{},
	}

	claim, err := binder.ClaimSlot(context.Background(), req)
	if err != nil {
		t.Fatalf("ClaimSlot: %v", err)
	}
	if !podClaimExists(t, c, "sbx-1") {
		t.Fatal("ClaimSlot did not reserve the slot")
	}
	_, err = binder.BindReservedSlot(context.Background(), req, claim.SandboxName, claim.SlotID)
	if err == nil {
		t.Fatal("BindReservedSlot succeeded, want a StartSession failure")
	}
	// The reserved slot was the only slot on the pod, so releasing it on the
	// failure deletes the per-pod claim and returns the pod to the pool. A
	// surviving claim would mean the active_slots count leaked.
	if podClaimExists(t, c, "sbx-1") {
		t.Error("BindReservedSlot did not release the reserved slot on a start failure (active_slots leaked)")
	}
}

// spec: §5.2 line 519
// diagnosis: BindSlot must distinguish an empty pool from a full one so
// the gateway can set the right details.reason. §5.2 line 519: a pool
// with no pods at all is "no_idle_pods" (ErrNoIdlePod, the sentinel
// session-mode exhaustion uses); pods-exist-but-full is
// "concurrent_slots_exhausted" (ErrNoConcurrentSlot). The empty pool
// here surfaces ErrNoIdlePod, passed through unwrapped for the gateway's
// errors.Is check.
func TestBindSlotReturnsErrNoIdlePodWhenPoolEmpty(t *testing.T) {
	a := newConcurrentAdapter()
	binder := newSlotBinder(t, k8sClient(t), concurrentAdapterDialer(t, a))

	_, err := binder.BindSlot(context.Background(), podsession.SlotBindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme",
		MaxConcurrentSessions: 8,
	})
	if !errors.Is(err, podclaim.ErrNoIdlePod) {
		t.Errorf("error = %v, want ErrNoIdlePod for an empty pool (§5.2 no_idle_pods)", err)
	}
}

// spec: 5.2
// diagnosis: ReleaseSlot drained the whole concurrent-session pod instead
// of decrementing one slot. §6.2: releasing one slot leaves a sibling
// slot's pod slot_active; the pod returns to idle only when its last
// slot drains.
func TestReleaseSlotLeavesSiblingSlotsRunning(t *testing.T) {
	a := newConcurrentAdapter()
	c := k8sClient(t, concurrentIdleSandbox("sbx-1", "10.244.1.7"))
	binder := newSlotBinder(t, c, concurrentAdapterDialer(t, a))

	r1, err := binder.BindSlot(context.Background(), podsession.SlotBindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
		MaxConcurrentSessions: 4, Plan: &adapterv1.WorkspacePlan{},
	})
	if err != nil {
		t.Fatalf("BindSlot sess-1: %v", err)
	}
	r2, err := binder.BindSlot(context.Background(), podsession.SlotBindRequest{
		Pool: testPool, SessionID: "sess-2", TenantID: "acme", Runtime: "claude-code",
		MaxConcurrentSessions: 4, Plan: &adapterv1.WorkspacePlan{},
	})
	if err != nil {
		t.Fatalf("BindSlot sess-2: %v", err)
	}

	// Release the first slot — the per-pod claim stays while sess-2 runs.
	if err := binder.ReleaseSlot(context.Background(), r1); err != nil {
		t.Fatalf("ReleaseSlot sess-1: %v", err)
	}
	if !podClaimExists(t, c, "sbx-1") {
		t.Error("after releasing one slot the per-pod claim must remain while a sibling slot runs")
	}

	// Release the last slot — the per-pod claim is deleted, returning the
	// pod to the pool.
	if err := binder.ReleaseSlot(context.Background(), r2); err != nil {
		t.Fatalf("ReleaseSlot sess-2: %v", err)
	}
	if podClaimExists(t, c, "sbx-1") {
		t.Error("after releasing the last slot the per-pod claim must be deleted")
	}
}

// TestReleaseSlotReturnsCredentialLeasesToPool_spec_7_1 asserts that the
// concurrent-session slot teardown also runs the §7.1 step 23 credential-
// lease release, matching session-mode Release.
func TestReleaseSlotReturnsCredentialLeasesToPool_spec_7_1(t *testing.T) {
	a := newConcurrentAdapter()
	c := k8sClient(t, concurrentIdleSandbox("sbx-1", "10.244.1.7"))
	binder := newSlotBinder(t, c, concurrentAdapterDialer(t, a))
	assigner := &fakeAssigner{}
	binder.Credentials = assigner

	// No CredentialPools here: the §7.1 teardown hook fires on every slot
	// release regardless of whether the slot held leases, and the fake
	// concurrent adapter does not serve AssignCredentials. The actual
	// lease-freeing is covered by the credassign ReleaseSession unit test.
	r1, err := binder.BindSlot(context.Background(), podsession.SlotBindRequest{
		Pool: testPool, SessionID: "slot-sess", TenantID: "acme", Runtime: "claude-code",
		MaxConcurrentSessions: 4, Plan: &adapterv1.WorkspacePlan{},
	})
	if err != nil {
		t.Fatalf("BindSlot: %v", err)
	}
	if err := binder.ReleaseSlot(context.Background(), r1); err != nil {
		t.Fatalf("ReleaseSlot: %v", err)
	}
	if len(assigner.released) != 1 || assigner.released[0] != "slot-sess" {
		t.Errorf("ReleaseSession calls = %v, want [slot-sess]", assigner.released)
	}
}

type slotFailureCall struct{ errorType, pool, podName string }

// spec: §5.2 line 12
// A slot bind that fails after the slot was reserved emits
// lenny_slot_failure_total labeled by the failing stage (error_type),
// the pool, and the pod (k8s_pod_name).
func TestBindSlotEmitsSlotFailureOnStartError_spec_5_2(t *testing.T) {
	a := newConcurrentAdapter()
	a.startErr = errors.New("runtime refused to start")
	c := k8sClient(t, concurrentIdleSandbox("sbx-1", "10.244.1.7"))
	binder := newSlotBinder(t, c, concurrentAdapterDialer(t, a))
	var failures []slotFailureCall
	binder.SlotFailure = func(errorType, pool, podName string) {
		failures = append(failures, slotFailureCall{errorType, pool, podName})
	}

	_, err := binder.BindSlot(context.Background(), podsession.SlotBindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
		MaxConcurrentSessions: 8,
		Plan:                  &adapterv1.WorkspacePlan{},
	})
	if err == nil {
		t.Fatal("BindSlot succeeded, want a StartSession failure")
	}
	if len(failures) != 1 {
		t.Fatalf("slot failures = %d, want 1", len(failures))
	}
	got := failures[0]
	if got.errorType != "session_start" || got.pool != testPool || got.podName != "sbx-1" {
		t.Errorf("slot failure = %+v, want session_start/%s/sbx-1", got, testPool)
	}
}

// spec: §5.2 line 12
// A successful slot bind emits no slot-failure counter.
func TestBindSlotEmitsNoSlotFailureOnSuccess_spec_5_2(t *testing.T) {
	a := newConcurrentAdapter()
	c := k8sClient(t, concurrentIdleSandbox("sbx-1", "10.244.1.7"))
	binder := newSlotBinder(t, c, concurrentAdapterDialer(t, a))
	failed := false
	binder.SlotFailure = func(string, string, string) { failed = true }

	res, err := binder.BindSlot(context.Background(), podsession.SlotBindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
		MaxConcurrentSessions: 8,
		Plan:                  &adapterv1.WorkspacePlan{},
	})
	if err != nil {
		t.Fatalf("BindSlot: %v", err)
	}
	defer res.Adapter.Close()
	if failed {
		t.Error("a successful slot bind emitted lenny_slot_failure_total")
	}
}

// spec: §5.2 — a slot bind failure after reservation surfaces a
// *SlotBindError carrying the pod, slot, and stage so the gateway retry
// policy can release the slot and classify the failure. A ResourceExhausted
// classifies as oom (non-retryable).
func TestBindSlotReturnsSlotBindError_spec_5_2(t *testing.T) {
	a := newConcurrentAdapter()
	a.startErr = status.Error(codes.ResourceExhausted, "pod OOM-killed")
	c := k8sClient(t, concurrentIdleSandbox("sbx-1", "10.244.1.7"))
	binder := newSlotBinder(t, c, concurrentAdapterDialer(t, a))

	_, err := binder.BindSlot(context.Background(), podsession.SlotBindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
		MaxConcurrentSessions: 8,
		Plan:                  &adapterv1.WorkspacePlan{},
	})
	var sbe *podsession.SlotBindError
	if !errors.As(err, &sbe) {
		t.Fatalf("BindSlot error = %v, want *SlotBindError", err)
	}
	if sbe.Pod != "sbx-1" || sbe.SlotID != "sess-1" || sbe.Stage != "session_start" {
		t.Errorf("SlotBindError = {Pod:%q SlotID:%q Stage:%q}, want {sbx-1 sess-1 session_start}",
			sbe.Pod, sbe.SlotID, sbe.Stage)
	}
	if got := sbe.Reason(); got != podsession.SlotReasonOOM {
		t.Errorf("Reason() = %q, want oom", got)
	}
}

// spec: §4.6.3 — DrainSandbox retires a concurrent-session pod that crossed
// the §5.2 unhealthy threshold by stamping the lenny.dev/drain-request
// annotation on the agent Pod; the gateway writes no Sandbox.status.phase
// (the WarmPoolController is the sole writer and consumes the annotation).
// The stamp is idempotent and a missing pod is tolerated.
//
// diagnosis: a failure means the gateway either failed to stamp the
// drain-request annotation the WarmPoolController keys the unhealthy drain on,
// or it wrote Sandbox.status.phase directly, breaking the §4.6.3 single-writer
// invariant.
func TestBinderDrainSandbox_spec_4_6_3(t *testing.T) {
	sb := concurrentIdleSandbox("sbx-1", "10.244.1.7")
	c := k8sClient(t, sb)
	if err := c.Create(context.Background(), drainAgentPod("sbx-1")); err != nil {
		t.Fatalf("seed agent pod: %v", err)
	}
	binder := newSlotBinder(t, c, concurrentAdapterDialer(t, newConcurrentAdapter()))
	ctx := context.Background()

	if err := binder.DrainSandbox(ctx, "sbx-1"); err != nil {
		t.Fatalf("DrainSandbox: %v", err)
	}
	// The gateway stamps the drain-request annotation on the agent Pod and
	// writes no Sandbox.status.phase.
	var pod corev1.Pod
	if err := c.Get(ctx, client.ObjectKey{Namespace: testNS, Name: "sbx-1"}, &pod); err != nil {
		t.Fatalf("get pod: %v", err)
	}
	if pod.Annotations[lennyv1.AnnotationDrainRequest] == "" {
		t.Errorf("pod missing %s annotation after DrainSandbox", lennyv1.AnnotationDrainRequest)
	}
	var got lennyv1.Sandbox
	if err := c.Get(ctx, client.ObjectKey{Namespace: testNS, Name: "sbx-1"}, &got); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if got.Status.Phase == string(state.Draining) {
		t.Errorf("gateway wrote Sandbox.status.phase=draining; the WPC is the sole writer (§4.6.3)")
	}
	// Idempotent: a second drain re-stamps and does not error.
	if err := binder.DrainSandbox(ctx, "sbx-1"); err != nil {
		t.Fatalf("idempotent DrainSandbox: %v", err)
	}
	// Missing pod: tolerated (a pod with no slots needs no drain).
	if err := binder.DrainSandbox(ctx, "sbx-absent"); err != nil {
		t.Fatalf("DrainSandbox on missing pod: %v", err)
	}
}

// drainAgentPod builds a minimal managed agent Pod named for its Sandbox so
// the DrainSandbox annotation stamp has a pod to patch.
func drainAgentPod(name string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNS,
			Labels:    map[string]string{warmpool.LabelManaged: "true", warmpool.LabelPool: testPool},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "agent", Image: "k8s.gcr.io/pause"}},
		},
	}
}
