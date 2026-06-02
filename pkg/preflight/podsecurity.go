// SPDX-License-Identifier: MIT

package preflight

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
)

// WorkloadPodSecurity is the §13.1 pod-security-baseline projection of
// one Lenny-managed workload's pod template: the per-container effective
// settings the baseline check inspects. The §13.1 controls are
// authored into every chart-rendered Deployment, DaemonSet, and Job, but
// the guarantee §13.1 makes is bound to enforcement — a value override, a
// patched Job, or an operator-injected sidecar could weaken the baseline
// with no signal. This projection lets the preflight Job re-assert the
// baseline against the live cluster state.
type WorkloadPodSecurity struct {
	// Workload identifies the workload, e.g.
	// "lenny-system/Deployment/lenny-gateway".
	Workload string
	// Containers carries the §13.1 baseline projection of each
	// non-init container in the pod template.
	Containers []ContainerSecurity
}

// ContainerSecurity is the §13.1 baseline projection of one container.
// RunAsNonRoot is the effective value (the container securityContext
// override when set, else the pod-level securityContext).
type ContainerSecurity struct {
	// Name is the container name.
	Name string
	// RunAsNonRoot is the effective §13.1 "User: Non-root" setting.
	RunAsNonRoot bool
	// ReadOnlyRootFilesystem is the §13.1 "Root filesystem: Read-only"
	// setting (container-level only).
	ReadOnlyRootFilesystem bool
	// DropsAllCapabilities is true when the container drops the ALL
	// capability set (§13.1 "Capabilities: All dropped").
	DropsAllCapabilities bool
}

// CheckPodSecurityBaseline verifies the §13.1 pod-security baseline on
// every Lenny-managed workload's pod template: each container MUST run
// as non-root, drop all capabilities, and mount a read-only root
// filesystem (§13.1 lines 6-8). The chart authors these settings into
// every rendered Deployment, DaemonSet, and Job, so a passing check is
// the steady state; a failure means a value override, a patched Job, or
// an injected sidecar weakened the baseline. The first non-conformant
// container fails the check fail-closed with
// POD_SPEC_SECURITY_BASELINE_VIOLATION.
//
// spec: §13.1 lines 6-8 (User: Non-root; Capabilities: All dropped;
// Root filesystem: Read-only); §13.1 line 16 (the controls apply to
// every pod template Lenny generates). F-13.1.12.
func CheckPodSecurityBaseline(workloads []WorkloadPodSecurity) Decision {
	for _, w := range workloads {
		for _, c := range w.Containers {
			var control string
			switch {
			case !c.RunAsNonRoot:
				control = "run as non-root (securityContext.runAsNonRoot: true; User: Non-root)"
			case !c.DropsAllCapabilities:
				control = "drop all capabilities (securityContext.capabilities.drop: [ALL]; Capabilities: All dropped)"
			case !c.ReadOnlyRootFilesystem:
				control = "mount a read-only root filesystem (securityContext.readOnlyRootFilesystem: true; Root filesystem: Read-only)"
			default:
				continue
			}
			return Decision{Passed: false, Reason: fmt.Sprintf(
				"POD_SPEC_SECURITY_BASELINE_VIOLATION: workload %s container %q does not %s; "+
					"the §13.1 pod-security baseline is mandatory on every Lenny-managed pod template",
				w.Workload, c.Name, control,
			)}
		}
	}
	return Decision{Passed: true}
}

// projectPodSecurity reduces a pod template to its §13.1 baseline
// projection. Init and ephemeral containers are out of scope: the
// chart's system pod templates declare no init containers, and the
// agent-pod baseline is covered by the controller podspec and the
// agent-pod host-sharing / credential audits. The effective
// runAsNonRoot for each container is the container securityContext
// override when set, otherwise the pod-level securityContext.
func projectPodSecurity(workload string, spec *corev1.PodSpec) WorkloadPodSecurity {
	podNonRoot := spec.SecurityContext != nil &&
		spec.SecurityContext.RunAsNonRoot != nil &&
		*spec.SecurityContext.RunAsNonRoot
	out := WorkloadPodSecurity{Workload: workload}
	for i := range spec.Containers {
		out.Containers = append(out.Containers, projectContainerSecurity(&spec.Containers[i], podNonRoot))
	}
	return out
}

// projectContainerSecurity projects one container onto the §13.1
// baseline fields, resolving runAsNonRoot against the pod-level default.
func projectContainerSecurity(c *corev1.Container, podNonRoot bool) ContainerSecurity {
	sc := c.SecurityContext
	nonRoot := podNonRoot
	if sc != nil && sc.RunAsNonRoot != nil {
		nonRoot = *sc.RunAsNonRoot
	}
	readOnly := sc != nil && sc.ReadOnlyRootFilesystem != nil && *sc.ReadOnlyRootFilesystem
	return ContainerSecurity{
		Name:                   c.Name,
		RunAsNonRoot:           nonRoot,
		ReadOnlyRootFilesystem: readOnly,
		DropsAllCapabilities:   dropsAll(sc),
	}
}

// dropsAll reports whether the container securityContext drops the ALL
// capability set. Kubernetes accepts the capability name case-sensitively
// as "ALL"; the chart renders it uppercase.
func dropsAll(sc *corev1.SecurityContext) bool {
	if sc == nil || sc.Capabilities == nil {
		return false
	}
	for _, c := range sc.Capabilities.Drop {
		if c == "ALL" {
			return true
		}
	}
	return false
}
