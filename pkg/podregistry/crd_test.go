// SPDX-License-Identifier: MIT

package podregistry_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/podregistry"
)

// scheme is the shared scheme every test wires into the fake client
// so Sandbox CRUD round-trips through the controller-runtime API.
func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := lennyv1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	return s
}

func newRegistry(t *testing.T, namespace string, seed ...client.Object) *podregistry.CRDPodRegistry {
	t.Helper()
	cli := newFakeClient(t, seed...)
	r, err := podregistry.New(cli, namespace)
	if err != nil {
		t.Fatalf("podregistry.New: %v", err)
	}
	return r
}

// newFakeClient builds the fake controller-runtime client every test uses.
// Both Sandbox and the per-pod SandboxClaim carry a status subresource so
// the §4.6.1 claim writes (SandboxClaim CREATE + `bound` status) and the
// WarmPoolController-projected Sandbox.status round-trip through the API.
func newFakeClient(t *testing.T, seed ...client.Object) client.Client {
	t.Helper()
	scheme := newScheme(t)
	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(seed...).
		WithStatusSubresource(&lennyv1.Sandbox{}, &lennyv1.SandboxClaim{}).
		Build()
}

func seedSandbox(name, pool, phase string) *lennyv1.Sandbox {
	sb := &lennyv1.Sandbox{}
	sb.Namespace = "lenny-agents"
	sb.Name = name
	sb.Labels = map[string]string{podregistry.PoolLabel: pool}
	sb.Spec.PoolRef = pool
	sb.Spec.RuntimeRef = "echo"
	sb.Spec.IsolationProfile = "sandboxed"
	sb.Status.Phase = phase
	return sb
}

// spec: §12.6 (GetPod returns the §6.2 phase via the CRD status)
func TestGetPodReturnsCRDStatus(t *testing.T) {
	r := newRegistry(t, "lenny-agents",
		seedSandbox("alpha", "echo-pool", "idle"))
	rec, err := r.GetPod(context.Background(), "alpha")
	if err != nil {
		t.Fatalf("GetPod: %v", err)
	}
	if rec.State != "idle" {
		t.Errorf("State = %q, want idle", rec.State)
	}
	if rec.PoolID != "echo-pool" {
		t.Errorf("PoolID = %q, want echo-pool", rec.PoolID)
	}
	if rec.IsolationProfile != "sandboxed" {
		t.Errorf("IsolationProfile = %q, want sandboxed", rec.IsolationProfile)
	}
}

