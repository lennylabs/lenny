// SPDX-License-Identifier: MIT

package podsession_test

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/adapter"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
	"github.com/lennylabs/lenny/pkg/gateway/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/podclaim"
	"github.com/lennylabs/lenny/pkg/gateway/podsession"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// concurrentAdapter is a gRPC adapter fake that models a concurrent-mode
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
	// test can assert that workspace-concurrent finalizes a workspace
	// and stateless-concurrent does not.
	finalized map[string]bool
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
	a.started[req.GetSessionId().GetValue()] = true
	a.mu.Unlock()
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
// host concurrent-mode slots.
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
	binder := newBinder(c, concurrentAdapterDialer(t, a))

	res, err := binder.BindSlot(context.Background(), podsession.SlotBindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
		Style: podclaim.StyleWorkspace, MaxConcurrent: 8,
		Plan: &adapterv1.WorkspacePlan{},
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

	var sb lennyv1.Sandbox
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: "sbx-1"}, &sb); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if sb.Status.Phase != "slot_active" || sb.Status.ActiveSlots != 1 {
		t.Errorf("phase=%q slots=%d, want slot_active/1", sb.Status.Phase, sb.Status.ActiveSlots)
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
	c := k8sClient(t,
		concurrentIdleSandbox("sbx-1", "10.244.1.7"),
		concurrentIdleSandbox("sbx-2", "10.244.1.8"),
	)
	binder := newBinder(c, concurrentAdapterDialer(t, a))

	r1, err := binder.BindSlot(context.Background(), podsession.SlotBindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
		Style: podclaim.StyleWorkspace, MaxConcurrent: 4, Plan: &adapterv1.WorkspacePlan{},
	})
	if err != nil {
		t.Fatalf("BindSlot sess-1: %v", err)
	}
	defer r1.Adapter.Close()

	r2, err := binder.BindSlot(context.Background(), podsession.SlotBindRequest{
		Pool: testPool, SessionID: "sess-2", TenantID: "acme", Runtime: "claude-code",
		Style: podclaim.StyleWorkspace, MaxConcurrent: 4, Plan: &adapterv1.WorkspacePlan{},
	})
	if err != nil {
		t.Fatalf("BindSlot sess-2: %v", err)
	}
	defer r2.Adapter.Close()

	if r1.SandboxName != r2.SandboxName {
		t.Errorf("the two slots landed on different pods (%s, %s); §5.2 packs them onto one pod",
			r1.SandboxName, r2.SandboxName)
	}

	var shared lennyv1.Sandbox
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: r1.SandboxName}, &shared); err != nil {
		t.Fatalf("get shared pod: %v", err)
	}
	if shared.Status.ActiveSlots != 2 {
		t.Errorf("shared pod activeSlots = %d, want 2", shared.Status.ActiveSlots)
	}
	started := a.startedSet()
	if !started["sess-1"] || !started["sess-2"] {
		t.Errorf("both slots' runtimes must start; started=%v", started)
	}
}

// spec: 5.2
// diagnosis: stateless-concurrent BindSlot ran the workspace path. §5.2
// stateless-concurrent materializes no workspace and tracks no per-slot
// lifecycle — BindSlot must start the slot's runtime without
// FinalizeWorkspace or RunSetup, and a stateless slot bind must succeed
// with no workspace plan supplied.
func TestBindSlotStatelessConcurrentSkipsWorkspace(t *testing.T) {
	a := newConcurrentAdapter()
	c := k8sClient(t, concurrentIdleSandbox("sbx-1", "10.244.1.7"))
	binder := newBinder(c, concurrentAdapterDialer(t, a))

	res, err := binder.BindSlot(context.Background(), podsession.SlotBindRequest{
		Pool: testPool, SessionID: "sess-s", TenantID: "acme", Runtime: "stateless-runtime",
		Style: podclaim.StyleStateless, MaxConcurrent: 8,
		// No Plan: stateless-concurrent materializes no workspace.
	})
	if err != nil {
		t.Fatalf("BindSlot stateless: %v", err)
	}
	defer res.Adapter.Close()

	if !a.startedSet()["sess-s"] {
		t.Error("the stateless slot's runtime was not started")
	}
	// §5.2: stateless-concurrent materializes no workspace, so
	// FinalizeWorkspace must not have run.
	if a.finalizedSet()["sess-s"] {
		t.Error("stateless-concurrent must not finalize a workspace")
	}

	var sb lennyv1.Sandbox
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: "sbx-1"}, &sb); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if sb.Status.Phase != "slot_active" || sb.Status.ActiveSlots != 1 {
		t.Errorf("stateless slot: phase=%q slots=%d, want slot_active/1", sb.Status.Phase, sb.Status.ActiveSlots)
	}
}

