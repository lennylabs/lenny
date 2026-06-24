// SPDX-License-Identifier: MIT

package stack

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
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

// TestEchoPoolUnstructuredCarriesGVKForDynamicApply_spec_4_6_2 asserts the echo
// pool pair the bring-up applies is encoded as unstructured objects carrying the
// lenny.dev/v1alpha1 SandboxTemplate/SandboxWarmPool GVK and preserving the
// canonical field mapping, so the C1 dynamic-apply path (apply.go applyObject)
// can resolve each object's GVR via the RESTMapper and server-side-apply it. The
// echo seed and the runtime-agnostic CLI verb share this dynamic-apply transport
// rather than a parallel typed-client upsert; the GVK on each object is what lets
// the shared applier route them. A missing apiVersion/kind would fail RESTMapping
// at apply time.
//
// spec: §4.6.2 (the bring-up materializes the pool CRDs through the
// dynamic-apply path), §5.2 (single-pod hot pool), §17.4 (one apply path).
func TestEchoPoolUnstructuredCarriesGVKForDynamicApply_spec_4_6_2(t *testing.T) {
	const ns = "lenny-agents"
	objs, err := echoPoolUnstructured(ns)
	if err != nil {
		t.Fatalf("echoPoolUnstructured: %v", err)
	}
	if len(objs) != 2 {
		t.Fatalf("echoPoolUnstructured returned %d objects, want 2 (SandboxTemplate + SandboxWarmPool)", len(objs))
	}

	byKind := map[string]unstructured.Unstructured{}
	for _, o := range objs {
		if got := o.GetAPIVersion(); got != lennyv1alpha1.GroupVersion.String() {
			t.Errorf("%s apiVersion = %q, want %q", o.GetKind(), got, lennyv1alpha1.GroupVersion.String())
		}
		if o.GetNamespace() != ns {
			t.Errorf("%s namespace = %q, want %q", o.GetKind(), o.GetNamespace(), ns)
		}
		if o.GetName() != EchoPoolName {
			t.Errorf("%s name = %q, want %q", o.GetKind(), o.GetName(), EchoPoolName)
		}
		byKind[o.GetKind()] = o
	}

	tmpl, ok := byKind["SandboxTemplate"]
	if !ok {
		t.Fatal("echoPoolUnstructured produced no SandboxTemplate")
	}
	if got, _, _ := unstructured.NestedString(tmpl.Object, "spec", "runtimeRef"); got != EchoRuntimeName {
		t.Errorf("template spec.runtimeRef = %q, want %q", got, EchoRuntimeName)
	}
	if got, _, _ := unstructured.NestedString(tmpl.Object, "spec", "isolationProfile"); got != "standard" {
		t.Errorf("template spec.isolationProfile = %q, want standard (§17.4 local fidelity)", got)
	}

	pool, ok := byKind["SandboxWarmPool"]
	if !ok {
		t.Fatal("echoPoolUnstructured produced no SandboxWarmPool")
	}
	if got, _, _ := unstructured.NestedString(pool.Object, "spec", "templateRef"); got != EchoPoolName {
		t.Errorf("warm pool spec.templateRef = %q, want %q", got, EchoPoolName)
	}
	minWarm, _, _ := unstructured.NestedInt64(pool.Object, "spec", "minWarm")
	maxWarm, _, _ := unstructured.NestedInt64(pool.Object, "spec", "maxWarm")
	if minWarm != int64(echoPoolWarmCount) || maxWarm != int64(echoPoolWarmCount) {
		t.Errorf("warm pool minWarm/maxWarm = %d/%d, want %d/%d (single-pod hot pool)",
			minWarm, maxWarm, echoPoolWarmCount, echoPoolWarmCount)
	}
}
