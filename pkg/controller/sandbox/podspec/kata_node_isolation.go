// SPDX-License-Identifier: MIT

package podspec

import (
	corev1 "k8s.io/api/core/v1"
)

const (
	// KataNodePoolLabelKey and KataNodePoolValue are the §17.2
	// dedicated-node-pool label (`lenny.dev/node-pool: kata`). The chart
	// stamps it on the Kata RuntimeClass's scheduling.nodeSelector
	// (control 1) and on the Kata node pool's nodes; the controller
	// injects the matching hard node affinity (control 2). The literal
	// must stay in sync with the `lenny.dev/node-pool` value the chart's
	// microvm RuntimeClass nodeSelector renders.
	KataNodePoolLabelKey = "lenny.dev/node-pool"
	KataNodePoolValue    = "kata"

	// KataIsolationTaintKey and KataIsolationTaintValue are the §17.2 dedicated-node taint (`lenny.dev/isolation=kata:NoSchedule`).
	// Kata node pools carry the taint so only pods with the matching
	// toleration (control 3, injected below) can land on Kata-dedicated
	// hardware, keeping non-Kata workloads off it even if the node-pool
	// label drifts.
	KataIsolationTaintKey   = "lenny.dev/isolation"
	KataIsolationTaintValue = "kata"
)

// applyKataNodeIsolation stamps the §17.2 hard node
// affinity and dedicated-node toleration onto a Kata (microvm) pod.
// spec: §17.2.
//
// Control 2 is a requiredDuringSchedulingIgnoredDuringExecution node
// affinity matching `lenny.dev/node-pool: kata`: as a defense-in-depth
// measure beyond the RuntimeClass nodeSelector (control 1), it makes
// scheduling fail rather than fall back to an unsuitable node. The
// requirement is ANDed into every existing node-selector term (or a
// single term is created when none exist) so it constrains every
// scheduling option, and the merge is idempotent across re-reconciles.
//
// Control 3 is the toleration for the `lenny.dev/isolation=kata:NoSchedule`
// taint the Kata node pool carries, appended only when an equivalent
// entry is not already present.
func applyKataNodeIsolation(pod *corev1.Pod) {
	req := corev1.NodeSelectorRequirement{
		Key:      KataNodePoolLabelKey,
		Operator: corev1.NodeSelectorOpIn,
		Values:   []string{KataNodePoolValue},
	}
	if pod.Spec.Affinity == nil {
		pod.Spec.Affinity = &corev1.Affinity{}
	}
	if pod.Spec.Affinity.NodeAffinity == nil {
		pod.Spec.Affinity.NodeAffinity = &corev1.NodeAffinity{}
	}
	na := pod.Spec.Affinity.NodeAffinity
	if na.RequiredDuringSchedulingIgnoredDuringExecution == nil ||
		len(na.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms) == 0 {
		na.RequiredDuringSchedulingIgnoredDuringExecution = &corev1.NodeSelector{
			NodeSelectorTerms: []corev1.NodeSelectorTerm{
				{MatchExpressions: []corev1.NodeSelectorRequirement{req}},
			},
		}
	} else {
		terms := na.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms
		for i := range terms {
			if !hasNodeSelectorRequirement(terms[i].MatchExpressions, req) {
				terms[i].MatchExpressions = append(terms[i].MatchExpressions, req)
			}
		}
	}

	want := corev1.Toleration{
		Key:      KataIsolationTaintKey,
		Operator: corev1.TolerationOpEqual,
		Value:    KataIsolationTaintValue,
		Effect:   corev1.TaintEffectNoSchedule,
	}
	for _, tol := range pod.Spec.Tolerations {
		if tol.Key == want.Key && tol.Value == want.Value && tol.Effect == want.Effect {
			return
		}
	}
	pod.Spec.Tolerations = append(pod.Spec.Tolerations, want)
}

// hasNodeSelectorRequirement reports whether reqs already contains an
// equivalent (key, operator, values) requirement, so applyKataNodeIsolation
// stays idempotent across re-reconciles.
func hasNodeSelectorRequirement(reqs []corev1.NodeSelectorRequirement, want corev1.NodeSelectorRequirement) bool {
	for _, r := range reqs {
		if r.Key == want.Key && r.Operator == want.Operator && stringsEqual(r.Values, want.Values) {
			return true
		}
	}
	return false
}

// stringsEqual reports whether two string slices are element-wise equal.
func stringsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
