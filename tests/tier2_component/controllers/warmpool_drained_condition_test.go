// SPDX-License-Identifier: MIT

//go:build component

package controllers_test

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/dynamic"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

// spec: 25.6 line 2956 (warmPoolStuckReplenish keys its dwell off the
// zero-in-flight state), 5.2 (SandboxTemplate.status conditions owned by the
// WarmPoolController).
//
// diagnosis: the WarmPoolController does not write the dedicated PoolDrained
// condition the doctor's warmPoolStuckReplenish dwell gate keys off, or the
// lenny.dev CRD schema rejects it. The doctor reads PoolDrained rather than
// the PoolWarmingUp condition because PoolWarmingUp's Available and Drained
// states share Status False, so meta.SetStatusCondition never refreshes its
// timestamp across that transition and the >5m dwell would read a stale entry
// time. This test confirms the controller emits PoolDrained onto the template
// status against a real apiserver (schema-validated, status-subresource
// write) in its False state while the pool is provisioning warm pods.
//
// TestWarmPoolDrainedConditionWritten drives the WarmPoolController against an
// envtest apiserver and asserts the PoolDrained condition is present on the
// SandboxTemplate. The False→True transition semantics are pinned by the
// in-package unit test TestPoolDrainedTracksEntryTransition.
func TestWarmPoolDrainedConditionWritten(t *testing.T) {
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
	const poolName = "echo-worker"

	mustCreate(t, ctx, c, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}})
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
			MinWarm:     2,
			MaxWarm:     10,
		},
	})

	r := &warmpool.Reconciler{Client: c, Scheme: scheme}
	req := ctrl.Request{NamespacedName: client.ObjectKey{Namespace: ns, Name: poolName}}

	// The pool creates warming pods, so it is Provisioning, not drained.
	// PoolDrained must be present and False.
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("Reconcile (provisioning): %v", err)
	}
	drained := drainedCondition(t, ctx, c, req.NamespacedName)
	if drained == nil {
		t.Fatalf("template carries no PoolDrained condition after reconcile")
	}
	if drained.Status != metav1.ConditionFalse {
		t.Fatalf("PoolDrained Status = %q while provisioning, want False", drained.Status)
	}
	if drained.LastTransitionTime.IsZero() {
		t.Errorf("PoolDrained condition carries no lastTransitionTime")
	}
}

// spec: 25.6 line 2956 (warmPoolStuckReplenish re-drive: "triggers controller
// to re-drive").
//
// diagnosis: the warmPoolStuckReplenish remediation's re-drive write does not
// register against a real apiserver. A direct .metadata.generation write is a
// server-side no-op (generation is server-managed and recomputed on update),
// so it emits no watch event and never re-drives the controller. An
// annotation write is honored, advancing .metadata.resourceVersion and
// emitting a watch Update the WarmPoolController wakes on. This test pins that
// distinction against the envtest apiserver so the fake-client generation
// persistence does not mask the no-op.
//
// TestWarmPoolReDriveWriteIsHonored confirms, against a real apiserver, that
// the re-drive annotation write the remediation performs advances
// resourceVersion, while a client-set .metadata.generation write does not.
func TestWarmPoolReDriveWriteIsHonored(t *testing.T) {
	env := envtest.Start(t)

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(lennyv1.AddToScheme(scheme))
	c, err := client.New(env.RESTConfig(), client.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	dyn, err := dynamic.NewForConfig(env.RESTConfig())
	if err != nil {
		t.Fatalf("dynamic.NewForConfig: %v", err)
	}
	ctx := context.Background()

	const ns = "lenny-agents"
	const poolName = "redrive-pool"
	mustCreate(t, ctx, c, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}})
	mustCreate(t, ctx, c, &lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: poolName, Namespace: ns},
		Spec:       lennyv1.SandboxWarmPoolSpec{TemplateRef: poolName, MinWarm: 1, MaxWarm: 2},
	})

	gvr := schema.GroupVersionResource{Group: "lenny.dev", Version: "v1alpha1", Resource: "sandboxwarmpools"}
	poolClient := dyn.Resource(gvr).Namespace(ns)

	before, err := poolClient.Get(ctx, poolName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get pool: %v", err)
	}
	rvBefore := before.GetResourceVersion()

	// A direct .metadata.generation write is a server-side no-op: the
	// apiserver strips and recomputes generation, so the stored object is
	// unchanged and resourceVersion does not advance.
	before.SetGeneration(before.GetGeneration() + 1)
	genWrite, err := poolClient.Update(ctx, before, metav1.UpdateOptions{})
	if err != nil {
		t.Fatalf("generation update: %v", err)
	}
	if genWrite.GetResourceVersion() != rvBefore {
		t.Errorf("a .metadata.generation write advanced resourceVersion (%s → %s): the fake-client "+
			"no-op assumption does not hold and a generation bump would re-drive; the remediation must "+
			"not rely on it", rvBefore, genWrite.GetResourceVersion())
	}

	// The re-drive annotation write is honored: it advances resourceVersion
	// and emits a watch Update the WarmPoolController (For(SandboxWarmPool))
	// wakes on, so the stalled pool is re-driven.
	patch := []byte(`{"metadata":{"annotations":{"lenny.dev/doctor-redrive":"2026-07-01T12:00:00Z"}}}`)
	afterAnn, err := poolClient.Patch(ctx, poolName, types.MergePatchType, patch, metav1.PatchOptions{})
	if err != nil {
		t.Fatalf("re-drive annotation patch: %v", err)
	}
	if afterAnn.GetResourceVersion() == rvBefore {
		t.Errorf("re-drive annotation write did not advance resourceVersion (%s); the apiserver did not "+
			"register the write, so the controller would not be re-driven", rvBefore)
	}
	if afterAnn.GetAnnotations()["lenny.dev/doctor-redrive"] != "2026-07-01T12:00:00Z" {
		t.Errorf("re-drive annotation not persisted: %v", afterAnn.GetAnnotations())
	}
}

// drainedCondition returns the PoolDrained condition the WarmPoolController
// wrote onto the SandboxTemplate, or nil when absent.
func drainedCondition(t *testing.T, ctx context.Context, c client.Client, key client.ObjectKey) *metav1.Condition {
	t.Helper()
	var tmpl lennyv1.SandboxTemplate
	if err := c.Get(ctx, key, &tmpl); err != nil {
		t.Fatalf("get template: %v", err)
	}
	for i := range tmpl.Status.Conditions {
		if tmpl.Status.Conditions[i].Type == "PoolDrained" {
			return &tmpl.Status.Conditions[i]
		}
	}
	return nil
}
