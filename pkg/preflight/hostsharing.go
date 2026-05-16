// SPDX-License-Identifier: MIT

package preflight

import "fmt"

// HostSharingPodSpec is the host-sharing projection of one
// Lenny-managed workload's pod template. The gatherer sets each flag
// to true only when the underlying pod-spec field is explicitly true;
// an unset *bool shareProcessNamespace projects to false.
type HostSharingPodSpec struct {
	// Workload identifies the workload, e.g. "Deployment/lenny-gateway".
	Workload string
	// ShareProcessNamespace is spec.shareProcessNamespace.
	ShareProcessNamespace bool
	// HostPID is spec.hostPID.
	HostPID bool
	// HostNetwork is spec.hostNetwork.
	HostNetwork bool
	// HostIPC is spec.hostIPC.
	HostIPC bool
}

// CheckHostSharing verifies that no Lenny-managed workload's pod
// template enables a host-sharing or process-sharing flag (§13.1).
// shareProcessNamespace, hostPID, hostNetwork, and hostIPC must each be
// unset or false on every gateway, agent, lenny-ops, lenny-preflight,
// and RuntimePool pod; any one set true collapses pod-namespace
// isolation and fails the check fail-closed.
func CheckHostSharing(pods []HostSharingPodSpec) Decision {
	for _, p := range pods {
		var field string
		switch {
		case p.ShareProcessNamespace:
			field = "shareProcessNamespace"
		case p.HostPID:
			field = "hostPID"
		case p.HostNetwork:
			field = "hostNetwork"
		case p.HostIPC:
			field = "hostIPC"
		default:
			continue
		}
		return Decision{Passed: false, Reason: fmt.Sprintf(
			"POD_SPEC_HOST_SHARING_FORBIDDEN: workload %s sets %s: true; host-sharing and "+
				"process-sharing flags are forbidden on every Lenny-managed pod (§13.1)",
			p.Workload, field)}
	}
	return Decision{Passed: true}
}
