// SPDX-License-Identifier: MIT

package sandbox_test

import (
	"context"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/sandbox"
)

const requireSoPeercredFalseFlag = "--require-so-peercred=false"

// nonceOnlyFixture seeds the §4.7 activation chain: a Runtime carrying the
// supplied deploymentModel and requireSoPeercred, a Sandbox referencing it
// through the pool, the pool, and the pool's SandboxTemplate carrying the
// supplied acknowledgment. It returns the booted client.
func nonceOnlyFixture(t *testing.T, deploymentModel string, requireSoPeercred *bool, acknowledge bool) client.Client {
	t.Helper()
	s := newScheme(t)
	rt := runtimeCR()
	rt.Spec.DeploymentModel = deploymentModel
	rt.Spec.RequireSoPeercred = requireSoPeercred
	tmpl := &lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "claude-template", Namespace: testNS},
		Spec: lennyv1.SandboxTemplateSpec{
			RuntimeRef:               "claude-code",
			AcknowledgeNonceOnlyAuth: acknowledge,
		},
	}
	pool := &lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: "claude-worker", Namespace: testNS},
		Spec:       lennyv1.SandboxWarmPoolSpec{TemplateRef: "claude-template", MinWarm: 1, MaxWarm: 3},
	}
	return newClient(t, s, sandboxCR(""), rt, tmpl, pool)
}

// adapterArgs returns the args of the pod's adapter container, failing the
// test when the pod or the container is absent.
func adapterArgs(t *testing.T, c client.Client) []string {
	t.Helper()
	var pod corev1.Pod
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: testName}, &pod); err != nil {
		t.Fatalf("get pod: %v", err)
	}
	for _, ctr := range pod.Spec.Containers {
		if ctr.Name == "adapter" {
			return ctr.Args
		}
	}
	t.Fatalf("pod has no adapter container; containers=%v", pod.Spec.Containers)
	return nil
}

func hasArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

// TestReconcileRendersNonceOnlyForAcknowledgedSidecarPool_spec_4_7 verifies
// the §4.7 activation path end to end through the reconciler: a
// deploymentModel: sidecar runtime carrying requireSoPeercred: false in a
// pool that acknowledges nonce-only mode renders
// --require-so-peercred=false into the adapter container, records the
// resolved decision on Sandbox.spec.requireSoPeercred (the carrier the
// pool-level trigger reads back), and sets SOPeercredDisabled=True on
// Sandbox.status.
//
// diagnosis: a failure means the nonce-only render gate or one of its two
// WarmPoolController-mediated writes (the spec carrier or the pod-level
// condition) is broken; nonce-only mode is either unreachable or unsurfaced.
//
// spec: §4.3, §4.4, §4.7, §7.1.
func TestReconcileRendersNonceOnlyForAcknowledgedSidecarPool_spec_4_7(t *testing.T) {
	c := nonceOnlyFixture(t, "sidecar", ptr.To(false), true)
	s := newScheme(t)

	if err := reconcile(t, c, s); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if args := adapterArgs(t, c); !hasArg(args, requireSoPeercredFalseFlag) {
		t.Errorf("adapter args %v missing %q", args, requireSoPeercredFalseFlag)
	}

	sb := getSandbox(t, c)
	if sb.Spec.RequireSoPeercred == nil || *sb.Spec.RequireSoPeercred {
		t.Errorf("Sandbox.spec.requireSoPeercred = %v, want explicit false (the §4.5 carrier)", sb.Spec.RequireSoPeercred)
	}
	cond := apimeta.FindStatusCondition(sb.Status.Conditions, lennyv1.SandboxConditionSOPeercredDisabled)
	if cond == nil {
		t.Fatalf("Sandbox.status missing %s condition", lennyv1.SandboxConditionSOPeercredDisabled)
	}
	if cond.Status != metav1.ConditionTrue {
		t.Errorf("%s = %s, want True", lennyv1.SandboxConditionSOPeercredDisabled, cond.Status)
	}
}

