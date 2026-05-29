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
// credential-delivery path requires on the pod-level fsGroup. The
// lenny-webhook binary passes podspec.CredReadersGID (the Go constant
// in pkg/controller/sandbox/podspec), so the validator and the
// controller stay aligned on a single source of truth. Spec §13.1
// leaves the specific GID to the implementation (the spec mandates
// only "distinct non-root identities"); the constant value is
// documented in pkg/controller/sandbox/podspec/podspec.go.
//
// rcPolicy carries the §17.2 RuntimeClass-aware relaxations (the
// gVisor and Kata RuntimeClass names); the binary passes the names from
// the chart's runtimeClasses.profiles.{sandboxed,microvm}.name values.
//
// A decode failure rejects, consistent with the webhook's fail-closed
// deployment: a pod the webhook cannot inspect must not be admitted
// with a pod-security posture it could not verify (§13.1).
// credVolumeName is the name of the pod-level credential tmpfs volume
// carrying /run/lenny/credentials.json. The §13.1 membership boundary
// (POD_SPEC_CRED_GROUP_OVERBROAD) rejects a non-adapter, non-agent
// container that mounts it; the binary passes podspec.CredVolumeName.
func PodSecurity(credReadersGID int64, credVolumeName string, rcPolicy podsecurity.RuntimeClassPolicy) Decider {
	return func(_ context.Context, req *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse {
		var pod corev1.Pod
		if err := json.Unmarshal(req.Object.Raw, &pod); err != nil {
			return Deny(http.StatusBadRequest, "decode Pod object: "+err.Error())
		}

		if err := podsecurity.ValidateAgentPod(translatePodSpec(&pod, credVolumeName), credReadersGID, rcPolicy); err != nil {
			return Deny(http.StatusForbidden, err.Error())
		}
		return Allow()
	}
}

// Agent-pod credential-container names. The Sandbox podspec builder
// names the §4.7 adapter container "adapter" and the agent runtime
// container "runtime"; §13.1 confines the lenny-cred-readers GID to
// exactly those two. The sidecar deployment model emits both; the
// embedded model emits only "runtime".
const (
	adapterContainerName = "adapter"
	agentContainerName   = "runtime"
)

// translatePodSpec projects a Kubernetes Pod into the transport-agnostic
// podsecurity.PodSpec the §13.1 validator consults. Init, regular, and
// ephemeral containers are all flattened into the validator's container
// list: §13.1 applies the per-container invariants to "every container
// in the pod" (line 16), and line 27 calls out ephemeral debug
// containers attached post-hoc via the pods/ephemeralcontainers
// subresource as a specific risk surface — an actor that does not trip
// the dedicated lenny-ephemeral-container-cred-guard can still attach an
// ephemeral container that breaches the general baseline (privileged,
// added capabilities, writable root, disabled seccomp). Ephemeral
// containers are never treated as credential containers, so an ephemeral
// container that mounts the credential volume trips the §13.1 membership
// boundary here in addition to the cred-guard.
func translatePodSpec(pod *corev1.Pod, credVolumeName string) podsecurity.PodSpec {
	spec := podsecurity.PodSpec{
		ShareProcessNamespace: derefBool(pod.Spec.ShareProcessNamespace),
		HostPID:               pod.Spec.HostPID,
		HostNetwork:           pod.Spec.HostNetwork,
		HostIPC:               pod.Spec.HostIPC,
		RuntimeClassName:      derefString(pod.Spec.RuntimeClassName),
		CredVolumeName:        credVolumeName,
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
		if c.Name == adapterContainerName || c.Name == agentContainerName {
			spec.CredentialContainerNames = append(spec.CredentialContainerNames, c.Name)
		}
	}
	// spec: §13.1 line 27 — ephemeral containers attached via the
	// pods/ephemeralcontainers subresource carry the same per-container
	// baseline. EphemeralContainerCommon is a field-for-field copy of
	// Container, so the conversion reuses the same translation.
	for _, ec := range pod.Spec.EphemeralContainers {
		spec.Containers = append(spec.Containers, translateContainer(corev1.Container(ec.EphemeralContainerCommon)))
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
		out.RunAsGroup = sc.RunAsGroup
		out.SeccompProfileType = seccompType(sc.SeccompProfile)
		if caps := sc.Capabilities; caps != nil {
			out.CapabilitiesDrop = capabilityStrings(caps.Drop)
			out.CapabilitiesAdd = capabilityStrings(caps.Add)
		}
	}
	// §13.1 credential-boundary surface: the validator rejects a
	// non-adapter, non-agent container that mounts the credential volume
	// or the /run/lenny path prefix.
	for _, m := range c.VolumeMounts {
		out.VolumeMounts = append(out.VolumeMounts, podsecurity.VolumeMount{Name: m.Name, MountPath: m.MountPath})
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

// derefString returns the pointed-to string, or "" when the pointer is
// nil. An unset *string runtimeClassName is treated as the empty
// string, which selects full §13.1 enforcement (no §17.2 relaxation).
func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
