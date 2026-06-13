// SPDX-License-Identifier: MIT

package v1alpha1

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

	// ExecutionMode is the §5.2 pod-reuse mode: `session` (a pod
	// parameterized by the pool's sessionPolicy) or `service` (a claimless,
	// request-routed pod). Empty defaults to `session` at registration
	// time.
	// +kubebuilder:validation:Enum=session;service
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

	// SharedAssets is the §6.4 concurrent-workspace read-only shared-asset
	// list. Each entry is an inline file the platform materializes into the
	// pod's `/workspace/shared/` directory at warm time, before any slot is
	// assigned. `/workspace/shared/` is then mounted read-only on the
	// runtime container, so a runtime write returns EROFS. The directory is
	// mounted (empty, read-only) even when this list is empty, denying the
	// runtime writable scratch space there. Artifact-reference assets
	// (§4.5) are delivered by the gateway during pod initialization and are
	// not expressed inline here. spec: §6.4 line 409 — F-6.4.3.
	// +optional
	SharedAssets []SharedAsset `json:"sharedAssets,omitempty"`

	// SetupCommandPolicy is the §5.1 / §7.5 setupCommandPolicy block: the
	// per-runtime command allow/block list, shell-execution flag, and
	// per-session command cap the gateway enforces at session create-time
	// (§7.5 line 488). An empty block declares no policy (the legacy
	// admit-everything path). F-7.5.10.
	// +optional
	SetupCommandPolicy *SetupCommandPolicy `json:"setupCommandPolicy,omitempty"`

	// ArchivePolicy is the §13.4 per-Runtime archive-extraction opt-in
	// block: AllowSymlinks lifts the §7.4 line 458 default-deny on symlink
	// entries inside uploadArchive sources. An empty block leaves the
	// platform default (symlinks rejected). spec: §7.4 lines 458, 462;
	// §13.4 lines 663-672 — F-7.4.4.
	// +optional
	ArchivePolicy *ArchivePolicy `json:"archivePolicy,omitempty"`

	// Capabilities is the §5.1 capabilities block on a runtime: the
	// runtime's interaction model and its mid-session injection support.
	// §26 reference runtimes (mastra, openai-assistants, crewai, …)
	// declare these so the registered Runtime CRD advertises the
	// §15.4.6 conformance battery the runtime actually implements; the
	// runtime controller mirrors them into the gateway registry's
	// RuntimeCapabilities at reconciliation time so §7.2 path-4 (queued
	// injection) is gated on the runtime's declared injection.modes.
	// spec: §5.1 lines 60-64; §26.9 line 407; §26.10 line 432; §26.11
	// line 466 — F-26.9.2 / F-26.10.2 / F-26.11.2.
	// +optional
	Capabilities *RuntimeCapabilitiesCRD `json:"capabilities,omitempty"`

	// RuntimeOptionsSchemaRef is the §5.1 / §26 runtimeOptionsSchema URL
	// the runtime advertises. The §14 path resolves it (or the inline
	// schema set via the admin API) when validating session-creation
	// runtimeOptions. The CRD carries the URL form so the chart can
	// declaratively register schemas hosted at
	// `https://schemas.lenny.dev/runtime-options/<name>/v1.json` per §26.
	// The runtime controller stamps it into the gateway registry as a
	// JSON `{"$ref": "<url>"}` schema fragment. spec: §5.1 line 92; §26.9
	// line 408; §26.10 line 434; §26.11 line 465 — F-26.9.2 / F-26.10.2 /
	// F-26.11.2.
	// +optional
	// +kubebuilder:validation:Pattern=`^https?://`
	RuntimeOptionsSchemaRef string `json:"runtimeOptionsSchemaRef,omitempty"`

	// DelegationPolicyRef is the §5.1 delegationPolicyRef: the
	// DelegationPolicy this runtime is bound to. §26.11 (crewai) declares
	// the field as required on a runtime that delegates. The runtime
	// controller propagates it through the gateway registry.
	// spec: §5.1 line 68; §26.11 line 467 — F-26.11.2.
	// +optional
	DelegationPolicyRef string `json:"delegationPolicyRef,omitempty"`

	// Limits is the §5.1 limits block: the per-runtime session-age,
	// upload-size, and §11.3 request-input / elicitation wait caps. The
	// §26 coding-agent catalog declares it (maxSessionAge 14400,
	// maxUploadSize 500MB, maxRequestInputWaitSeconds 1800). The runtime
	// controller mirrors it into the gateway registry so the
	// §11.3 / §6.2 session-age watchdog, the upload-handler cap, and the
	// inter-agent request_input timeout read the values the deployer
	// declared on the Runtime resource. An empty block leaves the platform
	// defaults. spec: §5.1 lines 76-79; §26.2 lines 81-86; §26.3 lines
	// 166-169 — F-26.2.1 / F-26.3.2.
	// +optional
	Limits *RuntimeLimits `json:"limits,omitempty"`

	// SetupPolicy is the §5.1 setupPolicy block: the aggregate cap on the
	// pod setup phase and the disposition when the cap is hit. The §26
	// coding-agent catalog declares it (timeoutSeconds 600, onTimeout
	// fail). The runtime controller mirrors it into the gateway registry
	// so the §6.2 finalizing-state watchdog enforces the runtime-declared
	// setup timeout. An empty block leaves the runtime with no aggregate
	// setup cap. spec: §5.1 lines 90-92; §26.3 lines 174-176 — F-26.2.1 /
	// F-26.3.4.
	// +optional
	SetupPolicy *SetupPolicy `json:"setupPolicy,omitempty"`

	// DefaultPoolConfig is the §5.1 defaultPoolConfig block: the
	// runtime-declared default pool sizing the §5.2 pool resolver consults
	// before falling back to platform defaults. The §26 coding-agent
	// catalog declares it (warmCount 2, resourceClass medium, egressProfile
	// restricted). The runtime controller mirrors it into the gateway
	// registry. An empty block leaves the platform defaults. spec: §5.1
	// lines 97-100; §26.3 lines 181-184 — F-26.2.1 / F-26.3.5.
	// +optional
	DefaultPoolConfig *DefaultPoolConfig `json:"defaultPoolConfig,omitempty"`

	// AgentInterface is the §5.1 agentInterface descriptor on a
	// type:agent runtime: the human-readable description, the input and
	// output media types, the workspace-files flag, and the declared
	// skills. §26 reference runtimes declare it so the registered Runtime
	// advertises the agent's skills and the §15 A2A agent-card auto-
	// generation has a source. The runtime controller mirrors it into the
	// gateway registry; the admin path regenerates the agent card from it.
	// It is nil for type:mcp runtimes and for type:agent runtimes that omit
	// the block. spec: §5.1 lines 102-130; §26.3 lines 185-199 — F-26.2.1 /
	// F-26.3.5.
	// +optional
	AgentInterface *AgentInterface `json:"agentInterface,omitempty"`
}

