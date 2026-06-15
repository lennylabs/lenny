// SPDX-License-Identifier: MIT

package podlifecycle_test

import (
	"context"
	"errors"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/podlifecycle"
)

func sandboxScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := lennyv1.AddToScheme(s); err != nil {
		t.Fatalf("add scheme: %v", err)
	}
	return s
}

func newClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	return fake.NewClientBuilder().
		WithScheme(sandboxScheme(t)).
		WithObjects(objs...).
		WithStatusSubresource(&lennyv1.Sandbox{}, &lennyv1.SandboxTemplate{}, &lennyv1.SandboxWarmPool{}, &lennyv1.SandboxClaim{}).
		Build()
}

// TestAgentSandboxPoolReader_GetPoolStatus_Spec4_6_1 confirms the
// PoolReader translator merges the SandboxTemplate, SandboxWarmPool,
// and observed Sandbox phase counts into one PoolStatus.
// spec: spec/04_system-components.md lines 335-338, 359.
func TestAgentSandboxPoolReader_GetPoolStatus_Spec4_6_1(t *testing.T) {
	tmpl := &lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "claude-prod", Namespace: "agents"},
		Spec: lennyv1.SandboxTemplateSpec{
			RuntimeRef:       "claude-prod",
			IsolationProfile: "sandboxed",
		},
	}
	swp := &lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: "claude-prod", Namespace: "agents"},
		Spec:       lennyv1.SandboxWarmPoolSpec{TemplateRef: "claude-prod", MinWarm: 3, MaxWarm: 10},
		Status:     lennyv1.SandboxWarmPoolStatus{WarmCount: 4},
	}
	idle := &lennyv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "p-idle", Namespace: "agents"},
		Spec:       lennyv1.SandboxSpec{RuntimeRef: "claude-prod", PoolRef: "claude-prod"},
		Status:     lennyv1.SandboxStatus{Phase: string(podlifecycle.PodStateIdle)},
	}
	claimed := &lennyv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "p-claimed", Namespace: "agents"},
		Spec:       lennyv1.SandboxSpec{RuntimeRef: "claude-prod", PoolRef: "claude-prod"},
		Status:     lennyv1.SandboxStatus{Phase: string(podlifecycle.PodStateClaimed)},
	}
	c := newClient(t, tmpl, swp, idle, claimed)
	r := &podlifecycle.AgentSandboxPoolReader{Client: c, Namespace: "agents"}

	got, err := r.GetPoolStatus(context.Background(), "claude-prod")
	if err != nil {
		t.Fatalf("GetPoolStatus: %v", err)
	}
	if got.MinWarm != 3 || got.MaxWarm != 10 {
		t.Errorf("min/max warm = %d/%d, want 3/10", got.MinWarm, got.MaxWarm)
	}
	if got.WarmCount != 4 {
		t.Errorf("warmCount = %d, want 4", got.WarmCount)
	}
	if got.IdleCount != 1 || got.ClaimedCount != 1 {
		t.Errorf("idle/claimed = %d/%d, want 1/1", got.IdleCount, got.ClaimedCount)
	}
	if got.IsolationProfile != "sandboxed" {
		t.Errorf("isolationProfile = %q, want sandboxed", got.IsolationProfile)
	}
}

// TestAgentSandboxPoolReader_GetPoolStatus_NotFound surfaces the
// sentinel error on a missing SandboxTemplate.
// spec: spec/04_system-components.md line 338.
func TestAgentSandboxPoolReader_GetPoolStatus_NotFound(t *testing.T) {
	c := newClient(t)
	r := &podlifecycle.AgentSandboxPoolReader{Client: c, Namespace: "agents"}
	if _, err := r.GetPoolStatus(context.Background(), "missing"); !errors.Is(err, podlifecycle.ErrPoolNotFound) {
		t.Errorf("GetPoolStatus on missing pool = %v, want ErrPoolNotFound", err)
	}
}

