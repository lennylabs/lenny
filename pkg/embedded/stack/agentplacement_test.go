// SPDX-License-Identifier: MIT

package stack

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	lennyv1alpha1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
)

// lennyScheme builds a runtime.Scheme carrying the lenny.dev/v1alpha1 types
// the fake client serves Runtime CRs through.
func lennyScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := lennyv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("lenny AddToScheme: %v", err)
	}
	return s
}

// TestEchoRuntimeCR_spec_4_7 asserts the echo Runtime CR the embedded bring-up
// applies carries the §4.7 embedded deployment model and the import-time-resolved
// digest-pinned image, plus the Basic level, session execution mode, and the
// locally-runnable standard isolation profile the embedded single-node cluster
// degrades sandboxed/microvm to. The CR name matches EchoRuntimeName so the
// seeded pool's runtimeRef and `--runtime echo` both resolve to it, and the
// cluster-scoped Runtime carries no namespace.
//
// spec: §4.7 (embedded deployment model), §5.1 (Runtime CR), §17.4 (local
// fidelity standard isolation).
func TestEchoRuntimeCR_spec_4_7(t *testing.T) {
	const image = "ghcr.io/lennylabs/runtime-echo-embedded@sha256:" +
		"abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	cr := echoRuntimeCR(image)
	if cr.Name != EchoRuntimeName {
		t.Errorf("Runtime CR name = %q, want %q", cr.Name, EchoRuntimeName)
	}
	if cr.Namespace != "" {
		t.Errorf("Runtime CR is cluster-scoped, want no namespace, got %q", cr.Namespace)
	}
	if cr.Spec.DeploymentModel != "embedded" {
		t.Errorf("deploymentModel = %q, want embedded (§4.7 single-container model)", cr.Spec.DeploymentModel)
	}
	if cr.Spec.Image != image {
		t.Errorf("image = %q, want the import-time-resolved digest %q", cr.Spec.Image, image)
	}
	if cr.Spec.IntegrationLevel != "basic" {
		t.Errorf("integrationLevel = %q, want basic (echo-embedded is Basic-level)", cr.Spec.IntegrationLevel)
	}
	if cr.Spec.ExecutionMode != "session" {
		t.Errorf("executionMode = %q, want session", cr.Spec.ExecutionMode)
	}
	if cr.Spec.IsolationProfile != "standard" {
		t.Errorf("isolationProfile = %q, want standard (§17.4 local fidelity)", cr.Spec.IsolationProfile)
	}
	if cr.Spec.Type != "agent" {
		t.Errorf("type = %q, want agent", cr.Spec.Type)
	}
}

// TestUpsertRuntimeCRCreatesWhenAbsent_spec_5_1 asserts upsertRuntimeCR creates
// the echo Runtime CR when none exists, so the first lenny up applies the CR the
// Sandbox controller resolves the runtime from by name.
//
// spec: §5.1 (the Runtime CR is the declarative source the Sandbox controller
// resolves by name).
func TestUpsertRuntimeCRCreatesWhenAbsent_spec_5_1(t *testing.T) {
	cl := fake.NewClientBuilder().WithScheme(lennyScheme(t)).Build()
	const image = "ghcr.io/lennylabs/runtime-echo-embedded@sha256:" +
		"1111111111111111111111111111111111111111111111111111111111111111"
	if err := upsertRuntimeCR(context.Background(), cl, echoRuntimeCR(image)); err != nil {
		t.Fatalf("upsertRuntimeCR create: %v", err)
	}
	var got lennyv1alpha1.Runtime
	if err := cl.Get(context.Background(), ctrlclient.ObjectKey{Name: EchoRuntimeName}, &got); err != nil {
		t.Fatalf("get created Runtime: %v", err)
	}
	if got.Spec.Image != image {
		t.Errorf("created Runtime image = %q, want %q", got.Spec.Image, image)
	}
}

