// SPDX-License-Identifier: MIT

//go:build component

package controllers_test

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

func mustCreate(t *testing.T, ctx context.Context, c client.Client, obj client.Object) {
	t.Helper()
	if err := c.Create(ctx, obj); err != nil {
		t.Fatalf("create %T %q: %v", obj, obj.GetName(), err)
	}
}

// spec: 12.2.4 (the Warm Pool Controller reconciles a SandboxWarmPool
//
//	toward its minWarm target)
//
// diagnosis: The WarmPoolController failed to converge a pool against
//
//	a real API server. Either the lenny.dev CRD schemas
//	rejected a controller write, the status subresource is
//	misconfigured, or owner references are not stamped.
//
// TestWarmPoolController runs the reconciler against an envtest API
// server with the lenny.dev CRDs installed. Unlike the fake-client
// unit tests in pkg/controller/warmpool, this exercises the real CRD
// schema validation, the status subresource, GenerateName, and owner
// references.
func TestWarmPoolController(t *testing.T) {
	env := envtest.Start(t)

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(lennyv1.AddToScheme(scheme))

	c, err := client.New(env.RESTConfig(), client.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	ctx := context.Background()

	const ns = "lenny-agents"
	const poolName = "claude-worker-small"

	mustCreate(t, ctx, c, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: ns},
	})
	mustCreate(t, ctx, c, &lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: poolName, Namespace: ns},
		Spec: lennyv1.SandboxTemplateSpec{
			RuntimeRef:       "claude-code",
			IsolationProfile: "sandboxed",
		},
	})
	mustCreate(t, ctx, c, &lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: poolName, Namespace: ns},
		Spec: lennyv1.SandboxWarmPoolSpec{
			TemplateRef: poolName,
			MinWarm:     3,
			MaxWarm:     10,
		},
	})

	r := &warmpool.Reconciler{Client: c, Scheme: scheme}
	req := ctrl.Request{NamespacedName: client.ObjectKey{Namespace: ns, Name: poolName}}
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	listPool := func() []lennyv1.Sandbox {
		t.Helper()
		var l lennyv1.SandboxList
		if err := c.List(ctx, &l, client.InNamespace(ns),
			client.MatchingLabels{warmpool.LabelPool: poolName}); err != nil {
			t.Fatalf("list sandboxes: %v", err)
		}
		return l.Items
	}

	sandboxes := listPool()
	if len(sandboxes) != 3 {
		t.Fatalf("reconcile created %d sandboxes, want 3", len(sandboxes))
	}
	for _, sb := range sandboxes {
		if sb.Spec.RuntimeRef != "claude-code" {
			t.Errorf("sandbox %s runtimeRef = %q, want claude-code", sb.Name, sb.Spec.RuntimeRef)
		}
		if len(sb.OwnerReferences) != 1 ||
			sb.OwnerReferences[0].Kind != "SandboxWarmPool" ||
			sb.OwnerReferences[0].Name != poolName {
			t.Errorf("sandbox %s owner references = %+v, want one SandboxWarmPool/%s", sb.Name, sb.OwnerReferences, poolName)
		}
	}

	var pool lennyv1.SandboxWarmPool
	if err := c.Get(ctx, req.NamespacedName, &pool); err != nil {
		t.Fatalf("get pool: %v", err)
	}
	if pool.Status.WarmCount != 3 {
		t.Errorf("pool status warmCount = %d, want 3", pool.Status.WarmCount)
	}

	var tmpl lennyv1.SandboxTemplate
	if err := c.Get(ctx, req.NamespacedName, &tmpl); err != nil {
		t.Fatalf("get template: %v", err)
	}
	var foundCondition bool
	for _, cond := range tmpl.Status.Conditions {
		if cond.Type == "PoolWarmingUp" {
			foundCondition = true
			if cond.Reason != "Provisioning" {
				t.Errorf("PoolWarmingUp reason = %q, want Provisioning", cond.Reason)
			}
		}
	}
	if !foundCondition {
		t.Errorf("template carries no PoolWarmingUp condition after reconcile")
	}

	// A second reconcile must not create more pods: the three created
	// Sandboxes are observed as warming and already satisfy minWarm.
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("second Reconcile: %v", err)
	}
	if got := len(listPool()); got != 3 {
		t.Errorf("after second reconcile have %d sandboxes, want 3 (idempotent)", got)
	}
}
