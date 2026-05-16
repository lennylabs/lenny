// SPDX-License-Identifier: MIT

package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SandboxSpec is the desired state of a Sandbox — one managed agent
// pod (§4.6, §6). The WarmPoolController owns spec.*; the pod is
// created from the pool's SandboxTemplate, and these fields pin the
// per-pod identity the controller and gateway resolve against.
type SandboxSpec struct {
	// RuntimeRef names the lenny.dev/v1 Runtime this pod runs.
	// +kubebuilder:validation:Required
	RuntimeRef string `json:"runtimeRef"`

	// PoolRef names the SandboxWarmPool this pod is a member of.
	// +kubebuilder:validation:Required
	PoolRef string `json:"poolRef"`

	// IsolationProfile is the §5.3 sandbox isolation profile the pod
	// was warmed under.
	// +kubebuilder:validation:Enum=standard;sandboxed;microvm
	// +optional
	IsolationProfile string `json:"isolationProfile,omitempty"`

	// DeliveryMode is the §4.9 credential delivery mode.
	// +kubebuilder:validation:Enum=proxy;direct
	// +optional
	DeliveryMode string `json:"deliveryMode,omitempty"`
}

// SandboxStatus is the observed state of a Sandbox. status.phase is
// the §6.2 authoritative pod lifecycle state machine; it is the
// single source of truth that the §4.6 claim path and the
// lenny-sandboxclaim-guard webhook read.
type SandboxStatus struct {
	// Phase is the §6.2 pod lifecycle state.
	// +kubebuilder:validation:Enum=warming;sdk_connecting;idle;claimed;receiving_uploads;finalizing_workspace;running_setup;attached;task_cleanup;resuming;suspended;resume_pending;awaiting_client_action;completed;failed;cancelled;expired;draining;terminated
	// +optional
	Phase string `json:"phase,omitempty"`

	// PodName is the underlying Pod backing this Sandbox. Empty until
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
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=sbx
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Pool",type=string,JSONPath=`.spec.poolRef`
// +kubebuilder:printcolumn:name="Pod",type=string,JSONPath=`.status.podName`

// Sandbox is the lenny.dev/v1 record of one managed agent pod (§4.6).
// The status subresource carries the §6.2 authoritative pod state
// machine; the gateway claims pods by referencing them from a
// SandboxClaim.
type Sandbox struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SandboxSpec   `json:"spec,omitempty"`
	Status SandboxStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// SandboxList is a list of Sandbox resources.
type SandboxList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Sandbox `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Sandbox{}, &SandboxList{})
}