// spec: 5.2
// diagnosis: BindSlot did not surface ErrNoConcurrentSlot when the pool
// has no idle pod and no slot capacity. §5.2 maps this to
// WARM_POOL_EXHAUSTED with reason "concurrent_slots_exhausted".
func TestBindSlotReturnsErrNoConcurrentSlotWhenPoolEmpty(t *testing.T) {
	a := newConcurrentAdapter()
	binder := newBinder(k8sClient(t), concurrentAdapterDialer(t, a))

	_, err := binder.BindSlot(context.Background(), podsession.SlotBindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme",
		Style: podclaim.StyleWorkspace, MaxConcurrent: 8,
	})
	if !errors.Is(err, podclaim.ErrNoConcurrentSlot) {
		t.Errorf("error = %v, want ErrNoConcurrentSlot for an empty pool", err)
	}
}

// spec: 5.2
// diagnosis: ReleaseSlot drained the whole concurrent-mode pod instead
// of decrementing one slot. §6.2: releasing one slot leaves a sibling
// slot's pod slot_active; the pod returns to idle only when its last
// slot drains.
func TestReleaseSlotLeavesSiblingSlotsRunning(t *testing.T) {
	a := newConcurrentAdapter()
	c := k8sClient(t, concurrentIdleSandbox("sbx-1", "10.244.1.7"))
	binder := newBinder(c, concurrentAdapterDialer(t, a))

	r1, err := binder.BindSlot(context.Background(), podsession.SlotBindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
		Style: podclaim.StyleWorkspace, MaxConcurrent: 4, Plan: &adapterv1.WorkspacePlan{},
	})
	if err != nil {
		t.Fatalf("BindSlot sess-1: %v", err)
	}
	r2, err := binder.BindSlot(context.Background(), podsession.SlotBindRequest{
		Pool: testPool, SessionID: "sess-2", TenantID: "acme", Runtime: "claude-code",
		Style: podclaim.StyleWorkspace, MaxConcurrent: 4, Plan: &adapterv1.WorkspacePlan{},
	})
	if err != nil {
		t.Fatalf("BindSlot sess-2: %v", err)
	}

	// Release the first slot — the pod stays slot_active for sess-2.
	if err := binder.ReleaseSlot(context.Background(), r1); err != nil {
		t.Fatalf("ReleaseSlot sess-1: %v", err)
	}
	var sb lennyv1.Sandbox
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: "sbx-1"}, &sb); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if sb.Status.Phase != "slot_active" || sb.Status.ActiveSlots != 1 {
		t.Errorf("after releasing one slot: phase=%q slots=%d, want slot_active/1",
			sb.Status.Phase, sb.Status.ActiveSlots)
	}

	// Release the last slot — the pod returns to idle.
	if err := binder.ReleaseSlot(context.Background(), r2); err != nil {
		t.Fatalf("ReleaseSlot sess-2: %v", err)
	}
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: "sbx-1"}, &sb); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if sb.Status.Phase != "idle" || sb.Status.ActiveSlots != 0 {
		t.Errorf("after releasing the last slot: phase=%q slots=%d, want idle/0",
			sb.Status.Phase, sb.Status.ActiveSlots)
	}
}
