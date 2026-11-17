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

func agentPodScheme(t *testing.T) *runtime.Scheme {
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
		WithScheme(agentPodScheme(t)).
		WithObjects(objs...).
		WithStatusSubresource(&lennyv1.AgentPod{}, &lennyv1.PodSet{}, &lennyv1.PodSet{}, &lennyv1.PodClaim{}).
		Build()
}

// TestCRDPoolReader_GetPoolStatus confirms the
// PoolReader translator merges the PodSet,
// and observed AgentPod phase counts into one PoolStatus.
// spec: §4.6.1.
func TestCRDPoolReader_GetPoolStatus(t *testing.T) {
	tmpl := &lennyv1.PodSet{
		ObjectMeta: metav1.ObjectMeta{Name: "claude-prod", Namespace: "agents"},
		Spec: lennyv1.PodSetSpec{
			RuntimeRef:       "claude-prod",
			IsolationProfile: "sandboxed",
		},
	}
	swp := &lennyv1.PodSet{
		ObjectMeta: metav1.ObjectMeta{Name: "claude-prod", Namespace: "agents"},
		Spec:       lennyv1.PodSetSpec{TemplateRef: "claude-prod", MinWarm: 3, MaxWarm: 10},
		Status:     lennyv1.PodSetStatus{WarmCount: 4},
	}
	idle := &lennyv1.AgentPod{
		ObjectMeta: metav1.ObjectMeta{Name: "p-idle", Namespace: "agents"},
		Spec:       lennyv1.AgentPodSpec{RuntimeRef: "claude-prod", PodSetRef: "claude-prod"},
		Status:     lennyv1.AgentPodStatus{Phase: string(podlifecycle.PodStateIdle)},
	}
	claimed := &lennyv1.AgentPod{
		ObjectMeta: metav1.ObjectMeta{Name: "p-claimed", Namespace: "agents"},
		Spec:       lennyv1.AgentPodSpec{RuntimeRef: "claude-prod", PodSetRef: "claude-prod"},
		Status:     lennyv1.AgentPodStatus{Phase: string(podlifecycle.PodStateClaimed)},
	}
	c := newClient(t, tmpl, swp, idle, claimed)
	r := &podlifecycle.CRDPoolReader{Client: c, Namespace: "agents"}

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

// TestCRDPoolReader_GetPoolStatus_NotFound surfaces the
// sentinel error on a missing PodSet.
// spec: §4.6.1.
func TestCRDPoolReader_GetPoolStatus_NotFound(t *testing.T) {
	c := newClient(t)
	r := &podlifecycle.CRDPoolReader{Client: c, Namespace: "agents"}
	if _, err := r.GetPoolStatus(context.Background(), "missing"); !errors.Is(err, podlifecycle.ErrPoolNotFound) {
		t.Errorf("GetPoolStatus on missing pool = %v, want ErrPoolNotFound", err)
	}
}

// TestCRDPodLifecycleManager_ClaimPod_CreatesPerPodClaim
// confirms ClaimPod selects an idle AgentPod and creates the §4.6.1 per-pod
// occupancy PodClaim (`claim-<podName>`) with a `bound` binding state,
// without writing AgentPod.status — the WarmPoolController projects the
// claimed phase from the claim.
// spec: §4.6.1; §4.6.3 (gateway is not a AgentPod.status writer).
func TestCRDPodLifecycleManager_ClaimPod_CreatesPerPodClaim(t *testing.T) {
	tmpl := &lennyv1.PodSet{
		ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: "agents"},
	}
	idle := &lennyv1.AgentPod{
		ObjectMeta: metav1.ObjectMeta{Name: "pod-1", Namespace: "agents"},
		Spec:       lennyv1.AgentPodSpec{PodSetRef: "p1"},
		Status:     lennyv1.AgentPodStatus{Phase: string(podlifecycle.PodStateIdle)},
	}
	c := newClient(t, tmpl, idle)
	m := &podlifecycle.CRDPodLifecycleManager{
		CRDPoolReader: podlifecycle.CRDPoolReader{Client: c, Namespace: "agents"},
	}

	handle, err := m.ClaimPod(context.Background(), "p1", "sess-1", podlifecycle.ClaimOpts{TenantID: "acme"})
	if err != nil {
		t.Fatalf("ClaimPod: %v", err)
	}
	if handle.AgentPodName != "pod-1" || handle.SessionID != "sess-1" {
		t.Errorf("handle = %+v", handle)
	}

	// The per-pod claim exists and is bound.
	var claim lennyv1.PodClaim
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: "agents", Name: "claim-pod-1"}, &claim); err != nil {
		t.Fatalf("get per-pod claim: %v", err)
	}
	if claim.Spec.AgentPodRef != "pod-1" {
		t.Errorf("claim AgentPodRef = %q, want pod-1", claim.Spec.AgentPodRef)
	}
	// The claim's spec carries agentPodRef and tenantId (§4.6.1
	// claim-spec contract); the tenant from ClaimOpts is stamped on the claim.
	if claim.Spec.TenantID != "acme" {
		t.Errorf("claim TenantID = %q, want acme (claim-spec tenant pin)", claim.Spec.TenantID)
	}
	if claim.Status.Phase != "bound" {
		t.Errorf("claim binding state = %q, want bound", claim.Status.Phase)
	}
	// spec: §4.6.1 — the `bound` write stamps the binding-state-transition
	// time the orphan GC keys its live-binding-state reclaim on.
	if claim.Status.BindingStateTransitionTime == nil {
		t.Error("claim BindingStateTransitionTime = nil, want a stamp on the bound write")
	}

	// AgentPod.status is left untouched: the gateway is not a AgentPod.status
	// writer; the WarmPoolController projects the claimed phase.
	var got lennyv1.AgentPod
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: "agents", Name: "pod-1"}, &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.Phase != string(podlifecycle.PodStateIdle) {
		t.Errorf("AgentPod.status.phase = %q, want idle (unwritten by the gateway)", got.Status.Phase)
	}
	if _, ok := got.Annotations["lenny.dev/session-id"]; ok {
		t.Errorf("per-session annotation set: %v, want none (claim is per-pod)", got.Annotations)
	}
}

