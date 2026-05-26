// SPDX-License-Identifier: MIT

package sandbox_test

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
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
	"github.com/lennylabs/lenny/pkg/controller/sandbox"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"
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

// newClient boots an envtest API server, pre-creates the
// `lenny-agents` namespace, and seeds the supplied objects (Spec
// via Create, then Status via SSA Apply under the
// `lenny-warm-pool-controller` field manager so the production
// reconciler's Apply path sees a same-manager update rather than
// a cross-manager conflict). The fake client does not yet implement
// SSA (kubernetes/kubernetes#115598), so this routes through a real
// kube-apiserver via envtest.
func newClient(t *testing.T, s *runtime.Scheme, objs ...client.Object) client.Client {
	t.Helper()
	env := envtest.Start(t)
	c, err := client.New(env.RESTConfig(), client.Options{Scheme: s})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	ctx := context.Background()
	if err := c.Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: testNS},
	}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create namespace %s: %v", testNS, err)
	}
	// Pre-create the three §5.3 RuntimeClass objects podspec.Build
	// references. envtest enforces the RuntimeClass admission check
	// (real apiserver); the fake client did not. The handlers are
	// stand-ins — the test never schedules these pods anywhere.
	for _, rc := range []string{"runc", "gvisor", "kata"} {
		if err := c.Create(ctx, &nodev1.RuntimeClass{
			ObjectMeta: metav1.ObjectMeta{Name: rc},
			Handler:    rc,
		}); err != nil && !apierrors.IsAlreadyExists(err) {
			t.Fatalf("create RuntimeClass %s: %v", rc, err)
		}
	}
	for _, o := range objs {
		var (
			sbStatus  lennyv1.SandboxStatus
			podStatus corev1.PodStatus
			seedAfter func()
		)
		switch v := o.(type) {
		case *lennyv1.Sandbox:
			sbStatus = v.Status
			v.Status = lennyv1.SandboxStatus{}
			seedAfter = func() {
				if sbStatus.Phase == "" && sbStatus.PodName == "" &&
					sbStatus.NodeName == "" && sbStatus.PodIP == "" {
					return
				}
				patch := &lennyv1.Sandbox{
					TypeMeta: metav1.TypeMeta{
						APIVersion: lennyv1.GroupVersion.String(),
						Kind:       "Sandbox",
					},
					ObjectMeta: metav1.ObjectMeta{Name: v.Name, Namespace: v.Namespace},
				}
				patch.Status = sbStatus
				if err := c.Status().Patch(ctx, patch, client.Apply, client.FieldOwner(string(ownership.WarmPoolController))); err != nil {
					t.Fatalf("seed status Sandbox %s: %v", v.Name, err)
				}
			}
		case *corev1.Pod:
			podStatus = v.Status
			v.Status = corev1.PodStatus{}
			seedAfter = func() {
				if podStatus.Phase == "" && podStatus.PodIP == "" && len(podStatus.Conditions) == 0 {
					return
				}
				v.Status = podStatus
				if err := c.Status().Update(ctx, v); err != nil {
					t.Fatalf("seed status Pod %s: %v", v.Name, err)
				}
			}
		}
		if err := c.Create(ctx, o); err != nil {
			t.Fatalf("create %T %s: %v", o, o.GetName(), err)
		}
		if seedAfter != nil {
			seedAfter()
		}
	}
	return c
}

// utilruntime + clientgoscheme stay referenced for tests that build a
// fresh scheme directly.
var _ = utilruntime.Must
var _ = clientgoscheme.AddToScheme

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

// TestReconcileClampsPodGraceToTemplateCeiling_spec_4_6_1 confirms the
// reconciler resolves the Sandbox's pool and template and clamps the
// created pod's terminationGracePeriodSeconds to the template's
// maxTerminationGracePeriodSeconds ceiling.
func TestReconcileClampsPodGraceToTemplateCeiling_spec_4_6_1(t *testing.T) {
	s := newScheme(t)
	ceiling := int64(45)
	tmpl := &lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "claude-template", Namespace: testNS},
		Spec: lennyv1.SandboxTemplateSpec{
			RuntimeRef:                       "claude-code",
			MaxTerminationGracePeriodSeconds: &ceiling,
		},
	}
	pool := &lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: "claude-worker", Namespace: testNS},
		Spec:       lennyv1.SandboxWarmPoolSpec{TemplateRef: "claude-template", MinWarm: 1, MaxWarm: 3},
	}
	c := newClient(t, s, sandboxCR(""), runtimeCR(), tmpl, pool)

	if err := reconcile(t, c, s); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	var pod corev1.Pod
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: testName}, &pod); err != nil {
		t.Fatalf("get pod: %v", err)
	}
	if g := pod.Spec.TerminationGracePeriodSeconds; g == nil || *g != ceiling {
		t.Errorf("terminationGracePeriodSeconds = %v, want %d (template ceiling)", g, ceiling)
	}
}