// RuntimeCapabilitiesCRD mirrors the §5.1 capabilities block onto the
// Runtime CRD so the registered Runtime advertises the runtime's
// interaction model and mid-session injection support declaratively.
// The runtime controller plumbs every field into the gateway registry's
// runtimestore.RuntimeCapabilities. spec: §5.1 lines 60-64 — F-26.9.2.
type RuntimeCapabilitiesCRD struct {
	// Interaction is the runtime's §5.1 capabilities.interaction model:
	// one_shot (one message, one response) or multi_turn (repeated message
	// delivery over a task). §26's reference runtimes declare multi_turn.
	// +kubebuilder:validation:Enum=one_shot;multi_turn
	// +optional
	Interaction string `json:"interaction,omitempty"`

	// Injection is the runtime's §5.1 capabilities.injection block:
	// whether mid-session message injection is supported and the modes
	// (immediate, queued) the runtime accepts. §26's reference runtimes
	// declare supported=true with both modes.
	// +optional
	Injection *InjectionCapabilityCRD `json:"injection,omitempty"`

	// PreConnect is the §5.1 capabilities.preConnect flag: when true the
	// §6.1 warm-pool controller pre-connects the agent SDK process during
	// the warm phase. Default false.
	// +optional
	PreConnect bool `json:"preConnect,omitempty"`
}

// InjectionCapabilityCRD is the §5.1 capabilities.injection block: whether
// the runtime accepts mid-session message injection and the §7.2 delivery
// modes it supports.
type InjectionCapabilityCRD struct {
	// Supported is the §5.1 capabilities.injection.supported flag. False
	// (the default) rejects injection per §7.2 line 222.
	// +optional
	Supported bool `json:"supported,omitempty"`

	// Modes lists the §7.2 injection modes the runtime accepts:
	// `immediate` interrupts a suspended session to deliver, `queued`
	// buffers the message for the next runtime turn.
	// +optional
	Modes []string `json:"modes,omitempty"`
}

// ArchivePolicy mirrors the §13.4 archivePolicy block onto the Runtime CRD
// so an operator can opt a runtime into symlink-bearing upload archives
// declaratively. The runtime controller plumbs it into the gateway
// registry; the gateway then forwards it to the adapter on
// FinalizeWorkspace so uploadArchive symlink entries are admitted (and
// target-validated) per §7.4 line 458 and §13.4. spec: §7.4 lines 458, 462
// — F-7.4.4.
type ArchivePolicy struct {
	// AllowSymlinks lifts the §7.4 line 458 default-deny on symlink
	// entries inside uploadArchive sources. When false (the default),
	// extraction aborts on any symlink with details.reason = "symlink".
	// +optional
	AllowSymlinks bool `json:"allowSymlinks,omitempty"`
}

