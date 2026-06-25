// SPDX-License-Identifier: MIT

package stack_test

import (
	"context"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1alpha1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/embedded/stack"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

// runtimeApplyImage is the digest-pinned reference a runtime reaching
// ApplyRuntimeSetFromConfig carries (RunRuntimeApply resolves a tag-based
// image to this form before calling it). The @sha256 digest satisfies the
// §5.3 Runtime CRD pattern the API server enforces.
const runtimeApplyImage = "ghcr.io/acme/my-agent@sha256:" +
	"4444444444444444444444444444444444444444444444444444444444444444"

// TestApplyRuntimeSetFromConfigMaterializesRuntimeAndPool_spec_17_4 covers the
// §17.4 runtime-apply verb's apply path against a real kube-apiserver with the
// lenny.dev CRDs installed: ApplyRuntimeSetFromConfig applies the cluster-scoped
// Runtime CR through the typed-client upsert and the SandboxTemplate/SandboxWarmPool
// pair through the C1 dynamic-apply path, so a custom runtime materialized
// through the verb has the Runtime the Sandbox controller resolves by name and
// the pool the unconditionally-registered WarmPoolController reconciles to a
// warm pod. Under the no-Postgres development profile no PoolScalingController
// runs, so without this set ResolvePool returns ErrNoMatchingPool and a custom
// runtime never warms a pod; this is the runtime-agnostic counterpart of the
// echo seed's direct materialization.
//
// diagnosis: a failure means the runtime-apply verb cannot materialize a custom
// runtime's CRD set in the embedded cluster, so the §17.4 walkthrough's runtime
// has no Runtime CR or no warm pool and lenny session new against it fails with
// no matching pool.
//
// spec: §17.4 (the runtime-apply verb materializes the CRD set without a
// PoolScalingController), §5.1 (the Runtime CR is the declarative source the
// Sandbox controller resolves by name), §5.2 (ResolvePool lists the applied
// SandboxWarmPool), §4.6.2 (direct pool materialization).
func TestApplyRuntimeSetFromConfigMaterializesRuntimeAndPool_spec_17_4(t *testing.T) {
	env := envtest.Start(t)
	ctx := context.Background()
	cfg := env.RESTConfig()

	// The agent namespace the verb materializes the pool CRDs into must exist
	// first, exactly as the bring-up creates it before placement.
	const ns = "lenny-agents"
	if err := stack.EnsureAgentNamespaceFromConfigForTest(ctx, cfg, ns); err != nil {
		t.Fatalf("ensure agent namespace: %v", err)
	}

	rt := &lennyv1alpha1.Runtime{}
	rt.Name = "my-agent"
	rt.Spec.Image = runtimeApplyImage
	rt.Spec.IntegrationLevel = "basic"
	rt.Spec.DeploymentModel = "sidecar"

	if err := stack.ApplyRuntimeSetFromConfig(ctx, cfg, rt); err != nil {
		t.Fatalf("ApplyRuntimeSetFromConfig: %v", err)
	}

	cl := lennyClient(t, cfg)

	// The Runtime CR is cluster-scoped, resolved by name, digest-pinned, and
	// defaulted (type/executionMode/isolationProfile) by the verb.
	var got lennyv1alpha1.Runtime
	if err := cl.Get(ctx, ctrlclient.ObjectKey{Name: "my-agent"}, &got); err != nil {
		t.Fatalf("get Runtime after apply: %v", err)
	}
	if got.Spec.Image != runtimeApplyImage {
		t.Errorf("Runtime image = %q, want the digest-pinned %q", got.Spec.Image, runtimeApplyImage)
	}
	if got.Spec.Type != "agent" || got.Spec.ExecutionMode != "session" || got.Spec.IsolationProfile != "standard" {
		t.Errorf("Runtime defaults = type=%q mode=%q profile=%q, want agent/session/standard",
			got.Spec.Type, got.Spec.ExecutionMode, got.Spec.IsolationProfile)
	}

	// The pool pair is named for the runtime and lands in the agent namespace,
	// so the WarmPoolController reconciles it independently of any other pool.
	poolName := stack.RuntimePoolName("my-agent")
	var tmpl lennyv1alpha1.SandboxTemplate
	if err := cl.Get(ctx, ctrlclient.ObjectKey{Namespace: ns, Name: poolName}, &tmpl); err != nil {
		t.Fatalf("get SandboxTemplate after apply: %v", err)
	}
	if tmpl.Spec.RuntimeRef != "my-agent" {
		t.Errorf("SandboxTemplate runtimeRef = %q, want my-agent", tmpl.Spec.RuntimeRef)
	}
	var pool lennyv1alpha1.SandboxWarmPool
	if err := cl.Get(ctx, ctrlclient.ObjectKey{Namespace: ns, Name: poolName}, &pool); err != nil {
		t.Fatalf("get SandboxWarmPool after apply: %v", err)
	}
	if pool.Spec.TemplateRef != poolName || pool.Spec.MinWarm != 1 || pool.Spec.MaxWarm != 1 {
		t.Errorf("SandboxWarmPool = templateRef=%q minWarm=%d maxWarm=%d, want %q/1/1",
			pool.Spec.TemplateRef, pool.Spec.MinWarm, pool.Spec.MaxWarm, poolName)
	}

	// A re-run of the verb (a second walkthrough apply, or a re-imported image)
	// reconverges the live set in place rather than failing on AlreadyExists.
	rt.Spec.IsolationProfile = "standard"
	if err := stack.ApplyRuntimeSetFromConfig(ctx, cfg, rt); err != nil {
		t.Fatalf("re-apply must be idempotent, got: %v", err)
	}
	pools := &lennyv1alpha1.SandboxWarmPoolList{}
	if err := cl.List(ctx, pools, ctrlclient.InNamespace(ns)); err != nil {
		t.Fatalf("list warm pools after re-apply: %v", err)
	}
	var count int
	for i := range pools.Items {
		if pools.Items[i].Name == poolName {
			count++
		}
	}
	if count != 1 {
		t.Errorf("after re-apply there are %d %q SandboxWarmPools, want exactly 1", count, poolName)
	}
}

// TestApplyRuntimeSetFromConfigRejectsTagOnlyImage_spec_5_3 covers the §5.3
// digest-pinning invariant the Runtime CRD pattern enforces at the API server
// from inside the verb's apply path: a Runtime reaching ApplyRuntimeSetFromConfig
// with a tag-only image (the caller failed to resolve a digest) is rejected at
// apply rather than registering a mutable image reference. RunRuntimeApply
// resolves a tag to a digest before calling this, so a tag-only image here is a
// programming error the API server fails closed on.
//
// diagnosis: a failure means the verb registered a non-digest-pinned runtime
// image, so a mutable tag-based reference could reach a Sandbox pod, violating
// the §5.3 supply-chain MUST.
//
// spec: §5.3 (digest-pinned image references enforced at the API server).
func TestApplyRuntimeSetFromConfigRejectsTagOnlyImage_spec_5_3(t *testing.T) {
	env := envtest.Start(t)
	ctx := context.Background()
	cfg := env.RESTConfig()

	rt := &lennyv1alpha1.Runtime{}
	rt.Name = "tag-agent"
	rt.Spec.Image = "ghcr.io/acme/my-agent:dev"

	err := stack.ApplyRuntimeSetFromConfig(ctx, cfg, rt)
	if err == nil {
		t.Fatal("ApplyRuntimeSetFromConfig accepted a tag-only image, want a digest-pinning rejection")
	}
	if !apierrors.IsInvalid(err) {
		t.Errorf("error = %v, want an API-server Invalid rejection of the tag-only image", err)
	}

	// The rejected Runtime must not exist: the verb fails closed before the pool
	// pair so a half-applied set does not linger.
	cl := lennyClient(t, cfg)
	var got lennyv1alpha1.Runtime
	if err := cl.Get(ctx, ctrlclient.ObjectKey{Name: "tag-agent"}, &got); !apierrors.IsNotFound(err) {
		t.Errorf("Runtime get after rejected apply = %v, want NotFound (nothing registered)", err)
	}
}
