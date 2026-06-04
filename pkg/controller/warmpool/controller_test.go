// SPDX-License-Identifier: MIT

package warmpool_test

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/admission/ownership"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

const (
	testNS   = "lenny-agents"
	testPool = "claude-worker-small"
)

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := lennyv1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme corev1: %v", err)
	}
	if err := policyv1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme policyv1: %v", err)
	}
	return s
}

// applyStatus seeds a status using SSA Apply under the same field
// manager the reconciler uses. Status().Update would create CSA
// ownership of the same fields, which then conflicts with the
// reconciler's SSA Apply at runtime (CSA and SSA track ownership
// independently, so two managers claiming the same field with
// different strategies always conflict). Using Apply here puts the
// test seeding on the same path the production reconciler uses,
// so the reconciler's subsequent Apply is a same-manager update
// rather than a cross-manager conflict.
func applyStatus(ctx context.Context, c client.Client, obj client.Object) error {
	switch v := obj.(type) {
	case *lennyv1.SandboxWarmPool:
		patch := &lennyv1.SandboxWarmPool{
			TypeMeta: metav1.TypeMeta{
				APIVersion: lennyv1.GroupVersion.String(),
				Kind:       "SandboxWarmPool",
			},
			ObjectMeta: metav1.ObjectMeta{Name: v.Name, Namespace: v.Namespace},
		}
		patch.Status = v.Status
		return c.Status().Patch(ctx, patch, client.Apply, client.FieldOwner(string(ownership.WarmPoolController)))
	case *lennyv1.SandboxTemplate:
		patch := &lennyv1.SandboxTemplate{
			TypeMeta: metav1.TypeMeta{
				APIVersion: lennyv1.GroupVersion.String(),
				Kind:       "SandboxTemplate",
			},
			ObjectMeta: metav1.ObjectMeta{Name: v.Name, Namespace: v.Namespace},
		}
		patch.Status = v.Status
		return c.Status().Patch(ctx, patch, client.Apply, client.FieldOwner(string(ownership.WarmPoolController)))
	case *lennyv1.Sandbox:
		patch := &lennyv1.Sandbox{
			TypeMeta: metav1.TypeMeta{
				APIVersion: lennyv1.GroupVersion.String(),
				Kind:       "Sandbox",
			},
			ObjectMeta: metav1.ObjectMeta{Name: v.Name, Namespace: v.Namespace},
		}
		patch.Status = v.Status
		return c.Status().Patch(ctx, patch, client.Apply, client.FieldOwner(string(ownership.WarmPoolController)))
	}
	return nil
}

// newClient boots an envtest API server, pre-creates the
// `lenny-agents` namespace, and seeds the supplied objects (Spec
// then Status) so the reconciler observes them on first Reconcile.
// envtest backs the controller-runtime client with a real
// kube-apiserver so the §4.6.3 SSA Apply path the controllers use
// works; the fake client does not yet implement SSA
// (kubernetes/kubernetes#115598).
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
	for _, o := range objs {
		// Capture and clear status before Create — the API server
		// rejects status on Create, so we set it via a follow-up
		// Status().Update.
		var (
			wpStatus    lennyv1.SandboxWarmPoolStatus
			tmplStatus  lennyv1.SandboxTemplateStatus
			sbStatus    lennyv1.SandboxStatus
			updateAfter func()
		)
		switch v := o.(type) {
		case *lennyv1.SandboxWarmPool:
			wpStatus = v.Status
			v.Status = lennyv1.SandboxWarmPoolStatus{}
			updateAfter = func() {
				v.Status = wpStatus
				if err := applyStatus(ctx, c, v); err != nil {
					t.Fatalf("set status SandboxWarmPool %s: %v", v.Name, err)
				}
			}
		case *lennyv1.SandboxTemplate:
			tmplStatus = v.Status
			v.Status = lennyv1.SandboxTemplateStatus{}
			updateAfter = func() {
				v.Status = tmplStatus
				if err := applyStatus(ctx, c, v); err != nil {
					t.Fatalf("set status SandboxTemplate %s: %v", v.Name, err)
				}
			}
		case *lennyv1.Sandbox:
			sbStatus = v.Status
			v.Status = lennyv1.SandboxStatus{}
			updateAfter = func() {
				if sbStatus.Phase == "" {
					return
				}
				v.Status = sbStatus
				if err := applyStatus(ctx, c, v); err != nil {
					t.Fatalf("set status Sandbox %s: %v", v.Name, err)
				}
			}
		}
		if err := c.Create(ctx, o); err != nil {
			t.Fatalf("create %T %s: %v", o, o.GetName(), err)
		}
		if updateAfter != nil {
			updateAfter()
		}
	}
	return c
}

