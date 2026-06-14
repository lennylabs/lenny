// SPDX-License-Identifier: MIT

package webhook_test

import (
	"context"
	"encoding/json"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/lennylabs/lenny/pkg/admission/webhook"
	"github.com/lennylabs/lenny/pkg/controller/sandbox/podspec"
	"github.com/lennylabs/lenny/pkg/podsecurity"
)

// buildOverriddenAgentPod returns a real controller-built agent pod whose
// non-root identities are overridden to a non-default triple, exercising
// the same podspec.Build path the controller runs in production.
func buildOverriddenAgentPod(t *testing.T, adapter, agent, gid int64) corev1.Pod {
	t.Helper()
	pod, err := podspec.Build(podspec.Inputs{
		Name:             "claude-worker-lockstep",
		Namespace:        "lenny-agents",
		RuntimeImage:     "ghcr.io/acme/claude-code:v1",
		AdapterImage:     "ghcr.io/lennylabs/lenny-adapter:v1",
		IsolationProfile: "sandboxed",
		AdapterUID:       adapter,
		AgentUID:         agent,
		CredReadersGID:   gid,
	})
	if err != nil {
		t.Fatalf("podspec.Build: %v", err)
	}
	return *pod
}

func decidePodSecurity(t *testing.T, pod corev1.Pod, webhookGID int64) *admissionv1.AdmissionResponse {
	t.Helper()
	raw, err := json.Marshal(pod)
	if err != nil {
		t.Fatalf("marshal pod: %v", err)
	}
	return webhook.PodSecurity(webhookGID, podspec.CredVolumeName, podsecurity.RuntimeClassPolicy{})(
		context.Background(),
		&admissionv1.AdmissionRequest{
			UID:       "lockstep",
			Operation: admissionv1.Create,
			Kind:      metav1.GroupVersionKind{Kind: "Pod"},
			Object:    runtime.RawExtension{Raw: raw},
		},
	)
}

// TestPodSecurityAdmitsControllerBuiltPodInLockStep_spec_13_1_16 is the
// load-bearing assertion behind F-13.1.16: a pod the controller builds
// with overridden §13.1 non-root UIDs is admitted by the lenny-pod-security
// webhook only when the webhook is wired with the SAME credReadersGID
// (the chart sources both from security.podUIDs). A webhook wired with the
// stale default GID rejects the same pod, which is exactly the breakage a
// partial override would cause — so the test proves the lock-step is real.
func TestPodSecurityAdmitsControllerBuiltPodInLockStep_spec_13_1_16(t *testing.T) {
	const (
		adapter = int64(70000)
		agent   = int64(70001)
		gid     = int64(70002)
	)
	pod := buildOverriddenAgentPod(t, adapter, agent, gid)

	if resp := decidePodSecurity(t, pod, gid); !resp.Allowed {
		t.Fatalf("controller-built pod with overridden UIDs must pass the pod-security webhook wired with the matching GID %d: %+v", gid, resp.Result)
	}

	// A webhook left at the default GID while the controller stamps the
	// override is the exact mismatch F-13.1.16 warns about: every agent
	// pod gets rejected (POD_SPEC_CRED_FSGROUP_MISSING).
	if resp := decidePodSecurity(t, pod, podspec.CredReadersGID); resp.Allowed {
		t.Fatalf("a pod-security webhook wired with the stale default GID %d must reject a pod built with fsGroup %d", podspec.CredReadersGID, gid)
	}
}

// TestEphemeralCredGuardHonoursOverriddenUIDs_spec_13_1_16 confirms the
// sibling ephemeral-container-cred-guard webhook also keys on the
// operator-tunable UIDs: an ephemeral debug container that runs as the
// overridden adapter UID is rejected (it would inherit the credential
// mount), the same protection the default UID receives. F-13.1.16.
func TestEphemeralCredGuardHonoursOverriddenUIDs_spec_13_1_16(t *testing.T) {
	const (
		adapter = int64(70000)
		agent   = int64(70001)
		gid     = int64(70002)
	)
	base := buildOverriddenAgentPod(t, adapter, agent, gid)

	// Add an ephemeral container that runs as the overridden adapter UID.
	updated := *base.DeepCopy()
	updated.Spec.EphemeralContainers = []corev1.EphemeralContainer{{
		EphemeralContainerCommon: corev1.EphemeralContainerCommon{
			Name:            "debugger",
			Image:           "busybox",
			SecurityContext: &corev1.SecurityContext{RunAsUser: ptrInt64(adapter)},
		},
	}}

	oldRaw, err := json.Marshal(base)
	if err != nil {
		t.Fatalf("marshal base: %v", err)
	}
	newRaw, err := json.Marshal(updated)
	if err != nil {
		t.Fatalf("marshal updated: %v", err)
	}
	resp := webhook.EphemeralContainerCredGuard(adapter, agent, gid, podspec.CredVolumeName)(
		context.Background(),
		&admissionv1.AdmissionRequest{
			UID:         "ephemeral",
			Operation:   admissionv1.Update,
			Kind:        metav1.GroupVersionKind{Kind: "Pod"},
			SubResource: "ephemeralcontainers",
			Object:      runtime.RawExtension{Raw: newRaw},
			OldObject:   runtime.RawExtension{Raw: oldRaw},
		},
	)
	if resp.Allowed {
		t.Fatalf("ephemeral container running as the overridden adapter UID %d must be rejected", adapter)
	}
}

func ptrInt64(v int64) *int64 { return &v }
