// SPDX-License-Identifier: MIT

package podspec

import (
	corev1 "k8s.io/api/core/v1"

	"github.com/lennylabs/lenny/pkg/admission/t4_node_isolation"
)

// WorkspaceTierT4 is the §12.9 / §5.2 Restricted-tier value the Runtime
// CRD's `workspaceTier` enum carries. The pod builder injects the §6.4
// dedicated-node label/selector/toleration whenever Inputs.WorkspaceTier
// equals this constant.
const WorkspaceTierT4 = "T4"

// applyT4NodeIsolation stamps the §6.4 T4 dedicated-node selector,
// toleration, and pod label onto pod. spec: §6.4.
//
// The injection is idempotent and additive: it preserves any
// deployer-supplied nodeSelector entries (so a pool that pins a more
// specific node label still applies its own constraints), only adding
// the T4 label/value entry the webhook predicate matches on. The
// toleration is appended only when an equivalent entry is not already
// present so re-reconciliations do not accumulate duplicates.
func applyT4NodeIsolation(pod *corev1.Pod) {
	if pod.Labels == nil {
		pod.Labels = map[string]string{}
	}
	pod.Labels[t4_node_isolation.WorkspaceTierLabel] = t4_node_isolation.WorkspaceTierT4
	if pod.Spec.NodeSelector == nil {
		pod.Spec.NodeSelector = map[string]string{}
	}
	pod.Spec.NodeSelector[t4_node_isolation.NodeLabelKey] = t4_node_isolation.NodeLabelValue
	want := corev1.Toleration{
		Key:      t4_node_isolation.NodeTaintKey,
		Operator: corev1.TolerationOpEqual,
		Value:    t4_node_isolation.NodeTaintValue,
		Effect:   corev1.TaintEffectNoSchedule,
	}
	for _, tol := range pod.Spec.Tolerations {
		if tol.Key == want.Key && tol.Value == want.Value && tol.Effect == want.Effect {
			return
		}
	}
	pod.Spec.Tolerations = append(pod.Spec.Tolerations, want)
}
