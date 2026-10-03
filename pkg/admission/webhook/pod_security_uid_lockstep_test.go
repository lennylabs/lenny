// SPDX-License-Identifier: MIT

package webhook_test

import (
	"context"
	"encoding/json"
	"strings"
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
	return buildLockstepPod(t, lockstepVariant{model: string(podspec.DeploymentSidecar)}, adapter, agent, gid)
}

// lockstepVariant selects which controller-built pod the lock-step tests
// render: the deployment model and whether the test-only egress-capture
// sidecar is injected.
type lockstepVariant struct {
	name          string
	model         string
	egressCapture bool
}

// lockstepVariants are the controller-built pods every UID triple must
// pass the webhook with: the sidecar and embedded models, and the
// sidecar model with the egress-capture container, which runs at its own
// UID outside both reserved UIDs.
var lockstepVariants = []lockstepVariant{
	{name: "sidecar", model: string(podspec.DeploymentSidecar)},
	{name: "embedded", model: string(podspec.DeploymentEmbedded)},
	{name: "egress-capture", model: string(podspec.DeploymentSidecar), egressCapture: true},
}

// buildLockstepPod builds the agent pod for v through podspec.Build with
// the given identity triple.
func buildLockstepPod(t *testing.T, v lockstepVariant, adapter, agent, gid int64) corev1.Pod {
	t.Helper()
	in := podspec.Inputs{
		Name:             "claude-worker-lockstep",
		Namespace:        "lenny-agents",
		RuntimeImage:     "ghcr.io/acme/claude-code:v1",
		AdapterImage:     "ghcr.io/lennylabs/lenny-adapter:v1",
		IsolationProfile: "sandboxed",
		DeploymentModel:  v.model,
		AdapterUID:       adapter,
		AgentUID:         agent,
		CredReadersGID:   gid,
	}
	if v.egressCapture {
		in.EgressCapture = &podspec.EgressCapture{
			Image:    "ghcr.io/lennylabs/lenny-egress-capture:e2e",
			Upstream: "api.openai.com:443",
		}
	}
	pod, err := podspec.Build(in)
	if err != nil {
		t.Fatalf("podspec.Build(%s): %v", v.name, err)
	}
	return *pod
}

// decidePodSecurity runs pod through a lenny-pod-security webhook wired
// with the given reserved UIDs and lenny-cred-readers GID.
func decidePodSecurity(t *testing.T, pod corev1.Pod, adapterUID, agentUID, gid int64) *admissionv1.AdmissionResponse {
	t.Helper()
	raw, err := json.Marshal(pod)
	if err != nil {
		t.Fatalf("marshal pod: %v", err)
	}
	return webhook.PodSecurity(adapterUID, agentUID, gid, podspec.CredVolumeName, podsecurity.RuntimeClassPolicy{})(
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

	if resp := decidePodSecurity(t, pod, adapter, agent, gid); !resp.Allowed {
		t.Fatalf("controller-built pod with overridden UIDs must pass the pod-security webhook wired with the matching GID %d: %+v", gid, resp.Result)
	}

	// A webhook left at the default GID while the controller stamps the
	// override is the exact mismatch F-13.1.16 warns about: every agent
	// pod gets rejected (POD_SPEC_CRED_FSGROUP_MISSING).
	if resp := decidePodSecurity(t, pod, adapter, agent, podspec.CredReadersGID); resp.Allowed {
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

// TestPodSecurityAdmitsBuiltPodsAtEveryUIDTriple_spec_13_1 asserts the
// pod builder and the §13.1 container identity clause agree: the
// sidecar, embedded, and egress-capture pods the controller builds are
// admitted by a webhook wired with the same triple, at the chart
// defaults and at an overridden triple. A builder container without an
// explicit runAsGroup, or a platform container at a reserved UID it does
// not own, fails here.
//
// spec: 13.1 (Pod Security)
func TestPodSecurityAdmitsBuiltPodsAtEveryUIDTriple_spec_13_1(t *testing.T) {
	triples := []struct {
		name                string
		adapter, agent, gid int64
	}{
		{"default", podspec.AdapterUID, podspec.AgentUID, podspec.CredReadersGID},
		{"overridden", 70000, 70001, 70002},
	}
	for _, tr := range triples {
		for _, v := range lockstepVariants {
			t.Run(tr.name+"/"+v.name, func(t *testing.T) {
				pod := buildLockstepPod(t, v, tr.adapter, tr.agent, tr.gid)
				if resp := decidePodSecurity(t, pod, tr.adapter, tr.agent, tr.gid); !resp.Allowed {
					t.Fatalf("built %s pod must be admitted at the %s triple: %+v", v.name, tr.name, resp.Result)
				}
			})
		}
	}
}

// TestPodSecurityReservesOverriddenAgentUID_spec_13_1 asserts the webhook
// keys the §13.1 container identity reservation on the UIDs it is wired
// with: with security.podUIDs overridden, an injected regular container
// at the overridden agent UID is denied, and the same container at the
// default agent UID (no longer reserved) is admitted.
//
// spec: 13.1 (Pod Security)
func TestPodSecurityReservesOverriddenAgentUID_spec_13_1(t *testing.T) {
	const (
		adapter = int64(70000)
		agent   = int64(70001)
	)
	withInjected := func(uid int64) corev1.Pod {
		pod := buildLockstepPod(t, lockstepVariants[0], adapter, agent, podspec.CredReadersGID)
		injected := *pod.Spec.Containers[0].DeepCopy()
		injected.Name = "mesh-proxy"
		injected.VolumeMounts = nil
		injected.SecurityContext.RunAsUser = ptrInt64(uid)
		injected.SecurityContext.RunAsGroup = ptrInt64(uid)
		pod.Spec.Containers = append(pod.Spec.Containers, injected)
		return pod
	}

	resp := decidePodSecurity(t, withInjected(agent), adapter, agent, podspec.CredReadersGID)
	if resp.Allowed {
		t.Fatalf("an injected container at the overridden agent UID %d must be denied", agent)
	}
	want := `container "mesh-proxy" runAsUser 70001 is reserved for the "runtime" container (§13.1 Container identity)`
	if !strings.Contains(resp.Result.Message, want) {
		t.Errorf("denial should carry %q, got %q", want, resp.Result.Message)
	}

	if resp := decidePodSecurity(t, withInjected(podspec.AgentUID), adapter, agent, podspec.CredReadersGID); !resp.Allowed {
		t.Fatalf("an injected container at the default agent UID %d is not reserved once overridden: %+v", podspec.AgentUID, resp.Result)
	}
}

func ptrInt64(v int64) *int64 { return &v }
