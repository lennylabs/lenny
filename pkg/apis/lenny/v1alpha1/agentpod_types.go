// SPDX-License-Identifier: MIT

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// FinalizerSessionCleanup is the §4.6.1 finalizer every AgentPod carries
// to prevent Kubernetes from deleting the backing pod (and its local
// workspace) while a session is still active. The WarmPoolController's
// AgentPod-to-Pod reconciler removes it only after confirming no active
// PodClaim references the AgentPod; until then a deletion blocks in
// the Terminating state. spec: §4.6.1 "AgentPod finalizers".
const FinalizerSessionCleanup = "lenny.dev/session-cleanup"

// AgentPodConditionSOPeercredDisabled marks an AgentPod running in §4.7
// nonce-only mode. Set by the WarmPoolController; the adapter has no
// apiserver access (§10.3).
const AgentPodConditionSOPeercredDisabled = "SOPeercredDisabled"

// AgentPodSpec is the desired state of an AgentPod — one managed agent
// pod (§4.6, §6). The WarmPoolController owns spec.*; the pod is
// created from the pool's PodSet, and these fields pin the
// per-pod identity the controller and gateway resolve against.
type AgentPodSpec struct {
	// RuntimeRef names the lenny.dev/v1alpha1 Runtime this pod runs.
	// +kubebuilder:validation:Required
	RuntimeRef string `json:"runtimeRef"`

	// PodSetRef names the PodSet this pod is a member of. The PodSet is
	// also the AgentPod's controller owner, and the reference cannot change
	// after create.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="podSetRef is immutable"
	PodSetRef string `json:"podSetRef"`

	// IsolationProfile is the §5.3 sandbox isolation profile the pod
	// was warmed under.
	// +kubebuilder:validation:Enum=standard;sandboxed;microvm
	// +optional
	IsolationProfile string `json:"isolationProfile,omitempty"`

	// ExecutionMode is the §5.2 pod-reuse mode the pod was warmed
	// under: `session` (a pod parameterized by the pool's sessionPolicy)
	// or `service` (a claimless, request-routed pod). spec: §12.6 — the PodRegistry projects it as PodRecord.ExecutionMode.
	// The WarmPoolController denormalizes it from the pool's
	// PodSet.spec.executionMode onto each AgentPod (the same
	// copy-down as IsolationProfile) so the registry can project it
	// without resolving the PodSet on every read. Empty is treated as
	// `session`.
	// +kubebuilder:validation:Enum=session;service
	// +optional
	ExecutionMode string `json:"executionMode,omitempty"`

	// DeliveryMode is the §4.9 credential delivery mode.
	// +kubebuilder:validation:Enum=proxy;direct
	// +optional
	DeliveryMode string `json:"deliveryMode,omitempty"`

	// ResourceClass is the §5.1/§5.2 resource class (small, medium,
	// large, or a deployer-defined class) the pod's CPU/memory limits
	// resolve from. spec: §12.6 — the PodRegistry PodSpec
	// carries the per-pod resource limits, which this codebase models as
	// a named resource class rather than raw quantities. A warm pod
	// created by the WarmPoolController resolves its class from the
	// PodSet at pod-build time and leaves this empty; the §12.6
	// CreatePod path (a gateway-created pod for a non-prewarmed session
	// at Tier 4+) stamps the resolved class here so it round-trips
	// through the registry.
	// +optional
	ResourceClass string `json:"resourceClass,omitempty"`

	// TopologySpreadConstraints distribute the pod across zones and
	// nodes. spec: §5.2 — the WarmPoolController copies the
	// pool's PodSet.spec.topologySpreadConstraints onto each
	// AgentPod it creates, and the AgentPod-to-Pod reconciler stamps them
	// onto the agent pod. The PoolScalingController owns the defaults it
	// writes into the PodSet; this field carries the resolved
	// constraints (defaults or deployer override) down to the pod.
	// +optional
	TopologySpreadConstraints []corev1.TopologySpreadConstraint `json:"topologySpreadConstraints,omitempty"`

	// RequireSoPeercred carries the resolved §4.7 nonce-only render
	// decision the WarmPoolController's AgentPod reconciler computes from
	// the referenced Runtime's requireSoPeercred, its deploymentModel, and
	// the pool's acknowledgeNonceOnlyAuth. An explicit false records that
	// the pod was rendered with --require-so-peercred=false (nonce-only
	// mode); a nil value records the default SO_PEERCRED-enforcing render.
	// The pool-level SecurityDegradedMode trigger reads this field back
	// from the member AgentPods without resolving the Runtime CR. spec:
	// §4.7.
	// +optional
	RequireSoPeercred *bool `json:"requireSoPeercred,omitempty"`
}

