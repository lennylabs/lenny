// SPDX-License-Identifier: MIT

//go:build component

package controllers_test

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	nodev1 "k8s.io/api/node/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/admission/ownership"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/sandbox"
	"github.com/lennylabs/lenny/pkg/controller/sandbox/podspec"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
	"github.com/lennylabs/lenny/pkg/sandbox/state"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

const (
	taNS   = "lenny-agents"
	taPool = "ta-pool"
)

// terminatedAdapterEnv is one envtest API server seeded with a warm pool
// (minWarm 1) and the two WarmPoolController arms the test drives by
// hand: the pool sizing reconciler and the Sandbox-to-Pod reconciler.
type terminatedAdapterEnv struct {
	t    *testing.T
	ctx  context.Context
	c    client.Client
	pool *warmpool.Reconciler
	sb   *sandbox.Reconciler
}

func newTerminatedAdapterEnv(t *testing.T) *terminatedAdapterEnv {
	t.Helper()
	env := envtest.Start(t)
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(lennyv1.AddToScheme(scheme))
	c, err := client.New(env.RESTConfig(), client.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	ctx := context.Background()
	mustCreate(t, ctx, c, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: taNS}})
	for _, rc := range []string{"runc", "gvisor", "kata"} {
		if err := c.Create(ctx, &nodev1.RuntimeClass{ObjectMeta: metav1.ObjectMeta{Name: rc}, Handler: rc}); err != nil && !apierrors.IsAlreadyExists(err) {
			t.Fatalf("create RuntimeClass %s: %v", rc, err)
		}
	}
	mustCreate(t, ctx, c, &lennyv1.Runtime{
		ObjectMeta: metav1.ObjectMeta{Name: "claude-code"},
		Spec: lennyv1.RuntimeSpec{
			Type:             "agent",
			Image:            "ghcr.io/acme/claude-code@sha256:" + "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
			Command:          []string{"/usr/local/bin/lenny"},
			IntegrationLevel: "full",
		},
	})
	mustCreate(t, ctx, c, &lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: taPool, Namespace: taNS},
		Spec:       lennyv1.SandboxTemplateSpec{RuntimeRef: "claude-code", IsolationProfile: "standard"},
	})
	mustCreate(t, ctx, c, &lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: taPool, Namespace: taNS},
		Spec:       lennyv1.SandboxWarmPoolSpec{TemplateRef: taPool, MinWarm: 1, MaxWarm: 3},
	})
	return &terminatedAdapterEnv{
		t: t, ctx: ctx, c: c,
		pool: &warmpool.Reconciler{Client: c, Scheme: scheme},
		sb:   &sandbox.Reconciler{Client: c, Scheme: scheme, AdapterImage: "ghcr.io/lennylabs/lenny-adapter:v1", SupervisorImage: "ghcr.io/lennylabs/lenny-supervisor:v1"},
	}
}

func (e *terminatedAdapterEnv) reconcilePool() {
	e.t.Helper()
	if _, err := e.pool.Reconcile(e.ctx, ctrl.Request{NamespacedName: client.ObjectKey{Namespace: taNS, Name: taPool}}); err != nil {
		e.t.Fatalf("pool Reconcile: %v", err)
	}
}

func (e *terminatedAdapterEnv) reconcileSandbox(name string) {
	e.t.Helper()
	if _, err := e.sb.Reconcile(e.ctx, ctrl.Request{NamespacedName: client.ObjectKey{Namespace: taNS, Name: name}}); err != nil {
		e.t.Fatalf("sandbox Reconcile %s: %v", name, err)
	}
}

func (e *terminatedAdapterEnv) sandboxes() []lennyv1.Sandbox {
	e.t.Helper()
	var l lennyv1.SandboxList
	if err := e.c.List(e.ctx, &l, client.InNamespace(taNS), client.MatchingLabels{warmpool.LabelPool: taPool}); err != nil {
		e.t.Fatalf("list sandboxes: %v", err)
	}
	return l.Items
}

func (e *terminatedAdapterEnv) phase(name string) state.State {
	e.t.Helper()
	var sb lennyv1.Sandbox
	if err := e.c.Get(e.ctx, client.ObjectKey{Namespace: taNS, Name: name}, &sb); err != nil {
		e.t.Fatalf("get sandbox %s: %v", name, err)
	}
	return state.State(sb.Status.Phase)
}

