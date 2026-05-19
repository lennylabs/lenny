// SPDX-License-Identifier: MIT

// Package podsecurity implements the §13.1 pod-spec validator: a
// pure-function check that a pod template satisfies every §13.1 Pod
// Security row plus the §13.1 host-sharing prohibition.
//
// The validator is keyed off a transport-agnostic PodSpec struct so
// callers do not have to pull in the Kubernetes API types. Production
// code paths translate `k8s.io/api/core/v1.PodSpec` and its
// SecurityContext fields into this struct; tests build the struct
// directly.
//
// The Kubernetes admission webhook (`POD_SPEC_HOST_SHARING_FORBIDDEN`,
// `POD_SPEC_CRED_FSGROUP_MISSING`, `POD_SPEC_CRED_GROUP_OVERBROAD`)
// and the `lenny-preflight` startup hard-fail wrap this validator.
package podsecurity

import "fmt"

// SeccompRuntimeDefault is the only seccomp profile type the §13.1
// pod-security baseline accepts on an agent pod. It matches the
// Kubernetes SeccompProfileType value RuntimeDefault, which engages the
// container runtime's default syscall filter. §17.2 records that the
// seccompType: RuntimeDefault requirement is enforced at admission for
// runc pods; the agent podspec sets it at the pod level.
const SeccompRuntimeDefault = "RuntimeDefault"

// PodSpec is the subset of a Kubernetes pod spec the §13.1 validator
// consults. Fields not in §13.1 are intentionally omitted.
type PodSpec struct {
	// HostSharing flags. §13.1 explicitly forbids all four.
	ShareProcessNamespace bool
	HostPID               bool
	HostNetwork           bool
	HostIPC               bool

	// Pod-level SecurityContext fields enforced by §13.1.
	RunAsNonRoot *bool
	FSGroup      *int64

	// SeccompProfileType is the pod-level
	// securityContext.seccompProfile.type. A container that sets no
	// seccomp profile of its own inherits this value. An empty string
	// means no pod-level profile was set; §13.1 requires the effective
	// per-container profile to be RuntimeDefault, so an empty pod-level
	// profile combined with an empty container profile is a violation.
	SeccompProfileType string

	// SupplementalGroups present on the pod-level SecurityContext.
	// §13.1 requires the lenny-cred-readers GID to be present so the
	// cross-UID file-delivery path works.
	SupplementalGroups []int64

	// Containers describes every container in the pod (init + main
	// + sidecars). Each must satisfy the §13.1 container-level
	// SecurityContext invariants.
	Containers []ContainerSpec
}

// ContainerSpec is the subset of a Kubernetes container spec the
// §13.1 validator consults.
type ContainerSpec struct {
	Name string

	// SecurityContext fields enforced by §13.1.
	AllowPrivilegeEscalation *bool
	Privileged               *bool
	ReadOnlyRootFilesystem   *bool
	RunAsNonRoot             *bool

	// SeccompProfileType is the container-level
	// securityContext.seccompProfile.type. An empty string means the
	// container set no profile of its own and inherits the pod-level
	// PodSpec.SeccompProfileType. The validator checks the effective
	// profile — container value when set, pod-level value otherwise.
	SeccompProfileType string

	// CapabilitiesDrop is the capabilities.drop list. §13.1 mandates
	// "All dropped"; the validator requires this to contain "ALL"
	// exactly.
	CapabilitiesDrop []string
	CapabilitiesAdd  []string
}