// TestCRDPodLifecycleManager_ClaimPod_ConflictsOnExistingClaim
// confirms a second claim attempt against a pod that already has a per-pod
// claim surfaces ErrClaimConflict so the caller retries with a fresh pod.
// spec: §4.6.1.
func TestCRDPodLifecycleManager_ClaimPod_ConflictsOnExistingClaim(t *testing.T) {
	tmpl := &lennyv1.PodSet{ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: "agents"}}
	idle := &lennyv1.AgentPod{
		ObjectMeta: metav1.ObjectMeta{Name: "pod-1", Namespace: "agents"},
		Spec:       lennyv1.AgentPodSpec{PodSetRef: "p1"},
		Status:     lennyv1.AgentPodStatus{Phase: string(podlifecycle.PodStateIdle)},
	}
	existing := &lennyv1.PodClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "claim-pod-1", Namespace: "agents"},
		Spec:       lennyv1.PodClaimSpec{AgentPodRef: "pod-1"},
	}
	c := newClient(t, tmpl, idle, existing)
	m := &podlifecycle.CRDPodLifecycleManager{
		CRDPoolReader: podlifecycle.CRDPoolReader{Client: c, Namespace: "agents"},
	}
	if _, err := m.ClaimPod(context.Background(), "p1", "sess-1", podlifecycle.ClaimOpts{}); !errors.Is(err, podlifecycle.ErrClaimConflict) {
		t.Errorf("ClaimPod with existing claim = %v, want ErrClaimConflict", err)
	}
}

// TestCRDPodLifecycleManager_ReleasePod_DeletesClaim confirms
// ReleasePod deletes the per-pod claim.
// spec: §4.6.1.
func TestCRDPodLifecycleManager_ReleasePod_DeletesClaim(t *testing.T) {
	claim := &lennyv1.PodClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "claim-pod-1", Namespace: "agents"},
		Spec:       lennyv1.PodClaimSpec{AgentPodRef: "pod-1"},
		Status:     lennyv1.PodClaimStatus{Phase: "bound"},
	}
	c := newClient(t, claim)
	m := &podlifecycle.CRDPodLifecycleManager{
		CRDPoolReader: podlifecycle.CRDPoolReader{Client: c, Namespace: "agents"},
	}
	if err := m.ReleasePod(context.Background(), podlifecycle.PodHandle{AgentPodName: "pod-1", Namespace: "agents"}); err != nil {
		t.Fatalf("ReleasePod: %v", err)
	}
	var got lennyv1.PodClaim
	err := c.Get(context.Background(), client.ObjectKey{Namespace: "agents", Name: "claim-pod-1"}, &got)
	if !apierrors.IsNotFound(err) {
		t.Errorf("claim still present after release: err = %v, want NotFound", err)
	}
}