// TestAgentSandboxPodLifecycleManager_ClaimPod_CreatesPerPodClaim
// confirms ClaimPod selects an idle Sandbox and creates the §4.6.1 per-pod
// occupancy SandboxClaim (`claim-<podName>`) with a `bound` binding state,
// without writing Sandbox.status — the WarmPoolController projects the
// claimed phase from the claim.
// spec: spec/04_system-components.md lines 342, 386, 388 (occupancy
// projection); §4.6.3 (gateway is not a Sandbox.status writer).
func TestAgentSandboxPodLifecycleManager_ClaimPod_CreatesPerPodClaim(t *testing.T) {
	tmpl := &lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: "agents"},
	}
	idle := &lennyv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "pod-1", Namespace: "agents"},
		Spec:       lennyv1.SandboxSpec{PoolRef: "p1"},
		Status:     lennyv1.SandboxStatus{Phase: string(podlifecycle.PodStateIdle)},
	}
	c := newClient(t, tmpl, idle)
	m := &podlifecycle.AgentSandboxPodLifecycleManager{
		AgentSandboxPoolReader: podlifecycle.AgentSandboxPoolReader{Client: c, Namespace: "agents"},
	}

	handle, err := m.ClaimPod(context.Background(), "p1", "sess-1", podlifecycle.ClaimOpts{})
	if err != nil {
		t.Fatalf("ClaimPod: %v", err)
	}
	if handle.SandboxName != "pod-1" || handle.SessionID != "sess-1" {
		t.Errorf("handle = %+v", handle)
	}

	// The per-pod claim exists and is bound.
	var claim lennyv1.SandboxClaim
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: "agents", Name: "claim-pod-1"}, &claim); err != nil {
		t.Fatalf("get per-pod claim: %v", err)
	}
	if claim.Spec.SandboxRef != "pod-1" {
		t.Errorf("claim SandboxRef = %q, want pod-1", claim.Spec.SandboxRef)
	}
	if claim.Status.Phase != "bound" {
		t.Errorf("claim binding state = %q, want bound", claim.Status.Phase)
	}

	// Sandbox.status is left untouched: the gateway is not a Sandbox.status
	// writer; the WarmPoolController projects the claimed phase.
	var got lennyv1.Sandbox
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: "agents", Name: "pod-1"}, &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.Phase != string(podlifecycle.PodStateIdle) {
		t.Errorf("Sandbox.status.phase = %q, want idle (unwritten by the gateway)", got.Status.Phase)
	}
	if _, ok := got.Annotations["lenny.dev/session-id"]; ok {
		t.Errorf("per-session annotation set: %v, want none (claim is per-pod)", got.Annotations)
	}
}

// TestAgentSandboxPodLifecycleManager_ClaimPod_ConflictsOnExistingClaim
// confirms a second claim attempt against a pod that already has a per-pod
// claim surfaces ErrClaimConflict so the caller retries with a fresh pod.
// spec: spec/04_system-components.md line 386 (AlreadyExists retry).
func TestAgentSandboxPodLifecycleManager_ClaimPod_ConflictsOnExistingClaim(t *testing.T) {
	tmpl := &lennyv1.SandboxTemplate{ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: "agents"}}
	idle := &lennyv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "pod-1", Namespace: "agents"},
		Spec:       lennyv1.SandboxSpec{PoolRef: "p1"},
		Status:     lennyv1.SandboxStatus{Phase: string(podlifecycle.PodStateIdle)},
	}
	existing := &lennyv1.SandboxClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "claim-pod-1", Namespace: "agents"},
		Spec:       lennyv1.SandboxClaimSpec{SandboxRef: "pod-1"},
	}
	c := newClient(t, tmpl, idle, existing)
	m := &podlifecycle.AgentSandboxPodLifecycleManager{
		AgentSandboxPoolReader: podlifecycle.AgentSandboxPoolReader{Client: c, Namespace: "agents"},
	}
	if _, err := m.ClaimPod(context.Background(), "p1", "sess-1", podlifecycle.ClaimOpts{}); !errors.Is(err, podlifecycle.ErrClaimConflict) {
		t.Errorf("ClaimPod with existing claim = %v, want ErrClaimConflict", err)
	}
}

