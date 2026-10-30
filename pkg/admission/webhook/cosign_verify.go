// SPDX-License-Identifier: MIT

package webhook

import (
	"context"
	"encoding/json"
	"net/http"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"

	cv "github.com/lennylabs/lenny/pkg/admission/cosign_verify"
)

// CosignVerify returns the Decider for the lenny-cosign-verify
// ValidatingAdmissionWebhook (§5.2, §13.1, §18.5). It is installed
// fail-closed on Pod CREATE in agent namespaces and rejects any pod
// whose in-scope container images do not carry a valid cosign/Sigstore
// signature.
//
// verifier performs the signature check; the binary constructs it from
// the deployment's verification configuration (a trusted public key or
// a keyless identity policy) at startup. cfg names the registry
// prefixes whose images must be verified. An image outside every
// configured prefix is admitted unchecked — the prefix list is the
// boundary of the deployer's signing program.
//
// A decode failure rejects, consistent with the webhook's fail-closed
// deployment: a pod the webhook cannot inspect must not be admitted
// with images whose signatures it could not verify (§5.2).
func CosignVerify(verifier cv.Verifier, cfg cv.Config) Decider {
	return func(ctx context.Context, req *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse {
		var pod corev1.Pod
		if err := json.Unmarshal(req.Object.Raw, &pod); err != nil {
			return Deny(http.StatusBadRequest, "decode Pod object: "+err.Error())
		}

		decision := cv.Decide(ctx, verifier, cv.Request{
			PodName:   podDisplayName(&pod, req),
			Namespace: podNamespace(&pod, req),
			Images:    podImages(&pod),
			Config:    cfg,
		})
		if decision.Allowed {
			return Allow()
		}
		return Deny(int32(decision.Code), decision.Reason)
	}
}

// podImages flattens every container image on a pod spec — init
// containers, regular containers, and ephemeral containers — into the
// flat slice the cosign_verify decision package consumes. Every image
// that runs in the pod is in scope for signature verification,
// including init and ephemeral containers, because each runs code on
// the node.
func podImages(pod *corev1.Pod) []string {
	images := make([]string, 0,
		len(pod.Spec.InitContainers)+len(pod.Spec.Containers)+len(pod.Spec.EphemeralContainers))
	for _, c := range pod.Spec.InitContainers {
		images = append(images, c.Image)
	}
	for _, c := range pod.Spec.Containers {
		images = append(images, c.Image)
	}
	for _, c := range pod.Spec.EphemeralContainers {
		images = append(images, c.Image)
	}
	return images
}