// TestReconcileSkipsNonceOnlyForUnacknowledgedPool_spec_4_7 verifies the
// §4.7 render-gate's acknowledgment requirement: a sidecar runtime carrying
// requireSoPeercred: false in a pool that has not acknowledged nonce-only
// mode renders no flag, records no carrier, and sets no condition, so the
// pool keeps full SO_PEERCRED enforcement against a Runtime CR applied
// directly without the pool opt-in.
//
// diagnosis: a failure means the render gate ignores the pool
// acknowledgment, so a directly-applied Runtime CR could weaken the
// adapter-agent authentication boundary on an unacknowledged pool.
//
// spec: §4.7, §7.1.
func TestReconcileSkipsNonceOnlyForUnacknowledgedPool_spec_4_7(t *testing.T) {
	c := nonceOnlyFixture(t, "sidecar", ptr.To(false), false)
	s := newScheme(t)

	if err := reconcile(t, c, s); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if args := adapterArgs(t, c); hasArg(args, requireSoPeercredFalseFlag) {
		t.Errorf("adapter args %v must not carry %q without the pool acknowledgment", args, requireSoPeercredFalseFlag)
	}

	sb := getSandbox(t, c)
	if sb.Spec.RequireSoPeercred != nil {
		t.Errorf("Sandbox.spec.requireSoPeercred = %v, want nil (no carrier without acknowledgment)", *sb.Spec.RequireSoPeercred)
	}
	if cond := apimeta.FindStatusCondition(sb.Status.Conditions, lennyv1.SandboxConditionSOPeercredDisabled); cond != nil {
		t.Errorf("Sandbox.status carries %s condition without the pool acknowledgment", lennyv1.SandboxConditionSOPeercredDisabled)
	}
}

// TestReconcileSkipsNonceOnlyForEmbeddedRuntime_spec_4_7 verifies the §4.1
// deployment-model qualifier in the render decision: an embedded runtime CR
// carrying requireSoPeercred: false in an acknowledged pool renders no flag,
// records no carrier, and sets no condition. The embedded model has no
// adapter-agent socket boundary to fall back from, so an acknowledged pool
// referencing such a runtime runs zero nonce-only pods and never trips the
// pool-level trigger.
//
// diagnosis: a failure means the deployment-model qualifier is missing from
// the render decision, so an embedded runtime (which has no SO_PEERCRED
// boundary) could falsely surface a degraded-mode condition.
//
// spec: §4.7, §7.1.
func TestReconcileSkipsNonceOnlyForEmbeddedRuntime_spec_4_7(t *testing.T) {
	c := nonceOnlyFixture(t, "embedded", ptr.To(false), true)
	s := newScheme(t)

	if err := reconcile(t, c, s); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	// The embedded pod has a single container and no adapter; assert no flag
	// surfaces anywhere in its container args.
	var pod corev1.Pod
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: testName}, &pod); err != nil {
		t.Fatalf("get pod: %v", err)
	}
	for _, ctr := range pod.Spec.Containers {
		if hasArg(ctr.Args, requireSoPeercredFalseFlag) {
			t.Errorf("embedded pod container %q carries %q", ctr.Name, requireSoPeercredFalseFlag)
		}
	}

	sb := getSandbox(t, c)
	if sb.Spec.RequireSoPeercred != nil {
		t.Errorf("Sandbox.spec.requireSoPeercred = %v, want nil for an embedded runtime", *sb.Spec.RequireSoPeercred)
	}
	if cond := apimeta.FindStatusCondition(sb.Status.Conditions, lennyv1.SandboxConditionSOPeercredDisabled); cond != nil {
		t.Errorf("Sandbox.status carries %s condition for an embedded runtime", lennyv1.SandboxConditionSOPeercredDisabled)
	}
}