func runtimeCR() *lennyv1.Runtime {
	return &lennyv1.Runtime{
		ObjectMeta: metav1.ObjectMeta{Name: "claude-code"},
		Spec: lennyv1.RuntimeSpec{
			Type: "agent",
			// spec: §5.3 (F-5.3.4) — the Runtime CRD pins images by digest;
			// envtest enforces the @sha256:<64-hex> Pattern, so the test
			// helper uses a digest reference rather than a tag.
			Image:            "ghcr.io/acme/claude-code@sha256:" + "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
			IntegrationLevel: "full",
		},
	}
}

func podCR(phase corev1.PodPhase, ready bool) *corev1.Pod {
	// envtest enforces real Pod validation (spec.containers is
	// required). A stand-in container suffices; the reconciler reads
	// only pod.Status, not pod.Spec, for its phase decisions.
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: testName, Namespace: testNS},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "stub", Image: "k8s.gcr.io/pause"}},
		},
		Status: corev1.PodStatus{Phase: phase, PodIP: testPodIP},
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

// TestReconcileDevModeDefaultsPodToRunc_spec_5_3 verifies the §5.3 line
// 677 pod-fallback default: a Sandbox that omits an isolation profile
// stamps runtimeClassName=runc under dev mode (so a gVisor-less cluster
// can launch the pod), and gvisor without dev mode.
//
// spec: §5.3 line 677.
func TestReconcileDevModeDefaultsPodToRunc_spec_5_3(t *testing.T) {
	s := newScheme(t)
	sb := sandboxCR("")
	sb.Spec.IsolationProfile = "" // exercise the controller default fallback
	c := newClient(t, s, sb, runtimeCR())

	r := &sandbox.Reconciler{Client: c, Scheme: s, AdapterImage: "ghcr.io/lennylabs/lenny-adapter:v1", DevMode: true}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: testNS, Name: testName},
	}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	var pod corev1.Pod
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: testName}, &pod); err != nil {
		t.Fatalf("expected a backing pod: %v", err)
	}
	if pod.Spec.RuntimeClassName == nil || *pod.Spec.RuntimeClassName != "runc" {
		t.Errorf("dev-mode runtimeClassName = %v, want runc", pod.Spec.RuntimeClassName)
	}
}

func TestReconcileCreatesPodForNewSandbox(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s, sandboxCR(""), runtimeCR())

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
	// spec: §4.6.1 — a Sandbox with no resolvable template ceiling gets
	// the 120s disruption-protection default.
	if g := pod.Spec.TerminationGracePeriodSeconds; g == nil || *g != 120 {
		t.Errorf("terminationGracePeriodSeconds = %v, want 120 (§4.6.1 default)", g)
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
	c := newClient(t, s, sandboxCR(""), rt)

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
	c := newClient(t, s, sandboxCR("warming"), runtimeCR(), podCR(corev1.PodRunning, true))

	if err := reconcile(t, c, s); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if got := getSandbox(t, c).Status.Phase; got != "idle" {
		t.Errorf("phase = %q, want idle once the pod is ready", got)
	}
}

func TestReconcileRecordsPodIP(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s, sandboxCR("warming"), runtimeCR(), podCR(corev1.PodRunning, true))

	if err := reconcile(t, c, s); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if got := getSandbox(t, c).Status.PodIP; got != testPodIP {
		t.Errorf("sandbox status.podIP = %q, want %q so the gateway can reach the adapter", got, testPodIP)
	}
}

func TestReconcileAdvancesWarmingToFailedOnPodFailure(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s, sandboxCR("warming"), runtimeCR(), podCR(corev1.PodFailed, false))

	if err := reconcile(t, c, s); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if got := getSandbox(t, c).Status.Phase; got != "failed" {
		t.Errorf("phase = %q, want failed when the pod failed", got)
	}
}

func TestReconcileWaitsWhilePodPending(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s, sandboxCR("warming"), runtimeCR(), podCR(corev1.PodPending, false))

	if err := reconcile(t, c, s); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if got := getSandbox(t, c).Status.Phase; got != "warming" {
		t.Errorf("phase = %q, want warming while the pod is still pending", got)
	}
}

func TestReconcileDrainsIdleSandboxWhosePodVanished(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s, sandboxCR("idle"), runtimeCR())

	if err := reconcile(t, c, s); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if got := getSandbox(t, c).Status.Phase; got != "draining" {
		t.Errorf("phase = %q, want draining when an idle pod has vanished", got)
	}
}

func TestReconcileDeletesPodWhileDraining(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s, sandboxCR("draining"), runtimeCR(), podCR(corev1.PodRunning, true))

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
	c := newClient(t, s, sandboxCR("draining"), runtimeCR())

	if err := reconcile(t, c, s); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if got := getSandbox(t, c).Status.Phase; got != "terminated" {
		t.Errorf("phase = %q, want terminated once a draining sandbox has no pod", got)
	}
}

func TestReconcileMissingRuntimeErrors(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s, sandboxCR("")) // no Runtime CR

	if err := reconcile(t, c, s); err == nil {
		t.Fatal("Reconcile should error when the referenced Runtime is missing")
	}
}

func TestReconcileSandboxNotFound(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s)

	if err := reconcile(t, c, s); err != nil {
		t.Errorf("Reconcile of a deleted sandbox should not error: %v", err)
	}
}