// TestAgentSandboxPodLifecycleManager_ReleasePod_DeletesClaim confirms
// ReleasePod deletes the per-pod claim; the WarmPoolController returns the
// pod to idle as a projection of the claim's absence.
// spec: spec/04_system-components.md line 386 (claim deleted at release).
func TestAgentSandboxPodLifecycleManager_ReleasePod_DeletesClaim(t *testing.T) {
	claim := &lennyv1.SandboxClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "claim-pod-1", Namespace: "agents"},
		Spec:       lennyv1.SandboxClaimSpec{SandboxRef: "pod-1"},
		Status:     lennyv1.SandboxClaimStatus{Phase: "bound"},
	}
	c := newClient(t, claim)
	m := &podlifecycle.AgentSandboxPodLifecycleManager{
		AgentSandboxPoolReader: podlifecycle.AgentSandboxPoolReader{Client: c, Namespace: "agents"},
	}
	if err := m.ReleasePod(context.Background(), podlifecycle.PodHandle{SandboxName: "pod-1", Namespace: "agents"}); err != nil {
		t.Fatalf("ReleasePod: %v", err)
	}
	var got lennyv1.SandboxClaim
	err := c.Get(context.Background(), client.ObjectKey{Namespace: "agents", Name: "claim-pod-1"}, &got)
	if !apierrors.IsNotFound(err) {
		t.Errorf("claim still present after release: err = %v, want NotFound", err)
	}
}

// TestAgentSandboxPodLifecycleManager_ClaimPod_NoIdlePod surfaces
// ErrPodNotIdle when the pool has nothing assignable.
func TestAgentSandboxPodLifecycleManager_ClaimPod_NoIdlePod(t *testing.T) {
	tmpl := &lennyv1.SandboxTemplate{ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: "agents"}}
	claimed := &lennyv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "pod-1", Namespace: "agents"},
		Spec:       lennyv1.SandboxSpec{PoolRef: "p1"},
		Status:     lennyv1.SandboxStatus{Phase: string(podlifecycle.PodStateClaimed)},
	}
	c := newClient(t, tmpl, claimed)
	m := &podlifecycle.AgentSandboxPodLifecycleManager{
		AgentSandboxPoolReader: podlifecycle.AgentSandboxPoolReader{Client: c, Namespace: "agents"},
	}
	if _, err := m.ClaimPod(context.Background(), "p1", "sess-1", podlifecycle.ClaimOpts{}); !errors.Is(err, podlifecycle.ErrPodNotIdle) {
		t.Errorf("ClaimPod on empty pool = %v, want ErrPodNotIdle", err)
	}
}

// TestAgentSandboxPodLifecycleManager_ReleasePod_IdempotentForMissing
// confirms ReleasePod on a deleted Sandbox is a no-op.
func TestAgentSandboxPodLifecycleManager_ReleasePod_IdempotentForMissing(t *testing.T) {
	c := newClient(t)
	m := &podlifecycle.AgentSandboxPodLifecycleManager{
		AgentSandboxPoolReader: podlifecycle.AgentSandboxPoolReader{Client: c, Namespace: "agents"},
	}
	if err := m.ReleasePod(context.Background(), podlifecycle.PodHandle{SandboxName: "ghost", Namespace: "agents"}); err != nil {
		t.Errorf("ReleasePod on missing pod = %v, want nil", err)
	}
}