// warmCount counts the Sandboxes the pool sizing plan counts as warm
// inventory (warming, sdk_connecting, or idle).
func (e *terminatedAdapterEnv) warmCount() int {
	n := 0
	for _, sb := range e.sandboxes() {
		switch state.State(sb.Status.Phase) {
		case "", state.Warming, state.SDKConnecting, state.Idle:
			n++
		}
	}
	return n
}

func (e *terminatedAdapterEnv) podExists(name string) bool {
	e.t.Helper()
	var p corev1.Pod
	err := e.c.Get(e.ctx, client.ObjectKey{Namespace: taNS, Name: name}, &p)
	if err != nil && !apierrors.IsNotFound(err) {
		e.t.Fatalf("get pod %s: %v", name, err)
	}
	return err == nil
}

// setPodStatus writes a Running pod status whose adapter container is
// running or terminated, standing in for the kubelet.
func (e *terminatedAdapterEnv) setPodStatus(name string, adapterExited, ready bool) {
	e.t.Helper()
	var p corev1.Pod
	if err := e.c.Get(e.ctx, client.ObjectKey{Namespace: taNS, Name: name}, &p); err != nil {
		e.t.Fatalf("get pod %s: %v", name, err)
	}
	adapter := corev1.ContainerStatus{
		Name: podspec.AdapterContainerName, Ready: ready, Image: "adapter",
		State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
	}
	if adapterExited {
		adapter.Ready = false
		adapter.State = corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 1, Reason: "Error"}}
	}
	cond := corev1.ConditionFalse
	if ready && !adapterExited {
		cond = corev1.ConditionTrue
	}
	p.Status = corev1.PodStatus{
		Phase: corev1.PodRunning,
		Conditions: []corev1.PodCondition{
			{Type: corev1.PodReady, Status: cond},
			{Type: corev1.ContainersReady, Status: cond},
		},
		ContainerStatuses: []corev1.ContainerStatus{
			adapter,
			{
				Name: podspec.RuntimeContainerName, Ready: ready, Image: "runtime",
				State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
			},
		},
	}
	if err := e.c.Status().Update(e.ctx, &p); err != nil {
		e.t.Fatalf("update pod %s status: %v", name, err)
	}
}

// applySandboxPhase seeds a Sandbox phase under the WarmPoolController
// field manager, standing in for the occupancy projection.
func (e *terminatedAdapterEnv) applySandboxPhase(name string, phase state.State) {
	e.t.Helper()
	patch := &lennyv1.Sandbox{
		TypeMeta:   metav1.TypeMeta{APIVersion: lennyv1.GroupVersion.String(), Kind: "Sandbox"},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: taNS},
	}
	patch.Status.Phase = string(phase)
	patch.Status.PodName = name
	if err := e.c.Status().Patch(e.ctx, patch, client.Apply, client.FieldOwner(string(ownership.WarmPoolController))); err != nil {
		e.t.Fatalf("apply sandbox %s phase %s: %v", name, phase, err)
	}
}

// fillOne fills the pool to its single Sandbox, drives that Sandbox to a
// created pod, and returns its name.
func (e *terminatedAdapterEnv) fillOne() string {
	e.t.Helper()
	e.reconcilePool()
	sbs := e.sandboxes()
	if len(sbs) != 1 {
		e.t.Fatalf("pool fill created %d sandboxes, want 1", len(sbs))
	}
	name := sbs[0].Name
	e.reconcileSandbox(name)
	if !e.podExists(name) {
		e.t.Fatalf("sandbox %s has no pod after reconcile", name)
	}
	return name
}

// drainToTerminated reconciles a Sandbox until its pod is deleted and it
// reaches terminated, asserting each step of the §6.2 reclaim.
func (e *terminatedAdapterEnv) drainToTerminated(name string) {
	e.t.Helper()
	if got := e.phase(name); got != state.Draining {
		e.t.Fatalf("sandbox %s phase = %q, want draining", name, got)
	}
	e.reconcileSandbox(name) // draining with a live pod: delete it
	if e.podExists(name) {
		e.t.Fatalf("draining sandbox %s still has its pod", name)
	}
	e.reconcileSandbox(name) // draining with no pod: terminated
	if got := e.phase(name); got != state.Terminated {
		e.t.Fatalf("sandbox %s phase = %q, want terminated", name, got)
	}
}

