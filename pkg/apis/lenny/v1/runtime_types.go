// SPDX-License-Identifier: MIT

package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RuntimeSpec is the desired state of a registered agent Runtime
// (§5.1). The gateway mirrors the registered runtimes into its
// runtime_definitions registry; the CRD is the declarative source.
type RuntimeSpec struct {
	// Type is the runtime kind: `agent` runs an interactive agent,
	// `mcp` exposes an MCP server.
	// +kubebuilder:validation:Enum=agent;mcp
	// +kubebuilder:validation:Required
	Type string `json:"type"`

	// Image is the OCI image reference the warm pool launches. Per §5.3
	// images must be pinned by digest, not tag: the reference must
	// contain an `@sha256:<64-hex>` digest. The Pattern enforces the
	// §5.3 supply-chain MUST at the CRD layer so a `kubectl apply` of a
	// tag-pinned Runtime is rejected by the API server, matching the
	// admin-API digest check.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`@sha256:[A-Fa-f0-9]{64}$`
	Image string `json:"image"`

	// IntegrationLevel is the §5.1 conformance level the runtime
	// implements.
	// +kubebuilder:validation:Enum=basic;standard;full
	// +kubebuilder:validation:Required
	IntegrationLevel string `json:"integrationLevel"`

	// ExecutionMode is the §5.2 pod-reuse mode. Empty defaults to
	// `session` at registration time.
	// +kubebuilder:validation:Enum=session;task;concurrent
	// +optional
	ExecutionMode string `json:"executionMode,omitempty"`

	// IsolationProfile is the §5.3 default sandbox isolation profile.
	// +kubebuilder:validation:Enum=standard;sandboxed;microvm
	// +optional
	IsolationProfile string `json:"isolationProfile,omitempty"`

	// DeploymentModel is the §4.7 agent-pod deployment model. `sidecar`
	// (the default) runs the lenny-adapter as a separate sidecar
	// container that bridges the runtime over an abstract Unix socket;
	// `embedded` runs a first-party runtime image that links the adapter
	// as a library and serves the gRPC contract from a single container.
	// Empty defaults to `sidecar` at registration time.
	// +kubebuilder:validation:Enum=sidecar;embedded
	// +optional
	DeploymentModel string `json:"deploymentModel,omitempty"`

	// AllowedResourceClasses lists the §5 resource classes a session
	// of this runtime may request.
	// +optional
	AllowedResourceClasses []string `json:"allowedResourceClasses,omitempty"`

	// SupportedProviders lists the LLM providers the runtime accepts
	// leased credentials for.
	// +optional
	SupportedProviders []string `json:"supportedProviders,omitempty"`

	// CredentialCapabilities declares the runtime's §4.9 credential
	// hot-rotation support and the LLM-proxy dialects its SDK speaks. It
	// is required when a pool bound to this runtime uses
	// deliveryMode: proxy, and empty for direct-mode-only runtimes.
	// +optional
	CredentialCapabilities *CredentialCapabilities `json:"credentialCapabilities,omitempty"`

	// WorkspaceTier is the §12.9 / §5.2 data-classification tier this
	// runtime processes. The default workspace classification is `T3`
	// (Confidential); runtimes that handle Restricted data (PHI, regulated
	// credentials) declare `T4`. A `T4` Runtime forbids cross-tenant pod
	// reuse (§5.2 line 396) and triggers the §6.4 dedicated-node controls:
	// the sandbox reconciler injects the `lenny.dev/workspace-tier: t4`
	// pod label, the T4 nodeSelector, and the T4 NoSchedule toleration so
	// the lenny-t4-node-isolation admission webhook admits the pod onto a
	// dedicated T4 node pool. An empty value is treated as `T3`.
	// +kubebuilder:validation:Enum=T3;T4
	// +optional
	WorkspaceTier string `json:"workspaceTier,omitempty"`
}

// CredentialCapabilities is the §5.1 credentialCapabilities block on a
// Runtime.
type CredentialCapabilities struct {
	// HotRotation reports mid-session credential hot-rotation support.
	// +optional
	HotRotation bool `json:"hotRotation,omitempty"`

	// ProxyDialect lists the §4.9 LLM-proxy dialects the runtime's SDK
	// speaks (openai, anthropic). An empty list declares direct-mode-only
	// support.
	// +optional
	ProxyDialect []string `json:"proxyDialect,omitempty"`
}

// RuntimeStatus is the observed state of a Runtime.
type RuntimeStatus struct {
	// ObservedGeneration is the .metadata.generation the controller
	// last reconciled into the gateway registry.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions is the standard Kubernetes condition list. The
	// controller sets a `Registered` condition once the runtime is
	// mirrored into the gateway.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=rt
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.type`
// +kubebuilder:printcolumn:name="Image",type=string,JSONPath=`.spec.image`
// +kubebuilder:printcolumn:name="Level",type=string,JSONPath=`.spec.integrationLevel`

// Runtime is the lenny.dev/v1 declaration of a registered agent
// runtime (§5.1). Runtimes are platform-global, so the resource is
// cluster-scoped.
type Runtime struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RuntimeSpec   `json:"spec,omitempty"`
	Status RuntimeStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// RuntimeList is a list of Runtime resources.
type RuntimeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Runtime `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Runtime{}, &RuntimeList{})
}