// TestAgentSandboxPodLifecycleManager_DrainPod_AnnotatesCheckpointFirst
// confirms a checkpointFirst drain sets the §7.1 seal-and-export
// annotation alongside the draining phase.
// spec: spec/04_system-components.md line 344.
func TestAgentSandboxPodLifecycleManager_DrainPod_AnnotatesCheckpointFirst(t *testing.T) {
	pod := &lennyv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "pod-1", Namespace: "agents"},
		Status:     lennyv1.SandboxStatus{Phase: string(podlifecycle.PodStateClaimed)},
	}
	c := newClient(t, pod)
	m := &podlifecycle.AgentSandboxPodLifecycleManager{
		AgentSandboxPoolReader: podlifecycle.AgentSandboxPoolReader{Client: c, Namespace: "agents"},
	}
	res, err := m.DrainPod(context.Background(), podlifecycle.PodHandle{SandboxName: "pod-1", Namespace: "agents"}, true)
	if err != nil {
		t.Fatalf("DrainPod: %v", err)
	}
	if !res.TornDown {
		t.Errorf("DrainResult.TornDown = false, want true")
	}
	var got lennyv1.Sandbox
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: "agents", Name: "pod-1"}, &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.Phase != string(podlifecycle.PodStateDraining) {
		t.Errorf("phase = %q, want draining", got.Status.Phase)
	}
	if got.Annotations["lenny.dev/drain-checkpoint-first"] != "true" {
		t.Errorf("checkpoint-first annotation not set: %v", got.Annotations)
	}
}

// TestAgentSandboxPoolManager_ReconcilePool_CreatesTemplateAndWarmPool
// confirms ReconcilePool creates both CRDs on first apply.
// spec: spec/04_system-components.md line 349.
func TestAgentSandboxPoolManager_ReconcilePool_CreatesTemplateAndWarmPool(t *testing.T) {
	c := newClient(t)
	m := &podlifecycle.AgentSandboxPoolManager{
		AgentSandboxPoolReader: podlifecycle.AgentSandboxPoolReader{Client: c, Namespace: "agents"},
	}
	cfg := podlifecycle.PoolConfig{
		Name: "p1", Namespace: "agents",
		RuntimeRef: "claude-prod",
		MinWarm:    2, MaxWarm: 8,
		IsolationProfile: "sandboxed",
	}
	if err := m.ReconcilePool(context.Background(), cfg); err != nil {
		t.Fatalf("ReconcilePool: %v", err)
	}
	var tmpl lennyv1.SandboxTemplate
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: "agents", Name: "p1"}, &tmpl); err != nil {
		t.Fatalf("template not created: %v", err)
	}
	if tmpl.Spec.RuntimeRef != "claude-prod" || tmpl.Spec.IsolationProfile != "sandboxed" {
		t.Errorf("template spec = %+v", tmpl.Spec)
	}
	var swp lennyv1.SandboxWarmPool
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: "agents", Name: "p1"}, &swp); err != nil {
		t.Fatalf("SandboxWarmPool not created: %v", err)
	}
	if swp.Spec.MinWarm != 2 || swp.Spec.MaxWarm != 8 || swp.Spec.TemplateRef != "p1" {
		t.Errorf("SWP spec = %+v", swp.Spec)
	}
}

// TestAgentSandboxPoolManager_ApplyPoolDefinition_DeleteTearsTemplate
// confirms Deleted: true removes the SandboxTemplate.
// spec: spec/04_system-components.md line 350.
func TestAgentSandboxPoolManager_ApplyPoolDefinition_DeleteTearsTemplate(t *testing.T) {
	tmpl := &lennyv1.SandboxTemplate{ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: "agents"}}
	c := newClient(t, tmpl)
	m := &podlifecycle.AgentSandboxPoolManager{
		AgentSandboxPoolReader: podlifecycle.AgentSandboxPoolReader{Client: c, Namespace: "agents"},
	}
	def := podlifecycle.PoolDefinition{
		Spec:    podlifecycle.PoolConfig{Name: "p1", Namespace: "agents"},
		Deleted: true,
	}
	if err := m.ApplyPoolDefinition(context.Background(), def); err != nil {
		t.Fatalf("ApplyPoolDefinition delete: %v", err)
	}
	var got lennyv1.SandboxTemplate
	err := c.Get(context.Background(), client.ObjectKey{Namespace: "agents", Name: "p1"}, &got)
	if err == nil {
		t.Errorf("template still exists after delete: %+v", got)
	}
}

