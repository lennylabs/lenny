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

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/poolscaling"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
	"github.com/lennylabs/lenny/pkg/embedded/stack"
	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/pkg/sandbox/state"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

// TestEmbeddedEchoPoolMaterializesToWarmPod_spec_17_4 exercises the full
// §17.4 Embedded Mode echo materialization chain against a real
// kube-apiserver: the §17.4 echo warm-pool seed (warmCount: 1, standard
// isolation, dnsPolicy: cluster-default) lands as a poolstore row, the
// §4.6.2 PoolScalingController (activated in Embedded Mode by the
// --agent-namespaces thread proposal 0016 wires) projects that row into a
// SandboxTemplate/SandboxWarmPool pair carrying DNSPolicy: cluster-default on
// the SandboxTemplate, and the §12.2.4 WarmPoolController pre-warms exactly one
// echo Sandbox for the minWarm = 1 pool. The pod carries the
// lenny.dev/dns-policy: cluster-default label (so the embedded substrate's
// kube-system CoreDNS egress is admitted) and the template surfaces the §5.2
// PoolWarmingUp condition during the initial fill, the condition the
// session-start path maps to 503 RUNTIME_UNAVAILABLE until the pod is idle.
//
// The test pins both phases of the §5.2 condition lifecycle the proposal
// names: PoolWarmingUp is True/Provisioning during the initial fill while the
// single pod is still warming, then clears to False/Available once that pod
// reaches the idle phase, the transition the gateway maps from
// 503 RUNTIME_UNAVAILABLE back to a successful claim.
//
// This is the component-level witness that proposal 0016 activates the §4.7 pod
// path end to end in Embedded Mode: the gateway-side placement path resolves the
// warm pool the controllers materialize from the seed, rather than falling
// through to the in-process echo executor.
//
// diagnosis: a failure means the embedded echo seed does not materialize into a
// warm echo pod the gateway can claim. Either the PoolScalingController did not
// project the seeded poolstore row into the SandboxTemplate/SandboxWarmPool pair
// (so the gateway resolves no warm pool and §4.7 placement stays inert), the
// DNSPolicy did not carry onto the SandboxTemplate (so the echo pod loses its
// kube-system CoreDNS egress on the embedded substrate that runs no dedicated
// lenny-system CoreDNS), or the WarmPoolController did not pre-warm the single
// hot-pool pod with the PoolWarmingUp condition the §5.2 initial-fill window
// reports.
//
// spec: §17.4 (Embedded Mode echo seed materializes a warm pod), §4.6.2 (the
// PoolScalingController projects the poolstore row into the CRD pair), §5.2
// (single-pod hot pool, PoolWarmingUp), §13.2 (dnsPolicy: cluster-default
// opt-out label).
func TestEmbeddedEchoPoolMaterializesToWarmPod_spec_17_4(t *testing.T) {
	env := envtest.Start(t)

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(lennyv1.AddToScheme(scheme))

	c, err := client.New(env.RESTConfig(), client.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	ctx := context.Background()

	// The embedded agent namespace the gateway places into and the
	// PoolScalingController materializes the CRD pair in (proposal 0016 creates
	// it at bring-up via ensureAgentNamespace). Use the same namespace the stack
	// targets so the component chain matches the activated wiring.
	const ns = "lenny-agents"
	mustCreate(t, ctx, c, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}})

	// The §17.4 echo warm-pool seed as a poolstore row. These are the exact
	// field values buildBootstrapSeed emits for the echo pool, pinned by the
	// tier-1 TestBuildBootstrapSeedSeedsEchoPool_spec_5_2: a single-pod hot pool
	// (warmCount 1), standard (runc) isolation under the §17.4 local-fidelity
	// disclosure with the allowStandardIsolation opt-in, and the §13.2
	// cluster-default DNS opt-out the embedded substrate requires.
	store := poolstore.NewMemory()
	const poolName = "echo"
	if err := store.Create(ctx, poolstore.Pool{
		Name:                   poolName,
		RuntimeRef:             stack.EchoRuntimeName,
		WarmCount:              1,
		IsolationProfile:       isolation.Profile("standard"),
		ExecutionMode:          runtimestore.ExecutionMode("session"),
		ResourceClass:          "small",
		AllowStandardIsolation: true,
		DNSPolicy:              poolstore.DNSPolicyClusterDefault,
	}); err != nil {
		t.Fatalf("seed echo pool row: %v", err)
	}

	// The §4.6.2 PoolScalingController projects the poolstore row into the
	// SandboxTemplate/SandboxWarmPool pair in the agent namespace. It is gated
	// in production on a non-empty --agent-namespaces; the embedded stack threads
	// that flag (proposal 0016 C2), so here the source targets the same namespace.
	psc := &poolscaling.Reconciler{
		Client: c,
		Source: &poolscaling.PoolStoreSource{Store: store, Namespace: ns},
	}
	if err := psc.Sync(ctx); err != nil {
		t.Fatalf("PoolScalingController Sync: %v", err)
	}

	key := client.ObjectKey{Namespace: ns, Name: poolName}

	// The SandboxWarmPool is materialized at minWarm = maxWarm = 1 (warmCount 1
	// maps to both per the poolstore-to-CRD mapping).
	var pool lennyv1.SandboxWarmPool
	if err := c.Get(ctx, key, &pool); err != nil {
		t.Fatalf("get materialized SandboxWarmPool: %v", err)
	}
	if pool.Spec.MinWarm != 1 || pool.Spec.MaxWarm != 1 {
		t.Errorf("materialized pool minWarm/maxWarm = %d/%d, want 1/1 (single-pod hot pool)",
			pool.Spec.MinWarm, pool.Spec.MaxWarm)
	}

	// The SandboxTemplate carries the §13.2 cluster-default DNS opt-out so the
	// WarmPoolController stamps the pod label. The embedded substrate runs no
	// dedicated lenny-system CoreDNS, so this opt-out is the difference between a
	// pod that resolves DNS and one that cannot.
	var tmpl lennyv1.SandboxTemplate
	if err := c.Get(ctx, key, &tmpl); err != nil {
		t.Fatalf("get materialized SandboxTemplate: %v", err)
	}
	if tmpl.Spec.DNSPolicy != poolstore.DNSPolicyClusterDefault {
		t.Errorf("materialized template DNSPolicy = %q, want cluster-default (§13.2 opt-out carried from the seed)", tmpl.Spec.DNSPolicy)
	}
	if tmpl.Spec.RuntimeRef != stack.EchoRuntimeName {
		t.Errorf("materialized template runtimeRef = %q, want %q", tmpl.Spec.RuntimeRef, stack.EchoRuntimeName)
	}
	if tmpl.Spec.IsolationProfile != "standard" {
		t.Errorf("materialized template isolationProfile = %q, want standard (§17.4 local fidelity)", tmpl.Spec.IsolationProfile)
	}

	// The §12.2.4 WarmPoolController pre-warms the single hot-pool pod for the
	// minWarm = 1 pool.
	wpr := &warmpool.Reconciler{Client: c, Scheme: scheme}
	if _, err := wpr.Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatalf("WarmPoolController Reconcile: %v", err)
	}

	var sandboxes lennyv1.SandboxList
	if err := c.List(ctx, &sandboxes, client.InNamespace(ns),
		client.MatchingLabels{warmpool.LabelPool: poolName}); err != nil {
		t.Fatalf("list echo sandboxes: %v", err)
	}
	if len(sandboxes.Items) != 1 {
		t.Fatalf("WarmPoolController created %d echo sandboxes, want exactly 1 (single-pod hot pool)", len(sandboxes.Items))
	}
	sb := sandboxes.Items[0]
	if sb.Spec.RuntimeRef != stack.EchoRuntimeName {
		t.Errorf("warmed sandbox runtimeRef = %q, want %q", sb.Spec.RuntimeRef, stack.EchoRuntimeName)
	}
	// The §13.2 dns-policy label is stamped on the warmed pod so the
	// allow-pod-egress-kube-dns supplemental NetworkPolicy admits the pod's
	// kube-system DNS egress on the embedded substrate.
	if got := sb.Labels[warmpool.LabelDNSPolicy]; got != warmpool.DNSPolicyClusterDefault {
		t.Errorf("warmed sandbox %s label = %q, want lenny.dev/dns-policy: cluster-default", warmpool.LabelDNSPolicy, got)
	}

	// The §5.2 PoolWarmingUp condition is surfaced during the initial fill of the
	// minWarm > 0 pool; the session-start path maps it to 503 RUNTIME_UNAVAILABLE
	// until the pod is idle. Assert it is present and Provisioning.
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

	// Drive the warmed pod to idle and re-reconcile to pin the second half of
	// the §5.2 condition lifecycle: once the single hot-pool pod reaches the
	// idle phase (ready = 1), poolWarmingUpCondition clears PoolWarmingUp to
	// False/Available, the transition the gateway maps from 503
	// RUNTIME_UNAVAILABLE back to a successful claim. Without this leg a
	// regression that leaves the pool stuck warming (e.g. one that never
	// recomputes ready) would pass, because the first leg observes the pool
	// only in its initial state where True/Provisioning is trivially correct.
	sb.Status.Phase = string(state.Idle)
	if err := c.Status().Update(ctx, &sb); err != nil {
		t.Fatalf("set warmed sandbox phase to idle: %v", err)
	}
	if _, err := wpr.Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatalf("WarmPoolController Reconcile after idle: %v", err)
	}
	if err := c.Get(ctx, key, &tmpl); err != nil {
		t.Fatalf("get template after idle: %v", err)
	}
	var cleared bool
	for _, cond := range tmpl.Status.Conditions {
		if cond.Type == "PoolWarmingUp" {
			cleared = true
			if cond.Status != metav1.ConditionFalse {
				t.Errorf("PoolWarmingUp status = %q after the pod is idle, want False (the §5.2 window cleared)", cond.Status)
			}
			if cond.Reason != "Available" {
				t.Errorf("PoolWarmingUp reason = %q after the pod is idle, want Available (the idle pod is claimable)", cond.Reason)
			}
		}
	}
	if !cleared {
		t.Errorf("template carries no PoolWarmingUp condition after the pod is idle; the §5.2 clearing transition is not surfaced")
	}
}
