// SPDX-License-Identifier: MIT

package webhook

import (
	"context"
	"encoding/json"
	"net/http"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"

	guard "github.com/lennylabs/lenny/pkg/admission/ephemeral_container_cred_guard"
)

// EphemeralContainerCredGuard returns the Decider for the
// lenny-ephemeral-container-cred-guard ValidatingAdmissionWebhook
// (§13.1). It is installed on the pods/ephemeralcontainers subresource
// in agent namespaces and rejects, fail-closed, any ephemeral container
// that could read the pod's credential file.
//
// Only the ephemeral containers the request adds are evaluated:
// containers already present on the prior pod were admitted earlier and
// are not re-judged. adapterUID and agentUID are the pod's two
// privileged UIDs, credGID is the lenny-cred-readers GID, and
// credVolumeName is the pod volume carrying the §4.7 credential tmpfs.
func EphemeralContainerCredGuard(adapterUID, agentUID, credGID int64, credVolumeName string) Decider {
	return func(_ context.Context, req *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse {
		var newPod corev1.Pod
		if err := json.Unmarshal(req.Object.Raw, &newPod); err != nil {
			return Deny(http.StatusBadRequest, "decode Pod object: "+err.Error())
		}

		prior := map[string]struct{}{}
		if len(req.OldObject.Raw) > 0 {
			var oldPod corev1.Pod
			if err := json.Unmarshal(req.OldObject.Raw, &oldPod); err != nil {
				return Deny(http.StatusBadRequest, "decode prior Pod object: "+err.Error())
			}
			for _, ec := range oldPod.Spec.EphemeralContainers {
				prior[ec.Name] = struct{}{}
			}
		}

		var added []guard.Container
		for _, ec := range newPod.Spec.EphemeralContainers {
			if _, existed := prior[ec.Name]; existed {
				continue
			}
			added = append(added, ephemeralToGuardContainer(ec))
		}

		decision := guard.Decide(guard.Request{
			NewContainers:  added,
			AdapterUID:     adapterUID,
			AgentUID:       agentUID,
			CredGID:        credGID,
			CredVolumeName: credVolumeName,
		})
		if decision.Allowed {
			return Allow()
		}
		return Deny(int32(decision.Code), decision.Reason)
	}
}

// ephemeralToGuardContainer projects a Kubernetes ephemeral container
// onto the guard's decision input.
//
// Kubernetes' container-level SecurityContext — the type an
// EphemeralContainer carries — has no supplementalGroups field;
// supplementalGroups is pod-level only. An ephemeral container
// therefore never declares its own supplementalGroups, so it is
// reported as present-and-empty: §13.1 condition (iii) judges the two
// fields the container can set (runAsUser, runAsGroup), and condition
// (iv) remains the operative guard against the universal
// fsGroup-inheritance path that §13.1 describes.
func ephemeralToGuardContainer(ec corev1.EphemeralContainer) guard.Container {
	c := guard.Container{Name: ec.Name, SupplementalGroups: []int64{}}
	if sc := ec.SecurityContext; sc != nil {
		c.RunAsUser = sc.RunAsUser
		c.RunAsGroup = sc.RunAsGroup
	}
	for _, m := range ec.VolumeMounts {
		c.VolumeMounts = append(c.VolumeMounts, guard.VolumeMount{
			Name:      m.Name,
			MountPath: m.MountPath,
		})
	}
	return c
}