func template() *lennyv1.SandboxTemplate {
	return &lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: testPool, Namespace: testNS},
		Spec: lennyv1.SandboxTemplateSpec{
			RuntimeRef:       "claude-code",
			IsolationProfile: "sandboxed",
			DeliveryMode:     "proxy",
		},
	}
}

func pool(minWarm, maxWarm int32) *lennyv1.SandboxWarmPool {
	return &lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: testPool, Namespace: testNS, Generation: 1},
		Spec: lennyv1.SandboxWarmPoolSpec{
			TemplateRef: testPool,
			MinWarm:     minWarm,
			MaxWarm:     maxWarm,
		},
	}
}

func idleSandbox(name string) *lennyv1.Sandbox {
	return &lennyv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNS,
			Labels:    map[string]string{warmpool.LabelPool: testPool},
		},
		Status: lennyv1.SandboxStatus{Phase: "idle"},
	}
}

func reconcile(t *testing.T, c client.Client, s *runtime.Scheme) {
	t.Helper()
	r := &warmpool.Reconciler{Client: c, Scheme: s}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: testNS, Name: testPool},
	}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
}

// reconcilerWithEvents returns a Reconciler wired with em for the §4.0
// pool_state_changed emit path.
func reconcilerWithEvents(c client.Client, s *runtime.Scheme, em warmpool.EventEmitter) *warmpool.Reconciler {
	return &warmpool.Reconciler{Client: c, Scheme: s, Events: em}
}

// reconcileWith advances a pre-built reconciler one pass.
func reconcileWith(t *testing.T, r *warmpool.Reconciler) {
	t.Helper()
	if _, err := r.Reconcile(context.Background(), reconcileRequest()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
}

// reconcileRequest is the canonical reconcile.Request for the test pool.
func reconcileRequest() ctrl.Request {
	return ctrl.Request{NamespacedName: client.ObjectKey{Namespace: testNS, Name: testPool}}
}

// testContext returns the background context the warmpool tests pass to
// reconcile and client calls.
func testContext() context.Context { return context.Background() }

func poolSandboxes(t *testing.T, c client.Client) []lennyv1.Sandbox {
	t.Helper()
	var l lennyv1.SandboxList
	if err := c.List(context.Background(), &l,
		client.InNamespace(testNS),
		client.MatchingLabels{warmpool.LabelPool: testPool}); err != nil {
		t.Fatalf("list sandboxes: %v", err)
	}
	return l.Items
}

func getPool(t *testing.T, c client.Client) lennyv1.SandboxWarmPool {
	t.Helper()
	var p lennyv1.SandboxWarmPool
	if err := c.Get(context.Background(),
		client.ObjectKey{Namespace: testNS, Name: testPool}, &p); err != nil {
		t.Fatalf("get pool: %v", err)
	}
	return p
}

func getTemplate(t *testing.T, c client.Client) lennyv1.SandboxTemplate {
	t.Helper()
	var tm lennyv1.SandboxTemplate
	if err := c.Get(context.Background(),
		client.ObjectKey{Namespace: testNS, Name: testPool}, &tm); err != nil {
		t.Fatalf("get template: %v", err)
	}
	return tm
}

func warmingUpCondition(t *testing.T, tm lennyv1.SandboxTemplate) metav1.Condition {
	t.Helper()
	for _, c := range tm.Status.Conditions {
		if c.Type == "PoolWarmingUp" {
			return c
		}
	}
	t.Fatalf("template carries no PoolWarmingUp condition; conditions = %+v", tm.Status.Conditions)
	return metav1.Condition{}
}

func TestReconcileCreatesUpToMinWarm(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s, template(), pool(3, 10))

	reconcile(t, c, s)

	sandboxes := poolSandboxes(t, c)
	if len(sandboxes) != 3 {
		t.Fatalf("created %d sandboxes, want 3", len(sandboxes))
	}
	for _, sb := range sandboxes {
		if sb.Labels[warmpool.LabelPool] != testPool {
			t.Errorf("sandbox %s missing pool label", sb.Name)
		}
		if sb.Labels[warmpool.LabelManaged] != "true" {
			t.Errorf("sandbox %s missing managed label", sb.Name)
		}
		if sb.Spec.RuntimeRef != "claude-code" {
			t.Errorf("sandbox %s runtimeRef = %q, want claude-code", sb.Name, sb.Spec.RuntimeRef)
		}
		if sb.Spec.PoolRef != testPool {
			t.Errorf("sandbox %s poolRef = %q, want %q", sb.Name, sb.Spec.PoolRef, testPool)
		}
		if sb.Spec.IsolationProfile != "sandboxed" {
			t.Errorf("sandbox %s isolationProfile = %q, want sandboxed", sb.Name, sb.Spec.IsolationProfile)
		}
		if len(sb.OwnerReferences) != 1 ||
			sb.OwnerReferences[0].Kind != "SandboxWarmPool" ||
			sb.OwnerReferences[0].Name != testPool {
			t.Errorf("sandbox %s owner references = %+v, want one SandboxWarmPool/%s", sb.Name, sb.OwnerReferences, testPool)
		}
	}

	p := getPool(t, c)
	if p.Status.WarmCount != 3 || p.Status.ReadyCount != 0 {
		t.Errorf("status warm/ready = %d/%d, want 3/0 (fresh pods are warming)", p.Status.WarmCount, p.Status.ReadyCount)
	}
	if p.Status.ObservedGeneration != 1 {
		t.Errorf("status observedGeneration = %d, want 1", p.Status.ObservedGeneration)
	}
}

