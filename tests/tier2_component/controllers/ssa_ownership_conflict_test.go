// SPDX-License-Identifier: MIT

//go:build component

package controllers_test

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/admission/ownership"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

// TestSSARejectsCrossControllerFieldWrite drives the §4.6.3 Server-Side
// Apply field-ownership boundary against a real kube-apiserver rather than
// against the in-process ownership matrix in pkg/admission/ownership. The
// spec names SSA field-manager conflict as the primary enforcement
// mechanism: "If a controller attempts to update a field owned by another
// manager, the API server returns a conflict error (HTTP 409) without
// applying the update. This is the primary enforcement mechanism — it is
// enforced by the API server itself, without any additional controller
// logic."
//
// The ownership table assigns SandboxWarmPool.status.* (excluding the
// status.sdkWarmCircuitBreaker carve-out) to the WarmPoolController. The
// test applies status.warmCount as field manager lenny-warm-pool-controller,
// then applies the same field as lenny-pool-scaling-controller with a
// different value and no force-conflicts, and asserts the second apply is
// rejected with an IsConflict (HTTP 409) error and that the field retains
// the first writer's value.
//
// diagnosis: a failure means the API server did not enforce §4.6.3 field
// ownership through SSA field managers. Either the second manager's apply
// succeeded (ownership boundary not enforced — the primary production
// mechanism is broken), the error was not an HTTP 409 conflict (the
// controllers' SSA-conflict retry policy keys off IsConflict and would
// mishandle the response), or the conflicting apply silently mutated the
// WarmPoolController-owned field despite the rejection.
//
// spec: §4.6.3 (SSA enforcement — cross-manager write on a WarmPoolController-
// owned SandboxWarmPool.status field is rejected with HTTP 409 without
// mutating the field; SandboxWarmPool.status.* owned by WarmPoolController).
func TestSSARejectsCrossControllerFieldWrite(t *testing.T) {
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

	mustCreate(t, ctx, c, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}})
	mustCreate(t, ctx, c, &lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: poolName, Namespace: ns},
		Spec: lennyv1.SandboxWarmPoolSpec{
			TemplateRef: poolName,
			MinWarm:     3,
			MaxWarm:     10,
		},
	})

	key := client.ObjectKey{Namespace: ns, Name: poolName}

	// statusApply builds a status-subresource SSA patch that asserts
	// ownership of status.warmCount with the given value. TypeMeta is
	// mandatory on an Apply patch so the API server can route it.
	statusApply := func(warm int32) *lennyv1.SandboxWarmPool {
		return &lennyv1.SandboxWarmPool{
			TypeMeta: metav1.TypeMeta{
				APIVersion: lennyv1.GroupVersion.String(),
				Kind:       "SandboxWarmPool",
			},
			ObjectMeta: metav1.ObjectMeta{Name: poolName, Namespace: ns},
			Status:     lennyv1.SandboxWarmPoolStatus{WarmCount: warm},
		}
	}

	const warmPoolValue int32 = 3

	// First writer: the WarmPoolController claims status.warmCount. This
	// establishes lenny-warm-pool-controller as the field manager that owns
	// the field per the §4.6.3 ownership table.
	if err := c.Status().Patch(ctx, statusApply(warmPoolValue), client.Apply,
		client.FieldOwner(string(ownership.WarmPoolController))); err != nil {
		t.Fatalf("WarmPoolController SSA apply of status.warmCount: %v", err)
	}

	// Second writer: the PoolScalingController attempts to write the same
	// field with a different value and no force-conflicts. The API server
	// must reject this with an HTTP 409 conflict because the field is owned
	// by lenny-warm-pool-controller.
	const scalingValue int32 = 7
	err = c.Status().Patch(ctx, statusApply(scalingValue), client.Apply,
		client.FieldOwner(string(ownership.PoolScalingController)))
	if err == nil {
		t.Fatalf("PoolScalingController SSA apply of a WarmPoolController-owned field succeeded; §4.6.3 SSA ownership boundary not enforced")
	}
	if !apierrors.IsConflict(err) {
		t.Fatalf("cross-manager SSA apply returned %v (type %T); want an IsConflict HTTP 409 error", err, err)
	}

	// The rejected apply must not have mutated the field: it retains the
	// WarmPoolController's value.
	var live lennyv1.SandboxWarmPool
	if err := c.Get(ctx, key, &live); err != nil {
		t.Fatalf("get SandboxWarmPool after rejected apply: %v", err)
	}
	if live.Status.WarmCount != warmPoolValue {
		t.Fatalf("status.warmCount = %d after a rejected cross-manager apply, want %d (the rejected write must not mutate the field)",
			live.Status.WarmCount, warmPoolValue)
	}
}
