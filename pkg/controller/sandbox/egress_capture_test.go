// SPDX-License-Identifier: MIT

package sandbox_test

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/controller/sandbox"
	"github.com/lennylabs/lenny/pkg/controller/sandbox/podspec"
)

// spec: 12.9.8
// diagnosis: a Sandbox annotated with the §12.9.8 egress-capture
// upstream is reconciled into a pod that carries the
// lenny-egress-capture sidecar and the shared capture volume.
func TestReconcileInjectsEgressCaptureSidecarFromAnnotation(t *testing.T) {
	s := newScheme(t)
	sb := sandboxCR("")
	sb.Annotations = map[string]string{
		sandbox.EgressCaptureUpstreamAnnotation: "api.openai.com:443",
	}
	c := newClient(t, s, sb, runtimeCR())

	r := &sandbox.Reconciler{
		Client:             c,
		Scheme:             s,
		AdapterImage:       "ghcr.io/lennylabs/lenny-adapter:v1",
		EgressCaptureImage: "ghcr.io/lennylabs/lenny-egress-capture:e2e",
	}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: testNS, Name: testName},
	}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	var pod corev1.Pod
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: testName}, &pod); err != nil {
		t.Fatalf("get pod: %v", err)
	}

	if len(pod.Spec.Containers) != 3 {
		t.Fatalf("pod has %d containers, want 3 (adapter + runtime + egress-capture)", len(pod.Spec.Containers))
	}
	var captureSeen bool
	for _, ct := range pod.Spec.Containers {
		if ct.Name == podspec.EgressCaptureContainerName {
			captureSeen = true
			if ct.Image != "ghcr.io/lennylabs/lenny-egress-capture:e2e" {
				t.Errorf("egress-capture image = %q, want the reconciler's configured image", ct.Image)
			}
			argFound := false
			for _, a := range ct.Args {
				if a == "--upstream=api.openai.com:443" {
					argFound = true
				}
			}
			if !argFound {
				t.Errorf("egress-capture args = %v, missing --upstream from the annotation", ct.Args)
			}
		}
	}
	if !captureSeen {
		t.Errorf("egress-capture container missing from pod; want it injected by the §12.9.8 annotation")
	}
}

// spec: 12.9.8
// diagnosis: with no annotation the reconciler omits the
// egress-capture sidecar even when the image is configured. The
// §12.9.8 sidecar is opt-in per Sandbox.
func TestReconcileOmitsEgressCaptureWithoutAnnotation(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s, sandboxCR(""), runtimeCR())

	r := &sandbox.Reconciler{
		Client:             c,
		Scheme:             s,
		AdapterImage:       "ghcr.io/lennylabs/lenny-adapter:v1",
		EgressCaptureImage: "ghcr.io/lennylabs/lenny-egress-capture:e2e",
	}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: testNS, Name: testName},
	}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	var pod corev1.Pod
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: testName}, &pod); err != nil {
		t.Fatalf("get pod: %v", err)
	}
	for _, ct := range pod.Spec.Containers {
		if ct.Name == podspec.EgressCaptureContainerName {
			t.Errorf("egress-capture container present on un-annotated Sandbox; want absent")
		}
	}
}

// spec: 12.9.8
// diagnosis: with the EgressCaptureImage unset the reconciler omits
// the sidecar even when the annotation is present. The §12.9.8
// sidecar requires both the per-Sandbox annotation and the
// controller-wide image to be configured; production installs do not
// set the image so the sidecar is unreachable there.
func TestReconcileOmitsEgressCaptureWithoutControllerImage(t *testing.T) {
	s := newScheme(t)
	sb := sandboxCR("")
	sb.Annotations = map[string]string{
		sandbox.EgressCaptureUpstreamAnnotation: "api.openai.com:443",
	}
	c := newClient(t, s, sb, runtimeCR())

	r := &sandbox.Reconciler{
		Client:       c,
		Scheme:       s,
		AdapterImage: "ghcr.io/lennylabs/lenny-adapter:v1",
		// EgressCaptureImage left empty.
	}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: testNS, Name: testName},
	}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	var pod corev1.Pod
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: testName}, &pod); err != nil {
		t.Fatalf("get pod: %v", err)
	}
	for _, ct := range pod.Spec.Containers {
		if ct.Name == podspec.EgressCaptureContainerName {
			t.Errorf("egress-capture container present without the controller image; want production posture")
		}
	}
}

// metav1 is referenced indirectly through sandboxCR; keeping a
// no-op anchor avoids gofumpt churn if the helper changes later.
var _ = metav1.ObjectMeta{}
