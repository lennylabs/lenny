// SPDX-License-Identifier: MIT

//go:build component

package controllers_test

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	nodev1 "k8s.io/api/node/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
	"github.com/lennylabs/lenny/pkg/embedded/stack"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

// TestEmbeddedEchoPoolDirectApplyWarmsPod_spec_17_4 exercises the §17.4
// no-Postgres pool-materialization path against a real kube-apiserver: the
// embedded bring-up applies the echo SandboxTemplate/SandboxWarmPool pair
// directly (no Postgres, no PoolScalingController), and the unconditionally-
// registered WarmPoolController reconciles the pool to a warm pod when the
// `runc` RuntimeClass the `standard` isolation profile resolves to is present.
//
// This replaces the proposal-0016 materialize test that drove the echo seed
// through poolscaling.Reconciler.Sync (the Postgres-gated PoolScalingController
// path the development profile no longer registers). Here the pool is applied
// directly through stack.ApplyEchoPoolFromConfigForTest, reproducing the
// bring-up's no-Postgres path, and the test asserts the SandboxWarmPool CRD is
// created without Postgres and the WarmPoolController warms a pod rather than
// marking the pool Degraded=True and suppressing pod creation (decision.Create=0).
//
// The negative leg pins the precondition: with the `runc` RuntimeClass absent
// the pool is marked Degraded and no pod is created, so the test confirms the
// dev profile's runtimeClasses.create=true is load-bearing rather than incidental.
//
// diagnosis: a failure means the directly-applied echo pool does not warm a pod.
// Either the SandboxWarmPool CRD apply did not create the CRD the gateway's
// ResolvePool lists (so §4.7 placement stays inert), or the WarmPoolController
// did not pre-warm the single hot-pool pod with the `runc` RuntimeClass present
// (the pool stayed Degraded and decision.Create was suppressed), so the
// no-Postgres pool-materialization path is broken.
//
// spec: §17.4 (Embedded Mode echo seed materializes a warm pod without
// Postgres), §4.6.2 (the bring-up applies the CRD pair directly), §5.2
// (single-pod hot pool, PoolWarmingUp), §5.3 (standard→runc RuntimeClass gate).
func TestEmbeddedEchoPoolDirectApplyWarmsPod_spec_17_4(t *testing.T) {
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
	mustCreate(t, ctx, c, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}})

	// The §4.6.2 direct apply: with no Postgres and no PoolScalingController,
	// the bring-up applies the echo SandboxTemplate/SandboxWarmPool pair
	// directly. This is the path the embedded stack runs at lenny up.
	if err := stack.ApplyEchoPoolFromConfig(ctx, env.RESTConfig(), ns); err != nil {
		t.Fatalf("direct apply echo pool: %v", err)
	}

	poolName := stack.EchoPoolName
	key := client.ObjectKey{Namespace: ns, Name: poolName}

	// The SandboxWarmPool CRD is created without Postgres: this is the CRD the
	// gateway's ResolvePool lists. minWarm = maxWarm = 1 for the single-pod
	// hot pool.
	var pool lennyv1.SandboxWarmPool
	if err := c.Get(ctx, key, &pool); err != nil {
		t.Fatalf("get directly-applied SandboxWarmPool: %v", err)
	}
	if pool.Spec.MinWarm != 1 || pool.Spec.MaxWarm != 1 {
		t.Errorf("applied pool minWarm/maxWarm = %d/%d, want 1/1 (single-pod hot pool)",
			pool.Spec.MinWarm, pool.Spec.MaxWarm)
	}
	var tmpl lennyv1.SandboxTemplate
	if err := c.Get(ctx, key, &tmpl); err != nil {
		t.Fatalf("get directly-applied SandboxTemplate: %v", err)
	}
	if tmpl.Spec.RuntimeRef != stack.EchoRuntimeName {
		t.Errorf("applied template runtimeRef = %q, want %q", tmpl.Spec.RuntimeRef, stack.EchoRuntimeName)
	}
	if tmpl.Spec.IsolationProfile != "standard" {
		t.Errorf("applied template isolationProfile = %q, want standard (§17.4 local fidelity)", tmpl.Spec.IsolationProfile)
	}

	// ----- Idempotent re-apply -----
	// A second lenny up re-runs the same dynamic-apply (server-side apply under
	// the lenny-embedded field manager). It must reconverge the live pair in
	// place rather than fail on AlreadyExists or duplicate the pool, so the
	// warm-up path is stable across bring-ups.
	if err := stack.ApplyEchoPoolFromConfig(ctx, env.RESTConfig(), ns); err != nil {
		t.Fatalf("re-apply echo pool: %v", err)
	}
	var pools lennyv1.SandboxWarmPoolList
	if err := c.List(ctx, &pools, client.InNamespace(ns)); err != nil {
		t.Fatalf("list warm pools after re-apply: %v", err)
	}
	if len(pools.Items) != 1 {
		t.Fatalf("re-apply produced %d warm pools, want exactly 1 (reconverged in place)", len(pools.Items))
	}

	// ----- Negative leg: no RuntimeClass -----
	// With the `runc` RuntimeClass absent, the WarmPoolController must mark the
	// pool Degraded and suppress pod creation (decision.Create=0), because
	// every create would only produce an API-server rejection. This pins the
	// dev profile's runtimeClasses.create=true as a load-bearing precondition.
	wpr := &warmpool.Reconciler{
		Client:         c,
		Scheme:         scheme,
		RuntimeClasses: warmpool.NewReaderRuntimeClassChecker(c),
	}
	if _, err := wpr.Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatalf("WarmPoolController Reconcile (no RuntimeClass): %v", err)
	}
	var sandboxes lennyv1.SandboxList
	if err := c.List(ctx, &sandboxes, client.InNamespace(ns),
		client.MatchingLabels{warmpool.LabelPool: poolName}); err != nil {
		t.Fatalf("list echo sandboxes (no RuntimeClass): %v", err)
	}
	if len(sandboxes.Items) != 0 {
		t.Fatalf("WarmPoolController created %d sandboxes with the runc RuntimeClass absent, want 0 (Degraded suppresses creation)",
			len(sandboxes.Items))
	}

	// ----- Positive leg: with the runc RuntimeClass -----
	// Install the `runc` RuntimeClass the `standard` isolation profile resolves
	// to, the object the dev profile renders. The WarmPoolController must now
	// warm exactly one pod for the minWarm = 1 pool.
	mustCreate(t, ctx, c, &nodev1.RuntimeClass{
		ObjectMeta: metav1.ObjectMeta{Name: "runc"},
		Handler:    "runc",
	})
	if _, err := wpr.Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatalf("WarmPoolController Reconcile (with RuntimeClass): %v", err)
	}
	if err := c.List(ctx, &sandboxes, client.InNamespace(ns),
		client.MatchingLabels{warmpool.LabelPool: poolName}); err != nil {
		t.Fatalf("list echo sandboxes (with RuntimeClass): %v", err)
	}
	if len(sandboxes.Items) != 1 {
		t.Fatalf("WarmPoolController created %d echo sandboxes with the runc RuntimeClass present, want exactly 1 (single-pod hot pool)",
			len(sandboxes.Items))
	}
	sb := sandboxes.Items[0]
	if sb.Spec.RuntimeRef != stack.EchoRuntimeName {
		t.Errorf("warmed sandbox runtimeRef = %q, want %q", sb.Spec.RuntimeRef, stack.EchoRuntimeName)
	}
	// The §13.2 dns-policy label is stamped on the warmed pod so the
	// allow-pod-egress-kube-dns supplemental NetworkPolicy admits the pod's
	// kube-system DNS egress on the embedded substrate, which runs no
	// dedicated lenny-system CoreDNS.
	if got := sb.Labels[warmpool.LabelDNSPolicy]; got != warmpool.DNSPolicyClusterDefault {
		t.Errorf("warmed sandbox %s label = %q, want lenny.dev/dns-policy: cluster-default", warmpool.LabelDNSPolicy, got)
	}

	// The §5.2 PoolWarmingUp condition is surfaced during the initial fill of
	// the minWarm > 0 pool; the session-start path maps it to 503
	// RUNTIME_UNAVAILABLE until the pod is idle.
	if err := c.Get(ctx, key, &tmpl); err != nil {
		t.Fatalf("get template after warm: %v", err)
	}
	var warming bool
	for _, cond := range tmpl.Status.Conditions {
		if cond.Type == "PoolWarmingUp" {
			warming = true
			if cond.Reason != "Provisioning" {
				t.Errorf("PoolWarmingUp reason = %q, want Provisioning (the §5.2 initial-fill window)", cond.Reason)
			}
		}
	}
	if !warming {
		t.Errorf("template carries no PoolWarmingUp condition; the §5.2 initial-fill 503 RUNTIME_UNAVAILABLE window is not surfaced")
	}
}