// TestUpsertRuntimeCRUpdatesInPlace_spec_5_1 asserts a re-run of lenny up
// reconverges an existing echo Runtime CR to the new import-time-resolved digest
// in place rather than failing on AlreadyExists, so a second bring-up that
// imports a freshly-built echo image keeps the CR digest, the seed digest, and
// the containerd image digest identical.
//
// spec: §5.1 (the Runtime CR is the declarative source), §4.7 (digest-pinned
// pod image).
func TestUpsertRuntimeCRUpdatesInPlace_spec_5_1(t *testing.T) {
	const first = "ghcr.io/lennylabs/runtime-echo-embedded@sha256:" +
		"2222222222222222222222222222222222222222222222222222222222222222"
	const second = "ghcr.io/lennylabs/runtime-echo-embedded@sha256:" +
		"3333333333333333333333333333333333333333333333333333333333333333"
	cl := fake.NewClientBuilder().
		WithScheme(lennyScheme(t)).
		WithObjects(echoRuntimeCR(first)).
		Build()
	if err := upsertRuntimeCR(context.Background(), cl, echoRuntimeCR(second)); err != nil {
		t.Fatalf("upsertRuntimeCR update: %v", err)
	}
	var got lennyv1alpha1.Runtime
	if err := cl.Get(context.Background(), ctrlclient.ObjectKey{Name: EchoRuntimeName}, &got); err != nil {
		t.Fatalf("get updated Runtime: %v", err)
	}
	if got.Spec.Image != second {
		t.Errorf("Runtime image after re-apply = %q, want the new digest %q", got.Spec.Image, second)
	}
}

// TestEchoPoolObjectsReproducesToConfigMapping_spec_4_6_2 asserts the echo
// SandboxTemplate/SandboxWarmPool pair the embedded bring-up applies reproduces
// the canonical poolstore→CRD field mapping (poolscaling.PoolStoreSource.toConfig)
// for a §5.2 single-pod hot pool: the SandboxWarmPool sets templateRef to the
// pool name with minWarm = maxWarm = warmCount, and the SandboxTemplate carries
// the runtimeRef, the §17.4 local-fidelity `standard` isolation, the restricted
// egress profile, the §13.2 cluster-default DNS opt-out, and the small resource
// class. A drift between this direct apply and what the controller would have
// produced from the seed would warm a pod with the wrong template.
//
// spec: §4.6.2 (the poolstore→CRD projection), §5.2 (single-pod hot pool),
// §13.2 (cluster-default DNS opt-out), §17.4 (Embedded Mode echo seed).
func TestEchoPoolObjectsReproducesToConfigMapping_spec_4_6_2(t *testing.T) {
	const ns = "lenny-agents"
	tmpl, pool := echoPoolObjects(ns)

	if tmpl.Namespace != ns || pool.Namespace != ns {
		t.Errorf("CRD pair namespaces = %q/%q, want %q", tmpl.Namespace, pool.Namespace, ns)
	}
	if tmpl.Name != EchoPoolName || pool.Name != EchoPoolName {
		t.Errorf("CRD pair names = %q/%q, want %q", tmpl.Name, pool.Name, EchoPoolName)
	}
	// §5.2 single-pod hot pool: warmCount maps to minWarm = maxWarm.
	if pool.Spec.TemplateRef != EchoPoolName {
		t.Errorf("warm pool templateRef = %q, want %q", pool.Spec.TemplateRef, EchoPoolName)
	}
	if pool.Spec.MinWarm != echoPoolWarmCount || pool.Spec.MaxWarm != echoPoolWarmCount {
		t.Errorf("warm pool minWarm/maxWarm = %d/%d, want %d/%d (single-pod hot pool)",
			pool.Spec.MinWarm, pool.Spec.MaxWarm, echoPoolWarmCount, echoPoolWarmCount)
	}
	if tmpl.Spec.RuntimeRef != EchoRuntimeName {
		t.Errorf("template runtimeRef = %q, want %q", tmpl.Spec.RuntimeRef, EchoRuntimeName)
	}
	if tmpl.Spec.IsolationProfile != "standard" {
		t.Errorf("template isolationProfile = %q, want standard (§17.4 local fidelity)", tmpl.Spec.IsolationProfile)
	}
	if tmpl.Spec.EgressProfile != "restricted" {
		t.Errorf("template egressProfile = %q, want restricted", tmpl.Spec.EgressProfile)
	}
	if tmpl.Spec.DNSPolicy != "cluster-default" {
		t.Errorf("template dnsPolicy = %q, want cluster-default (§13.2 opt-out)", tmpl.Spec.DNSPolicy)
	}
	if tmpl.Spec.ResourceClass != "small" {
		t.Errorf("template resourceClass = %q, want small", tmpl.Spec.ResourceClass)
	}
	// The echo pool sets no execution mode, so the template leaves it empty
	// (the §5.2 session default), matching the toConfig mapping for a row
	// with no ExecutionMode.
	if tmpl.Spec.ExecutionMode != "" {
		t.Errorf("template executionMode = %q, want empty (session default)", tmpl.Spec.ExecutionMode)
	}
}