// spec: 6.2 (Pod State Machine), 4.6.1 (Warm Pool Controller (Pod Lifecycle))
// diagnosis: A warming pod whose adapter container terminated while the
// pod phase stayed Running was not moved to failed, the failed Sandbox was
// not reclaimed through draining, or the pool did not provision a
// replacement to restore minWarm. Check observePod's container-status
// reduction, the failed arm of lifecycle.Decide, and the pool plan's warm
// count.
func TestWarmingPodWithTerminatedAdapterIsFailedReclaimedAndReplaced(t *testing.T) {
	e := newTerminatedAdapterEnv(t)
	name := e.fillOne()

	e.setPodStatus(name, true, false)
	e.reconcileSandbox(name)
	if got := e.phase(name); got != state.Failed {
		t.Fatalf("warming sandbox with a terminated adapter: phase = %q, want failed", got)
	}

	// The failed Sandbox is no longer warm inventory, so the pool fills a
	// replacement and minWarm is restored.
	e.reconcilePool()
	if got := len(e.sandboxes()); got != 2 {
		t.Fatalf("after the failure the pool holds %d sandboxes, want 2 (failed plus replacement)", got)
	}
	if got := e.warmCount(); got != 1 {
		t.Fatalf("warm count after replacement = %d, want minWarm 1", got)
	}

	// failed → draining → (pod deleted) → terminated.
	e.reconcileSandbox(name)
	e.drainToTerminated(name)

	e.reconcilePool()
	if got := e.warmCount(); got != 1 {
		t.Fatalf("warm count after reclaim = %d, want minWarm 1", got)
	}
}

// spec: 6.2 (Pod State Machine), 4.6.1 (Warm Pool Controller (Pod Lifecycle))
// diagnosis: An idle pod whose adapter container terminated was not
// retired through draining, so it kept counting as claimable warm
// inventory, or the pool did not provision a replacement. Check
// observePod's container-status reduction and the idle arm of
// lifecycle.Decide.
func TestIdlePodWithTerminatedAdapterIsRetiredAndReplaced(t *testing.T) {
	e := newTerminatedAdapterEnv(t)
	name := e.fillOne()

	e.setPodStatus(name, false, true)
	e.reconcileSandbox(name)
	if got := e.phase(name); got != state.Idle {
		t.Fatalf("healthy ready pod: phase = %q, want idle", got)
	}

	e.setPodStatus(name, true, false)
	e.reconcileSandbox(name)
	e.drainToTerminated(name)

	e.reconcilePool()
	if got := e.warmCount(); got != 1 {
		t.Fatalf("warm count after the idle retire = %d, want minWarm 1", got)
	}
}

// spec: 6.2 (Pod State Machine), 4.6.1 (Warm Pool Controller (Pod Lifecycle)), 5.2 (Pool Configuration and Execution Modes)
// diagnosis: The Sandbox-to-Pod reconciler acted on a pod that carries a
// SandboxClaim: it re-phased or deleted a claimed pod whose adapter
// exited, or reclaimed a failed pod before its claim was released. A pod
// with a claim must retire through the gateway's release path or the
// orphaned-claim collector. Check the default arm of lifecycle.Decide and
// lifecycle.HoldFailedReclaim.
func TestClaimedPodWithTerminatedAdapterGoesThroughReleasePath(t *testing.T) {
	e := newTerminatedAdapterEnv(t)
	name := e.fillOne()

	claim := &lennyv1.SandboxClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "claim-" + name, Namespace: taNS},
		Spec:       lennyv1.SandboxClaimSpec{SandboxRef: name, TenantID: "acme"},
	}
	mustCreate(t, e.ctx, e.c, claim)

	// A claimed pod whose adapter exited is left to the gateway.
	e.applySandboxPhase(name, state.Claimed)
	e.setPodStatus(name, true, false)
	e.reconcileSandbox(name)
	if got := e.phase(name); got != state.Claimed {
		t.Fatalf("claimed sandbox with a terminated adapter: phase = %q, want claimed", got)
	}
	if !e.podExists(name) {
		t.Fatalf("claimed sandbox's pod was deleted outside the release path")
	}

	// A failed Sandbox whose claim still exists is held.
	e.applySandboxPhase(name, state.Failed)
	e.reconcileSandbox(name)
	if got := e.phase(name); got != state.Failed {
		t.Fatalf("failed sandbox with a claim: phase = %q, want failed (held)", got)
	}
	if !e.podExists(name) {
		t.Fatalf("failed sandbox's pod was deleted while its claim exists")
	}

	// Once the claim is released (deleted), the reclaim proceeds.
	if err := e.c.Delete(e.ctx, claim); err != nil {
		t.Fatalf("delete claim: %v", err)
	}
	e.reconcileSandbox(name)
	e.drainToTerminated(name)
}
