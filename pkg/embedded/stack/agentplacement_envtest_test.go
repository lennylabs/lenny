// SPDX-License-Identifier: MIT

package stack_test

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1alpha1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/embedded/stack"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

// The agent-namespace create and the echo Runtime CR apply are exported to the
// external test package through these test-only seams so the tier-2 envtest
// drives the same code paths Up wires, against a real kube-apiserver, without
// reaching into unexported helpers. They live in agentplacement_export_test.go.

// TestEnsureAgentNamespaceCreatesAndIsIdempotent_spec_4_6_2 covers the §4.6.2
// agent-namespace create against a real kube-apiserver: the first call creates
// the namespace the gateway places into and the PoolScalingController
// materializes the pool CRDs into, and a second call treats the AlreadyExists
// as success so a re-run of lenny up does not fail.
//
// diagnosis: a failure means the embedded bring-up cannot create or re-create
// the agent namespace, so the gateway resolves no warm pool and §4.7 placement
// stays inert in Embedded Mode.
//
// spec: §4.6.2 (the agent namespace holds the pool CRDs), §5.1 (platform-global
// pools materialize per agent namespace).
func TestEnsureAgentNamespaceCreatesAndIsIdempotent_spec_4_6_2(t *testing.T) {
	env := envtest.Start(t)
	ctx := context.Background()
	cfg := env.RESTConfig()

	const ns = "lenny-agents"
	if err := stack.EnsureAgentNamespaceFromConfigForTest(ctx, cfg, ns); err != nil {
		t.Fatalf("first ensureAgentNamespace: %v", err)
	}
	// A second call must succeed despite the namespace already existing.
	if err := stack.EnsureAgentNamespaceFromConfigForTest(ctx, cfg, ns); err != nil {
		t.Fatalf("idempotent re-ensureAgentNamespace: %v", err)
	}

	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("build core client: %v", err)
	}
	got, err := cs.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get namespace after ensure: %v", err)
	}
	if got.Name != ns {
		t.Errorf("namespace name = %q, want %q", got.Name, ns)
	}
	if got.Status.Phase == corev1.NamespaceTerminating {
		t.Errorf("namespace %q is terminating, want active", ns)
	}
}

// TestApplyEchoRuntimeCRCreatesAndReconverges_spec_4_7 covers the §4.7/§5.1 echo
// Runtime CR apply against a real kube-apiserver with the lenny.dev CRDs
// installed: the first apply creates the cluster-scoped Runtime carrying
// deploymentModel: embedded and the import-time-resolved digest, and a second
// apply with a fresh digest reconverges the CR in place (the digest the next
// lenny up imports) rather than failing on AlreadyExists. The CRD's
// @sha256:<64-hex> image pattern is enforced by the API server, so a digest the
// echo seed and the containerd image also carry passes admission.
//
// diagnosis: a failure means the embedded bring-up cannot register the echo
// Runtime CR the Sandbox controller resolves the runtime from by name, so the
// seeded warm pool's pod never renders the §4.7 embedded single-container model.
//
// spec: §4.7 (embedded deployment model), §5.1 (the Runtime CR is the
// declarative source the Sandbox controller resolves by name).
func TestApplyEchoRuntimeCRCreatesAndReconverges_spec_4_7(t *testing.T) {
	env := envtest.Start(t)
	ctx := context.Background()
	cfg := env.RESTConfig()

	const first = "ghcr.io/lennylabs/runtime-echo-embedded@sha256:" +
		"5555555555555555555555555555555555555555555555555555555555555555"
	const second = "ghcr.io/lennylabs/runtime-echo-embedded@sha256:" +
		"6666666666666666666666666666666666666666666666666666666666666666"

	if err := stack.ApplyEchoRuntimeCRFromConfigForTest(ctx, cfg, first); err != nil {
		t.Fatalf("first applyEchoRuntimeCR: %v", err)
	}

	cl := lennyClient(t, cfg)
	var rt lennyv1alpha1.Runtime
	if err := cl.Get(ctx, ctrlclient.ObjectKey{Name: stack.EchoRuntimeName}, &rt); err != nil {
		t.Fatalf("get echo Runtime after create: %v", err)
	}
	if rt.Spec.DeploymentModel != "embedded" {
		t.Errorf("deploymentModel = %q, want embedded", rt.Spec.DeploymentModel)
	}
	if rt.Spec.Image != first {
		t.Errorf("image = %q, want the first resolved digest %q", rt.Spec.Image, first)
	}
	if rt.Namespace != "" {
		t.Errorf("Runtime is cluster-scoped, want no namespace, got %q", rt.Namespace)
	}

	// A re-run of lenny up that imports a fresh echo image must reconverge the
	// existing CR to the new digest in place.
	if err := stack.ApplyEchoRuntimeCRFromConfigForTest(ctx, cfg, second); err != nil {
		t.Fatalf("re-applyEchoRuntimeCR: %v", err)
	}
	if err := cl.Get(ctx, ctrlclient.ObjectKey{Name: stack.EchoRuntimeName}, &rt); err != nil {
		t.Fatalf("get echo Runtime after re-apply: %v", err)
	}
	if rt.Spec.Image != second {
		t.Errorf("image after re-apply = %q, want the reconverged digest %q", rt.Spec.Image, second)
	}
}

// TestApplyEchoRuntimeCRRejectsTagOnlyImage_spec_5_1 covers the §5.1/§5.3
// digest-pinning invariant the CRD pattern enforces at the API server: a
// tag-only image (no @sha256 digest) is rejected at apply. This pins that the
// bring-up cannot register a non-digest-pinned echo image even if the import
// resolution returned a malformed reference.
//
// diagnosis: a failure means the digest-pinning admission the CRD pattern
// encodes is not enforced, so a tag-pinned (mutable) echo image could reach a
// Sandbox pod, violating the §5.3 supply-chain MUST.
//
// spec: §5.1 (Runtime image must be digest-pinned), §5.3 (supply-chain digest
// pin).
func TestApplyEchoRuntimeCRRejectsTagOnlyImage_spec_5_1(t *testing.T) {
	env := envtest.Start(t)
	ctx := context.Background()
	cfg := env.RESTConfig()

	err := stack.ApplyEchoRuntimeCRFromConfigForTest(ctx, cfg, "ghcr.io/lennylabs/runtime-echo-embedded:dev")
	if err == nil {
		t.Fatal("applyEchoRuntimeCR accepted a tag-only image, want a digest-pinning rejection")
	}
	if !apierrors.IsInvalid(err) {
		t.Errorf("error = %v, want an API-server Invalid rejection of the tag-only image", err)
	}
}

// lennyClient builds a controller-runtime client carrying the lenny.dev scheme
// for the envtest control plane.
func lennyClient(t *testing.T, cfg *rest.Config) ctrlclient.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	utilruntime.Must(lennyv1alpha1.AddToScheme(scheme))
	cl, err := ctrlclient.New(cfg, ctrlclient.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("build lenny client: %v", err)
	}
	return cl
}