// TestApplyEchoPoolFromConfigCreatesAndUpdatesInPlace_spec_4_6_2 asserts the
// echo-pool direct apply creates the SandboxTemplate/SandboxWarmPool pair when
// absent and updates them in place on a re-run, so a second lenny up does not
// fail on AlreadyExists and reconverges the pair rather than duplicating it.
//
// spec: §4.6.2 (the bring-up materializes the pool without a PoolScalingController),
// §5.2 (single-pod hot pool), §17.4 (idempotent re-apply).
func TestApplyEchoPoolFromConfigCreatesAndUpdatesInPlace_spec_4_6_2(t *testing.T) {
	const ns = "lenny-agents"
	cl := fake.NewClientBuilder().WithScheme(lennyScheme(t)).Build()
	ctx := context.Background()

	tmpl, pool := echoPoolObjects(ns)
	if err := upsertSandboxTemplate(ctx, cl, tmpl); err != nil {
		t.Fatalf("create SandboxTemplate: %v", err)
	}
	if err := upsertSandboxWarmPool(ctx, cl, pool); err != nil {
		t.Fatalf("create SandboxWarmPool: %v", err)
	}

	var gotTmpl lennyv1alpha1.SandboxTemplate
	if err := cl.Get(ctx, ctrlclient.ObjectKey{Name: EchoPoolName, Namespace: ns}, &gotTmpl); err != nil {
		t.Fatalf("get created SandboxTemplate: %v", err)
	}
	var gotPool lennyv1alpha1.SandboxWarmPool
	if err := cl.Get(ctx, ctrlclient.ObjectKey{Name: EchoPoolName, Namespace: ns}, &gotPool); err != nil {
		t.Fatalf("get created SandboxWarmPool: %v", err)
	}
	if gotPool.Spec.MinWarm != echoPoolWarmCount {
		t.Errorf("created warm pool minWarm = %d, want %d", gotPool.Spec.MinWarm, echoPoolWarmCount)
	}

	// A second apply must reconverge in place rather than fail on AlreadyExists.
	tmpl2, pool2 := echoPoolObjects(ns)
	if err := upsertSandboxTemplate(ctx, cl, tmpl2); err != nil {
		t.Fatalf("re-apply SandboxTemplate: %v", err)
	}
	if err := upsertSandboxWarmPool(ctx, cl, pool2); err != nil {
		t.Fatalf("re-apply SandboxWarmPool: %v", err)
	}
	var pools lennyv1alpha1.SandboxWarmPoolList
	if err := cl.List(ctx, &pools, ctrlclient.InNamespace(ns)); err != nil {
		t.Fatalf("list warm pools after re-apply: %v", err)
	}
	if len(pools.Items) != 1 {
		t.Errorf("re-apply produced %d warm pools, want exactly 1 (reconverged in place)", len(pools.Items))
	}
}