// spec: §12.6 (GetPod for an unknown pod returns ErrNotFound)
func TestGetPodMissingReturnsErrNotFound(t *testing.T) {
	r := newRegistry(t, "lenny-agents")
	_, err := r.GetPod(context.Background(), "missing")
	if !errors.Is(err, podregistry.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// spec: §6.2 (UpdatePodState writes the new phase under CAS)
func TestUpdatePodStateWritesPhase(t *testing.T) {
	r := newRegistry(t, "lenny-agents",
		seedSandbox("alpha", "echo-pool", "warming"))
	if err := r.UpdatePodState(context.Background(), "alpha",
		podregistry.StateTransition{From: "warming", To: "idle"}); err != nil {
		t.Fatalf("UpdatePodState: %v", err)
	}
	rec, _ := r.GetPod(context.Background(), "alpha")
	if rec.State != "idle" {
		t.Errorf("State = %q, want idle", rec.State)
	}
}

// spec: §6.2 (mismatched From rejects the transition)
func TestUpdatePodStateRejectsMismatchedFrom(t *testing.T) {
	r := newRegistry(t, "lenny-agents",
		seedSandbox("alpha", "echo-pool", "idle"))
	err := r.UpdatePodState(context.Background(), "alpha",
		podregistry.StateTransition{From: "warming", To: "claimed"})
	if !errors.Is(err, podregistry.ErrInvalidTransition) {
		t.Errorf("err = %v, want ErrInvalidTransition", err)
	}
}

// spec: §4.6.1 (ClaimPod picks an idle pod and creates the per-pod claim),
// §4.6.3 (gateway is not a Sandbox.status writer), §3.3 (occupancy
// projection): the claim is created with a `bound` binding state and the
// returned record echoes the projected claimed phase, the pinned tenant,
// and the requesting session, but Sandbox.status is left untouched for the
// WarmPoolController to project.
func TestClaimPodPicksIdleAndCreatesPerPodClaim_spec_4_6_1(t *testing.T) {
	cli := newFakeClient(t,
		seedSandbox("alpha", "echo-pool", "claimed"),
		seedSandbox("bravo", "echo-pool", "idle"))
	r, err := podregistry.New(cli, "lenny-agents")
	if err != nil {
		t.Fatalf("podregistry.New: %v", err)
	}
	rec, err := r.ClaimPod(context.Background(),
		podregistry.ClaimOpts{PoolID: "echo-pool", TenantID: "acme", SessionID: "s1"})
	if err != nil {
		t.Fatalf("ClaimPod: %v", err)
	}
	if rec.PodID != "bravo" {
		t.Errorf("PodID = %q, want bravo (the idle one)", rec.PodID)
	}
	// The record echoes the claimed occupancy projection and claim metadata.
	if rec.State != "claimed" {
		t.Errorf("State = %q, want claimed (projected from the bound claim)", rec.State)
	}
	if rec.TenantID != "acme" {
		t.Errorf("TenantID = %q, want acme (claim tenantId)", rec.TenantID)
	}
	if rec.SessionID != "s1" {
		t.Errorf("SessionID = %q, want s1 (attribution echo)", rec.SessionID)
	}
	// The per-pod claim exists, carries sandboxRef + tenantId, and is in the
	// `bound` binding state.
	var claim lennyv1.SandboxClaim
	if err := cli.Get(context.Background(),
		client.ObjectKey{Namespace: "lenny-agents", Name: "claim-bravo"}, &claim); err != nil {
		t.Fatalf("get per-pod claim: %v", err)
	}
	if claim.Spec.SandboxRef != "bravo" {
		t.Errorf("claim SandboxRef = %q, want bravo", claim.Spec.SandboxRef)
	}
	if claim.Spec.TenantID != "acme" {
		t.Errorf("claim TenantID = %q, want acme", claim.Spec.TenantID)
	}
	if claim.Status.Phase != "bound" {
		t.Errorf("claim binding state = %q, want bound", claim.Status.Phase)
	}
	// spec: §4.6.1 — the `bound` write stamps the binding-state-transition
	// time the orphan GC keys its live-binding-state reclaim on, so a claim
	// from this path is keyed on the binding transition rather than on its
	// creation timestamp (the start of the whole occupancy episode).
	if claim.Status.BindingStateTransitionTime == nil {
		t.Error("claim BindingStateTransitionTime = nil, want a stamp on the bound write")
	}
	// The gateway path does NOT write Sandbox.status: the claimed Sandbox's
	// stored phase stays idle until the WarmPoolController projects it, and
	// no session/tenant is stamped on Sandbox.status.
	var sb lennyv1.Sandbox
	if err := cli.Get(context.Background(),
		client.ObjectKey{Namespace: "lenny-agents", Name: "bravo"}, &sb); err != nil {
		t.Fatalf("get claimed sandbox: %v", err)
	}
	if sb.Status.Phase != "idle" {
		t.Errorf("Sandbox.status.phase = %q, want idle (unwritten by the gateway)", sb.Status.Phase)
	}
	if sb.Status.SessionID != "" || sb.Status.TenantID != "" {
		t.Errorf("Sandbox.status session/tenant = %q/%q, want both empty (gateway is not a Sandbox.status writer)",
			sb.Status.SessionID, sb.Status.TenantID)
	}
}

// spec: §4.6.1 (a duplicate per-pod claim collides on AlreadyExists, so
// ClaimPod skips the held pod and acquires the next idle pod): the second
// claim for the same pool lands on a different pod because the first pod's
// claim-<podName> already exists.
func TestClaimPodSkipsPodWithExistingClaim_spec_4_6_1(t *testing.T) {
	cli := newFakeClient(t,
		seedSandbox("alpha", "echo-pool", "idle"),
		seedSandbox("bravo", "echo-pool", "idle"))
	// Pre-create the per-pod claim for alpha so its CREATE collides.
	existing := &lennyv1.SandboxClaim{}
	existing.Namespace = "lenny-agents"
	existing.Name = "claim-alpha"
	existing.Spec.SandboxRef = "alpha"
	if err := cli.Create(context.Background(), existing); err != nil {
		t.Fatalf("seed existing claim: %v", err)
	}
	r, err := podregistry.New(cli, "lenny-agents")
	if err != nil {
		t.Fatalf("podregistry.New: %v", err)
	}
	rec, err := r.ClaimPod(context.Background(),
		podregistry.ClaimOpts{PoolID: "echo-pool", TenantID: "acme", SessionID: "s1"})
	if err != nil {
		t.Fatalf("ClaimPod: %v", err)
	}
	if rec.PodID != "bravo" {
		t.Errorf("PodID = %q, want bravo (alpha's claim already exists)", rec.PodID)
	}
}

// spec: §12.6 line 424 (ClaimOpts carries RequiresDemotion, Priority,
// ClusterID; v1 leaves them inert but a claim with all three set still
// succeeds via the per-pod claim).
func TestClaimOptsCarriesV1InertFields_spec_12_6_424(t *testing.T) {
	r := newRegistry(t, "lenny-agents",
		seedSandbox("alpha", "echo-pool", "idle"))
	prio := int32(5)
	cid := podregistry.ClusterID("east-1")
	rec, err := r.ClaimPod(context.Background(), podregistry.ClaimOpts{
		PoolID:           "echo-pool",
		TenantID:         "acme",
		SessionID:        "s1",
		RequiresDemotion: true,
		Priority:         &prio,
		ClusterID:        &cid,
	})
	if err != nil {
		t.Fatalf("ClaimPod with extended opts: %v", err)
	}
	if rec.State != "claimed" || rec.SessionID != "s1" {
		t.Errorf("rec = %+v, want claimed/s1", rec)
	}
}

// spec: §4.6.1 (ClaimPod returns ErrPoolExhausted when nothing idle)
func TestClaimPodExhaustedWhenNoneIdle(t *testing.T) {
	r := newRegistry(t, "lenny-agents",
		seedSandbox("alpha", "echo-pool", "claimed"))
	_, err := r.ClaimPod(context.Background(),
		podregistry.ClaimOpts{PoolID: "echo-pool", TenantID: "acme", SessionID: "s1"})
	if !errors.Is(err, podregistry.ErrPoolExhausted) {
		t.Errorf("err = %v, want ErrPoolExhausted", err)
	}
}

// spec: §4.6.1 (occupancy projection on claim DELETE), §4.6.3 (gateway is not
// a Sandbox.status writer). ReleasePod deletes the deterministic per-pod
// SandboxClaim and leaves Sandbox.status untouched: the WarmPoolController
// projects the pod back to idle from the claim's absence. The release reason
// no longer maps to a Sandbox phase.
func TestReleasePodDeletesPerPodClaim_spec_4_6_3(t *testing.T) {
	cli := newFakeClient(t, seedSandbox("alpha", "echo-pool", "claimed"))
	// Seed the per-pod claim the release deletes.
	claim := &lennyv1.SandboxClaim{}
	claim.Namespace = "lenny-agents"
	claim.Name = "claim-alpha"
	claim.Spec.SandboxRef = "alpha"
	claim.Spec.TenantID = "acme"
	if err := cli.Create(context.Background(), claim); err != nil {
		t.Fatalf("seed claim: %v", err)
	}
	r, err := podregistry.New(cli, "lenny-agents")
	if err != nil {
		t.Fatalf("podregistry.New: %v", err)
	}
	if err := r.ReleasePod(context.Background(), "alpha", podregistry.ReleaseCompleted); err != nil {
		t.Fatalf("ReleasePod: %v", err)
	}
	// The per-pod claim is gone.
	var got lennyv1.SandboxClaim
	if err := cli.Get(context.Background(),
		client.ObjectKey{Namespace: "lenny-agents", Name: "claim-alpha"}, &got); !apierrors.IsNotFound(err) {
		t.Errorf("claim still present after release: err = %v, want NotFound", err)
	}
	// Sandbox.status is untouched: the gateway is not a Sandbox.status writer,
	// so the stored phase stays as the WarmPoolController last projected it and
	// no session/tenant is cleared on Sandbox.status here.
	var sb lennyv1.Sandbox
	if err := cli.Get(context.Background(),
		client.ObjectKey{Namespace: "lenny-agents", Name: "alpha"}, &sb); err != nil {
		t.Fatalf("get released sandbox: %v", err)
	}
	if sb.Status.Phase != "claimed" {
		t.Errorf("Sandbox.status.phase = %q, want claimed (unwritten by the gateway)", sb.Status.Phase)
	}
}

// spec: §4.6.1 (release is idempotent). A release of a pod whose per-pod claim
// is already gone (a double release, or a claim the orphan GC reclaimed) is a
// no-op rather than an error.
func TestReleasePodIdempotentForMissingClaim_spec_4_6_1(t *testing.T) {
	r := newRegistry(t, "lenny-agents", seedSandbox("alpha", "echo-pool", "idle"))
	if err := r.ReleasePod(context.Background(), "alpha", podregistry.ReleaseCompleted); err != nil {
		t.Errorf("ReleasePod with no claim = %v, want nil (idempotent)", err)
	}
}

// spec: §12.6 (ListPodsByPool sorts deterministically)
func TestListPodsByPoolSortsByName(t *testing.T) {
	r := newRegistry(t, "lenny-agents",
		seedSandbox("z-pod", "echo-pool", "idle"),
		seedSandbox("a-pod", "echo-pool", "idle"),
		seedSandbox("other-pod", "other-pool", "idle"))
	out, err := r.ListPodsByPool(context.Background(), "echo-pool", podregistry.PodFilter{})
	if err != nil {
		t.Fatalf("ListPodsByPool: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("len = %d, want 2", len(out))
	}
	if out[0].PodID != "a-pod" || out[1].PodID != "z-pod" {
		t.Errorf("order = %v, want [a-pod z-pod]", []podregistry.PodID{out[0].PodID, out[1].PodID})
	}
}

// spec: §12.6 (filter by state narrows results)
func TestListPodsByPoolFiltersByState(t *testing.T) {
	r := newRegistry(t, "lenny-agents",
		seedSandbox("a-pod", "echo-pool", "idle"),
		seedSandbox("b-pod", "echo-pool", "claimed"))
	out, _ := r.ListPodsByPool(context.Background(), "echo-pool",
		podregistry.PodFilter{State: "idle"})
	if len(out) != 1 || out[0].PodID != "a-pod" {
		t.Errorf("filtered = %v, want [a-pod]", out)
	}
}

// spec: §12.6 (CountByState returns the §6.2 phase histogram)
func TestCountByStateReturnsHistogram(t *testing.T) {
	r := newRegistry(t, "lenny-agents",
		seedSandbox("a", "echo-pool", "idle"),
		seedSandbox("b", "echo-pool", "idle"),
		seedSandbox("c", "echo-pool", "claimed"))
	counts, err := r.CountByState(context.Background(), "echo-pool")
	if err != nil {
		t.Fatalf("CountByState: %v", err)
	}
	if counts["idle"] != 2 || counts["claimed"] != 1 {
		t.Errorf("counts = %v, want idle=2 claimed=1", counts)
	}
}

// spec: §12.6 (CreatePod writes a new Sandbox with the PoolLabel)
func TestCreatePodWritesNewSandbox(t *testing.T) {
	r := newRegistry(t, "lenny-agents")
	spec := podregistry.PodSpec{PoolID: "echo-pool", IsolationProfile: "sandboxed"}
	rec, err := r.CreatePod(context.Background(), "echo-pool", spec)
	if err != nil {
		t.Fatalf("CreatePod: %v", err)
	}
	if rec.PoolID != "echo-pool" {
		t.Errorf("PoolID = %q, want echo-pool", rec.PoolID)
	}
	if rec.State != "warming" {
		t.Errorf("State = %q, want warming", rec.State)
	}
	// The new pod is observable via Get and List.
	got, err := r.GetPod(context.Background(), rec.PodID)
	if err != nil {
		t.Fatalf("Get after Create: %v", err)
	}
	if got.PodID != rec.PodID {
		t.Errorf("created pod id %q does not match Get id %q", rec.PodID, got.PodID)
	}
	pods, _ := r.ListPodsByPool(context.Background(), "echo-pool", podregistry.PodFilter{})
	if len(pods) != 1 {
		t.Errorf("ListPodsByPool len = %d, want 1", len(pods))
	}
}

// spec: §12.6 (DeletePod removes the Sandbox)
func TestDeletePodRemovesSandbox(t *testing.T) {
	r := newRegistry(t, "lenny-agents", seedSandbox("alpha", "echo-pool", "idle"))
	if err := r.DeletePod(context.Background(), "alpha"); err != nil {
		t.Fatalf("DeletePod: %v", err)
	}
	if _, err := r.GetPod(context.Background(), "alpha"); !errors.Is(err, podregistry.ErrNotFound) {
		t.Errorf("Get after Delete = %v, want ErrNotFound", err)
	}
	if err := r.DeletePod(context.Background(), "missing"); !errors.Is(err, podregistry.ErrNotFound) {
		t.Errorf("Delete unknown = %v, want ErrNotFound", err)
	}
}

// spec: §12.6 line 482 (WatchPods MUST NOT emit an initial state
// snapshot; a pre-existing pod produces no event until it changes, at
// which point a delta — here Updated — is emitted).
func TestWatchPodsDoesNotEmitInitialSnapshot_spec_12_6_482(t *testing.T) {
	r := newRegistry(t, "lenny-agents", seedSandbox("alpha", "echo-pool", "idle"))
	r.SetWatchTuningForTest(10*time.Millisecond, 32)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, err := r.WatchPods(ctx, "echo-pool")
	if err != nil {
		t.Fatalf("WatchPods: %v", err)
	}
	// Many poll cycles elapse with the pod stable: the stream stays
	// silent because the consumer owns initial state via ListPodsByPool.
	select {
	case e := <-events:
		t.Fatalf("unexpected initial-snapshot event %+v; want none", e)
	case <-time.After(150 * time.Millisecond):
	}
	// A subsequent state change produces a delta (Updated, not Created).
	if err := r.UpdatePodState(ctx, "alpha",
		podregistry.StateTransition{From: "idle", To: "claimed"}); err != nil {
		t.Fatalf("UpdatePodState: %v", err)
	}
	select {
	case e := <-events:
		if e.EventType != podregistry.EventUpdated {
			t.Errorf("event type = %q, want updated", e.EventType)
		}
		if e.PodID != "alpha" {
			t.Errorf("event PodID = %q, want alpha", e.PodID)
		}
		if e.PodRecord.State != "claimed" {
			t.Errorf("event PodRecord.State = %q, want claimed", e.PodRecord.State)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no Updated event after state change")
	}
}

// spec: §12.6 line 482 (when the channel falls behind, WatchPods emits
// a synthetic resync frame carrying no PodRecord rather than blocking).
func TestWatchPodsEmitsResyncUnderBackpressure_spec_12_6_482(t *testing.T) {
	r := newRegistry(t, "lenny-agents",
		seedSandbox("alpha", "echo-pool", "idle"),
		seedSandbox("bravo", "echo-pool", "idle"))
	// A single-slot buffer plus a fast poll makes a non-draining consumer
	// fall behind within one reconcile: two simultaneous deltas cannot
	// both fit, so the loop coalesces and signals resync.
	r.SetWatchTuningForTest(5*time.Millisecond, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, err := r.WatchPods(ctx, "echo-pool")
	if err != nil {
		t.Fatalf("WatchPods: %v", err)
	}
	// Let the initial snapshot seed before introducing deltas, so the
	// transitions register as changes against the seeded idle state.
	time.Sleep(20 * time.Millisecond)
	for _, name := range []string{"alpha", "bravo"} {
		if err := r.UpdatePodState(ctx, podregistry.PodID(name),
			podregistry.StateTransition{From: "idle", To: "claimed"}); err != nil {
			t.Fatalf("UpdatePodState %s: %v", name, err)
		}
	}
	// Let the poll loop observe both changes and fall behind on the
	// single-slot buffer.
	time.Sleep(120 * time.Millisecond)
	// Draining the channel must surface a resync frame that carries no
	// PodRecord — the line 482 re-read signal.
	deadline := time.After(2 * time.Second)
	for {
		select {
		case e := <-events:
			if e.EventType != podregistry.EventResync {
				continue
			}
			if e.PodID != "" {
				t.Errorf("resync PodID = %q, want empty", e.PodID)
			}
			if e.PodRecord != (podregistry.PodRecord{}) {
				t.Errorf("resync PodRecord = %+v, want zero", e.PodRecord)
			}
			return
		case <-deadline:
			t.Fatal("no resync frame within 2s under backpressure")
		}
	}
}

// spec: §12.6 line 421 (toPodRecord projects ExecutionMode from the
// denormalized Sandbox spec and SessionID from the status).
func TestToPodRecordProjectsExecutionModeAndSessionID_spec_12_6_421(t *testing.T) {
	sb := seedSandbox("alpha", "echo-pool", "claimed")
	sb.Spec.ExecutionMode = "task"
	sb.Status.SessionID = "sess-9"
	r := newRegistry(t, "lenny-agents", sb)
	rec, err := r.GetPod(context.Background(), "alpha")
	if err != nil {
		t.Fatalf("GetPod: %v", err)
	}
	if rec.ExecutionMode != "task" {
		t.Errorf("ExecutionMode = %q, want task", rec.ExecutionMode)
	}
	if rec.SessionID != "sess-9" {
		t.Errorf("SessionID = %q, want sess-9", rec.SessionID)
	}
}

// spec: §12.6 line 422 (CreatePod stamps the per-pod PodSpec fields onto
// the Sandbox so they round-trip through toPodRecord).
func TestCreatePodStampsExecutionModeAndIsolation_spec_12_6_422(t *testing.T) {
	r := newRegistry(t, "lenny-agents")
	spec := podregistry.PodSpec{PoolID: "echo-pool", IsolationProfile: "microvm", ExecutionMode: "concurrent"}
	rec, err := r.CreatePod(context.Background(), "echo-pool", spec)
	if err != nil {
		t.Fatalf("CreatePod: %v", err)
	}
	if rec.IsolationProfile != "microvm" {
		t.Errorf("IsolationProfile = %q, want microvm", rec.IsolationProfile)
	}
	if rec.ExecutionMode != "concurrent" {
		t.Errorf("ExecutionMode = %q, want concurrent", rec.ExecutionMode)
	}
	got, _ := r.GetPod(context.Background(), rec.PodID)
	if got.ExecutionMode != "concurrent" || got.IsolationProfile != "microvm" {
		t.Errorf("persisted exec/iso = %q/%q, want concurrent/microvm", got.ExecutionMode, got.IsolationProfile)
	}
}

// spec: §12.6 line 422 — PodSpec carries RuntimeDefinitionRef,
// WorkspacePlan, and resource limits (ResourceClass), and CreatePod
// stamps them onto the Sandbox spec so a gateway-created pod expresses
// the runtime, workspace, and resource class it was created for rather
// than silently inheriting the pool template's defaults. RuntimeRef is
// a required CRD field, so a CreatePod that does not stamp it produces
// an invalid Sandbox.
func TestCreatePodStampsRuntimeWorkspaceAndResourceClass_spec_12_6_422(t *testing.T) {
	scheme := newScheme(t)
	cli := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&lennyv1.Sandbox{}).
		Build()
	r, err := podregistry.New(cli, "lenny-agents")
	if err != nil {
		t.Fatalf("podregistry.New: %v", err)
	}
	plan := []byte(`{"schemaVersion":1,"sources":[{"type":"gitClone","gitClone":{"url":"https://github.com/acme/repo"}}]}`)
	spec := podregistry.PodSpec{
		PoolID:               "claude-pool",
		RuntimeDefinitionRef: "claude-code",
		IsolationProfile:     "sandboxed",
		ExecutionMode:        "session",
		ResourceClass:        "large",
		WorkspacePlan:        plan,
	}
	rec, err := r.CreatePod(context.Background(), "claude-pool", spec)
	if err != nil {
		t.Fatalf("CreatePod: %v", err)
	}
	var sb lennyv1.Sandbox
	if err := cli.Get(context.Background(), client.ObjectKey{Namespace: "lenny-agents", Name: string(rec.PodID)}, &sb); err != nil {
		t.Fatalf("get created sandbox: %v", err)
	}
	if sb.Spec.RuntimeRef != "claude-code" {
		t.Errorf("RuntimeRef = %q, want claude-code", sb.Spec.RuntimeRef)
	}
	if sb.Spec.ResourceClass != "large" {
		t.Errorf("ResourceClass = %q, want large", sb.Spec.ResourceClass)
	}
	if sb.Spec.WorkspacePlan == nil {
		t.Fatal("WorkspacePlan was not stamped onto the Sandbox spec")
	}
	// The API server round-trips the preserved JSON and may reorder
	// object keys, so compare the decoded structure rather than the
	// byte string.
	var gotPlan, wantPlan map[string]any
	if err := json.Unmarshal(sb.Spec.WorkspacePlan.Raw, &gotPlan); err != nil {
		t.Fatalf("decode stamped WorkspacePlan: %v", err)
	}
	if err := json.Unmarshal(plan, &wantPlan); err != nil {
		t.Fatalf("decode want WorkspacePlan: %v", err)
	}
	if !reflect.DeepEqual(gotPlan, wantPlan) {
		t.Errorf("WorkspacePlan = %v, want %v", gotPlan, wantPlan)
	}
}

// spec: §12.6 line 422 — an empty WorkspacePlan (a warm pod, whose
// workspace is materialized at session claim) leaves the Sandbox spec
// field nil rather than writing an empty JSON blob the API server would
// reject.
func TestCreatePodLeavesWorkspacePlanNilWhenEmpty_spec_12_6_422(t *testing.T) {
	scheme := newScheme(t)
	cli := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&lennyv1.Sandbox{}).
		Build()
	r, err := podregistry.New(cli, "lenny-agents")
	if err != nil {
		t.Fatalf("podregistry.New: %v", err)
	}
	rec, err := r.CreatePod(context.Background(), "echo-pool", podregistry.PodSpec{
		PoolID:               "echo-pool",
		RuntimeDefinitionRef: "echo",
	})
	if err != nil {
		t.Fatalf("CreatePod: %v", err)
	}
	var sb lennyv1.Sandbox
	if err := cli.Get(context.Background(), client.ObjectKey{Namespace: "lenny-agents", Name: string(rec.PodID)}, &sb); err != nil {
		t.Fatalf("get created sandbox: %v", err)
	}
	if sb.Spec.WorkspacePlan != nil {
		t.Errorf("WorkspacePlan = %v, want nil for a warm pod", sb.Spec.WorkspacePlan)
	}
}
