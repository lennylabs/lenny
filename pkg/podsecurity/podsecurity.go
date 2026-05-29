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

// CredMountPathPrefix is the in-pod directory carrying the §4.7
// credential file (`/run/lenny/credentials.json`) and the adapter
// manifest. A volume mount at this path or below reaches the
// credential file. §13.1 confines that read boundary to the adapter
// and agent containers; a non-credential container that mounts this
// prefix is rejected with POD_SPEC_CRED_GROUP_OVERBROAD. It mirrors the
// lenny-ephemeral-container-cred-guard condition (iv) for the regular
// (non-ephemeral) containers the lenny-pod-security webhook validates.
const CredMountPathPrefix = "/run/lenny"

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

	// RuntimeClassName is the pod's spec.runtimeClassName. §17.2
	// applies a RuntimeClass-aware split-enforcement model: a gVisor
	// pod skips the seccomp profile check (RuntimeDefault is a no-op
	// under gVisor's userspace syscall interception) and a Kata pod may
	// set allowPrivilegeEscalation: true for its device plugins. Every
	// other §13.1 control still applies. An empty or unrecognized
	// RuntimeClass receives full enforcement (fail-closed). The
	// validator maps the name to a relaxation through the
	// RuntimeClassPolicy passed to ValidateAgentPod.
	RuntimeClassName string

	// CredentialContainerNames lists the containers that legitimately
	// carry the lenny-cred-readers GID: the adapter container, which
	// writes the credential file, and the agent container, which reads
	// it. §13.1 keeps that group membership deliberately narrow and
	// rejects any other container that declares the GID. The webhook
	// populates this from the agent-pod container convention; when the
	// list is empty every container is treated as non-credential.
	CredentialContainerNames []string

	// CredVolumeName is the name of the pod-level credential tmpfs
	// volume carrying /run/lenny/credentials.json. §13.1 line 27: a
	// non-adapter, non-agent container that mounts this volume (by name)
	// or mounts the /run/lenny path prefix reaches the credential file
	// and is rejected with POD_SPEC_CRED_GROUP_OVERBROAD. Empty disables
	// the by-name match, leaving the by-path match in force.
	CredVolumeName string

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

	// RunAsGroup is the container-level securityContext.runAsGroup.
	// §13.1 forbids a non-adapter, non-agent container from declaring
	// the lenny-cred-readers GID here: that GID is the credential-file
	// read boundary and its container membership is deliberately narrow.
	// supplementalGroups has no container-level field in the Kubernetes
	// API, so runAsGroup is the per-container vector for this control.
	RunAsGroup *int64

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

	// VolumeMounts is the container's volume mounts. §13.1 line 27: a
	// non-adapter, non-agent container that mounts the credential volume
	// or the /run/lenny path prefix reaches the credential file. This is
	// the operative attack surface the validator inspects, because the
	// pod-level fsGroup grants every container lenny-cred-readers
	// supplementary-group membership regardless of its explicit
	// runAsGroup — so group membership alone is insufficient to access
	// the file, but membership plus a credential-volume mount is not.
	VolumeMounts []VolumeMount
}

// VolumeMount is the credential-boundary-relevant projection of one of
// a container's volume mounts: the volume it references and the path it
// is mounted at.
type VolumeMount struct {
	Name      string
	MountPath string
}

// RuntimeClassPolicy maps the cluster's RuntimeClass names onto the
// §17.2 split-enforcement relaxations. RuntimeClass names are
// deployer-configurable (the chart's runtimeClasses.profiles.*.name
// values; default gvisor/kata/runc from isolation.RuntimeClassName), so
// the webhook supplies the names it is configured with and the
// validator owns the §17.2 policy (which relaxation each profile gets).
//
// A pod whose RuntimeClassName matches neither name receives full
// §13.1 enforcement.
type RuntimeClassPolicy struct {
	// GVisorRuntimeClass is the RuntimeClass name mapped to the
	// sandboxed (gVisor) profile. §17.2: a pod with this
	// runtimeClassName skips the seccomp profile check. Empty disables
	// the gVisor relaxation (every pod is seccomp-checked).
	GVisorRuntimeClass string
	// KataRuntimeClass is the RuntimeClass name mapped to the microvm
	// (Kata) profile. §17.2: a pod with this runtimeClassName may set
	// allowPrivilegeEscalation: true for its device plugins. Empty
	// disables the Kata relaxation (every pod must set
	// allowPrivilegeEscalation: false).
	KataRuntimeClass string
}

