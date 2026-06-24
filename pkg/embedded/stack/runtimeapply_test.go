// SPDX-License-Identifier: MIT

package stack

import (
	"os"
	"path/filepath"
	"testing"

	lennyv1alpha1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
)

const testRuntimeImage = "ghcr.io/lennylabs/runtime-my-agent@sha256:" +
	"7777777777777777777777777777777777777777777777777777777777777777"

// writeRuntimeFile writes body to a temp file and returns its path so the
// loader tests exercise the on-disk YAML decode the verb runs.
func writeRuntimeFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "runtime-crds.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write runtime file: %v", err)
	}
	return path
}

// TestLoadRuntimeFileDecodesMinimalWalkthroughFile_spec_5_1 covers the §17.4
// walkthrough's runtime-crds.yaml decode: a one-document Runtime carrying only
// name, image, integrationLevel, and deploymentModel decodes into the typed
// Runtime the verb derives the CRD set from.
//
// spec: §5.1 (the Runtime declarative record), §17.4 (the walkthrough file).
func TestLoadRuntimeFileDecodesMinimalWalkthroughFile_spec_5_1(t *testing.T) {
	path := writeRuntimeFile(t, `apiVersion: lenny.dev/v1alpha1
kind: Runtime
metadata:
  name: my-agent
spec:
  image: `+testRuntimeImage+`
  integrationLevel: basic
  deploymentModel: sidecar
`)
	rt, err := loadRuntimeFile(path)
	if err != nil {
		t.Fatalf("loadRuntimeFile: %v", err)
	}
	if rt.Name != "my-agent" {
		t.Errorf("name = %q, want my-agent", rt.Name)
	}
	if rt.Spec.Image != testRuntimeImage {
		t.Errorf("image = %q, want %q", rt.Spec.Image, testRuntimeImage)
	}
	if rt.Spec.IntegrationLevel != "basic" {
		t.Errorf("integrationLevel = %q, want basic", rt.Spec.IntegrationLevel)
	}
	if rt.Spec.DeploymentModel != "sidecar" {
		t.Errorf("deploymentModel = %q, want sidecar", rt.Spec.DeploymentModel)
	}
}

// TestLoadRuntimeFileFailsClosed_spec_5_1 covers the fail-closed loader: a
// missing file, a Runtime with no name, and a Runtime with no image are each
// rejected before any apply, so the verb does not apply an unresolvable CRD
// set the API server would reject partway through.
//
// spec: §5.1 (a Runtime record requires a name and a digest-pinned image).
func TestLoadRuntimeFileFailsClosed_spec_5_1(t *testing.T) {
	if _, err := loadRuntimeFile(filepath.Join(t.TempDir(), "absent.yaml")); err == nil {
		t.Error("loadRuntimeFile accepted a missing file, want an error")
	}
	noName := writeRuntimeFile(t, `apiVersion: lenny.dev/v1alpha1
kind: Runtime
spec:
  image: `+testRuntimeImage+`
`)
	if _, err := loadRuntimeFile(noName); err == nil {
		t.Error("loadRuntimeFile accepted a Runtime with no metadata.name, want an error")
	}
	noImage := writeRuntimeFile(t, `apiVersion: lenny.dev/v1alpha1
kind: Runtime
metadata:
  name: my-agent
spec:
  integrationLevel: basic
`)
	if _, err := loadRuntimeFile(noImage); err == nil {
		t.Error("loadRuntimeFile accepted a Runtime with no spec.image, want an error")
	}
}

// TestRuntimeCRFromFileDefaultsRequiredFields_spec_5_1 covers the §5.1
// defaulting the verb applies so a minimal walkthrough file applies cleanly:
// type→agent, executionMode→session, isolationProfile→standard, and
// deploymentModel→sidecar (the §17.4 walkthrough default). An explicit value
// in the file is preserved rather than overwritten.
//
// spec: §5.1 (Runtime defaults), §4.7 (sidecar default deployment model).
func TestRuntimeCRFromFileDefaultsRequiredFields_spec_5_1(t *testing.T) {
	minimal := &lennyv1alpha1.Runtime{}
	minimal.Name = "my-agent"
	minimal.Spec.Image = testRuntimeImage
	minimal.Spec.IntegrationLevel = "basic"

	cr := runtimeCRFromFile(minimal)
	if cr.Name != "my-agent" || cr.Namespace != "" {
		t.Errorf("Runtime is cluster-scoped: name/namespace = %q/%q, want my-agent/empty", cr.Name, cr.Namespace)
	}
	if cr.Kind != "Runtime" || cr.APIVersion != lennyv1alpha1.GroupVersion.String() {
		t.Errorf("Runtime GVK = %s/%s, want %s/Runtime", cr.APIVersion, cr.Kind, lennyv1alpha1.GroupVersion.String())
	}
	if cr.Spec.Type != "agent" {
		t.Errorf("type = %q, want agent (default)", cr.Spec.Type)
	}
	if cr.Spec.ExecutionMode != "session" {
		t.Errorf("executionMode = %q, want session (default)", cr.Spec.ExecutionMode)
	}
	if cr.Spec.IsolationProfile != "standard" {
		t.Errorf("isolationProfile = %q, want standard (default)", cr.Spec.IsolationProfile)
	}
	if cr.Spec.DeploymentModel != "sidecar" {
		t.Errorf("deploymentModel = %q, want sidecar (§17.4 walkthrough default)", cr.Spec.DeploymentModel)
	}

	// An explicit isolation/deployment value is preserved.
	pinned := &lennyv1alpha1.Runtime{}
	pinned.Name = "embedded-agent"
	pinned.Spec.Image = testRuntimeImage
	pinned.Spec.IsolationProfile = "standard"
	pinned.Spec.DeploymentModel = "embedded"
	cr = runtimeCRFromFile(pinned)
	if cr.Spec.DeploymentModel != "embedded" {
		t.Errorf("explicit deploymentModel overwritten = %q, want embedded", cr.Spec.DeploymentModel)
	}
}