// AgentPodStatus is the observed state of an AgentPod. status.phase is
// the §6.2 authoritative pod lifecycle state machine; it is the
// single source of truth that the §4.6 claim path and the
// lenny-podclaim-guard webhook read.
type AgentPodStatus struct {
	// Phase is the §6.2 coarse pod-occupancy state. The WarmPoolController
	// is the sole writer and computes it as a level-triggered projection of
	// per-pod PodClaim existence, the claim's binding state, and the
	// pool's sessionPolicy (§4.6.3). The fine session-lifecycle states live
	// in the Postgres session model (§7.2, §8.8) and are not projected onto
	// the CRD; concurrent occupancy projects `claimed`.
	// +kubebuilder:validation:Enum=warming;idle;reserved;claimed;sdk_connecting;draining;failed;terminated
	// +optional
	Phase string `json:"phase,omitempty"`

	// TenantID records the tenant a recycling or concurrent-session pod is
	// pinned to (§5.2 tenant pinning). It is set on the pod's first
	// assignment and is never reassigned to a different tenant for the
	// pod's lifetime. Empty for an unassigned pod.
	// +optional
	TenantID string `json:"tenantId,omitempty"`

	// SessionID records the session a pod is bound to. spec: §12.6 —
	// PodRecord.SessionID (nullable) in the store interface table, and
	// the PodRegistry agent_pod_state.session_id column, which the
	// schema comment describes as set on claim. The §4.6.1
	// claim path writes it on ClaimPod and clears it on ReleasePod; the
	// orphan-session reconciler (§10.1) cross-references it via the
	// partial session_id index. Empty for an unclaimed pod.
	// +optional
	SessionID string `json:"sessionId,omitempty"`

	// PodName is the underlying Pod backing this AgentPod. Empty until
	// the pod is created.
	// +optional
	PodName string `json:"podName,omitempty"`

	// NodeName is the host node the pod is scheduled on. Empty until
	// the scheduler binds the pod.
	// +optional
	NodeName string `json:"nodeName,omitempty"`

	// PodIP is the cluster IP of the backing Pod. The gateway dials the
	// pod's §4.7 adapter at this address. Empty until the pod is running.
	// +optional
	PodIP string `json:"podIP,omitempty"`

	// ObservedGeneration is the .metadata.generation the controller
	// last reconciled.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions is the standard Kubernetes condition list.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// The lenny.dev/schema-version annotation is read at controller startup and
// by lenny-preflight, and a mismatch with preflight.CurrentCRDSchemaVersion
// is fatal, so a stale CRD never silently strips new fields. Bump the value
// here and in that constant together. The top-level spec and status fields
// carry +kubebuilder:pruning:PreserveUnknownFields for the same reason: a CRD
// older than the controller does not prune fields a newer controller writes.
// spec: §10.5 (Upgrade and Rollback Strategy). F-15.5.12.
// +kubebuilder:metadata:annotations="lenny.dev/schema-version=1"
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=apod
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="PodSet",type=string,JSONPath=`.spec.podSetRef`
// +kubebuilder:printcolumn:name="Pod",type=string,JSONPath=`.status.podName`

// AgentPod is the lenny.dev/v1alpha1 record of one managed agent pod (§4.6).
// The status subresource carries the §6.2 authoritative pod state
// machine; the gateway claims pods by referencing them from a
// PodClaim.
type AgentPod struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:pruning:PreserveUnknownFields
	Spec AgentPodSpec `json:"spec,omitempty"`
	// +kubebuilder:pruning:PreserveUnknownFields
	Status AgentPodStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AgentPodList is a list of AgentPod resources.
type AgentPodList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentPod `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AgentPod{}, &AgentPodList{})
}
