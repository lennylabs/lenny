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

// errSelftestPodInput is the class of every values-file or flag input the
// self-test pod cannot be built from.
var errSelftestPodInput = errors.New("preflight supervisor: cannot build the self-test pod")

// selftestPodSpecFromValues resolves the pod inputs from the values file. It
// refuses a values file that names no agent namespace, no supervisor image,
// or no lenny-cred-readers GID, because a pod built from a guessed value
// would test a namespace, an image, or an fsGroup other than the install's.
// spec: §24.2; §13.1 (Runtime supervisor volume; Container identity).
func selftestPodSpecFromValues(v preflightValues, runtimeClass, name string) (selftestPodSpec, error) {
	switch {
	case runtimeClass == "":
		return selftestPodSpec{}, fmt.Errorf("%w: --runtime-class is required", errSelftestPodInput)
	case len(v.AgentNamespaces) == 0 || v.AgentNamespaces[0].Name == "":
		return selftestPodSpec{}, fmt.Errorf("%w: the values file names no agentNamespaces", errSelftestPodInput)
	case v.Controller.SupervisorImage == "":
		return selftestPodSpec{}, fmt.Errorf("%w: the values file sets no controller.supervisorImage", errSelftestPodInput)
	case v.Security.PodUIDs.CredReadersGID == 0:
		return selftestPodSpec{}, fmt.Errorf("%w: the values file sets no security.podUIDs.credReadersGID", errSelftestPodInput)
	}
	uids := v.Security.PodUIDs
	if uids.Adapter == podspec.SupervisorStageUID || uids.Agent == podspec.SupervisorStageUID {
		return selftestPodSpec{}, fmt.Errorf("%w: the supervisor stage UID %d collides with security.podUIDs", errSelftestPodInput, podspec.SupervisorStageUID)
	}
	return selftestPodSpec{
		Name:             name,
		Namespace:        v.AgentNamespaces[0].Name,
		RuntimeClassName: runtimeClass,
		SupervisorImage:  v.Controller.SupervisorImage,
		CredReadersGID:   uids.CredReadersGID,
	}, nil
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
