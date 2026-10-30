// SPDX-License-Identifier: MIT

package webhook

import (
	"context"
	"encoding/json"
	"net/http"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"

	rd "github.com/lennylabs/lenny/pkg/admission/registry_digest"
)

// RegistryDigest returns the Decider for the lenny-registry-digest
// ValidatingAdmissionWebhook (§13.1, §18.33). It is installed
// fail-closed on Pod CREATE in agent namespaces and rejects any pod
// whose container image references are not digest-pinned when
// platform.registry.requireDigest is true.
//
// requireDigest mirrors the chart's `platform.registry.requireDigest`
// value: when false the Decider admits every pod regardless of image
// references (the development-friendly default); when true a single
// tag-only reference rejects the pod with POD_IMAGE_DIGEST_REQUIRED.
//
// A decode failure rejects, consistent with the webhook's fail-closed
// deployment: a pod the webhook cannot inspect must not be admitted
// with image references it could not verify (§13.1).
func RegistryDigest(requireDigest bool) Decider {
	return func(_ context.Context, req *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse {
		var pod corev1.Pod
		if err := json.Unmarshal(req.Object.Raw, &pod); err != nil {
			return Deny(http.StatusBadRequest, "decode Pod object: "+err.Error())
		}

		decision := rd.Decide(rd.Request{
			PodName:   podDisplayName(&pod, req),
			Namespace: podNamespace(&pod, req),
			Images:    podImages(&pod),
			Config:    rd.Config{RequireDigest: requireDigest},
		})
		if decision.Allowed {
			return Allow()
		}
		return Deny(int32(decision.Code), decision.Reason)
	}
}
