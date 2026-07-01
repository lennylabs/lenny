// SPDX-License-Identifier: MIT

//go:build component

package controllers_test

import (
	"context"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	runtimecontroller "github.com/lennylabs/lenny/pkg/controller/runtime"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

const digest64 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// TestRuntimeController_spec_5_1 drives the §5.1 RuntimeReconciler
// against an envtest API server with the lenny.dev CRDs installed and an
// in-memory runtime registry injected as the Store. It exercises the
// real Runtime CRD schema, the status subresource, and the finalizer
// path the fake-client unit tests cannot.
//
// spec: §5.1 — the controller mirrors a Runtime CRD into the gateway
// registry, sets the Registered condition, preserves admin-configured
// fields across updates, and soft-deletes on CRD deletion.
// diagnosis: a failure means the RuntimeReconciler does not faithfully
// mirror the Runtime CRD into the gateway registry, mismanages the
// Registered condition or finalizer, or loses admin-configured fields
// across reconciles.
func TestRuntimeController_spec_5_1(t *testing.T) {
	env := envtest.Start(t)

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(lennyv1.AddToScheme(scheme))

	c, err := client.New(env.RESTConfig(), client.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	ctx := context.Background()
	store := runtimestore.NewMemory()
	r := &runtimecontroller.Reconciler{Client: c, Scheme: scheme, Store: store}

	// reconcile drives a few passes so the finalizer-add pass and the
	// mirror pass both run (the add-finalizer pass returns early).
	reconcile := func(name string) {
		t.Helper()
		req := ctrl.Request{NamespacedName: client.ObjectKey{Name: name}}
		for i := 0; i < 3; i++ {
			if _, err := r.Reconcile(ctx, req); err != nil {
				t.Fatalf("Reconcile %q pass %d: %v", name, i, err)
			}
		}
	}

	t.Run("create mirrors and sets Registered", func(t *testing.T) {
		const name = "langgraph-runtime"
		mustCreate(t, ctx, c, &lennyv1.Runtime{
			ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{"team": "platform"}},
			Spec: lennyv1.RuntimeSpec{
				Type:               "agent",
				Image:              "registry.example.com/langgraph@sha256:" + digest64,
				IntegrationLevel:   "full",
				SupportedProviders: []string{"anthropic_direct"},
			},
		})
		reconcile(name)

		got, err := store.Get(ctx, name)
		if err != nil {
			t.Fatalf("store.Get: %v", err)
		}
		if got.Type != runtimestore.TypeAgent || got.IntegrationLevel != runtimestore.IntegrationLevelFull {
			t.Errorf("mirrored runtime = %+v", got)
		}
		if got.ExecutionMode != runtimestore.ExecutionModeSession {
			t.Errorf("execution mode = %q, want defaulted session", got.ExecutionMode)
		}
		if got.Labels["team"] != "platform" {
			t.Errorf("labels = %v, want team=platform", got.Labels)
		}

		var live lennyv1.Runtime
		if err := c.Get(ctx, client.ObjectKey{Name: name}, &live); err != nil {
			t.Fatalf("get runtime: %v", err)
		}
		cond := meta.FindStatusCondition(live.Status.Conditions, "Registered")
		if cond == nil || cond.Status != metav1.ConditionTrue {
			t.Fatalf("Registered condition = %+v, want True", cond)
		}
		if live.Status.ObservedGeneration != live.Generation {
			t.Errorf("observedGeneration = %d, want %d", live.Status.ObservedGeneration, live.Generation)
		}
	})

	t.Run("update preserves admin-configured fields", func(t *testing.T) {
		const name = "scanner"
		mustCreate(t, ctx, c, &lennyv1.Runtime{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec:       lennyv1.RuntimeSpec{Type: "agent", Image: "img@sha256:" + digest64, IntegrationLevel: "basic"},
		})
		reconcile(name)
		// Simulate an admin-API layer adding a field the CRD does not model.
		if _, err := store.Update(ctx, name, func(rt *runtimestore.Runtime) error {
			rt.Description = "configured via admin API"
			return nil
		}); err != nil {
			t.Fatalf("seed admin field: %v", err)
		}
		// Bump the CRD's integration level and re-reconcile.
		var live lennyv1.Runtime
		if err := c.Get(ctx, client.ObjectKey{Name: name}, &live); err != nil {
			t.Fatalf("get: %v", err)
		}
		live.Spec.IntegrationLevel = "standard"
		if err := c.Update(ctx, &live); err != nil {
			t.Fatalf("update CRD: %v", err)
		}
		reconcile(name)

		got, err := store.Get(ctx, name)
		if err != nil {
			t.Fatalf("store.Get: %v", err)
		}
		if got.IntegrationLevel != runtimestore.IntegrationLevelStandard {
			t.Errorf("integrationLevel = %q, want standard (CRD-owned field updated)", got.IntegrationLevel)
		}
		if got.Description != "configured via admin API" {
			t.Errorf("description = %q, admin-configured field wiped on update", got.Description)
		}
	})

	t.Run("delete soft-deletes the registry row", func(t *testing.T) {
		const name = "ephemeral"
		mustCreate(t, ctx, c, &lennyv1.Runtime{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec:       lennyv1.RuntimeSpec{Type: "agent", Image: "img@sha256:" + digest64, IntegrationLevel: "basic"},
		})
		reconcile(name)

		var live lennyv1.Runtime
		if err := c.Get(ctx, client.ObjectKey{Name: name}, &live); err != nil {
			t.Fatalf("get: %v", err)
		}
		if err := c.Delete(ctx, &live); err != nil {
			t.Fatalf("delete: %v", err)
		}
		reconcile(name)

		got, err := store.Get(ctx, name)
		if err != nil {
			t.Fatalf("store.Get after delete: %v", err)
		}
		if got.IsActive() {
			t.Errorf("runtime still active after CRD delete; DeletedAt=%v", got.DeletedAt)
		}
		// Finalizer removed → the object is gone from the API server.
		if err := c.Get(ctx, client.ObjectKey{Name: name}, &lennyv1.Runtime{}); !apierrors.IsNotFound(err) {
			t.Errorf("runtime still present after finalizer release: err=%v", err)
		}
	})

	t.Run("embedded runtime with requireSoPeercred false is rejected and not mirrored", func(t *testing.T) {
		// spec: §4.7 / §4.1 — requireSoPeercred: false (nonce-only mode)
		// is valid only on the sidecar deployment model. The embedded
		// model is a single trusted process with no adapter-agent socket
		// boundary, so the RuntimeReconciler marks it Registered=False
		// (InvalidRuntime) and does not mirror it into the registry.
		const name = "embedded-nonce-only"
		falseVal := false
		mustCreate(t, ctx, c, &lennyv1.Runtime{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: lennyv1.RuntimeSpec{
				Type:              "agent",
				Image:             "img@sha256:" + digest64,
				IntegrationLevel:  "basic",
				DeploymentModel:   "embedded",
				RequireSoPeercred: &falseVal,
			},
		})
		reconcile(name)

		if _, err := store.Get(ctx, name); err == nil {
			t.Error("embedded+requireSoPeercred:false runtime was mirrored into the registry")
		}
		var live lennyv1.Runtime
		if err := c.Get(ctx, client.ObjectKey{Name: name}, &live); err != nil {
			t.Fatalf("get: %v", err)
		}
		cond := meta.FindStatusCondition(live.Status.Conditions, "Registered")
		if cond == nil || cond.Status != metav1.ConditionFalse {
			t.Fatalf("Registered condition = %+v, want False", cond)
		}
		if cond.Reason != "InvalidRuntime" {
			t.Errorf("Registered reason = %q, want InvalidRuntime", cond.Reason)
		}
		if cond.Message != "requireSoPeercred: false is only valid on deploymentModel: sidecar runtimes" {
			t.Errorf("Registered message = %q, want the §4.7 nonce-only embedded message", cond.Message)
		}
	})

	t.Run("sidecar runtime with requireSoPeercred false mirrors with the field propagated", func(t *testing.T) {
		// spec: §4.7 / §4.2 — a deploymentModel: sidecar runtime carrying
		// requireSoPeercred: false mirrors normally, with the explicit
		// false propagated CRD-to-store so the §5.3 pool admission gate
		// sees the nonce-only posture.
		const name = "sidecar-nonce-only"
		falseVal := false
		mustCreate(t, ctx, c, &lennyv1.Runtime{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: lennyv1.RuntimeSpec{
				Type:              "agent",
				Image:             "img@sha256:" + digest64,
				IntegrationLevel:  "basic",
				DeploymentModel:   "sidecar",
				RequireSoPeercred: &falseVal,
			},
		})
		reconcile(name)

		got, err := store.Get(ctx, name)
		if err != nil {
			t.Fatalf("store.Get: %v", err)
		}
		if got.RequireSoPeercred == nil || *got.RequireSoPeercred {
			t.Errorf("mirrored RequireSoPeercred = %v, want explicit false (nonce-only)", got.RequireSoPeercred)
		}
		var live lennyv1.Runtime
		if err := c.Get(ctx, client.ObjectKey{Name: name}, &live); err != nil {
			t.Fatalf("get: %v", err)
		}
		cond := meta.FindStatusCondition(live.Status.Conditions, "Registered")
		if cond == nil || cond.Status != metav1.ConditionTrue {
			t.Fatalf("Registered condition = %+v, want True", cond)
		}
	})

	t.Run("sidecar runtime with requireSoPeercred unset mirrors the default true", func(t *testing.T) {
		// spec: §4.7 — an unset field resolves to the default true through
		// the CRD-to-store mirror, so the registry row keeps the
		// SO_PEERCRED self-test mandatory.
		const name = "sidecar-default"
		mustCreate(t, ctx, c, &lennyv1.Runtime{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: lennyv1.RuntimeSpec{
				Type:             "agent",
				Image:            "img@sha256:" + digest64,
				IntegrationLevel: "basic",
				DeploymentModel:  "sidecar",
			},
		})
		reconcile(name)

		got, err := store.Get(ctx, name)
		if err != nil {
			t.Fatalf("store.Get: %v", err)
		}
		if got.RequireSoPeercred == nil || !*got.RequireSoPeercred {
			t.Errorf("mirrored RequireSoPeercred = %v, want default true", got.RequireSoPeercred)
		}
	})

	t.Run("invalid name marks Registered False without requeue", func(t *testing.T) {
		// A dotted name is a valid DNS-1123 subdomain (so the API server
		// accepts it) but violates the §5.1 registry-name pattern.
		const name = "bad.name"
		mustCreate(t, ctx, c, &lennyv1.Runtime{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec:       lennyv1.RuntimeSpec{Type: "agent", Image: "img@sha256:" + digest64, IntegrationLevel: "basic"},
		})
		reconcile(name)

		if _, err := store.Get(ctx, name); err == nil {
			t.Error("invalid-name runtime was mirrored into the registry")
		}
		var live lennyv1.Runtime
		if err := c.Get(ctx, client.ObjectKey{Name: name}, &live); err != nil {
			t.Fatalf("get: %v", err)
		}
		cond := meta.FindStatusCondition(live.Status.Conditions, "Registered")
		if cond == nil || cond.Status != metav1.ConditionFalse {
			t.Fatalf("Registered condition = %+v, want False", cond)
		}
	})
}
