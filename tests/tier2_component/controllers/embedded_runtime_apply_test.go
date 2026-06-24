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
	"github.com/lennylabs/lenny/pkg/gateway/podsession"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

// customRuntimeImage is a digest-pinned reference the Runtime CRD's §5.3
// supply-chain pattern accepts at the API server, so the runtime-apply verb
// can register the Runtime CR in the envtest control plane.
const customRuntimeImage = "ghcr.io/lennylabs/runtime-my-agent@sha256:" +
	"8888888888888888888888888888888888888888888888888888888888888888"

// TestEmbeddedRuntimeApplyWarmsPod_spec_17_4 exercises the §17.4 runtime-apply
// verb against a real kube-apiserver: with no Postgres and no
// PoolScalingController, applying an arbitrary (non-echo) runtime's Runtime,
// SandboxTemplate, and SandboxWarmPool CRD set materializes the SandboxWarmPool
// the gateway's ResolvePool lists, and the unconditionally-registered
// WarmPoolController reconciles the pool to a warm Sandbox when the `runc`
// RuntimeClass the `standard` isolation profile resolves to is present. This is
// the runtime-agnostic counterpart of the echo direct-apply test: it proves the
// no-Postgres pool-materialization path works for any registered runtime, the
// property the §17.4 custom-runtime walkthrough depends on.
//
// The verb's input is a minimal Runtime carrying only name, image,
// integrationLevel, and deploymentModel: sidecar (the walkthrough default), so
// the test also covers that the verb derives the SandboxTemplate/SandboxWarmPool
// pair and defaults the §5.1 Runtime fields the Sandbox controller resolves by.
//
// The negative leg pins the precondition: with the `runc` RuntimeClass absent
// the pool stays Degraded and no Sandbox is created (decision.Create = 0), so
// the dev profile's runtimeClasses.create=true is load-bearing for a custom
// runtime exactly as it is for echo.
//
// diagnosis: a failure means the runtime-apply verb does not materialize a
// usable pool for a custom runtime. Either the SandboxWarmPool CRD apply did
// not create the CRD the gateway's ResolvePool lists (so §4.7 placement stays
// inert for the runtime), or the WarmPoolController did not warm a Sandbox with
// the `runc` RuntimeClass present (the pool stayed Degraded and decision.Create
// was suppressed), so the runtime-agnostic deliverable is broken.
//
// spec: §17.4 (the runtime-apply verb materializes the CRD set without a
// PoolScalingController), §5.2 (ResolvePool lists the applied SandboxWarmPool),
// §4.6.2 (direct pool materialization), §5.3 (standard→runc RuntimeClass gate).
func TestEmbeddedRuntimeApplyWarmsPod_spec_17_4(t *testing.T) {
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

	// The verb's input: a minimal sidecar-model Runtime, the §17.4 walkthrough
	// default. The verb derives the SandboxTemplate/SandboxWarmPool from it and
	// defaults the omitted §5.1 fields.
	const runtimeName = "my-agent"
	rt := &lennyv1.Runtime{
		ObjectMeta: metav1.ObjectMeta{Name: runtimeName},
		Spec: lennyv1.RuntimeSpec{
			Image:            customRuntimeImage,
			IntegrationLevel: "basic",
			DeploymentModel:  "sidecar",
		},
	}
	if err := stack.ApplyRuntimeSetFromConfig(ctx, env.RESTConfig(), rt); err != nil {
		t.Fatalf("runtime apply: %v", err)
	}

	// The cluster-scoped Runtime CR the Sandbox controller resolves by name is
	// created, with the §5.1 defaults the verb stamps.
	var cr lennyv1.Runtime
	if err := c.Get(ctx, client.ObjectKey{Name: runtimeName}, &cr); err != nil {
		t.Fatalf("get applied Runtime CR: %v", err)
	}
	if cr.Spec.Type != "agent" {
		t.Errorf("Runtime type = %q, want agent (default)", cr.Spec.Type)
	}
	if cr.Spec.IsolationProfile != "standard" {
		t.Errorf("Runtime isolationProfile = %q, want standard (default)", cr.Spec.IsolationProfile)
	}
	if cr.Spec.DeploymentModel != "sidecar" {
		t.Errorf("Runtime deploymentModel = %q, want sidecar", cr.Spec.DeploymentModel)
	}

	poolName := stack.RuntimePoolName(runtimeName)
	key := client.ObjectKey{Namespace: ns, Name: poolName}

	// The SandboxWarmPool CRD the gateway's ResolvePool lists is created
	// without Postgres, with minWarm = maxWarm = 1 for the single-pod hot pool.
	var pool lennyv1.SandboxWarmPool
	if err := c.Get(ctx, key, &pool); err != nil {
		t.Fatalf("get applied SandboxWarmPool: %v", err)
	}
	if pool.Spec.MinWarm != 1 || pool.Spec.MaxWarm != 1 {
		t.Errorf("applied pool minWarm/maxWarm = %d/%d, want 1/1", pool.Spec.MinWarm, pool.Spec.MaxWarm)
	}
	var tmpl lennyv1.SandboxTemplate
	if err := c.Get(ctx, key, &tmpl); err != nil {
		t.Fatalf("get applied SandboxTemplate: %v", err)
	}
	if tmpl.Spec.RuntimeRef != runtimeName {
		t.Errorf("applied template runtimeRef = %q, want %q", tmpl.Spec.RuntimeRef, runtimeName)
	}

	// The gateway resolves the runtime to the applied pool by name, the
	// property that fails with ErrNoMatchingPool when no pool is materialized.
	match, err := podsession.ResolvePool(ctx, c, nil, ns, runtimeName, "standard")
	if err != nil {
		t.Fatalf("ResolvePool for the applied runtime: %v", err)
	}
	if match.Pool != poolName {
		t.Errorf("ResolvePool matched pool %q, want %q", match.Pool, poolName)
	}

	// ----- Idempotent re-apply -----
	// A re-run reconverges the live set in place rather than failing on
	// AlreadyExists or duplicating the pool.
	if err := stack.ApplyRuntimeSetFromConfig(ctx, env.RESTConfig(), rt); err != nil {
		t.Fatalf("re-apply runtime set: %v", err)
	}
	var pools lennyv1.SandboxWarmPoolList
	if err := c.List(ctx, &pools, client.InNamespace(ns)); err != nil {
		t.Fatalf("list warm pools after re-apply: %v", err)
	}
	if len(pools.Items) != 1 {
		t.Fatalf("re-apply produced %d warm pools, want exactly 1 (reconverged in place)", len(pools.Items))
	}

	wpr := &warmpool.Reconciler{
		Client:         c,
		Scheme:         scheme,
		RuntimeClasses: warmpool.NewReaderRuntimeClassChecker(c),
	}

	// ----- Negative leg: no RuntimeClass -----
	// With the `runc` RuntimeClass absent the WarmPoolController marks the pool
	// Degraded and suppresses creation (decision.Create = 0).
	if _, err := wpr.Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatalf("WarmPoolController Reconcile (no RuntimeClass): %v", err)
	}
	var sandboxes lennyv1.SandboxList
	if err := c.List(ctx, &sandboxes, client.InNamespace(ns),
		client.MatchingLabels{warmpool.LabelPool: poolName}); err != nil {
		t.Fatalf("list sandboxes (no RuntimeClass): %v", err)
	}
	if len(sandboxes.Items) != 0 {
		t.Fatalf("WarmPoolController created %d sandboxes with the runc RuntimeClass absent, want 0 (Degraded suppresses creation)",
			len(sandboxes.Items))
	}

	// ----- Positive leg: with the runc RuntimeClass -----
	// Install the `runc` RuntimeClass the `standard` isolation profile resolves
	// to. The WarmPoolController now warms exactly one Sandbox for the
	// minWarm = 1 pool, so the pool no longer stays Degraded with
	// decision.Create = 0.
	mustCreate(t, ctx, c, &nodev1.RuntimeClass{
		ObjectMeta: metav1.ObjectMeta{Name: "runc"},
		Handler:    "runc",
	})
	if _, err := wpr.Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatalf("WarmPoolController Reconcile (with RuntimeClass): %v", err)
	}
	if err := c.List(ctx, &sandboxes, client.InNamespace(ns),
		client.MatchingLabels{warmpool.LabelPool: poolName}); err != nil {
		t.Fatalf("list sandboxes (with RuntimeClass): %v", err)
	}
	if len(sandboxes.Items) != 1 {
		t.Fatalf("WarmPoolController created %d sandboxes with the runc RuntimeClass present, want exactly 1 (single-pod hot pool)",
			len(sandboxes.Items))
	}
	if sandboxes.Items[0].Spec.RuntimeRef != runtimeName {
		t.Errorf("warmed sandbox runtimeRef = %q, want %q", sandboxes.Items[0].Spec.RuntimeRef, runtimeName)
	}
}
