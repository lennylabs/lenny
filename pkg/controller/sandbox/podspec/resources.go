// SPDX-License-Identifier: MIT

package podspec

import (
	corev1 "k8s.io/api/core/v1"
)

// applyResources stamps the §5.2 resource class (resolved to CPU/memory
// requests and limits) onto every Lenny container in the pod. It runs
// before injectEgressCaptureSidecar so the test-only egress sidecar is not
// given the agent's resource budget. spec: §6.4 — the memory limit
// bounds the pod's combined agent-process-plus-tmpfs footprint with a
// predictable OOM boundary. A nil Resources leaves containers unconstrained.
func applyResources(in Inputs, pod *corev1.Pod) {
	if in.Resources == nil {
		return
	}
	for i := range pod.Spec.Containers {
		pod.Spec.Containers[i].Resources = *in.Resources.DeepCopy()
	}
}
