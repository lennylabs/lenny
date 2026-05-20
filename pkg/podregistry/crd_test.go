// SPDX-License-Identifier: MIT

package podregistry_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
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
	scheme := newScheme(t)
	cli := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(seed...).
		WithStatusSubresource(&lennyv1.Sandbox{}).
		Build()
	r, err := podregistry.New(cli, namespace)
	if err != nil {
		t.Fatalf("podregistry.New: %v", err)
	}
	return r
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

// spec: §4.6.1 (ClaimPod picks an idle pod from the pool)
func TestClaimPodPicksIdleAndPins(t *testing.T) {
	r := newRegistry(t, "lenny-agents",
		seedSandbox("alpha", "echo-pool", "claimed"),
		seedSandbox("bravo", "echo-pool", "idle"))
	rec, err := r.ClaimPod(context.Background(),
		podregistry.ClaimOpts{PoolID: "echo-pool", TenantID: "acme", SessionID: "s1"})
	if err != nil {
		t.Fatalf("ClaimPod: %v", err)
	}
	if rec.PodID != "bravo" {
		t.Errorf("PodID = %q, want bravo (the idle one)", rec.PodID)
	}
	if rec.State != "claimed" {
		t.Errorf("State = %q, want claimed", rec.State)
	}
	if rec.TenantID != "acme" {
		t.Errorf("TenantID = %q, want acme (pinned)", rec.TenantID)
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

// spec: §12.6 (ReleasePod transitions to task_cleanup on completed)
func TestReleasePodTransitionsByReason(t *testing.T) {
	r := newRegistry(t, "lenny-agents",
		seedSandbox("alpha", "echo-pool", "claimed"))
	if err := r.ReleasePod(context.Background(), "alpha", podregistry.ReleaseCompleted); err != nil {
		t.Fatalf("ReleasePod: %v", err)
	}
	rec, _ := r.GetPod(context.Background(), "alpha")
	if rec.State != "task_cleanup" {
		t.Errorf("State = %q, want task_cleanup", rec.State)
	}
	if rec.TenantID != "" {
		t.Errorf("TenantID = %q, want cleared", rec.TenantID)
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

// spec: §12.6 (WatchPods emits a created event for an existing pod)
func TestWatchPodsEmitsCreatedForExistingPod(t *testing.T) {
	r := newRegistry(t, "lenny-agents", seedSandbox("alpha", "echo-pool", "idle"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, err := r.WatchPods(ctx, "echo-pool")
	if err != nil {
		t.Fatalf("WatchPods: %v", err)
	}
	select {
	case e := <-events:
		if e.EventType != podregistry.EventCreated {
			t.Errorf("first event type = %q, want created", e.EventType)
		}
		if e.PodID != "alpha" {
			t.Errorf("first event PodID = %q, want alpha", e.PodID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WatchPods did not emit a created event within 2s")
	}
}
