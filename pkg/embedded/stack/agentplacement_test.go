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

// TestGatewayAgentNamespaceGating_spec_4_7 covers the §4.7 fail-closed gating
// the embedded gateway uses to decide whether to place sessions on a pod or keep
// the in-process echo executor. Placement is activated only when the substrate
// is up and the echo-embedded import resolved a digest. The import-failure edge
// (k3s up, no resolved digest) is the case the gating must fail closed on: no
// echo Runtime CR is applied and the seed keeps its sentinel placeholder, so
// routing the session through the pod path would fail rather than start; the
// gateway must keep the in-process echo executor by leaving the namespace unset.
//
// diagnosis: a failure means the import-failure edge is mis-gated. A non-empty
// result for (k3s up, no image) routes `lenny session new --runtime echo` to the
// §4.7 pod path against a runtime with no runnable image and no Runtime CR, so
// the session fails instead of degrading to the in-process echo executor. An
// empty result for (k3s up, image resolved) leaves placement inert.
//
// spec: §17.4 (Embedded Mode degrades to the in-process echo executor when
// placement cannot run), §4.7 (the pod path needs a runnable digest-pinned image
// and an applied Runtime CR).
func TestGatewayAgentNamespaceGating_spec_4_7(t *testing.T) {
	const resolved = "ghcr.io/lennylabs/runtime-echo-embedded@sha256:" +
		"4444444444444444444444444444444444444444444444444444444444444444"
	cases := []struct {
		name         string
		k3sEnabled   bool
		echoImageRef string
		want         string
	}{
		{"substrate up and image resolved activates placement", true, resolved, agentNamespace},
		{"import failure fails closed to in-process echo", true, "", ""},
		{"substrate down keeps in-process echo", false, resolved, ""},
		{"substrate down and no image keeps in-process echo", false, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := gatewayAgentNamespace(tc.k3sEnabled, tc.echoImageRef)
			if got != tc.want {
				t.Errorf("gatewayAgentNamespace(%v, %q) = %q, want %q",
					tc.k3sEnabled, tc.echoImageRef, got, tc.want)
			}
		})
	}
}
