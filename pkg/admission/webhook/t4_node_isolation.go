// SPDX-License-Identifier: MIT

package webhook

import (
	"context"
	"encoding/json"
	"net/http"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"

	t4 "github.com/lennylabs/lenny/pkg/admission/t4_node_isolation"
)

// T4NodeIsolation returns the Decider for the lenny-t4-node-isolation
// ValidatingAdmissionWebhook (§6.4 STR-003, §12.8, §12.9). It is
// installed fail-closed on Pod resources in agent namespaces and
// enforces the §6.4 T4 dedicated-node requirement: a T4 pod (carrying
// the lenny.dev/workspace-tier: t4 label the pool controller injects)
// must pin a T4 node via nodeSelector or nodeAffinity and tolerate the
// T4 node taint; a non-T4 pod must carry neither.
//
// A decode failure rejects, consistent with the webhook's fail-closed
// deployment — a pod the webhook cannot inspect must not be admitted
// onto a node whose T4 isolation it could not verify.
func T4NodeIsolation() Decider {
	return func(_ context.Context, req *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse {
		var pod corev1.Pod
		if err := json.Unmarshal(req.Object.Raw, &pod); err != nil {
			return Deny(http.StatusBadRequest, "decode Pod object: "+err.Error())
		}

		decision := t4.Decide(t4.Request{
			PodName:           podDisplayName(&pod, req),
			Namespace:         podNamespace(&pod, req),
			IsT4:              pod.Labels[t4.WorkspaceTierLabel] == t4.WorkspaceTierT4,
			NodeSelector:      pod.Spec.NodeSelector,
			NodeAffinityTerms: flattenNodeAffinity(pod.Spec.Affinity),
			Tolerations:       flattenTolerations(pod.Spec.Tolerations),
		})
		if decision.Allowed {
			return Allow()
		}
		return Deny(int32(decision.Code), decision.Reason)
	}
}

// podDisplayName returns a stable name for the pod in rejection
// messages. A pod created from a GenerateName has an empty
// metadata.name at admission time, so fall back to the GenerateName
// prefix and finally to the request name.
func podDisplayName(pod *corev1.Pod, req *admissionv1.AdmissionRequest) string {
	switch {
	case pod.Name != "":
		return pod.Name
	case pod.GenerateName != "":
		return pod.GenerateName + "*"
	default:
		return req.Name
	}
}

// podNamespace returns the pod's namespace, falling back to the
// admission request namespace when the object omits it.
func podNamespace(pod *corev1.Pod, req *admissionv1.AdmissionRequest) string {
	if pod.Namespace != "" {
		return pod.Namespace
	}
	return req.Namespace
}

// flattenNodeAffinity projects a pod's required node affinity into the
// flat term list the t4_node_isolation decision package consumes. Only
// requiredDuringSchedulingIgnoredDuringExecution terms are flattened:
// a preferred (soft) affinity does not guarantee placement and so does
// not satisfy the §6.4 dedicated-node requirement. Both matchExpressions
// and matchFields are included.
func flattenNodeAffinity(affinity *corev1.Affinity) []t4.NodeAffinityTerm {
	if affinity == nil || affinity.NodeAffinity == nil ||
		affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution == nil {
		return nil
	}
	var out []t4.NodeAffinityTerm
	for _, term := range affinity.NodeAffinity.
		RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms {
		for _, expr := range term.MatchExpressions {
			out = append(out, t4.NodeAffinityTerm{
				Key:      expr.Key,
				Operator: string(expr.Operator),
				Values:   expr.Values,
			})
		}
		for _, field := range term.MatchFields {
			out = append(out, t4.NodeAffinityTerm{
				Key:      field.Key,
				Operator: string(field.Operator),
				Values:   field.Values,
			})
		}
	}
	return out
}

// flattenTolerations projects a pod's tolerations into the flat list
// the t4_node_isolation decision package consumes.
func flattenTolerations(tolerations []corev1.Toleration) []t4.Toleration {
	if len(tolerations) == 0 {
		return nil
	}
	out := make([]t4.Toleration, 0, len(tolerations))
	for _, tol := range tolerations {
		out = append(out, t4.Toleration{
			Key:      tol.Key,
			Operator: string(tol.Operator),
			Value:    tol.Value,
		})
	}
	return out
}
