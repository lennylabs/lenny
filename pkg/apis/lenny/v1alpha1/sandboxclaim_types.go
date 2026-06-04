// SPDX-License-Identifier: MIT

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SandboxClaimSpec is the desired state of a SandboxClaim — the §4.6
// binding of a session to a claimed Sandbox pod. A SandboxClaim
// deliberately carries no ownerReference so the session survives pod
// deletion and can be reassigned; the WarmPoolController garbage-
// collects orphaned claims instead.
type SandboxClaimSpec struct {
	// SandboxRef names the Sandbox this claim binds. The
	// lenny-sandboxclaim-guard webhook resolves the referenced
	// Sandbox's status.phase to reject double-claims and stale writes.
	// +kubebuilder:validation:Required
	SandboxRef string `json:"sandboxRef"`

	// SessionID is the §15.1 session this claim serves.
	// +kubebuilder:validation:Required
	SessionID string `json:"sessionId"`

	// TenantID is the tenant that owns the session.
	// +optional
	TenantID string `json:"tenantId,omitempty"`

	// SlotID identifies a concurrent-mode (§5.2) slot on the referenced
	// Sandbox. In session and task mode one SandboxClaim binds one pod
	// exclusively and SlotID is empty. In `executionMode: concurrent`
	// each slot on a shared pod has its own SandboxClaim carrying a
	// distinct SlotID, so the pod's active-slot count is the number of
	// SandboxClaims referencing it.
	// +optional
	SlotID string `json:"slotId,omitempty"`
}

// SandboxClaimStatus is the observed state of a SandboxClaim. Per
// §4.6.3 status.phase is gateway-owned and tracks the session-to-pod
// binding — distinct from the Sandbox pod lifecycle state machine.
type SandboxClaimStatus struct {
	// Phase is the §4.6.3 session-binding state: `bound` once the
	// claim is bound to a claimed Sandbox, `active` once the session has
	// reached `attached` and is actively running work on the claimed pod,
	// `released` once the pod is returned to the pool, `failed` on a
	// binding failure. Per §4.6.3 only `released` and `failed` are terminal.
	// +kubebuilder:validation:Enum=bound;active;released;failed
	// +optional
	Phase string `json:"phase,omitempty"`

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
// +kubebuilder:printcolumn:name="Session",type=string,JSONPath=`.spec.sessionId`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`

// SandboxClaim is the lenny.dev/v1alpha1 record binding a session to a
// claimed Sandbox pod (§4.6). Gateway replicas create it with
// optimistic locking so exactly one wins a contested pod.
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
