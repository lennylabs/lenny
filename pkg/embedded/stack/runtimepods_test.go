// SPDX-License-Identifier: MIT

package stack

import (
	"context"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/lennylabs/lenny/pkg/sandbox/state"
)

// sidecarAgentPod builds an agent pod carrying the §6.2 runtime-name label and
// the sidecar-model container set (the runtime container plus the stamped
// "adapter" sidecar), so a test exercises the container names
// RunningRuntimePodContainerNames returns for a sidecar-model runtime.
func sidecarAgentPod(name, runtimeName string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: agentNamespace,
			Labels:    map[string]string{state.LabelRuntime: runtimeName},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "runtime", Image: "ghcr.io/acme/my-agent:dev"},
				{Name: "adapter", Image: "ghcr.io/lennylabs/lenny-adapter:dev"},
			},
		},
	}
}

// TestRunningRuntimePodContainerNamesReturnsSidecarAdapter asserts the helper
// the §17.4 custom-runtime smoke leg uses returns the "adapter" sidecar
// container name for a placed sidecar-model runtime, selected by the §6.2
// runtime-name label. This pins that a sidecar-model runtime's agent pod
// carries the lenny-adapter container the controller stamps, the runtime-agnostic
// placement property the smoke leg asserts end to end.
//
// spec: §17.4 (a sidecar-model runtime runs with the stamped lenny-adapter
// container), §6.2 (the runtime-name pod label), §4.7 (the adapter sidecar).
func TestRunningRuntimePodContainerNamesReturnsSidecarAdapter(t *testing.T) {
	withClusterClient(t, k8sfake.NewSimpleClientset(
		sidecarAgentPod("my-agent-pod-1", "my-agent"),
	))
	names, err := RunningRuntimePodContainerNames(context.Background(), "/state/kubeconfig.yaml", "my-agent")
	if err != nil {
		t.Fatalf("RunningRuntimePodContainerNames: %v", err)
	}
	if !containsString(names, "adapter") {
		t.Errorf("container names = %v, want the stamped sidecar 'adapter' container", names)
	}
	if !containsString(names, "runtime") {
		t.Errorf("container names = %v, want the runtime container", names)
	}
}

// TestRunningRuntimePodContainerNamesEmptyForUnplacedRuntime asserts the helper
// returns no container names (and no error) for a runtime with no placed agent
// pod, so the smoke leg distinguishes "not yet placed" from a stamping defect
// rather than treating an empty result as an error.
//
// spec: §17.4 (the runtime-name selector matches only placed agent pods).
func TestRunningRuntimePodContainerNamesEmptyForUnplacedRuntime(t *testing.T) {
	// A pod for a different runtime must not match the selector.
	withClusterClient(t, k8sfake.NewSimpleClientset(
		sidecarAgentPod("other-pod", "other-runtime"),
	))
	names, err := RunningRuntimePodContainerNames(context.Background(), "/state/kubeconfig.yaml", "my-agent")
	if err != nil {
		t.Fatalf("RunningRuntimePodContainerNames: %v", err)
	}
	if len(names) != 0 {
		t.Errorf("container names = %v, want none for an unplaced runtime", names)
	}
}

// TestRunningRuntimePodContainerNamesFailsClosedOnClientError asserts the helper
// returns an error when the cluster client cannot be built, so the smoke leg
// surfaces a cluster-access failure rather than silently reporting no adapter.
//
// spec: §17.4 (the cluster commands reach the control plane through the embedded
// kubeconfig and fail closed when it is unreachable).
func TestRunningRuntimePodContainerNamesFailsClosedOnClientError(t *testing.T) {
	prev := clusterClientFn
	t.Cleanup(func() { clusterClientFn = prev })
	clusterClientFn = func(string) (kubernetes.Interface, error) {
		return nil, errors.New("kubeconfig unreadable")
	}
	if _, err := RunningRuntimePodContainerNames(context.Background(), "/state/kubeconfig.yaml", "my-agent"); err == nil {
		t.Error("RunningRuntimePodContainerNames with an unbuildable client = nil error, want a fail-closed error")
	}
}

// containsString reports whether s is in list.
func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
