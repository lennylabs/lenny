// SPDX-License-Identifier: MIT

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SandboxClaimSpec is the desired state of a SandboxClaim — the §4.6
// per-pod-occupancy claim on a Sandbox pod. A gateway replica acquires a
// pod by creating a SandboxClaim with the deterministic name
// `claim-<podName>`; exactly one CREATE wins and the others receive an
// AlreadyExists conflict. The session-to-pod binding lives on the
// Postgres session row's `pod_assignment` column, so the claim carries no
// session identifier. A SandboxClaim deliberately omits an ownerReference
// so its deletion is an explicit step of the claim lifecycle (hold expiry,
// orphan GC, or pod termination) rather than a cascade from pod deletion;
// the WarmPoolController garbage-collects orphaned claims.
// spec: §4.6.3 (CRD field ownership), §5.2 (execution modes).
type SandboxClaimSpec struct {
	// SandboxRef names the Sandbox this claim binds. The
	// lenny-sandboxclaim-guard webhook rejects a second CREATE for the
	// same sandboxRef to prevent a double-claim.
	// +kubebuilder:validation:Required
	SandboxRef string `json:"sandboxRef"`

	// TenantID is the tenant the claimed pod is pinned to. spec: §5.2 —
	// a recycling or concurrent-session pod is pinned to a single tenant
	// for its lifetime; the claim records the pin the gateway enforces.
	// +optional
	TenantID string `json:"tenantId,omitempty"`
}

// SandboxClaimStatus is the observed state of a SandboxClaim. Per §4.6.3
// the status carries the gateway-owned binding state of the pod-occupancy
// claim, distinct from the Sandbox pod lifecycle state machine (§6.2) that
// the WarmPoolController projects from this binding state.
type SandboxClaimStatus struct {
	// Phase is the §4.6.3 binding state of the per-pod-occupancy claim:
	// `bound` once the claim is bound to a claimed pod; `recycling` while
	// the occupancy-zero whole-pod scrub (and, on preConnect pools, the
	// SDK re-warm) runs; `reserved` while the scrubbed pod is held for its
	// pinned tenant through the hold TTL; `released` once the pod is
	// returned to the pool; `failed` on a binding failure. Per §4.6.3 only
	// `released` and `failed` are terminal.
	// +kubebuilder:validation:Enum=bound;recycling;reserved;released;failed
	// +optional
	Phase string `json:"phase,omitempty"`

	// HoldExpiresAt is the §4.6.3 reserved-hold deadline the gateway
	// stamps when it patches the claim to `reserved`: the reservation time
	// plus the deployment-level hold TTL (`gateway.claimHoldTTLSeconds`,
	// default 10s). A same-tenant session arriving before this deadline
	// rebinds the claim (`reserved → bound`); after it the holder deletes
	// the claim and the pod returns to `idle`. Empty until the claim
	// enters `reserved`. spec: §4.6.3 (reserved hold), §6.2 (pod state
	// machine).
	// +optional
	HoldExpiresAt *metav1.Time `json:"holdExpiresAt,omitempty"`

	// RewarmStartedAt is the §4.6.3 re-warm-start stamp the gateway writes
	// on a preConnect pool when the whole-pod scrub report arrives and the
	// recycle disposition begins the SDK re-warm. The `sdkConnectTimeoutSeconds`
	// watchdog (§6.1) measures only the re-warm leg from this stamp, so
	// neither the prior occupancy episode nor the whole-pod scrub counts
	// against the re-warm budget. Empty on non-preConnect pools and before
	// the re-warm begins. spec: §4.6.3 (occupancy projection), §6.1, §6.2.
	// +optional
	RewarmStartedAt *metav1.Time `json:"rewarmStartedAt,omitempty"`

	// BindingStateTransitionTime is the time of the last `Phase`
	// (binding-state) transition. The WarmPoolController orphan GC keys
	// the §4.6.3 live-binding-state reclaim predicate on this stamp rather
	// than `metadata.creationTimestamp`, because a per-pod claim's creation
	// time marks the start of the whole occupancy episode rather than the
	// start of the orphan window. spec: §4.6.3 (orphan claim detection).
	// +optional
	BindingStateTransitionTime *metav1.Time `json:"bindingStateTransitionTime,omitempty"`

	// ObservedGeneration is the .metadata.generation last reconciled.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions is the standard Kubernetes condition list.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=sbxc
// +kubebuilder:printcolumn:name="Sandbox",type=string,JSONPath=`.spec.sandboxRef`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`

// SandboxClaim is the lenny.dev/v1alpha1 record of a per-pod-occupancy
// claim on a Sandbox pod (§4.6). Gateway replicas create it with the
// deterministic name `claim-<podName>` so exactly one wins a contested
// pod through the API server's CREATE uniqueness.
type SandboxClaim struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SandboxClaimSpec   `json:"spec,omitempty"`
	Status SandboxClaimStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// SandboxClaimList is a list of SandboxClaim resources.
type SandboxClaimList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SandboxClaim `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SandboxClaim{}, &SandboxClaimList{})
}