// TestAgentSandboxPoolManager_GarbageCollect_DetectsOrphanedSandbox
// confirms the GC sweep flags a Sandbox whose pool has no template.
// spec: spec/04_system-components.md line 353.
func TestAgentSandboxPoolManager_GarbageCollect_DetectsOrphanedSandbox(t *testing.T) {
	orphan := &lennyv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "pod-orphan", Namespace: "agents"},
		Spec:       lennyv1.SandboxSpec{PoolRef: "vanished"},
		Status:     lennyv1.SandboxStatus{Phase: string(podlifecycle.PodStateIdle)},
	}
	c := newClient(t, orphan)
	m := &podlifecycle.AgentSandboxPoolManager{
		AgentSandboxPoolReader: podlifecycle.AgentSandboxPoolReader{Client: c, Namespace: "agents"},
	}
	got, err := m.GarbageCollect(context.Background())
	if err != nil {
		t.Fatalf("GarbageCollect: %v", err)
	}
	if len(got) != 1 || got[0].Kind != "Sandbox" || got[0].Name != "pod-orphan" {
		t.Errorf("orphans = %+v", got)
	}
}

// TestAgentSandboxPoolManager_TransitionPodState_RejectsIllegalEdge
// confirms ErrInvalidTransition fires for a phase pair not in the
// §6.2 state-machine.
// spec: spec/04_system-components.md line 352.
func TestAgentSandboxPoolManager_TransitionPodState_RejectsIllegalEdge(t *testing.T) {
	pod := &lennyv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: "agents"},
		Status:     lennyv1.SandboxStatus{Phase: string(podlifecycle.PodStateIdle)},
	}
	c := newClient(t, pod)
	m := &podlifecycle.AgentSandboxPoolManager{
		AgentSandboxPoolReader: podlifecycle.AgentSandboxPoolReader{Client: c, Namespace: "agents"},
	}
	err := m.TransitionPodState(context.Background(),
		podlifecycle.PodHandle{SandboxName: "p1", Namespace: "agents"},
		podlifecycle.PodStateIdle, podlifecycle.PodStateReserved) // not in the idle→{claimed,draining} allow set
	if !errors.Is(err, podlifecycle.ErrInvalidTransition) {
		t.Errorf("TransitionPodState illegal edge = %v, want ErrInvalidTransition", err)
	}
}

// TestAgentSandboxPoolManager_ManageFinalizer_AddsAndRemoves
// covers the §4.6.1 lenny.dev/session-cleanup add/remove flow.
// spec: spec/04_system-components.md line 354.
func TestAgentSandboxPoolManager_ManageFinalizer_AddsAndRemoves(t *testing.T) {
	pod := &lennyv1.Sandbox{ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: "agents"}}
	c := newClient(t, pod)
	m := &podlifecycle.AgentSandboxPoolManager{
		AgentSandboxPoolReader: podlifecycle.AgentSandboxPoolReader{Client: c, Namespace: "agents"},
	}
	h := podlifecycle.PodHandle{SandboxName: "p1", Namespace: "agents"}
	if err := m.ManageFinalizer(context.Background(), h, podlifecycle.FinalizerAdd); err != nil {
		t.Fatalf("ManageFinalizer add: %v", err)
	}
	var got lennyv1.Sandbox
	_ = c.Get(context.Background(), client.ObjectKey{Namespace: "agents", Name: "p1"}, &got)
	if len(got.Finalizers) != 1 || got.Finalizers[0] != lennyv1.FinalizerSessionCleanup {
		t.Errorf("finalizers after add = %v", got.Finalizers)
	}
	if err := m.ManageFinalizer(context.Background(), h, podlifecycle.FinalizerRemove); err != nil {
		t.Fatalf("ManageFinalizer remove: %v", err)
	}
	_ = c.Get(context.Background(), client.ObjectKey{Namespace: "agents", Name: "p1"}, &got)
	if len(got.Finalizers) != 0 {
		t.Errorf("finalizers after remove = %v", got.Finalizers)
	}
}
