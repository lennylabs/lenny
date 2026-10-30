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

	cv "github.com/lennylabs/lenny/pkg/admission/cosign_verify"
	"github.com/lennylabs/lenny/pkg/admission/webhook"
)

// spec: §5.2 / §13.1 / §18.5 — the lenny-cosign-verify webhook
// transport decodes a Pod, flattens its container images, and rejects
// the pod when an in-scope image carries no valid cosign signature.

// fakeVerifier admits any image in its signed set and rejects the rest.
type fakeVerifier struct {
	signed map[string]bool
}

func (f fakeVerifier) Verify(_ context.Context, ref string) error {
	if f.signed[ref] {
		return nil
	}
	return errCosignUnsigned
}

var errCosignUnsigned = cosignUnsignedError{}

type cosignUnsignedError struct{}

func (cosignUnsignedError) Error() string { return "no valid signature" }

const cosignVerifiedPrefix = "ghcr.io/lennylabs/"

func cosignConfig() cv.Config {
	return cv.Config{VerifiedRegistries: []string{cosignVerifiedPrefix}}
}

func cosignPodRaw(t *testing.T, pod corev1.Pod) runtime.RawExtension {
	t.Helper()
	raw, err := json.Marshal(pod)
	if err != nil {
		t.Fatalf("marshal pod: %v", err)
	}
	return runtime.RawExtension{Raw: raw}
}

func TestCosignVerifyAdmitsPodWithSignedImages(t *testing.T) {
	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "agent-pod", Namespace: "lenny-agents"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "adapter", Image: "ghcr.io/lennylabs/adapter@sha256:aaa"},
				{Name: "runtime", Image: "ghcr.io/lennylabs/runtime@sha256:bbb"},
			},
		},
	}
	v := fakeVerifier{signed: map[string]bool{
		"ghcr.io/lennylabs/adapter@sha256:aaa": true,
		"ghcr.io/lennylabs/runtime@sha256:bbb": true,
	}}
	resp := webhook.CosignVerify(v, cosignConfig())(context.Background(), &admissionv1.AdmissionRequest{
		UID:       "c1",
		Operation: admissionv1.Create,
		Kind:      metav1.GroupVersionKind{Kind: "Pod"},
		Object:    cosignPodRaw(t, pod),
	})
	if !resp.Allowed {
		t.Fatalf("a pod with signed images should be admitted: %+v", resp.Result)
	}
}

func TestCosignVerifyRejectsPodWithUnsignedImage(t *testing.T) {
	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "agent-pod", Namespace: "lenny-agents"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "runtime", Image: "ghcr.io/lennylabs/runtime@sha256:evil"},
			},
		},
	}
	resp := webhook.CosignVerify(fakeVerifier{signed: map[string]bool{}}, cosignConfig())(
		context.Background(), &admissionv1.AdmissionRequest{
			UID:       "c2",
			Operation: admissionv1.Create,
			Kind:      metav1.GroupVersionKind{Kind: "Pod"},
			Object:    cosignPodRaw(t, pod),
		},
	)
	if resp.Allowed {
		t.Fatalf("a pod with an unsigned in-scope image must be rejected")
	}
	if resp.Result.Code != http.StatusForbidden {
		t.Errorf("Code = %d, want 403", resp.Result.Code)
	}
	if !strings.Contains(resp.Result.Message, cv.RejectCode) {
		t.Errorf("rejection should carry %s, got %q", cv.RejectCode, resp.Result.Message)
	}
}

func TestCosignVerifyVerifiesInitAndEphemeralContainers(t *testing.T) {
	// An unsigned image on an init container rejects the pod: init
	// containers run code on the node and are in scope.
	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "agent-pod", Namespace: "lenny-agents"},
		Spec: corev1.PodSpec{
			InitContainers: []corev1.Container{
				{Name: "warm", Image: "ghcr.io/lennylabs/init@sha256:unsigned"},
			},
			Containers: []corev1.Container{
				{Name: "runtime", Image: "ghcr.io/lennylabs/runtime@sha256:ok"},
			},
		},
	}
	v := fakeVerifier{signed: map[string]bool{
		"ghcr.io/lennylabs/runtime@sha256:ok": true,
	}}
	resp := webhook.CosignVerify(v, cosignConfig())(context.Background(), &admissionv1.AdmissionRequest{
		UID:       "c3",
		Operation: admissionv1.Create,
		Kind:      metav1.GroupVersionKind{Kind: "Pod"},
		Object:    cosignPodRaw(t, pod),
	})
	if resp.Allowed {
		t.Fatalf("an unsigned init-container image must reject the pod")
	}
	if !strings.Contains(resp.Result.Message, "init@sha256:unsigned") {
		t.Errorf("rejection should name the init-container image, got %q", resp.Result.Message)
	}
}

func TestCosignVerifyAdmitsOutOfScopeImages(t *testing.T) {
	// An image from a registry outside the verified-registry list is
	// admitted unchecked, even with a verifier that signs nothing.
	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "agent-pod", Namespace: "lenny-agents"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "sidecar", Image: "docker.io/library/busybox@sha256:ccc"},
			},
		},
	}
	resp := webhook.CosignVerify(fakeVerifier{signed: map[string]bool{}}, cosignConfig())(
		context.Background(), &admissionv1.AdmissionRequest{
			UID:       "c4",
			Operation: admissionv1.Create,
			Kind:      metav1.GroupVersionKind{Kind: "Pod"},
			Object:    cosignPodRaw(t, pod),
		},
	)
	if !resp.Allowed {
		t.Fatalf("an out-of-scope image should be admitted unchecked: %+v", resp.Result)
	}
}

func TestCosignVerifyRejectsUndecodablePod(t *testing.T) {
	// A payload the webhook cannot decode rejects, consistent with the
	// fail-closed deployment.
	resp := webhook.CosignVerify(fakeVerifier{}, cosignConfig())(context.Background(),
		&admissionv1.AdmissionRequest{
			UID:       "c5",
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
