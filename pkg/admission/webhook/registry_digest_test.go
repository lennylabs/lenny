// SPDX-License-Identifier: MIT

package webhook_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/lennylabs/lenny/pkg/admission/webhook"
)

// spec: §13.1 / §18.33 — the lenny-registry-digest webhook transport
// decodes a Pod, flattens its container images, and rejects any pod
// whose references are not digest-pinned when
// platform.registry.requireDigest is true.

func rdPodRaw(t *testing.T, pod corev1.Pod) runtime.RawExtension {
	t.Helper()
	raw, err := json.Marshal(pod)
	if err != nil {
		t.Fatalf("marshal pod: %v", err)
	}
	return runtime.RawExtension{Raw: raw}
}

func rdDecide(t *testing.T, requireDigest bool, pod corev1.Pod) *admissionv1.AdmissionResponse {
	t.Helper()
	return webhook.RegistryDigest(requireDigest)(context.Background(),
		&admissionv1.AdmissionRequest{
			UID:       "rd",
			Operation: admissionv1.Create,
			Kind:      metav1.GroupVersionKind{Kind: "Pod"},
			Namespace: pod.Namespace,
			Name:      pod.Name,
			Object:    rdPodRaw(t, pod),
		})
}

func rdHardenedPod() corev1.Pod {
	return corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "agent-pod", Namespace: "lenny-agents"},
		Spec: corev1.PodSpec{
			InitContainers: []corev1.Container{
				{Name: "warm", Image: "ghcr.io/lennylabs/warm@sha256:abc"},
			},
			Containers: []corev1.Container{
				{Name: "adapter", Image: "ghcr.io/lennylabs/adapter@sha256:def"},
				{Name: "runtime", Image: "ghcr.io/lennylabs/runtime@sha256:012"},
			},
		},
	}
}

func TestRegistryDigestAdmitsDigestPinnedPod(t *testing.T) {
	resp := rdDecide(t, true, rdHardenedPod())
	if !resp.Allowed {
		t.Fatalf("a digest-pinned pod should be admitted: %+v", resp.Result)
	}
}

func TestRegistryDigestAdmitsWhenDisabled(t *testing.T) {
	pod := rdHardenedPod()
	pod.Spec.Containers[0].Image = "ghcr.io/lennylabs/adapter:1.2.3"
	resp := rdDecide(t, false, pod)
	if !resp.Allowed {
		t.Fatalf("requireDigest=false should admit any pod, got %+v", resp.Result)
	}
}

func TestRegistryDigestRejectsTagPinnedContainer(t *testing.T) {
	pod := rdHardenedPod()
	pod.Spec.Containers[0].Image = "ghcr.io/lennylabs/adapter:1.5.0"
	resp := rdDecide(t, true, pod)
	if resp.Allowed {
		t.Fatalf("a tag-only image must reject when requireDigest=true")
	}
	if resp.Result.Code != http.StatusForbidden {
		t.Errorf("Code = %d, want 403", resp.Result.Code)
	}
	if !strings.Contains(resp.Result.Message, "POD_IMAGE_DIGEST_REQUIRED") {
		t.Errorf("rejection should cite POD_IMAGE_DIGEST_REQUIRED, got %q", resp.Result.Message)
	}
	if !strings.Contains(resp.Result.Message, "ghcr.io/lennylabs/adapter:1.5.0") {
		t.Errorf("rejection should name the offending image, got %q", resp.Result.Message)
	}
}

func TestRegistryDigestRejectsTagPinnedInitContainer(t *testing.T) {
	pod := rdHardenedPod()
	pod.Spec.InitContainers[0].Image = "ghcr.io/lennylabs/warm:1.5.0"
	resp := rdDecide(t, true, pod)
	if resp.Allowed {
		t.Fatalf("a tag-only init container must reject when requireDigest=true")
	}
	if !strings.Contains(resp.Result.Message, "warm:1.5.0") {
		t.Errorf("rejection should name the init-container image, got %q", resp.Result.Message)
	}
}

func TestRegistryDigestRejectsUndecodablePod(t *testing.T) {
	resp := webhook.RegistryDigest(true)(context.Background(),
		&admissionv1.AdmissionRequest{
			UID:       "rd-bad",
			Operation: admissionv1.Create,
			Kind:      metav1.GroupVersionKind{Kind: "Pod"},
			Object:    runtime.RawExtension{Raw: []byte("{not a pod")},
		})
	if resp.Allowed {
		t.Fatalf("an undecodable pod must be rejected fail-closed")
	}
	if resp.Result.Code != http.StatusBadRequest {
		t.Errorf("Code = %d, want 400", resp.Result.Code)
	}
}

func TestRegistryDigestFlattenedAllContainerKinds(t *testing.T) {
	pod := rdHardenedPod()
	// Add an ephemeral container with a tag-only image.
	pod.Spec.EphemeralContainers = []corev1.EphemeralContainer{
		{
			EphemeralContainerCommon: corev1.EphemeralContainerCommon{
				Name:  "debug",
				Image: "ghcr.io/lennylabs/debug:latest",
			},
		},
	}
	resp := rdDecide(t, true, pod)
	if resp.Allowed {
		t.Fatalf("an ephemeral container with a tag-only image must reject")
	}
	if !strings.Contains(resp.Result.Message, "debug:latest") {
		t.Errorf("rejection should name the ephemeral image, got %q", resp.Result.Message)
	}
}