func TestReconcileAtTargetCreatesNothing(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s, template(), pool(3, 10),
		idleSandbox("sb-a"), idleSandbox("sb-b"), idleSandbox("sb-c"))

	reconcile(t, c, s)

	if got := len(poolSandboxes(t, c)); got != 3 {
		t.Fatalf("have %d sandboxes, want 3 (no new pods)", got)
	}
	p := getPool(t, c)
	if p.Status.WarmCount != 3 || p.Status.ReadyCount != 3 {
		t.Errorf("status warm/ready = %d/%d, want 3/3", p.Status.WarmCount, p.Status.ReadyCount)
	}
}

func TestReconcileBelowTargetCreatesGap(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s, template(), pool(3, 10), idleSandbox("sb-a"))

	reconcile(t, c, s)

	if got := len(poolSandboxes(t, c)); got != 3 {
		t.Fatalf("have %d sandboxes, want 3 (1 existing + 2 created)", got)
	}
	p := getPool(t, c)
	if p.Status.WarmCount != 3 || p.Status.ReadyCount != 1 {
		t.Errorf("status warm/ready = %d/%d, want 3/1", p.Status.WarmCount, p.Status.ReadyCount)
	}
}

func TestReconcileOverTargetDrainsExcess(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s, template(), pool(2, 10),
		idleSandbox("sb-a"), idleSandbox("sb-b"), idleSandbox("sb-c"),
		idleSandbox("sb-d"), idleSandbox("sb-e"))

	reconcile(t, c, s)

	sandboxes := poolSandboxes(t, c)
	if len(sandboxes) != 5 {
		t.Fatalf("have %d sandboxes, want 5 (drain does not delete)", len(sandboxes))
	}
	draining, idle := 0, 0
	for _, sb := range sandboxes {
		switch sb.Status.Phase {
		case "draining":
			draining++
		case "idle":
			idle++
		}
	}
	if draining != 3 || idle != 2 {
		t.Errorf("draining/idle = %d/%d, want 3/2", draining, idle)
	}
	p := getPool(t, c)
	if p.Status.WarmCount != 2 || p.Status.ReadyCount != 2 {
		t.Errorf("status warm/ready = %d/%d, want 2/2", p.Status.WarmCount, p.Status.ReadyCount)
	}
}

func TestReconcileMaxWarmCapsCreation(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s, template(), pool(10, 4))

	reconcile(t, c, s)

	if got := len(poolSandboxes(t, c)); got != 4 {
		t.Fatalf("created %d sandboxes, want 4 (maxWarm cap)", got)
	}
}

