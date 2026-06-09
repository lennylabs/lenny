// SPDX-License-Identifier: MIT

//go:build integration

package integration_test

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
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// rcPolicy is the §17.2 RuntimeClass-aware relaxation the chart wires from
// runtimeClasses.profiles.{sandboxed,microvm}.name (gvisor, kata). The
// lenny-pod-security webhook applies the relaxed PSS baseline to pods
// carrying those RuntimeClass names and the full Restricted baseline to
// every other pod (the runc/standard profile).
var rcPolicy = podsecurity.RuntimeClassPolicy{
	GVisorRuntimeClass: isolation.MustRuntimeClassName(isolation.ProfileSandboxed),
	KataRuntimeClass:   isolation.MustRuntimeClassName(isolation.ProfileMicrovm),
}

// admitAgentPod runs a controller-built pod through the deployed
// lenny-pod-security ValidatingAdmissionWebhook Decider exactly as the
// lenny-webhook binary serves it (wired with the controller's
// CredReadersGID and CredVolumeName so validator and controller share one
// source of truth), and returns the admission response.
func admitAgentPod(t *testing.T, pod corev1.Pod) *admissionv1.AdmissionResponse {
	t.Helper()
	raw, err := json.Marshal(pod)
	if err != nil {
		t.Fatalf("marshal pod: %v", err)
	}
	return webhook.PodSecurity(podspec.CredReadersGID, podspec.CredVolumeName, rcPolicy)(
		context.Background(),
		&admissionv1.AdmissionRequest{
			UID:       "admission-policy",
			Operation: admissionv1.Create,
			Kind:      metav1.GroupVersionKind{Kind: "Pod"},
			Object:    runtime.RawExtension{Raw: raw},
		},
	)
}

// buildAgentPod builds a controller pod spec for the given isolation
// profile and workspace tier via the production podspec.Build path.
func buildAgentPod(t *testing.T, profile isolation.Profile, workspaceTier string) corev1.Pod {
	t.Helper()
	in := podspec.Inputs{
		Name:             "claude-worker-" + string(profile),
		Namespace:        "lenny-agents",
		Labels:           map[string]string{"lenny.dev/pool": "claude-worker"},
		RuntimeImage:     "ghcr.io/acme/claude-code:v1",
		AdapterImage:     "ghcr.io/lennylabs/lenny-adapter:v1",
		IsolationProfile: string(profile),
		WorkspaceTier:    workspaceTier,
	}
	pod, err := podspec.Build(in)
	if err != nil {
		t.Fatalf("podspec.Build(%s, tier=%q): %v", profile, workspaceTier, err)
	}
	return *pod
}

// TestAdmissionPolicyAcceptsControllerPodSpecsPerRuntimeClass is the §17.2
// admission_policy_test.go suite (line 84): it verifies that
// controller-generated pod specs for each RuntimeClass pass the deployed
// admission policies, preventing policy/spec drift from causing warm-pool
// deadlock (a controller building a spec the deployed lenny-pod-security
// webhook would then reject, so every warm pod fails admission).
//
// One subtest per isolation profile asserts its RuntimeClass-resolved pod
// is admitted: standard→runc (full Restricted PSS), sandboxed→gvisor and
// microvm→kata (RuntimeClass-relaxed PSS).
//
// spec: §17.2 line 84 (admission_policy_test.go); §13.1 (pod-security
// baseline); §17.2 items 1-2 (RuntimeClass-specific enforcement). F-17.2.15.
func TestAdmissionPolicyAcceptsControllerPodSpecsPerRuntimeClass(t *testing.T) {
	profiles := []isolation.Profile{
		isolation.ProfileStandard,
		isolation.ProfileSandboxed,
		isolation.ProfileMicrovm,
	}
	for _, p := range profiles {
		p := p
		t.Run(string(p), func(t *testing.T) {
			rc := isolation.MustRuntimeClassName(p)
			pod := buildAgentPod(t, p, "")
			if got := pod.Spec.RuntimeClassName; got == nil || *got != rc {
				t.Fatalf("controller did not stamp the expected RuntimeClass for %s: got %v, want %q", p, got, rc)
			}
			resp := admitAgentPod(t, pod)
			if !resp.Allowed {
				t.Fatalf("controller-built %s pod (RuntimeClass %q) was rejected by lenny-pod-security: %+v",
					p, rc, resp.Result)
			}
		})
	}
}

// TestAdmissionPolicyAcceptsT4ControllerPodSpec covers the §6.4 T4
// dedicated-node workspace tier: the controller stamps the T4 node
// isolation (nodeSelector, toleration, workspace-tier label) onto a
// sandboxed pod, and that pod must still pass the pod-security policy.
//
// spec: §17.2 line 84; §6.4 (T4 dedicated-node placement). F-17.2.15.
func TestAdmissionPolicyAcceptsT4ControllerPodSpec(t *testing.T) {
	pod := buildAgentPod(t, isolation.ProfileSandboxed, "T4")
	resp := admitAgentPod(t, pod)
	if !resp.Allowed {
		t.Fatalf("controller-built T4 sandboxed pod was rejected by lenny-pod-security: %+v", resp.Result)
	}
}
