// SPDX-License-Identifier: MIT

package sandbox_test

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
	"github.com/lennylabs/lenny/pkg/controller/sandbox"
)

const (
	testNS    = "lenny-agents"
	testName  = "claude-worker-1"
	testPodIP = "10.244.1.7"
)

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(s))
	utilruntime.Must(lennyv1.AddToScheme(s))
	return s
}

func newClient(s *runtime.Scheme, objs ...client.Object) client.Client {
	return fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(objs...).
		WithStatusSubresource(&lennyv1.Sandbox{}).
		Build()
}

func sandboxCR(phase string) *lennyv1.Sandbox {
	return &lennyv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: testName, Namespace: testNS},
		Spec: lennyv1.SandboxSpec{
			RuntimeRef:       "claude-code",
			PoolRef:          "claude-worker",
			IsolationProfile: "sandboxed",
		},
		Status: lennyv1.SandboxStatus{Phase: phase},
	}
}

func runtimeCR() *lennyv1.Runtime {
	return &lennyv1.Runtime{
		ObjectMeta: metav1.ObjectMeta{Name: "claude-code"},
		Spec: lennyv1.RuntimeSpec{
			Type:             "agent",
			Image:            "ghcr.io/acme/claude-code:v1",
			IntegrationLevel: "full",
		},
	}
}

func podCR(phase corev1.PodPhase, ready bool) *corev1.Pod {
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: testName, Namespace: testNS},
		Status:     corev1.PodStatus{Phase: phase, PodIP: testPodIP},
	}
	if ready {
		p.Status.Conditions = []corev1.PodCondition{
			{Type: corev1.PodReady, Status: corev1.ConditionTrue},
		}
	}
	return p
}

func reconcile(t *testing.T, c client.Client, s *runtime.Scheme) error {
	t.Helper()
	r := &sandbox.Reconciler{Client: c, Scheme: s, AdapterImage: "ghcr.io/lennylabs/lenny-adapter:v1"}
	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: testNS, Name: testName},
	})
	return err
}

func getSandbox(t *testing.T, c client.Client) lennyv1.Sandbox {
	t.Helper()
	var sb lennyv1.Sandbox
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: testName}, &sb); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	return sb
}

func TestReconcileCreatesPodForNewSandbox(t *testing.T) {
	s := newScheme(t)
	c := newClient(s, sandboxCR(""), runtimeCR())

	if err := reconcile(t, c, s); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	var pod corev1.Pod
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: testName}, &pod); err != nil {
		t.Fatalf("expected a backing pod to be created: %v", err)
	}
	if len(pod.Spec.Containers) != 2 {
		t.Errorf("pod has %d containers, want 2", len(pod.Spec.Containers))
	}
	if len(pod.OwnerReferences) != 1 ||
		pod.OwnerReferences[0].Kind != "Sandbox" ||
		pod.OwnerReferences[0].Name != testName {
		t.Errorf("pod owner references = %+v, want one Sandbox/%s", pod.OwnerReferences, testName)
	}
	if got := getSandbox(t, c).Status.PodName; got != testName {
		t.Errorf("sandbox status.podName = %q, want %q", got, testName)
	}
}

// TestReconcileCreatesEmbeddedPodForEmbeddedRuntime confirms the §4.7
// deploymentModel field threads from the Runtime CRD through the
// reconciler to podspec: an embedded runtime yields a single-container
// pod.
func TestReconcileCreatesEmbeddedPodForEmbeddedRuntime(t *testing.T) {
	s := newScheme(t)
	rt := runtimeCR()
	rt.Spec.DeploymentModel = "embedded"
	c := newClient(s, sandboxCR(""), rt)

	if err := reconcile(t, c, s); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	var pod corev1.Pod
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: testName}, &pod); err != nil {
		t.Fatalf("expected a backing pod to be created: %v", err)
	}
	if len(pod.Spec.Containers) != 1 {
		t.Errorf("embedded runtime pod has %d containers, want 1", len(pod.Spec.Containers))
	}
}

func TestReconcileAdvancesWarmingToIdleWhenPodReady(t *testing.T) {
	s := newScheme(t)
	c := newClient(s, sandboxCR("warming"), runtimeCR(), podCR(corev1.PodRunning, true))

	if err := reconcile(t, c, s); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if got := getSandbox(t, c).Status.Phase; got != "idle" {
		t.Errorf("phase = %q, want idle once the pod is ready", got)
	}
}

func TestReconcileRecordsPodIP(t *testing.T) {
	s := newScheme(t)
	c := newClient(s, sandboxCR("warming"), runtimeCR(), podCR(corev1.PodRunning, true))

	if err := reconcile(t, c, s); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if got := getSandbox(t, c).Status.PodIP; got != testPodIP {
		t.Errorf("sandbox status.podIP = %q, want %q so the gateway can reach the adapter", got, testPodIP)
	}
}

func TestReconcileAdvancesWarmingToFailedOnPodFailure(t *testing.T) {
	s := newScheme(t)
	c := newClient(s, sandboxCR("warming"), runtimeCR(), podCR(corev1.PodFailed, false))

	if err := reconcile(t, c, s); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if got := getSandbox(t, c).Status.Phase; got != "failed" {
		t.Errorf("phase = %q, want failed when the pod failed", got)
	}
}

func TestReconcileWaitsWhilePodPending(t *testing.T) {
	s := newScheme(t)
	c := newClient(s, sandboxCR("warming"), runtimeCR(), podCR(corev1.PodPending, false))

	if err := reconcile(t, c, s); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if got := getSandbox(t, c).Status.Phase; got != "warming" {
		t.Errorf("phase = %q, want warming while the pod is still pending", got)
	}
}

func TestReconcileDrainsIdleSandboxWhosePodVanished(t *testing.T) {
	s := newScheme(t)
	c := newClient(s, sandboxCR("idle"), runtimeCR())

	if err := reconcile(t, c, s); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if got := getSandbox(t, c).Status.Phase; got != "draining" {
		t.Errorf("phase = %q, want draining when an idle pod has vanished", got)
	}
}

func TestReconcileDeletesPodWhileDraining(t *testing.T) {
	s := newScheme(t)
	c := newClient(s, sandboxCR("draining"), runtimeCR(), podCR(corev1.PodRunning, true))

	if err := reconcile(t, c, s); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	var pod corev1.Pod
	err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: testName}, &pod)
	if !apierrors.IsNotFound(err) {
		t.Errorf("draining sandbox should have its pod deleted, got err=%v", err)
	}
}

func TestReconcileTerminatesDrainingSandboxWithNoPod(t *testing.T) {
	s := newScheme(t)
	c := newClient(s, sandboxCR("draining"), runtimeCR())

	if err := reconcile(t, c, s); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if got := getSandbox(t, c).Status.Phase; got != "terminated" {
		t.Errorf("phase = %q, want terminated once a draining sandbox has no pod", got)
	}
}

func TestReconcileMissingRuntimeErrors(t *testing.T) {
	s := newScheme(t)
	c := newClient(s, sandboxCR("")) // no Runtime CR

	if err := reconcile(t, c, s); err == nil {
		t.Fatal("Reconcile should error when the referenced Runtime is missing")
	}
}

func TestReconcileSandboxNotFound(t *testing.T) {
	s := newScheme(t)
	c := newClient(s)

	if err := reconcile(t, c, s); err != nil {
		t.Errorf("Reconcile of a deleted sandbox should not error: %v", err)
	}
}