func TestReconcileMissingTemplateErrors(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s, pool(3, 10)) // no SandboxTemplate

	r := &warmpool.Reconciler{Client: c, Scheme: s}
	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: testNS, Name: testPool},
	})
	if err == nil {
		t.Fatal("Reconcile with a missing SandboxTemplate should error")
	}
	if got := len(poolSandboxes(t, c)); got != 0 {
		t.Errorf("created %d sandboxes despite the missing template, want 0", got)
	}
}

func TestReconcilePoolNotFound(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s)

	r := &warmpool.Reconciler{Client: c, Scheme: s}
	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: testNS, Name: testPool},
	})
	if err != nil {
		t.Fatalf("Reconcile of a deleted pool should not error: %v", err)
	}
	if res.Requeue || res.RequeueAfter != 0 {
		t.Errorf("Reconcile of a deleted pool should not requeue, got %+v", res)
	}
}

func TestReconcileSetsPoolWarmingUpCondition(t *testing.T) {
	t.Run("provisioning while fresh pods warm", func(t *testing.T) {
		s := newScheme(t)
		c := newClient(t, s, template(), pool(3, 10))

		reconcile(t, c, s)

		cond := warmingUpCondition(t, getTemplate(t, c))
		if cond.Status != metav1.ConditionTrue || cond.Reason != "Provisioning" {
			t.Errorf("condition = %s/%s, want True/Provisioning", cond.Status, cond.Reason)
		}
	})

	t.Run("available once idle pods exist", func(t *testing.T) {
		s := newScheme(t)
		c := newClient(t, s, template(), pool(3, 10),
			idleSandbox("sb-a"), idleSandbox("sb-b"), idleSandbox("sb-c"))

		reconcile(t, c, s)

		cond := warmingUpCondition(t, getTemplate(t, c))
		if cond.Status != metav1.ConditionFalse || cond.Reason != "Available" {
			t.Errorf("condition = %s/%s, want False/Available", cond.Status, cond.Reason)
		}
	})

	t.Run("drained when a hot pool has no pods at all", func(t *testing.T) {
		s := newScheme(t)
		// minWarm 3 with maxWarm 0: the ceiling caps creation at zero,
		// leaving the hot pool with no idle and no warming pods.
		c := newClient(t, s, template(), pool(3, 0))

		reconcile(t, c, s)

		if got := len(poolSandboxes(t, c)); got != 0 {
			t.Fatalf("created %d sandboxes, want 0 (maxWarm 0 caps creation)", got)
		}
		cond := warmingUpCondition(t, getTemplate(t, c))
		if cond.Status != metav1.ConditionFalse || cond.Reason != "Drained" {
			t.Errorf("condition = %s/%s, want False/Drained", cond.Status, cond.Reason)
		}
	})
}

func TestReconcileIsIdempotent(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s, template(), pool(3, 10))

	reconcile(t, c, s)
	first := len(poolSandboxes(t, c))
	reconcile(t, c, s)
	second := len(poolSandboxes(t, c))

	if first != 3 || second != 3 {
		t.Errorf("sandbox count drifted across reconciles: first=%d second=%d, want 3/3", first, second)
	}
}

// spec: 12.9.8
// diagnosis: the §12.9.8 egress-capture annotation lives on the
// SandboxTemplate; the WarmPoolController propagates it to every
// Sandbox it creates so the Sandbox reconciler can read it on
// createPod. Other template annotations stay on the template.
func TestReconcilePropagatesEgressCaptureAnnotation(t *testing.T) {
	s := newScheme(t)
	tmpl := template()
	tmpl.Annotations = map[string]string{
		"lenny.dev/test-egress-capture-upstream": "api.openai.com:443",
		"lenny.dev/unrelated":                    "ignore-me",
	}
	c := newClient(t, s, tmpl, pool(1, 1))

	reconcile(t, c, s)

	sandboxes := poolSandboxes(t, c)
	if len(sandboxes) != 1 {
		t.Fatalf("created %d sandboxes, want 1", len(sandboxes))
	}
	got := sandboxes[0].Annotations
	if got["lenny.dev/test-egress-capture-upstream"] != "api.openai.com:443" {
		t.Errorf("sandbox annotations = %v, want egress-capture upstream propagated", got)
	}
	if _, leaked := got["lenny.dev/unrelated"]; leaked {
		t.Errorf("sandbox annotations = %v; unrelated annotation should not propagate", got)
	}
}