// TestRuntimePoolObjectsReproducesEchoFieldMapping_spec_4_6_2 covers the
// runtime-agnostic pool derivation: the verb reproduces the echo seed's
// poolstore→CRD field mapping (MinWarm = MaxWarm = warmCount, runtimeRef, the
// runtime's isolation profile, and the embedded egress/DNS/resource defaults)
// for an arbitrary runtime, so a runtime materialized through the verb warms a
// pod the same way the echo seed does. The pool is named for the runtime so
// multiple runtimes each get their own pool.
//
// spec: §4.6.2 (the poolstore→CRD projection), §5.2 (single-pod hot pool),
// §13.2 (cluster-default DNS opt-out).
func TestRuntimePoolObjectsReproducesEchoFieldMapping_spec_4_6_2(t *testing.T) {
	cr := &lennyv1alpha1.Runtime{}
	cr.Name = "my-agent"
	cr.Spec.IsolationProfile = "standard"

	tmpl, pool := runtimePoolObjects(cr)

	const wantPool = "my-agent-pool"
	if tmpl.Name != wantPool || pool.Name != wantPool {
		t.Errorf("CRD pair names = %q/%q, want %q", tmpl.Name, pool.Name, wantPool)
	}
	if tmpl.Namespace != agentNamespace || pool.Namespace != agentNamespace {
		t.Errorf("CRD pair namespaces = %q/%q, want %q", tmpl.Namespace, pool.Namespace, agentNamespace)
	}
	if tmpl.Spec.RuntimeRef != "my-agent" {
		t.Errorf("template runtimeRef = %q, want my-agent", tmpl.Spec.RuntimeRef)
	}
	if tmpl.Spec.IsolationProfile != "standard" {
		t.Errorf("template isolationProfile = %q, want standard (from runtime)", tmpl.Spec.IsolationProfile)
	}
	if tmpl.Spec.EgressProfile != echoPoolEgressProfile {
		t.Errorf("template egressProfile = %q, want %q (echo seed default)", tmpl.Spec.EgressProfile, echoPoolEgressProfile)
	}
	if tmpl.Spec.DNSPolicy != echoPoolDNSPolicy {
		t.Errorf("template dnsPolicy = %q, want %q (§13.2 opt-out)", tmpl.Spec.DNSPolicy, echoPoolDNSPolicy)
	}
	if tmpl.Spec.ResourceClass != echoPoolResourceClass {
		t.Errorf("template resourceClass = %q, want %q", tmpl.Spec.ResourceClass, echoPoolResourceClass)
	}
	if pool.Spec.TemplateRef != wantPool {
		t.Errorf("warm pool templateRef = %q, want %q", pool.Spec.TemplateRef, wantPool)
	}
	if pool.Spec.MinWarm != runtimeApplyWarmCount || pool.Spec.MaxWarm != runtimeApplyWarmCount {
		t.Errorf("warm pool minWarm/maxWarm = %d/%d, want %d/%d (single-pod hot pool)",
			pool.Spec.MinWarm, pool.Spec.MaxWarm, runtimeApplyWarmCount, runtimeApplyWarmCount)
	}
}

// TestRuntimePoolUnstructuredCarriesGVK_spec_4_6_2 covers that the derived pool
// pair is encoded as unstructured objects carrying the lenny.dev/v1alpha1 GVK,
// so the shared C1 applier resolves each GVR via the RESTMapper and
// server-side-applies it, the same dynamic-apply transport the echo seed uses.
//
// spec: §4.6.2 (the pool CRDs materialize through the dynamic-apply path).
func TestRuntimePoolUnstructuredCarriesGVK_spec_4_6_2(t *testing.T) {
	cr := &lennyv1alpha1.Runtime{}
	cr.Name = "my-agent"
	cr.Spec.IsolationProfile = "standard"

	objs, err := runtimePoolUnstructured(cr)
	if err != nil {
		t.Fatalf("runtimePoolUnstructured: %v", err)
	}
	if len(objs) != 2 {
		t.Fatalf("runtimePoolUnstructured returned %d objects, want 2", len(objs))
	}
	want := map[string]bool{"SandboxTemplate": false, "SandboxWarmPool": false}
	for _, o := range objs {
		if got := o.GetAPIVersion(); got != lennyv1alpha1.GroupVersion.String() {
			t.Errorf("%s apiVersion = %q, want %q", o.GetKind(), got, lennyv1alpha1.GroupVersion.String())
		}
		if o.GetNamespace() != agentNamespace {
			t.Errorf("%s namespace = %q, want %q", o.GetKind(), o.GetNamespace(), agentNamespace)
		}
		if _, ok := want[o.GetKind()]; ok {
			want[o.GetKind()] = true
		}
	}
	for kind, seen := range want {
		if !seen {
			t.Errorf("runtimePoolUnstructured produced no %s", kind)
		}
	}
}