// TestCRDPodLifecycleManager_ClaimPod_NoIdlePod surfaces
// ErrPodNotIdle when the pool has nothing assignable.
func TestCRDPodLifecycleManager_ClaimPod_NoIdlePod(t *testing.T) {
	tmpl := &lennyv1.PodSet{ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: "agents"}}
	claimed := &lennyv1.AgentPod{
		ObjectMeta: metav1.ObjectMeta{Name: "pod-1", Namespace: "agents"},
		Spec:       lennyv1.AgentPodSpec{PodSetRef: "p1"},
		Status:     lennyv1.AgentPodStatus{Phase: string(podlifecycle.PodStateClaimed)},
	}
	c := newClient(t, tmpl, claimed)
	m := &podlifecycle.CRDPodLifecycleManager{
		CRDPoolReader: podlifecycle.CRDPoolReader{Client: c, Namespace: "agents"},
	}
	if _, err := m.ClaimPod(context.Background(), "p1", "sess-1", podlifecycle.ClaimOpts{}); !errors.Is(err, podlifecycle.ErrPodNotIdle) {
		t.Errorf("ClaimPod on empty pool = %v, want ErrPodNotIdle", err)
	}
}

// TestCRDPodLifecycleManager_ReleasePod_IdempotentForMissing
// confirms ReleasePod on a deleted AgentPod is a no-op.
func TestCRDPodLifecycleManager_ReleasePod_IdempotentForMissing(t *testing.T) {
	c := newClient(t)
	m := &podlifecycle.CRDPodLifecycleManager{
		CRDPoolReader: podlifecycle.CRDPoolReader{Client: c, Namespace: "agents"},
	}
	if err := m.ReleasePod(context.Background(), podlifecycle.PodHandle{AgentPodName: "ghost", Namespace: "agents"}); err != nil {
		t.Errorf("ReleasePod on missing pod = %v, want nil", err)
	}
}

// TestCRDPodLifecycleManager_DrainPod_AnnotatesCheckpointFirst
// confirms a checkpointFirst drain sets the §7.1 seal-and-export
// annotation alongside the draining phase.
// spec: §4.6.1.
func TestCRDPodLifecycleManager_DrainPod_AnnotatesCheckpointFirst(t *testing.T) {
	pod := &lennyv1.AgentPod{
		ObjectMeta: metav1.ObjectMeta{Name: "pod-1", Namespace: "agents"},
		Status:     lennyv1.AgentPodStatus{Phase: string(podlifecycle.PodStateClaimed)},
	}
	c := newClient(t, pod)
	m := &podlifecycle.CRDPodLifecycleManager{
		CRDPoolReader: podlifecycle.CRDPoolReader{Client: c, Namespace: "agents"},
	}
	res, err := m.DrainPod(context.Background(), podlifecycle.PodHandle{AgentPodName: "pod-1", Namespace: "agents"}, true)
	if err != nil {
		t.Fatalf("DrainPod: %v", err)
	}
	if !res.TornDown {
		t.Errorf("DrainResult.TornDown = false, want true")
	}
	var got lennyv1.AgentPod
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

// TestCRDPoolManager_ReconcilePool_CreatesTemplateAndWarmPool
// confirms ReconcilePool creates both CRDs on first apply.
// spec: §4.6.1.
func TestCRDPoolManager_ReconcilePool_CreatesTemplateAndWarmPool(t *testing.T) {
	c := newClient(t)
	m := &podlifecycle.CRDPoolManager{
		CRDPoolReader: podlifecycle.CRDPoolReader{Client: c, Namespace: "agents"},
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
	var tmpl lennyv1.PodSet
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: "agents", Name: "p1"}, &tmpl); err != nil {
		t.Fatalf("template not created: %v", err)
	}
	if tmpl.Spec.RuntimeRef != "claude-prod" || tmpl.Spec.IsolationProfile != "sandboxed" {
		t.Errorf("template spec = %+v", tmpl.Spec)
	}
	var swp lennyv1.PodSet
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: "agents", Name: "p1"}, &swp); err != nil {
		t.Fatalf("PodSet not created: %v", err)
	}
	if swp.Spec.MinWarm != 2 || swp.Spec.MaxWarm != 8 || swp.Spec.TemplateRef != "p1" {
		t.Errorf("SWP spec = %+v", swp.Spec)
	}
}

