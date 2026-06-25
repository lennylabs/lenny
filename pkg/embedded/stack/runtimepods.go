// SPDX-License-Identifier: MIT

package stack

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/lennylabs/lenny/pkg/sandbox/state"
)

// RunningRuntimePodContainerNames lists the container names across the agent
// pods a runtime currently has placed in the embedded cluster, deduplicated.
// It selects the runtime's agent pods by the §6.2 runtime-name label the
// Sandbox controller stamps on every agent pod (state.LabelRuntime), the same
// selector lenny logs runtime-<name> uses, and returns the set of container
// names found across them.
//
// The §17.4 custom-runtime smoke leg uses it to assert that a sidecar-model
// runtime's agent pod carries the lenny-adapter sidecar container the
// controller stamps (named "adapter"; pkg/controller/sandbox/podspec
// builds it), proving placement is runtime-agnostic rather than echo-only.
// An empty result means the runtime has no placed pod yet.
//
// spec: §17.4 (Embedded Mode places any runtime over the §4.7 boundary; a
// sidecar-model runtime runs with the stamped lenny-adapter container), §6.2
// (the runtime-name pod label), §4.7 (the adapter sidecar).
func RunningRuntimePodContainerNames(ctx context.Context, kubeconfigPath, runtimeName string) ([]string, error) {
	client, err := clusterClientFn(kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("embedded: build cluster client: %w", err)
	}
	selector := labels.SelectorFromSet(labels.Set{state.LabelRuntime: runtimeName})
	pods, err := client.CoreV1().Pods(agentNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: selector.String(),
	})
	if err != nil {
		return nil, fmt.Errorf("embedded: list %s agent pods for runtime %q: %w", agentNamespace, runtimeName, err)
	}
	seen := make(map[string]struct{})
	names := make([]string, 0)
	for i := range pods.Items {
		for _, c := range pods.Items[i].Spec.Containers {
			if _, ok := seen[c.Name]; ok {
				continue
			}
			seen[c.Name] = struct{}{}
			names = append(names, c.Name)
		}
	}
	return names, nil
}
