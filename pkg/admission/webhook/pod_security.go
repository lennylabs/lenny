// SPDX-License-Identifier: MIT

package webhook

import (
	"context"
	"encoding/json"
	"net/http"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/lennylabs/lenny/pkg/podsecurity"
)

// PodSecurity returns the Decider for the lenny-pod-security
// ValidatingAdmissionWebhook (§13.1, §18.33). It is installed
// fail-closed on Pod CREATE in agent namespaces and wraps
// podsecurity.ValidateAgentPod: the webhook rejects any agent pod whose
// spec breaches a §13.1 pod-security invariant — a host-sharing flag,
// a missing or wrong credential fsGroup, a root pod, a privileged or
// privilege-escalating container, a writable root filesystem, an
// undropped capability, or a seccomp profile other than RuntimeDefault.
//
// credReadersGID is the lenny-cred-readers GID the §13.1 cross-UID
// credential-delivery path requires on the pod-level fsGroup; the
// binary passes the chart's agent.credReadersGID value.
//
// A decode failure rejects, consistent with the webhook's fail-closed
// deployment: a pod the webhook cannot inspect must not be admitted
// with a pod-security posture it could not verify (§13.1).
func PodSecurity(credReadersGID int64) Decider {
	return func(_ context.Context, req *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse {
		var pod corev1.Pod
		if err := json.Unmarshal(req.Object.Raw, &pod); err != nil {
			return Deny(http.StatusBadRequest, "decode Pod object: "+err.Error())
		}

		if err := podsecurity.ValidateAgentPod(translatePodSpec(&pod), credReadersGID); err != nil {
			return Deny(http.StatusForbidden, err.Error())
		}
		return Allow()
	}
}

// translatePodSpec projects a Kubernetes Pod into the transport-agnostic
// podsecurity.PodSpec the §13.1 validator consults. Init containers and
// regular containers are both flattened into the validator's container
// list: §13.1 applies the per-container invariants to every container
// in the pod, init and main alike.
func translatePodSpec(pod *corev1.Pod) podsecurity.PodSpec {
	spec := podsecurity.PodSpec{
		ShareProcessNamespace: derefBool(pod.Spec.ShareProcessNamespace),
		HostPID:               pod.Spec.HostPID,
		HostNetwork:           pod.Spec.HostNetwork,
		HostIPC:               pod.Spec.HostIPC,
	}

	if sc := pod.Spec.SecurityContext; sc != nil {
		spec.RunAsNonRoot = sc.RunAsNonRoot
		spec.FSGroup = sc.FSGroup
		spec.SupplementalGroups = sc.SupplementalGroups
		spec.SeccompProfileType = seccompType(sc.SeccompProfile)
	}

	for _, c := range pod.Spec.InitContainers {
		spec.Containers = append(spec.Containers, translateContainer(c))
	}
	for _, c := range pod.Spec.Containers {
		spec.Containers = append(spec.Containers, translateContainer(c))
	}
	return spec
}

// translateContainer projects one Kubernetes container into the
// podsecurity.ContainerSpec the §13.1 validator consults.
func translateContainer(c corev1.Container) podsecurity.ContainerSpec {
	out := podsecurity.ContainerSpec{Name: c.Name}
	if sc := c.SecurityContext; sc != nil {
		out.AllowPrivilegeEscalation = sc.AllowPrivilegeEscalation
		out.Privileged = sc.Privileged
		out.ReadOnlyRootFilesystem = sc.ReadOnlyRootFilesystem
		out.RunAsNonRoot = sc.RunAsNonRoot
		out.SeccompProfileType = seccompType(sc.SeccompProfile)
		if caps := sc.Capabilities; caps != nil {
			out.CapabilitiesDrop = capabilityStrings(caps.Drop)
			out.CapabilitiesAdd = capabilityStrings(caps.Add)
		}
	}
	return out
}

// seccompType returns the seccomp profile type string for a
// SeccompProfile, or "" when no profile is set. The §13.1 validator
// treats an empty string as "no profile" and resolves inheritance from
// the pod level.
func seccompType(p *corev1.SeccompProfile) string {
	if p == nil {
		return ""
	}
	return string(p.Type)
}

// capabilityStrings converts a Kubernetes capability list into the
// plain string slice the podsecurity validator consults.
func capabilityStrings(caps []corev1.Capability) []string {
	if len(caps) == 0 {
		return nil
	}
	out := make([]string, 0, len(caps))
	for _, c := range caps {
		out = append(out, string(c))
	}
	return out
}

// derefBool returns the pointed-to bool, or false when the pointer is
// nil. An unset *bool shareProcessNamespace is treated as false,
// matching the Kubernetes default.
func derefBool(b *bool) bool {
	return b != nil && *b
}