// SharedAsset is one §6.4 read-only shared-asset entry the platform
// materializes into the pod's `/workspace/shared/` directory at warm
// time. The directory is mounted read-only on the runtime container, so
// the agent reads these files but a write returns EROFS. spec: §6.4 line
// 409 — F-6.4.3.
type SharedAsset struct {
	// Path is the destination path relative to `/workspace/shared/`. It
	// must be a relative path with no parent-directory (`..`) segment; the
	// adapter rejects an absolute path or one that escapes the shared root.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Path string `json:"path"`

	// Content is the inline file body. An empty value materializes an empty
	// file. The length is capped so the rendered pod spec stays within the
	// API-server object-size budget; larger cross-slot assets are delivered
	// by the gateway as artifact references during pod initialization.
	// +optional
	// +kubebuilder:validation:MaxLength=32768
	Content string `json:"content,omitempty"`

	// Mode is the octal file mode for the materialized file (for example
	// `0444`). An empty value defaults to `0444` (read-only for all),
	// matching the read-only mount. setuid and setgid modes are rejected.
	// +optional
	// +kubebuilder:validation:Pattern=`^[0-7]{3,4}$`
	Mode string `json:"mode,omitempty"`
}

// SetupCommandPolicy mirrors the §5.1 / §7.5 setupCommandPolicy block onto
// the Runtime CRD so an operator can declare the policy declaratively. The
// runtime controller plumbs every field into the gateway registry's
// runtimestore.SetupCommandPolicy at reconciliation time. spec: §7.5 lines
// 481-490 — F-7.5.10.
type SetupCommandPolicy struct {
	// Mode selects allowlist (deny-by-default) or blocklist
	// (allow-by-default) §7.5 prefix matching. An empty value disables the
	// prefix gate but the rest of the policy (shell, maxCommands) still
	// applies.
	// +kubebuilder:validation:Enum=allowlist;blocklist
	// +optional
	Mode string `json:"mode,omitempty"`

	// Shell selects shell-vs-argv setup-command execution. False splits on
	// whitespace and execs the argv directly, neutering shell
	// metacharacters per §7.5 line 490.
	// +optional
	Shell bool `json:"shell,omitempty"`

	// Allowlist is the set of permitted §7.5 command prefixes when Mode is
	// allowlist.
	// +optional
	Allowlist []string `json:"allowlist,omitempty"`

	// Blocklist is the set of rejected §7.5 command prefixes when Mode is
	// blocklist.
	// +optional
	Blocklist []string `json:"blocklist,omitempty"`

	// MaxCommands caps the number of setup commands per session. Zero
	// declares no cap.
	// +optional
	// +kubebuilder:validation:Minimum=0
	MaxCommands int32 `json:"maxCommands,omitempty"`
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

// RuntimeLimits mirrors the §5.1 limits block onto the Runtime CRD so an
// operator can declare the per-runtime session-age, upload-size, and §11.3
// request-input / elicitation wait caps declaratively. The runtime
// controller plumbs every field into the gateway registry's
// runtimestore.Limits at reconciliation time. Fields carry explicit units
// (seconds, bytes) to match the registry's typed representation. spec:
// §5.1 lines 76-79; §11.3 lines 198-204 — F-26.2.1 / F-26.3.2.
type RuntimeLimits struct {
	// MaxSessionAgeSeconds caps a session's lifetime in seconds. Zero
	// declares no per-runtime session-age cap. The §26 coding-agent
	// catalog declares 14400 (4 hours).
	// +optional
	// +kubebuilder:validation:Minimum=0
	MaxSessionAgeSeconds int `json:"maxSessionAgeSeconds,omitempty"`

	// MaxUploadSizeBytes caps a single client upload in bytes. Zero
	// declares no per-runtime upload cap. The §26 coding-agent catalog
	// declares 500 MB (524288000 bytes).
	// +optional
	// +kubebuilder:validation:Minimum=0
	MaxUploadSizeBytes int64 `json:"maxUploadSizeBytes,omitempty"`

	// MaxRequestInputWaitSeconds is the §11.3 inter-agent
	// lenny/request_input timeout in seconds. Zero selects the platform
	// default. The §26 coding-agent catalog declares 1800 (30 minutes).
	// +optional
	// +kubebuilder:validation:Minimum=0
	MaxRequestInputWaitSeconds int `json:"maxRequestInputWaitSeconds,omitempty"`

	// MaxElicitationWaitSeconds is the §11.3 line 202 human-facing
	// lenny/request_elicitation wait timeout in seconds. Zero selects the
	// platform default (600s).
	// +optional
	// +kubebuilder:validation:Minimum=0
	MaxElicitationWaitSeconds int `json:"maxElicitationWaitSeconds,omitempty"`

	// MaxElicitationsPerSession is the §11.3 line 203 per-session lifetime
	// elicitation budget. Zero selects the platform default (50).
	// +optional
	// +kubebuilder:validation:Minimum=0
	MaxElicitationsPerSession int `json:"maxElicitationsPerSession,omitempty"`
}

// SetupTimeoutDisposition is the §5.1 setupPolicy.onTimeout enum: the
// disposition when a runtime's pod setup phase exceeds its cap.
// +kubebuilder:validation:Enum=fail;warn
type SetupTimeoutDisposition string

// SetupPolicy mirrors the §5.1 setupPolicy block onto the Runtime CRD: the
// aggregate cap on the pod setup phase and the disposition when the cap is
// hit. The runtime controller plumbs it into the gateway registry's
// runtimestore.SetupPolicy at reconciliation time. spec: §5.1 lines 90-92;
// §26.3 lines 174-176 — F-26.2.1 / F-26.3.4.
type SetupPolicy struct {
	// TimeoutSeconds is the aggregate cap on the setup phase in seconds.
	// Zero declares no aggregate cap (§5.1 line 260). The §26 coding-agent
	// catalog declares 600.
	// +optional
	// +kubebuilder:validation:Minimum=0
	TimeoutSeconds int `json:"timeoutSeconds,omitempty"`

	// OnTimeout is the disposition when the cap is exceeded. An empty value
	// is treated as the conservative "fail" default.
	// +optional
	OnTimeout SetupTimeoutDisposition `json:"onTimeout,omitempty"`
}

// DefaultPoolConfig mirrors the §5.1 defaultPoolConfig block onto the
// Runtime CRD: the default pool sizing the §5.2 pool resolver consults
// before falling back to platform defaults. The runtime controller plumbs
// it into the gateway registry's runtimestore.DefaultPoolConfig at
// reconciliation time. spec: §5.1 lines 97-100; §26.3 lines 181-184 —
// F-26.2.1 / F-26.3.5.
type DefaultPoolConfig struct {
	// WarmCount is the default number of warm pods the pool keeps.
	// +optional
	// +kubebuilder:validation:Minimum=0
	WarmCount int `json:"warmCount,omitempty"`

	// ResourceClass is the default §5.1 resource class for the pool.
	// +optional
	ResourceClass string `json:"resourceClass,omitempty"`

	// EgressProfile is the default §13.2 egress profile for the pool. The
	// §26 coding-agent catalog declares restricted.
	// +optional
	EgressProfile string `json:"egressProfile,omitempty"`
}

// AgentInterface mirrors the §5.1 agentInterface descriptor onto the
// Runtime CRD: the runtime's human-readable description, accepted and
// emitted media types, the workspace-files flag, and the declared skills.
// The runtime controller plumbs it into the gateway registry's
// runtimestore.AgentInterface at reconciliation time; the admin path then
// regenerates the §15 A2A agent card from it. type:mcp runtimes do not
// carry an agentInterface. spec: §5.1 lines 102-130; §26.3 lines 185-199 —
// F-26.2.1 / F-26.3.5.
type AgentInterface struct {
	// Description is the human-readable summary of the runtime's role.
	// +optional
	Description string `json:"description,omitempty"`

	// InputModes enumerates the media types the runtime accepts.
	// +optional
	InputModes []AgentInterfaceMode `json:"inputModes,omitempty"`

	// OutputModes enumerates the media types the runtime emits.
	// +optional
	OutputModes []AgentInterfaceMode `json:"outputModes,omitempty"`

	// SupportsWorkspaceFiles signals that the runtime honors workspace
	// files in a TaskSpec.
	// +optional
	SupportsWorkspaceFiles bool `json:"supportsWorkspaceFiles,omitempty"`

	// Skills enumerates the discrete capabilities the runtime advertises.
	// +optional
	Skills []AgentInterfaceSkill `json:"skills,omitempty"`
}

// AgentInterfaceMode is one entry in AgentInterface.InputModes or
// AgentInterface.OutputModes.
type AgentInterfaceMode struct {
	// Type is the IANA media type, for example "text/plain".
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Type string `json:"type"`

	// Role is an optional tag such as "primary".
	// +optional
	Role string `json:"role,omitempty"`
}

// AgentInterfaceSkill is one entry in AgentInterface.Skills.
type AgentInterfaceSkill struct {
	// ID is the stable skill identifier, for example "review".
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	ID string `json:"id"`

	// Name is the human-readable skill name.
	// +optional
	Name string `json:"name,omitempty"`

	// Description elaborates on what the skill does.
	// +optional
	Description string `json:"description,omitempty"`
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

// Runtime is the lenny.dev/v1alpha1 declaration of a registered agent
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
