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
	"k8s.io/utils/ptr"

	"github.com/lennylabs/lenny/pkg/admission/webhook"
)

// spec: §13.1 / §18.33 — the lenny-pod-security webhook transport
// decodes a Pod, translates its securityContext into the podsecurity
// validator's input, and rejects any pod that breaches a §13.1
// pod-security invariant, including the RuntimeDefault seccomp profile.

// psCredReadersGID is the lenny-cred-readers GID used in the tests.
const psCredReadersGID int64 = 65534

// hardenedContainer returns a container that satisfies every §13.1
// per-container invariant. It sets no seccomp profile of its own and
// inherits the pod-level profile.
func hardenedContainer(name string) corev1.Container {
	return corev1.Container{
		Name:  name,
		Image: "ghcr.io/lennylabs/" + name + "@sha256:aaa",
		SecurityContext: &corev1.SecurityContext{
			RunAsNonRoot:             ptr.To(true),
			AllowPrivilegeEscalation: ptr.To(false),
			Privileged:               ptr.To(false),
			ReadOnlyRootFilesystem:   ptr.To(true),
			Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		},
	}
}

// hardenedPod returns a pod that satisfies every §13.1 invariant: the
// shape the agent podspec produces.
func hardenedPod() corev1.Pod {
	return corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "agent-pod", Namespace: "lenny-agents"},
		Spec: corev1.PodSpec{
			SecurityContext: &corev1.PodSecurityContext{
				RunAsNonRoot:   ptr.To(true),
				FSGroup:        ptr.To(psCredReadersGID),
				SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
			},
			Containers: []corev1.Container{
				hardenedContainer("adapter"),
				hardenedContainer("runtime"),
			},
		},
	}
}

func psPodRaw(t *testing.T, pod corev1.Pod) runtime.RawExtension {
	t.Helper()
	raw, err := json.Marshal(pod)
	if err != nil {
		t.Fatalf("marshal pod: %v", err)
	}
	return runtime.RawExtension{Raw: raw}
}

func psDecide(t *testing.T, pod corev1.Pod) *admissionv1.AdmissionResponse {
	t.Helper()
	return webhook.PodSecurity(psCredReadersGID)(context.Background(), &admissionv1.AdmissionRequest{
		UID:       "ps",
		Operation: admissionv1.Create,
		Kind:      metav1.GroupVersionKind{Kind: "Pod"},
		Object:    psPodRaw(t, pod),
	})
}

func TestPodSecurityAdmitsHardenedPod(t *testing.T) {
	if resp := psDecide(t, hardenedPod()); !resp.Allowed {
		t.Fatalf("a §13.1-compliant pod should be admitted: %+v", resp.Result)
	}
}

func TestPodSecurityRejectsHostSharing(t *testing.T) {
	pod := hardenedPod()
	pod.Spec.HostPID = true
	resp := psDecide(t, pod)
	if resp.Allowed {
		t.Fatalf("hostPID: true must be rejected")
	}
	if !strings.Contains(resp.Result.Message, "POD_SPEC_HOST_SHARING_FORBIDDEN") {
		t.Errorf("rejection should cite POD_SPEC_HOST_SHARING_FORBIDDEN, got %q", resp.Result.Message)
	}
}

func TestPodSecurityRejectsMissingSeccompProfile(t *testing.T) {
	// Neither the pod nor any container sets a seccomp profile: the
	// §13.1 / §17.2 RuntimeDefault requirement is unmet.
	pod := hardenedPod()
	pod.Spec.SecurityContext.SeccompProfile = nil
	resp := psDecide(t, pod)
	if resp.Allowed {
		t.Fatalf("a pod with no seccomp profile must be rejected")
	}
	if !strings.Contains(resp.Result.Message, "seccompProfile.type must be RuntimeDefault") {
		t.Errorf("rejection should cite the seccomp requirement, got %q", resp.Result.Message)
	}
}

func TestPodSecurityRejectsUnconfinedSeccompProfile(t *testing.T) {
	pod := hardenedPod()
	pod.Spec.SecurityContext.SeccompProfile = &corev1.SeccompProfile{
		Type: corev1.SeccompProfileTypeUnconfined,
	}
	resp := psDecide(t, pod)
	if resp.Allowed {
		t.Fatalf("an Unconfined seccomp profile must be rejected")
	}
	if !strings.Contains(resp.Result.Message, "got Unconfined") {
		t.Errorf("rejection should name the offending profile, got %q", resp.Result.Message)
	}
}

func TestPodSecurityRejectsContainerSeccompOverride(t *testing.T) {
	// The pod-level profile is RuntimeDefault, but a container overrides
	// it with Unconfined. The container-level value wins and is rejected.
	pod := hardenedPod()
	pod.Spec.Containers[1].SecurityContext.SeccompProfile = &corev1.SeccompProfile{
		Type: corev1.SeccompProfileTypeUnconfined,
	}
	resp := psDecide(t, pod)
	if resp.Allowed {
		t.Fatalf("a container Unconfined seccomp override must be rejected")
	}
}

func TestPodSecurityRejectsMissingCredFSGroup(t *testing.T) {
	pod := hardenedPod()
	pod.Spec.SecurityContext.FSGroup = nil
	resp := psDecide(t, pod)
	if resp.Allowed {
		t.Fatalf("a pod missing the credential fsGroup must be rejected")
	}
	if !strings.Contains(resp.Result.Message, "POD_SPEC_CRED_FSGROUP_MISSING") {
		t.Errorf("rejection should cite POD_SPEC_CRED_FSGROUP_MISSING, got %q", resp.Result.Message)
	}
}

func TestPodSecurityRejectsPrivilegedContainer(t *testing.T) {
	pod := hardenedPod()
	pod.Spec.Containers[0].SecurityContext.Privileged = ptr.To(true)
	resp := psDecide(t, pod)
	if resp.Allowed {
		t.Fatalf("a privileged container must be rejected")
	}
}

func TestPodSecurityValidatesInitContainers(t *testing.T) {
	// An init container with an undropped capability set rejects the
	// pod: §13.1 invariants apply to init containers too.
	pod := hardenedPod()
	bad := hardenedContainer("warm")
	bad.SecurityContext.Capabilities = &corev1.Capabilities{Drop: []corev1.Capability{"NET_ADMIN"}}
	pod.Spec.InitContainers = []corev1.Container{bad}
	resp := psDecide(t, pod)
	if resp.Allowed {
		t.Fatalf("a non-compliant init container must reject the pod")
	}
	if !strings.Contains(resp.Result.Message, `container "warm"`) {
		t.Errorf("rejection should name the init container, got %q", resp.Result.Message)
	}
}

func TestPodSecurityRejectsUndecodablePod(t *testing.T) {
	resp := webhook.PodSecurity(psCredReadersGID)(context.Background(),
		&admissionv1.AdmissionRequest{
			UID:       "ps-bad",
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
