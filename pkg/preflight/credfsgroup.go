// SPDX-License-Identifier: MIT

package preflight

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
)

// AgentPodSpec is the §13.1 projection of one controller-spawned agent
// Pod: the host-sharing flags the host-sharing audit consults plus the
// pod-level fsGroup and supplementalGroups the credential-delivery audit
// inspects. The embedded HostSharingPodSpec lets the gathered agent pods
// feed CheckHostSharing alongside the workload templates.
type AgentPodSpec struct {
	HostSharingPodSpec

	// FSGroup is spec.securityContext.fsGroup. Nil when the pod sets no
	// pod-level fsGroup, which §13.1 forbids for an agent pod because the
	// kubelet would then never group-own the credential file.
	FSGroup *int64
	// SupplementalGroups is spec.securityContext.supplementalGroups.
	SupplementalGroups []int64
}

// projectAgentPod reduces an agent Pod spec to the §13.1 host-sharing and
// credential-fsGroup fields the preflight audits inspect.
func projectAgentPod(workload string, spec *corev1.PodSpec) AgentPodSpec {
	out := AgentPodSpec{HostSharingPodSpec: projectHostSharing(workload, spec)}
	if sc := spec.SecurityContext; sc != nil {
		out.FSGroup = sc.FSGroup
		out.SupplementalGroups = sc.SupplementalGroups
	}
	return out
}

// CheckAgentPodCredFSGroup verifies that every controller-spawned agent
// Pod carries the lenny-cred-readers fsGroup and supplementalGroups the
// §13.1 cross-UID credential-delivery path requires. The admission
// webhook enforces this at pod CREATE (POD_SPEC_CRED_FSGROUP_MISSING);
// this install-time backstop catches an agent pod that drifted out of
// compliance or was created before the webhook was in place. An empty
// pod set (a fresh install with no agent pods yet) passes. F-13.1.4.
//
// spec: §13.1 line 25 — "The admission webhook and lenny-preflight Job
// validate the presence ... of the fsGroup and supplementalGroups
// settings on every agent-pod template; a pod template missing the
// lenny-cred-readers fsGroup is rejected with POD_SPEC_CRED_FSGROUP_MISSING".
func CheckAgentPodCredFSGroup(pods []AgentPodSpec, credReadersGID int64) Decision {
	for _, p := range pods {
		if p.FSGroup == nil {
			return Decision{Passed: false, Reason: fmt.Sprintf(
				"POD_SPEC_CRED_FSGROUP_MISSING: agent pod %s sets no pod-level fsGroup; "+
					"the lenny-cred-readers GID %d is required so the kubelet group-owns "+
					"the credential file (§13.1)",
				p.Workload, credReadersGID,
			)}
		}
		if *p.FSGroup != credReadersGID {
			return Decision{Passed: false, Reason: fmt.Sprintf(
				"POD_SPEC_CRED_FSGROUP_MISSING: agent pod %s sets fsGroup %d, expected the "+
					"lenny-cred-readers GID %d (§13.1)",
				p.Workload, *p.FSGroup, credReadersGID,
			)}
		}
		if !containsGID(p.SupplementalGroups, credReadersGID) {
			return Decision{Passed: false, Reason: fmt.Sprintf(
				"POD_SPEC_CRED_FSGROUP_MISSING: agent pod %s omits the lenny-cred-readers "+
					"GID %d from securityContext.supplementalGroups; the explicit membership "+
					"is required for the cross-UID credential-delivery path (§13.1)",
				p.Workload, credReadersGID,
			)}
		}
	}
	return Decision{Passed: true}
}

// containsGID reports whether gids includes want. It mirrors the
// pod-level membership check the §13.1 admission validator performs.
func containsGID(gids []int64, want int64) bool {
	for _, g := range gids {
		if g == want {
			return true
		}
	}
	return false
}