// relaxations resolves the §17.2 per-RuntimeClass exemptions for spec.
// A gVisor pod is seccomp-exempt; a Kata pod is
// privilege-escalation-exempt. An empty policy name never matches, so
// the corresponding relaxation stays off.
func (p RuntimeClassPolicy) relaxations(runtimeClass string) (seccompExempt, privEscExempt bool) {
	if runtimeClass == "" {
		return false, false
	}
	if p.GVisorRuntimeClass != "" && runtimeClass == p.GVisorRuntimeClass {
		seccompExempt = true
	}
	if p.KataRuntimeClass != "" && runtimeClass == p.KataRuntimeClass {
		privEscExempt = true
	}
	return seccompExempt, privEscExempt
}

// ValidateAgentPod applies the §13.1 invariants to spec. Returns nil
// when the spec satisfies every rule and *PodSecurityError listing
// every violation otherwise.
//
// LennyCredReadersGID is the supplementary GID the §13.1 cross-UID
// file-delivery path requires on the pod-level fsGroup. Production
// callers pass podspec.CredReadersGID (the Go constant in
// pkg/controller/sandbox/podspec); spec §13.1 leaves the specific GID
// to the implementation and the constant is the single source of truth
// shared by the controller's pod builder and this validator.
//
// rcPolicy carries the §17.2 RuntimeClass-aware relaxations: a gVisor
// pod skips the seccomp check, a Kata pod may allow privilege
// escalation. A zero-value policy applies full enforcement to every
// pod regardless of RuntimeClass.
func ValidateAgentPod(spec PodSpec, lennyCredReadersGID int64, rcPolicy RuntimeClassPolicy) error {
	violations := []string{}
	seccompExempt, privEscExempt := rcPolicy.relaxations(spec.RuntimeClassName)

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

	// §13.1 line 25: the adapter and agent UIDs are declared in the
	// pod's spec.securityContext.supplementalGroups list, making
	// lenny-cred-readers a shared group both containers are members of.
	// Line 25 further states the admission webhook "validate[s] the
	// presence ... of the fsGroup and supplementalGroups settings on
	// every agent-pod template" — so the explicit declaration is
	// required, not merely the kubelet's implicit fsGroup-to-
	// supplementary-group propagation. A pod that sets the fsGroup but
	// omits the supplementalGroups declaration would still deliver the
	// file today (via that propagation side-effect), but the spec
	// mandates the explicit membership so a future kubelet that decouples
	// fsGroup from supplementary groups cannot silently break delivery.
	if !containsGID(spec.SupplementalGroups, lennyCredReadersGID) {
		violations = append(violations, fmt.Sprintf("pod securityContext.supplementalGroups must include the lenny-cred-readers GID %d for the cross-UID credential-delivery path (POD_SPEC_CRED_FSGROUP_MISSING)", lennyCredReadersGID))
	}

	// §13.1 pod-level runAsNonRoot: every agent pod is non-root.
	if spec.RunAsNonRoot == nil || !*spec.RunAsNonRoot {
		violations = append(violations, "runAsNonRoot must be true (§13.1 User row)")
	}

	// §13.1 lenny-cred-readers membership boundary: only the adapter
	// and agent containers may carry that GID. Build the allow-set the
	// per-container cred-group check consults.
	credentialContainer := make(map[string]bool, len(spec.CredentialContainerNames))
	for _, name := range spec.CredentialContainerNames {
		credentialContainer[name] = true
	}

	// §13.1 per-container invariants.
	if len(spec.Containers) == 0 {
		violations = append(violations, "pod must contain at least one container")
	}
	for _, c := range spec.Containers {
		if c.Privileged != nil && *c.Privileged {
			violations = append(violations, fmt.Sprintf("container %q: privileged is forbidden", c.Name))
		}
		// §17.2: Kata pods permit the privilege-escalation paths their
		// device plugins need; every other RuntimeClass must set
		// allowPrivilegeEscalation: false.
		if !privEscExempt && (c.AllowPrivilegeEscalation == nil || *c.AllowPrivilegeEscalation) {
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
		// §17.2 exempts gVisor pods: RuntimeDefault is a no-op under
		// gVisor's userspace syscall interception, so the check is
		// skipped for them while every other §13.1 control still applies.
		if !seccompExempt {
			if effective := effectiveSeccompProfile(c.SeccompProfileType, spec.SeccompProfileType); effective != SeccompRuntimeDefault {
				violations = append(violations, fmt.Sprintf(
					"container %q: seccompProfile.type must be %s, got %s (§13.1 pod-security baseline)",
					c.Name, SeccompRuntimeDefault, describeSeccomp(effective),
				))
			}
		}
		// §13.1 lenny-cred-readers membership boundary: a container that
		// is not the adapter or agent must not declare the cred-readers
		// GID in runAsGroup.
		if c.RunAsGroup != nil && *c.RunAsGroup == lennyCredReadersGID && !credentialContainer[c.Name] {
			violations = append(violations, fmt.Sprintf(
				"container %q declares the lenny-cred-readers GID %d in runAsGroup but is not the adapter or agent container (POD_SPEC_CRED_GROUP_OVERBROAD)",
				c.Name, lennyCredReadersGID,
			))
		}
		// §13.1 line 27: the operative credential-read surface is the
		// volume mount, not the group membership. The pod-level fsGroup
		// grants every container lenny-cred-readers membership regardless
		// of its explicit runAsGroup, so the runAsGroup check above cannot
		// close the fsGroup-inheritance side-channel on its own. A
		// non-adapter, non-agent container that mounts the credential
		// volume by name, or mounts a path at or under /run/lenny, reaches
		// /run/lenny/credentials.json and is rejected. This mirrors the
		// lenny-ephemeral-container-cred-guard condition (iv) for the
		// regular containers this webhook validates.
		if !credentialContainer[c.Name] {
			for _, m := range c.VolumeMounts {
				if spec.CredVolumeName != "" && m.Name == spec.CredVolumeName {
					violations = append(violations, fmt.Sprintf(
						"container %q mounts the credential volume %q but is not the adapter or agent container (POD_SPEC_CRED_GROUP_OVERBROAD)",
						c.Name, m.Name,
					))
					continue
				}
				if m.MountPath == CredMountPathPrefix || hasPathPrefix(m.MountPath, CredMountPathPrefix) {
					violations = append(violations, fmt.Sprintf(
						"container %q mounts %q under the credential path %s but is not the adapter or agent container (POD_SPEC_CRED_GROUP_OVERBROAD)",
						c.Name, m.MountPath, CredMountPathPrefix,
					))
				}
			}
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

// containsGID reports whether gids includes want. §13.1 uses it to
// check the lenny-cred-readers GID is declared in the pod-level
// supplementalGroups list.
func containsGID(gids []int64, want int64) bool {
	for _, g := range gids {
		if g == want {
			return true
		}
	}
	return false
}

// hasPathPrefix reports whether path is nested under prefix — that is,
// path equals prefix followed by a path separator and at least one more
// segment. The exact-equality case (path == prefix) is handled by the
// caller so that a sibling path such as /run/lenny-capture, which shares
// the textual prefix /run/lenny but is not nested under /run/lenny/, is
// not falsely matched.
func hasPathPrefix(path, prefix string) bool {
	p := prefix + "/"
	return len(path) >= len(p) && path[:len(p)] == p
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