// carrierPatchFailClient wraps a client and fails the §4.7 nonce-only carrier
// write (the spec.requireSoPeercred SSA Apply patch on a Sandbox) while
// passing every other call through. It models an apiserver hiccup on the
// carrier write that is not a 409 conflict, the failure mode the create-then-
// persist ordering could not recover from.
type carrierPatchFailClient struct {
	client.Client
	err  error
	fail *bool
}

// Patch fails a carrier apply (an ApplyPatchType patch targeting a Sandbox,
// not its status subresource) while *fail is true; status applies and other
// patches pass through. The carrier write is the only non-status Sandbox apply
// the reconciler issues, so matching on the type and object is sufficient.
func (c *carrierPatchFailClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	if c.fail != nil && *c.fail && patch.Type() == types.ApplyPatchType {
		if _, ok := obj.(*lennyv1.Sandbox); ok {
			return c.err
		}
	}
	return c.Client.Patch(ctx, obj, patch, opts...)
}

// TestReconcileRecoversNonceOnlyCarrierWriteFailure_spec_4_4 verifies that a
// carrier-write failure does not leave a nonce-only pod permanently without
// its §4.4 SOPeercredDisabled condition and §4.3 carrier. The carrier is
// persisted before the pod is created, so a failed carrier write leaves the
// pod uncreated; the next reconcile re-enters ActionCreatePod (pod still
// absent), re-resolves, retries the carrier write idempotently, creates the
// pod, and surfaces the condition.
//
// diagnosis: a failure means the carrier and SOPeercredDisabled condition are
// written after the pod is created, so a carrier-write error after a
// successful pod create is unrecoverable — the pod runs in nonce-only mode
// with no carrier and no condition forever, failing open on the §4.4
// security-degradation surfacing invariant.
//
// spec: §4.3, §4.4, §4.7.
func TestReconcileRecoversNonceOnlyCarrierWriteFailure_spec_4_4(t *testing.T) {
	c := nonceOnlyFixture(t, "sidecar", ptr.To(false), true)
	s := newScheme(t)

	fail := true
	wrapped := &carrierPatchFailClient{
		Client: c,
		err:    errors.New("apiserver hiccup on carrier write"),
		fail:   &fail,
	}
	r := &sandbox.Reconciler{Client: wrapped, Scheme: s, AdapterImage: "ghcr.io/lennylabs/lenny-adapter:v1"}
	req := ctrl.Request{NamespacedName: client.ObjectKey{Namespace: testNS, Name: testName}}

	// First reconcile: the carrier write fails before the pod is created.
	if _, err := r.Reconcile(context.Background(), req); err == nil {
		t.Fatal("expected the carrier-write failure to surface as a reconcile error")
	}
	// The pod must NOT exist: persisting the carrier first means a carrier
	// failure leaves the pod uncreated, so the next reconcile re-enters
	// ActionCreatePod and the decision is recoverable.
	var pod corev1.Pod
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: testName}, &pod); !apierrors.IsNotFound(err) {
		t.Fatalf("pod get after failed carrier write = %v, want NotFound (pod must not be created)", err)
	}

	// Second reconcile: the apiserver recovers; the carrier write succeeds,
	// the pod is created, and the condition surfaces.
	fail = false
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("recovery Reconcile: %v", err)
	}

	if args := adapterArgs(t, c); !hasArg(args, requireSoPeercredFalseFlag) {
		t.Errorf("adapter args %v missing %q after recovery", args, requireSoPeercredFalseFlag)
	}
	sb := getSandbox(t, c)
	if sb.Spec.RequireSoPeercred == nil || *sb.Spec.RequireSoPeercred {
		t.Errorf("Sandbox.spec.requireSoPeercred = %v, want explicit false after recovery", sb.Spec.RequireSoPeercred)
	}
	cond := apimeta.FindStatusCondition(sb.Status.Conditions, lennyv1.SandboxConditionSOPeercredDisabled)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("Sandbox.status %s = %v after recovery, want True", lennyv1.SandboxConditionSOPeercredDisabled, cond)
	}
}
