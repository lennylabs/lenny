// SPDX-License-Identifier: MIT

package prodsource

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/lennylabs/lenny/pkg/ops/diagnostics"
)

// K8sReader is the §25.6 Kubernetes pod-signal reader. It maps a pod's
// container statuses and scheduling conditions onto the diagnostics.Signals
// the cause chain classifies. spec: §25.6 lines 2890, 2899-2906. F-25.6.1.
type K8sReader struct {
	client    kubernetes.Interface
	namespace string
}

// NewK8sReader returns a K8sReader reading pods in namespace.
func NewK8sReader(client kubernetes.Interface, namespace string) *K8sReader {
	return &K8sReader{client: client, namespace: namespace}
}

// Compile-time assertion that *K8sReader satisfies the seam.
var _ PodSignals = (*K8sReader)(nil)

// imagePullWaitReasons are the kubelet waiting-state reasons that mean a
// pod cannot start because its container image will not pull.
var imagePullWaitReasons = map[string]bool{
	"ImagePullBackOff":  true,
	"ErrImagePull":      true,
	"InvalidImageName":  true,
	"ImageInspectError": true,
}

// Signals reads the failure signals for the named pod. A pod that no
// longer exists in the API returns found=false (the pod may have been
// garbage-collected after the session failed); a transient API error
// returns the error so the caller can mark the diagnosis degraded.
func (r *K8sReader) Signals(ctx context.Context, podName string) (diagnostics.Signals, bool, error) {
	pod, err := r.client.CoreV1().Pods(r.namespace).Get(ctx, podName, metav1.GetOptions{})
	if k8serrors.IsNotFound(err) {
		return diagnostics.Signals{}, false, nil
	}
	if err != nil {
		return diagnostics.Signals{}, false, err
	}
	return signalsFromPod(pod), true, nil
}

// signalsFromPod derives the §25.6 cause signals from a pod's status.
// Container-terminated state yields the exit code and OOM flag; a
// waiting image-pull reason yields the image-pull flag; an Unschedulable
// PodScheduled condition yields the resource-pressure flag. spec: §25.6
// lines 2899-2906.
func signalsFromPod(pod *corev1.Pod) diagnostics.Signals {
	var sig diagnostics.Signals
	statuses := append([]corev1.ContainerStatus{}, pod.Status.InitContainerStatuses...)
	statuses = append(statuses, pod.Status.ContainerStatuses...)
	for _, cs := range statuses {
		switch {
		case cs.State.Terminated != nil:
			if cs.State.Terminated.ExitCode != 0 {
				sig.ExitCode = int(cs.State.Terminated.ExitCode)
			}
			if cs.State.Terminated.Reason == "OOMKilled" {
				sig.OOMKilled = true
			}
		case cs.State.Waiting != nil:
			if imagePullWaitReasons[cs.State.Waiting.Reason] {
				sig.ImagePullError = true
			}
		}
		// A previous termination (CrashLoopBackOff) carries the last exit
		// code; surface it when the current state holds no terminated exit.
		if sig.ExitCode == 0 && cs.LastTerminationState.Terminated != nil {
			if cs.LastTerminationState.Terminated.ExitCode != 0 {
				sig.ExitCode = int(cs.LastTerminationState.Terminated.ExitCode)
			}
			if cs.LastTerminationState.Terminated.Reason == "OOMKilled" {
				sig.OOMKilled = true
			}
		}
	}
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodScheduled && cond.Status == corev1.ConditionFalse && cond.Reason == "Unschedulable" {
			sig.ResourcePressure = true
		}
	}
	return sig
}