// ValidateAgentPod applies the §13.1 invariants to spec. Returns nil
// when the spec satisfies every rule and *PodSecurityError listing
// every violation otherwise.
//
// LennyCredReadersGID is the supplementary GID the §13.1 cross-UID
// file-delivery path requires on the pod-level fsGroup. Pass the GID
// from the chart's `agent.credReadersGID` Helm value.
func ValidateAgentPod(spec PodSpec, lennyCredReadersGID int64) error {
	violations := []string{}

	// §13.1 host-sharing prohibitions.
	if spec.ShareProcessNamespace {
		violations = append(violations, "shareProcessNamespace: true is forbidden (POD_SPEC_HOST_SHARING_FORBIDDEN)")
	}
	if spec.HostPID {
		violations = append(violations, "hostPID: true is forbidden (POD_SPEC_HOST_SHARING_FORBIDDEN)")
	}
	if spec.HostNetwork {
		violations = append(violations, "hostNetwork: true is forbidden (POD_SPEC_HOST_SHARING_FORBIDDEN)")
	}
	if spec.HostIPC {
		violations = append(violations, "hostIPC: true is forbidden (POD_SPEC_HOST_SHARING_FORBIDDEN)")
	}

	// §13.1 pod-level fsGroup must equal the lenny-cred-readers GID
	// so the credential file is group-readable by the agent.
	if spec.FSGroup == nil {
		violations = append(violations, "fsGroup is unset (POD_SPEC_CRED_FSGROUP_MISSING)")
	} else if *spec.FSGroup != lennyCredReadersGID {
		violations = append(violations, fmt.Sprintf("fsGroup must equal lenny-cred-readers GID %d, got %d (POD_SPEC_CRED_FSGROUP_MISSING)", lennyCredReadersGID, *spec.FSGroup))
	}

	// §13.1 pod-level runAsNonRoot: every agent pod is non-root.
	if spec.RunAsNonRoot == nil || !*spec.RunAsNonRoot {
		violations = append(violations, "runAsNonRoot must be true (§13.1 User row)")
	}

	// §13.1 per-container invariants.
	if len(spec.Containers) == 0 {
		violations = append(violations, "pod must contain at least one container")
	}
	for _, c := range spec.Containers {
		if c.Privileged != nil && *c.Privileged {
			violations = append(violations, fmt.Sprintf("container %q: privileged is forbidden", c.Name))
		}
		if c.AllowPrivilegeEscalation == nil || *c.AllowPrivilegeEscalation {
			violations = append(violations, fmt.Sprintf("container %q: allowPrivilegeEscalation must be false", c.Name))
		}
		if c.ReadOnlyRootFilesystem == nil || !*c.ReadOnlyRootFilesystem {
			violations = append(violations, fmt.Sprintf("container %q: readOnlyRootFilesystem must be true (§13.1 Root filesystem row)", c.Name))
		}
		if !dropsAllCapabilities(c.CapabilitiesDrop) {
			violations = append(violations, fmt.Sprintf("container %q: capabilities.drop must contain ALL (§13.1 Capabilities row)", c.Name))
		}
		if len(c.CapabilitiesAdd) > 0 {
			violations = append(violations, fmt.Sprintf("container %q: capabilities.add must be empty (§13.1 Capabilities row)", c.Name))
		}
		// §13.1 / §17.2 seccomp baseline: the effective seccomp profile
		// must be RuntimeDefault. The effective profile is the
		// container-level value when set, otherwise the pod-level value.
		if effective := effectiveSeccompProfile(c.SeccompProfileType, spec.SeccompProfileType); effective != SeccompRuntimeDefault {
			violations = append(violations, fmt.Sprintf(
				"container %q: seccompProfile.type must be %s, got %s (§13.1 pod-security baseline)",
				c.Name, SeccompRuntimeDefault, describeSeccomp(effective),
			))
		}
	}

	if len(violations) == 0 {
		return nil
	}
	return &PodSecurityError{Violations: violations}
}

// effectiveSeccompProfile resolves the seccomp profile that applies to
// a container: the container-level type when it set one, otherwise the
// pod-level type the container inherits. This mirrors the kubelet's
// resolution, where a container securityContext.seccompProfile
// overrides the pod-level securityContext.seccompProfile.
func effectiveSeccompProfile(containerType, podType string) string {
	if containerType != "" {
		return containerType
	}
	return podType
}

// describeSeccomp renders a seccomp profile type for a violation
// message, naming the absent case so the operator sees that neither the
// pod nor the container set a profile.
func describeSeccomp(profileType string) string {
	if profileType == "" {
		return "no profile set"
	}
	return profileType
}

// dropsAllCapabilities reports whether the drop list contains "ALL".
// The check is case-sensitive matching the Kubernetes capability
// value the API server emits.
func dropsAllCapabilities(drops []string) bool {
	for _, d := range drops {
		if d == "ALL" {
			return true
		}
	}
	return false
}

// PodSecurityError carries the full set of §13.1 violations found on
// a pod spec. The admission webhook surfaces this as the chained
// rejection codes documented in §13.1.
type PodSecurityError struct {
	Violations []string
}

func (e *PodSecurityError) Error() string {
	return fmt.Sprintf("podsecurity: §13.1 violations: %v", e.Violations)
}

// HasViolation reports whether the error references the supplied
// rejection-code substring. Tests use this to assert that a specific
// §13.1 rule fired.
func (e *PodSecurityError) HasViolation(needle string) bool {
	for _, v := range e.Violations {
		if containsString(v, needle) {
			return true
		}
	}
	return false
}

func containsString(haystack, needle string) bool {
	n := len(needle)
	if n == 0 {
		return true
	}
	for i := 0; i+n <= len(haystack); i++ {
		if haystack[i:i+n] == needle {
			return true
		}
	}
	return false
}

// Helpers for tests / production wiring that need pointer literals.

// Ptr returns a pointer to v. The §13.1 SecurityContext fields are
// pointer-typed in k8s.io/api/core/v1; this helper keeps callers
// concise.
func Ptr[T any](v T) *T { return &v }
