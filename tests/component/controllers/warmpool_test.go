// SPDX-License-Identifier: MIT

//go:build component

package controllers_test

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
)

func mustCreate(t *testing.T, ctx context.Context, c client.Client, obj client.Object) {
	t.Helper()
	if err := c.Create(ctx, obj); err != nil {
		t.Fatalf("create %T %q: %v", obj, obj.GetName(), err)
	}
}

// spec: 12.2.4 (the Warm Pool Controller reconciles a PodSet
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
//
// spec: 12.2.4
// diagnosis: the WarmPoolController in pkg/controller/warmpool failed
// to converge a PodSet toward its minWarm target against a
// real API server. Either the lenny.dev CRD schema rejected a
// controller write, the status subresource is misconfigured, owner
// references are not stamped, or reconcile is not idempotent.
func TestWarmPoolController(t *testing.T) {
	env := sharedEnv.Start(t)

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(lennyv1.AddToScheme(scheme))

	c, err := client.New(env.RESTConfig(), client.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	ctx := context.Background()

	const poolName = "claude-worker-small"

	ns := testNamespaces.Create(t, c)
	mustCreate(t, ctx, c, &lennyv1.PodSet{
		ObjectMeta: metav1.ObjectMeta{Name: poolName, Namespace: ns},
		Spec: lennyv1.PodSetSpec{
			RuntimeRef:       "claude-code",
			IsolationProfile: "sandboxed",
			MinWarm:          3,
			MaxWarm:          10,
		},
	})

	r := &warmpool.Reconciler{Client: c, Scheme: scheme}
	req := ctrl.Request{NamespacedName: client.ObjectKey{Namespace: ns, Name: poolName}}
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	listPool := func() []lennyv1.AgentPod {
		t.Helper()
		var l lennyv1.AgentPodList
		if err := c.List(ctx, &l, client.InNamespace(ns),
			client.MatchingLabels{warmpool.LabelPool: poolName}); err != nil {
			t.Fatalf("list sandboxes: %v", err)
		}
		return l.Items
	}

	agentPods := listPool()
	if len(agentPods) != 3 {
		t.Fatalf("reconcile created %d agentpods, want 3", len(agentPods))
	}
	for _, sb := range agentPods {
		if sb.Spec.RuntimeRef != "claude-code" {
			t.Errorf("sandbox %s runtimeRef = %q, want claude-code", sb.Name, sb.Spec.RuntimeRef)
		}
		if len(sb.OwnerReferences) != 1 ||
			sb.OwnerReferences[0].Kind != "PodSet" ||
			sb.OwnerReferences[0].Name != poolName {
			t.Errorf("sandbox %s owner references = %+v, want one PodSet/%s", sb.Name, sb.OwnerReferences, poolName)
		}
	}

	var pool lennyv1.PodSet
	if err := c.Get(ctx, req.NamespacedName, &pool); err != nil {
		t.Fatalf("get pool: %v", err)
	}
	if pool.Status.WarmCount != 3 {
		t.Errorf("pool status warmCount = %d, want 3", pool.Status.WarmCount)
	}

	var foundCondition bool
	for _, cond := range pool.Status.Conditions {
		if cond.Type == "PoolWarmingUp" {
			foundCondition = true
			if cond.Reason != "Provisioning" {
				t.Errorf("PoolWarmingUp reason = %q, want Provisioning", cond.Reason)
			}
		}
	}
	if !foundCondition {
		t.Errorf("PodSet carries no PoolWarmingUp condition after reconcile")
	}

	// A second reconcile must not create more pods: the three created
	// AgentPods are observed as warming and already satisfy minWarm.
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("second Reconcile: %v", err)
	}
	if got := len(listPool()); got != 3 {
		t.Errorf("after second reconcile have %d agentpods, want 3 (idempotent)", got)
	}
}
