// SPDX-License-Identifier: MIT

package ctlcli

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"github.com/lennylabs/lenny/pkg/controller/sandbox/podspec"
)

const (
	// selftestPodPrefix prefixes the one-off self-test pod's name. The CLI
	// mints the full name before it creates the pod, so it can delete the
	// pod by name even when the create call fails after the API server
	// stored it.
	selftestPodPrefix = "lenny-supervisor-preflight-"
)

// selftestPodSpec is what the self-test pod is built from: the values file's
// agent namespaces, pod identities, and supervisor image, and the RuntimeClass
// the operator names.
type selftestPodSpec struct {
	Name             string
	Namespace        string
	RuntimeClassName string
	SupervisorImage  string
	CredReadersGID   int64
}

// chartDefaultAgentNamespace is the first entry of the chart's default
// agentNamespaces list, the namespace an install whose values file leaves
// agentNamespaces unset places its agent pods in. spec: §24.2; §17.2.
const chartDefaultAgentNamespace = "lenny-agents"

// errSelftestPodInput is the class of every values-file or flag input the
// self-test pod cannot be built from.
var errSelftestPodInput = errors.New("preflight supervisor: cannot build the self-test pod")

// selftestPodSpecFromValues resolves the pod inputs from the values file.
// A values file that omits agentNamespaces or security.podUIDs resolves them
// to the chart defaults, the same values the controller's pod builder and the
// lenny-pod-security webhook apply to that install, so the pod's fsGroup is
// the GID the webhook enforces. It refuses a values file with no supervisor
// image, because the chart default for controller.supervisorImage is empty,
// and an agentNamespaces entry with an empty name.
// spec: §24.2; §13.1 (Runtime supervisor volume; Container identity).
func selftestPodSpecFromValues(v preflightValues, runtimeClass, name string) (selftestPodSpec, error) {
	if runtimeClass == "" {
		return selftestPodSpec{}, fmt.Errorf("%w: --runtime-class is required", errSelftestPodInput)
	}
	if v.Controller.SupervisorImage == "" {
		return selftestPodSpec{}, fmt.Errorf("%w: the values file sets no controller.supervisorImage", errSelftestPodInput)
	}
	namespace, err := selftestNamespace(v)
	if err != nil {
		return selftestPodSpec{}, err
	}
	uids := v.Security.PodUIDs
	adapter := identityOrDefault(uids.Adapter, podspec.AdapterUID)
	agent := identityOrDefault(uids.Agent, podspec.AgentUID)
	if adapter == podspec.SupervisorStageUID || agent == podspec.SupervisorStageUID {
		return selftestPodSpec{}, fmt.Errorf("%w: the supervisor stage UID %d collides with security.podUIDs", errSelftestPodInput, podspec.SupervisorStageUID)
	}
	return selftestPodSpec{
		Name:             name,
		Namespace:        namespace,
		RuntimeClassName: runtimeClass,
		SupervisorImage:  v.Controller.SupervisorImage,
		CredReadersGID:   identityOrDefault(uids.CredReadersGID, podspec.CredReadersGID),
	}, nil
}

// selftestNamespace is the install's first agent namespace: the values
// file's first agentNamespaces entry, or the chart default when the file
// lists none. spec: §24.2.
func selftestNamespace(v preflightValues) (string, error) {
	if len(v.AgentNamespaces) == 0 {
		return chartDefaultAgentNamespace, nil
	}
	if v.AgentNamespaces[0].Name == "" {
		return "", fmt.Errorf("%w: the first agentNamespaces entry has no name", errSelftestPodInput)
	}
	return v.AgentNamespaces[0].Name, nil
}

// identityOrDefault resolves a security.podUIDs value the way the chart and
// the controller's pod builder do: an unset (zero) value is the default.
// Zero is never a legitimate override because §13.1 forbids root.
// spec: §13.1 (Container identity).
func identityOrDefault(override, def int64) int64 {
	if override != 0 {
		return override
	}
	return def
}

// newSelftestPodName mints a unique self-test pod name.
func newSelftestPodName() (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("mint self-test pod name: %w", err)
	}
	return selftestPodPrefix + hex.EncodeToString(b[:]), nil
}

// buildSelftestPod builds the one-off pod that runs the supervisor image's
// `selftest` mode. The supervisor is the container's entrypoint, so it is
// PID 1 of the pod's PID namespace, as it is in the runtime container, and
// the kill-reach and init-immunity tests run under the same conditions.
//
// The pod is built so that lenny-pod-security admits it under §13.1: a
// non-root pod with fsGroup and supplementalGroups set to the
// lenny-cred-readers GID (the webhook requires both on every agent-namespace
// pod, although this pod mounts no credential), the RuntimeDefault seccomp
// profile, and the podspec self-test container at the supervisor stage UID.
// It
// sets restartPolicy Never so the pod ends in Succeeded or Failed after the
// one run, mounts no service-account token, and declares its resources
// because the agent namespaces carry a ResourceQuota.
// spec: §24.2; §4.7.11 (Runtime supervisor, Property self-test); §13.1.
func buildSelftestPod(s selftestPodSpec) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      s.Name,
			Namespace: s.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":       "lenny-supervisor-preflight",
				"app.kubernetes.io/managed-by": "lenny-ctl",
			},
		},
		Spec: corev1.PodSpec{
			RuntimeClassName:             ptr.To(s.RuntimeClassName),
			RestartPolicy:                corev1.RestartPolicyNever,
			AutomountServiceAccountToken: ptr.To(false),
			EnableServiceLinks:           ptr.To(false),
			SecurityContext: &corev1.PodSecurityContext{
				RunAsNonRoot:       ptr.To(true),
				FSGroup:            ptr.To(s.CredReadersGID),
				SupplementalGroups: []int64{s.CredReadersGID},
				SeccompProfile:     &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
			},
			Containers: []corev1.Container{selftestContainer(s.SupervisorImage)},
		},
	}
}

// selftestContainer is the podspec self-test container with declared
// resources, because the agent namespaces carry a ResourceQuota that counts
// ephemeral-storage requests, which the namespace LimitRange does not
// default.
func selftestContainer(image string) corev1.Container {
	c := podspec.SupervisorSelftestContainer(image)
	c.Resources = corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:              resource.MustParse("10m"),
			corev1.ResourceMemory:           resource.MustParse("16Mi"),
			corev1.ResourceEphemeralStorage: resource.MustParse("1Mi"),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:              resource.MustParse("100m"),
			corev1.ResourceMemory:           resource.MustParse("64Mi"),
			corev1.ResourceEphemeralStorage: resource.MustParse("16Mi"),
		},
	}
	return c
}