// TestCRDPoolManager_ApplyPoolDefinition_DeleteTearsTemplate
// confirms Deleted: true removes the PodSet.
// spec: §4.6.1.
func TestCRDPoolManager_ApplyPoolDefinition_DeleteTearsTemplate(t *testing.T) {
	tmpl := &lennyv1.PodSet{ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: "agents"}}
	c := newClient(t, tmpl)
	m := &podlifecycle.CRDPoolManager{
		CRDPoolReader: podlifecycle.CRDPoolReader{Client: c, Namespace: "agents"},
	}
	def := podlifecycle.PoolDefinition{
		Spec:    podlifecycle.PoolConfig{Name: "p1", Namespace: "agents"},
		Deleted: true,
	}
	if err := m.ApplyPoolDefinition(context.Background(), def); err != nil {
		t.Fatalf("ApplyPoolDefinition delete: %v", err)
	}
	var got lennyv1.PodSet
	err := c.Get(context.Background(), client.ObjectKey{Namespace: "agents", Name: "p1"}, &got)
	if err == nil {
		t.Errorf("template still exists after delete: %+v", got)
	}
}

// TestCRDPoolManager_GarbageCollect_DetectsOrphanedAgentPod
// confirms the GC sweep flags a AgentPod whose pool has no template.
// spec: §4.6.1.
func TestCRDPoolManager_GarbageCollect_DetectsOrphanedAgentPod(t *testing.T) {
	orphan := &lennyv1.AgentPod{
		ObjectMeta: metav1.ObjectMeta{Name: "pod-orphan", Namespace: "agents"},
		Spec:       lennyv1.AgentPodSpec{PodSetRef: "vanished"},
		Status:     lennyv1.AgentPodStatus{Phase: string(podlifecycle.PodStateIdle)},
	}
	c := newClient(t, orphan)
	m := &podlifecycle.CRDPoolManager{
		CRDPoolReader: podlifecycle.CRDPoolReader{Client: c, Namespace: "agents"},
	}
	got, err := m.GarbageCollect(context.Background())
	if err != nil {
		t.Fatalf("GarbageCollect: %v", err)
	}
	if len(got) != 1 || got[0].Kind != "AgentPod" || got[0].Name != "pod-orphan" {
		t.Errorf("orphans = %+v", got)
	}
}

// TestCRDPoolManager_TransitionPodState_RejectsIllegalEdge
// confirms ErrInvalidTransition fires for a phase pair not in the
// §6.2 state-machine.
// spec: §4.6.1.
func TestCRDPoolManager_TransitionPodState_RejectsIllegalEdge(t *testing.T) {
	pod := &lennyv1.AgentPod{
		ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: "agents"},
		Status:     lennyv1.AgentPodStatus{Phase: string(podlifecycle.PodStateIdle)},
	}
	c := newClient(t, pod)
	m := &podlifecycle.CRDPoolManager{
		CRDPoolReader: podlifecycle.CRDPoolReader{Client: c, Namespace: "agents"},
	}
	err := m.TransitionPodState(context.Background(),
		podlifecycle.PodHandle{AgentPodName: "p1", Namespace: "agents"},
		podlifecycle.PodStateIdle, podlifecycle.PodStateReserved) // not in the idle→{claimed,draining} allow set
	if !errors.Is(err, podlifecycle.ErrInvalidTransition) {
		t.Errorf("TransitionPodState illegal edge = %v, want ErrInvalidTransition", err)
	}
}

// TestCRDPoolManager_ManageFinalizer_AddsAndRemoves
// covers the §4.6.1 lenny.dev/session-cleanup add/remove flow.
// spec: §4.6.1.
func TestCRDPoolManager_ManageFinalizer_AddsAndRemoves(t *testing.T) {
	pod := &lennyv1.AgentPod{ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: "agents"}}
	c := newClient(t, pod)
	m := &podlifecycle.CRDPoolManager{
		CRDPoolReader: podlifecycle.CRDPoolReader{Client: c, Namespace: "agents"},
	}
	h := podlifecycle.PodHandle{AgentPodName: "p1", Namespace: "agents"}
	if err := m.ManageFinalizer(context.Background(), h, podlifecycle.FinalizerAdd); err != nil {
		t.Fatalf("ManageFinalizer add: %v", err)
	}
	var got lennyv1.AgentPod
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
