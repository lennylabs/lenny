// SPDX-License-Identifier: MIT

package webhook_test

import (
	"context"
	"strings"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/lennylabs/lenny/pkg/admission/webhook"
	"github.com/lennylabs/lenny/pkg/controller/sandbox/podspec"
	"github.com/lennylabs/lenny/pkg/podsecurity"
)

// supervisedBuiltPod is the supervised sidecar pod the controller's pod
// builder renders, so the decider is checked against the production rendering.
func supervisedBuiltPod(t *testing.T) corev1.Pod {
	t.Helper()
	pod, err := podspec.Build(podspec.Inputs{
		Name: "sb", Namespace: "lenny-agents",
		RuntimeImage:     "ghcr.io/acme/agent@sha256:" + strings.Repeat("1", 64),
		AdapterImage:     "ghcr.io/lennylabs/lenny-adapter@sha256:" + strings.Repeat("2", 64),
		SupervisorImage:  "ghcr.io/lennylabs/lenny-supervisor@sha256:" + strings.Repeat("3", 64),
		IsolationProfile: "standard",
		RuntimeCommand:   []string{"/usr/local/bin/agent"},
		LaunchMode:       podspec.LaunchSession,
	})
	if err != nil {
		t.Fatalf("podspec.Build: %v", err)
	}
	return *pod
}

func decideBuilt(t *testing.T, pod corev1.Pod) *admissionv1.AdmissionResponse {
	t.Helper()
	return webhook.PodSecurity(podspec.AdapterUID, podspec.AgentUID, podspec.CredReadersGID,
		podspec.CredVolumeName, podspec.SupervisorVolumeName, podsecurity.RuntimeClassPolicy{})(
		context.Background(), &admissionv1.AdmissionRequest{
			UID: "ps-sup", Operation: admissionv1.Create,
			Kind: metav1.GroupVersionKind{Kind: "Pod"}, Object: psPodRaw(t, pod),
		},
	)
}

// writableSupervisorMount is a supervisor volume mount with readOnly unset.
func writableSupervisorMount() corev1.VolumeMount {
	return corev1.VolumeMount{Name: podspec.SupervisorVolumeName, MountPath: "/sup"}
}

// extraContainer is a §13.1-compliant container at a non-reserved UID.
func extraContainer(name string) corev1.Container {
	uid := int64(4321)
	no, yes := false, true
	return corev1.Container{
		Name: name, Image: "ghcr.io/acme/tool@sha256:" + strings.Repeat("4", 64),
		VolumeMounts: []corev1.VolumeMount{writableSupervisorMount()},
		SecurityContext: &corev1.SecurityContext{
			RunAsUser: &uid, RunAsGroup: &uid, RunAsNonRoot: &yes,
			AllowPrivilegeEscalation: &no, ReadOnlyRootFilesystem: &yes,
			Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		},
	}
}

// spec: 13.1 (Runtime supervisor volume; Container identity), 4.7.10 (Runtime supervisor)
func TestPodSecurityAdmitsTheSupervisedPodTheBuilderRenders_spec_13_1(t *testing.T) {
	if resp := decideBuilt(t, supervisedBuiltPod(t)); !resp.Allowed {
		t.Fatalf("the builder's supervised pod is refused: %s", resp.Result.Message)
	}
}

// spec: 13.1 (Runtime supervisor volume)
func TestPodSecurityRefusesWritableSupervisorVolumeOutsideTheStageInitContainer_spec_13_1(t *testing.T) {
	always := corev1.ContainerRestartPolicyAlways
	cases := map[string]func(*corev1.Pod){
		"runtime container read-write": func(p *corev1.Pod) {
			for i := range p.Spec.Containers {
				if p.Spec.Containers[i].Name == podspec.RuntimeContainerName {
					for j := range p.Spec.Containers[i].VolumeMounts {
						if p.Spec.Containers[i].VolumeMounts[j].Name == podspec.SupervisorVolumeName {
							p.Spec.Containers[i].VolumeMounts[j].ReadOnly = false
						}
					}
				}
			}
		},
		"adapter container read-write": func(p *corev1.Pod) {
			p.Spec.Containers[0].VolumeMounts = append(p.Spec.Containers[0].VolumeMounts, writableSupervisorMount())
		},
		"stage init container with restartPolicy Always": func(p *corev1.Pod) {
			p.Spec.InitContainers[0].RestartPolicy = &always
		},
		"init container with another name": func(p *corev1.Pod) {
			p.Spec.InitContainers = append(p.Spec.InitContainers, extraContainer("rewriter"))
		},
		"regular container named lenny-supervisor-stage": func(p *corev1.Pod) {
			p.Spec.Containers = append(p.Spec.Containers, extraContainer(podspec.SupervisorStageContainerName))
		},
		"ephemeral container named lenny-supervisor-stage": func(p *corev1.Pod) {
			p.Spec.EphemeralContainers = append(p.Spec.EphemeralContainers, corev1.EphemeralContainer{
				EphemeralContainerCommon: corev1.EphemeralContainerCommon(extraContainer(podspec.SupervisorStageContainerName)),
			})
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			pod := supervisedBuiltPod(t)
			mutate(&pod)
			resp := decideBuilt(t, pod)
			if resp.Allowed {
				t.Fatal("a read-write supervisor volume mount outside the stage init container was admitted")
			}
			if !strings.Contains(resp.Result.Message, "Runtime supervisor volume") {
				t.Errorf("refusal does not cite the supervisor volume rule: %s", resp.Result.Message)
			}
		})
	}
}

// spec: 13.1 (Runtime supervisor volume)
func TestValidateAgentPodSupervisorVolumeRuleDisabledWithoutAVolumeName_spec_13_1(t *testing.T) {
	c := podsecurity.ContainerSpec{
		Name:         "runtime",
		VolumeMounts: []podsecurity.VolumeMount{{Name: podspec.SupervisorVolumeName, MountPath: "/sup"}},
	}
	err := podsecurity.ValidateAgentPod(podsecurity.PodSpec{Containers: []podsecurity.ContainerSpec{c}}, podspec.CredReadersGID, podsecurity.RuntimeClassPolicy{})
	if pse, ok := err.(*podsecurity.PodSecurityError); ok && pse.HasViolation("Runtime supervisor volume") {
		t.Errorf("rule fired with no supervisor volume name wired: %v", err)
	}
	spec := podsecurity.PodSpec{SupervisorVolumeName: podspec.SupervisorVolumeName, Containers: []podsecurity.ContainerSpec{c}}
	err = podsecurity.ValidateAgentPod(spec, podspec.CredReadersGID, podsecurity.RuntimeClassPolicy{})
	pse, ok := err.(*podsecurity.PodSecurityError)
	if !ok || !pse.HasViolation("Runtime supervisor volume") {
		t.Errorf("err = %v, want the supervisor volume violation", err)
	}
	c.VolumeMounts[0].ReadOnly = true
	spec.Containers = []podsecurity.ContainerSpec{c}
	err = podsecurity.ValidateAgentPod(spec, podspec.CredReadersGID, podsecurity.RuntimeClassPolicy{})
	if pse, ok := err.(*podsecurity.PodSecurityError); ok && pse.HasViolation("Runtime supervisor volume") {
		t.Errorf("read-only mount refused: %v", err)
	}
}
