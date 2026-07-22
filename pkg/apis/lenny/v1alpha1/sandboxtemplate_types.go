// SPDX-License-Identifier: MIT

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SandboxTemplateConditionSecurityDegradedMode marks a pool rendering
// nonce-only pods (§4.7): its deploymentModel: sidecar runtime sets
// requireSoPeercred: false and the pool carries the §5.3
// acknowledgeNonceOnlyAuth acknowledgment. A pool that still runs a
// nonce-only pod after a revert keeps the condition. Set by the
// WarmPoolController per the §4.6.3 ownership table.
const SandboxTemplateConditionSecurityDegradedMode = "SecurityDegradedMode"

// SessionPolicy parameterizes session-mode pod reuse (§5.2). One
// mechanism replaces the former session, task, and concurrent-workspace
// modes: `maxConcurrentSessions` sets simultaneous sessions per pod and
// the nested Recycle block governs sequential pod reuse with a whole-pod
// scrub between sessions. The CRD carries the cross-tenant microvm scrub
// controls (ScrubProfile and AcknowledgeMicrovmResidualState) that the
// pool controller's pool_config_validator reads to enforce the
// fail-closed in-place acknowledgment gate; the gateway's poolstore
// holds the remaining sizing and acknowledgment knobs (§4.6.3
// ownership). spec: §5.2 (pool configuration and execution modes).
type SessionPolicy struct {
	// Recycle configures sequential pod reuse with a whole-pod scrub at
	// the occupancy-zero recycle boundary. It is nil on pools that do not
	// recycle (`recycle.enabled: false`, the default).
	// +optional
	Recycle *RecyclePolicy `json:"recycle,omitempty"`
}

// RecyclePolicy is the §5.2 sessionPolicy.recycle block: sequential pod
// reuse across whole sessions of one tenant. The CRD declares the
// cross-tenant microvm scrub controls (ScrubProfile and
// AcknowledgeMicrovmResidualState) so the pool controller's
// pool_config_validator can read them; that validator enforces the
// fail-closed `in-place`-gated acknowledgment (it rejects
// `scrubProfile: in-place` without `acknowledgeMicrovmResidualState`).
// The API server validates only the ScrubProfile enum; it does not
// enforce the in-place→acknowledgment gate. The remaining recycle knobs
// (maxSessionsPerPod, maxScrubFailures, etc.) live in the gateway
// poolstore. spec: §5.2 (recycle lifecycle, Kata scrub variant).
type RecyclePolicy struct {
	// ScrubProfile selects the whole-pod scrub variant: `standard` (the
	// default) runs the in-guest scrub steps; `vm-restart` boots a fresh
	// guest VM between tenants on a microvm cross-tenant-reuse pool;
	// `in-place` reuses the running guest and leaves guest-kernel residual
	// state. The pool controller rejects `standard` on a pool with
	// `recycle.allowCrossTenantReuse: true`. spec: §5.2 (Kata/microvm
	// scrub variant).
	// +kubebuilder:validation:Enum=standard;vm-restart;in-place
	// +optional
	ScrubProfile string `json:"scrubProfile,omitempty"`

	// AcknowledgeMicrovmResidualState records the deployer's
	// acknowledgment that guest-kernel residual state (DNS cache, TCP
	// TIME_WAIT, page cache, inotify/fanotify registrations) persists
	// across tenants. The pool controller requires it when ScrubProfile is
	// `in-place` and rejects the pool otherwise. spec: §5.2 (Kata/microvm
	// scrub variant).
	// +optional
	AcknowledgeMicrovmResidualState bool `json:"acknowledgeMicrovmResidualState,omitempty"`
}

// SandboxTemplateSpec is the desired state of a SandboxTemplate — the
// §5.2 declaration of one warmable pool. Per §4.6.3 the
// PoolScalingController owns spec.*, reconciling it from the Postgres
// pool definitions that the admin API treats as the source of truth.
type SandboxTemplateSpec struct {
	// RuntimeRef names the lenny.dev/v1alpha1 Runtime this pool warms.
	// +kubebuilder:validation:Required
	RuntimeRef string `json:"runtimeRef"`

	// IsolationProfile is the §5.3 isolation profile every pod in the
	// pool is warmed under.
	// +kubebuilder:validation:Enum=standard;sandboxed;microvm
	// +optional
	IsolationProfile string `json:"isolationProfile,omitempty"`

	// WorkspaceTier is the §12.9 data-classification tier of the runtime
	// this pool serves. The pool-config webhook reads it to enforce the
	// §12.9 line 1043 cross-tenant-reuse prohibition: a `T4` (Restricted)
	// pool may not set `sessionPolicy.recycle.allowCrossTenantReuse: true`, because
	// T4 workspace state must never be reused across tenants regardless of
	// isolation profile. Empty leaves the tier unclassified at the pool
	// level (the cross-tenant rule does not fire). spec: §12.9 line 1043.
	// +kubebuilder:validation:Enum=T1;T2;T3;T4
	// +optional
	WorkspaceTier string `json:"workspaceTier,omitempty"`

	// EgressProfile is the §13.2 egress profile the WarmPoolController
	// stamps onto the pool's pods so the matching pre-created
	// NetworkPolicy takes effect. Empty inherits the §13.2 default
	// (`restricted`). The `internet` profile is rejected on a `standard`
	// (runc) pool at admission per the §13.2 cross-control.
	// +kubebuilder:validation:Enum=restricted;provider-direct;internet
	// +optional
	EgressProfile string `json:"egressProfile,omitempty"`

	// DNSPolicy opts the pool out of the dedicated lenny-system CoreDNS
	// instance. The only accepted value is `cluster-default`: pods in the
	// pool revert to the Kubernetes default ClusterFirst behavior and
	// resolve through kube-system CoreDNS. Empty (the default) routes DNS
	// through the dedicated instance, where the WarmPoolController sets
	// `dnsPolicy: None` plus a `dnsConfig` targeting the dedicated Service.
	// Opting out is permitted only for `standard` (runc) pools and removes
	// the dedicated instance's query logging, rate limiting, and response
	// filtering for that pool. spec: §13.2 lines 470-490.
	// +kubebuilder:validation:Enum=cluster-default
	// +optional
	DNSPolicy string `json:"dnsPolicy,omitempty"`

	// ResourceClass is the §5.2 pod resource class.
	// +kubebuilder:validation:Enum=small;medium;large
	// +optional
	ResourceClass string `json:"resourceClass,omitempty"`

	// ExecutionMode is the §5.2 pod-reuse mode: `session` (a pod
	// parameterized by SessionPolicy) or `service` (a claimless,
	// request-routed pod). Empty is treated as `session`.
	// +kubebuilder:validation:Enum=session;service
	// +optional
	ExecutionMode string `json:"executionMode,omitempty"`

	// MaxConcurrent is the per-pod slot count for service mode.
	// +kubebuilder:validation:Minimum=1
	// +optional
	MaxConcurrent int32 `json:"maxConcurrent,omitempty"`

	// CheckpointGrantWindow is the per-pool override for the number of
	// chunk-upload capabilities the gateway keeps outstanding at once
	// while draining this pool's workspace checkpoints. When unset the
	// gateway falls back to its deployment-wide checkpointGrantWindow
	// default. spec: §10.1 line 131 chunk-grant window; §17.8.1
	// checkpointGrantWindow default 4.
	// +kubebuilder:validation:Minimum=1
	// +optional
	CheckpointGrantWindow *int32 `json:"checkpointGrantWindow,omitempty"`

	// DeliveryMode is the §4.9 credential delivery mode for the pool.
	// +kubebuilder:validation:Enum=proxy;direct
	// +optional
	DeliveryMode string `json:"deliveryMode,omitempty"`

	// SpiffeBinding is the §4.9 proxy-mode lease-token SPIFFE binding.
	// `enabled` (the default for proxy-mode pools) binds each lease
	// token to the issuing pod's SPIFFE identity; `disabled` is
	// permitted only in single-tenant or development deployments and is
	// rejected by the lenny-direct-mode-isolation webhook in
	// multi-tenant mode.
	// +kubebuilder:validation:Enum=enabled;disabled
	// +optional
	SpiffeBinding string `json:"spiffeBinding,omitempty"`

	// MaxSessionAgeSeconds caps the lifetime of a session served by a
	// pod in this pool.
	// +optional
	MaxSessionAgeSeconds int64 `json:"maxSessionAgeSeconds,omitempty"`

	// CheckpointCadenceSeconds is the interval between periodic
	// workspace checkpoints for sessions in this pool.
	// +optional
	CheckpointCadenceSeconds int64 `json:"checkpointCadenceSeconds,omitempty"`

	// MaxTerminationGracePeriodSeconds is a hard ceiling on the
	// terminationGracePeriodSeconds the pool-config webhook computes
	// from the per-slot checkpoint budget. When set, a pool whose
	// computed grace-period floor exceeds this value is rejected
	// rather than warned.
	// +optional
	MaxTerminationGracePeriodSeconds *int64 `json:"maxTerminationGracePeriodSeconds,omitempty"`

	// TerminationGracePeriodSeconds is the §5.2 deployer-set
	// terminationGracePeriodSeconds for this pool's agent pods. The
	// pool-config webhook vets it against the agent-pod grace floor
	// `maxConcurrent × max_tiered_checkpoint_cap + 30`: the per-slot
	// checkpoint cap summed across all slots plus the minimum
	// stream-drain budget, where a session-mode pool uses a multiplier
	// of 1. The floor omits checkpointBarrierAckTimeoutSeconds, which is
	// the gateway's wall-clock wait for CheckpointBarrierAck and belongs
	// to the gateway pod's grace period rather than the agent-pod floor.
	// The webhook evaluates the floor against the effective grace
	// period: this declared value when set, otherwise the §4.6.1 120s
	// agent default. §6.4 additionally requires at least
	// `LENNY_DEMOTE_TIMEOUT_SECONDS + 5s`. When set, it replaces the
	// §4.6.1 120s default base; MaxTerminationGracePeriodSeconds still
	// clamps it down. spec: §5.2, §4.6.1.
	// +optional
	TerminationGracePeriodSeconds *int64 `json:"terminationGracePeriodSeconds,omitempty"`

	// WorkspaceSizeLimitBytes is the §4.4 line 254 / §10.1 line 122 per-pod
	// workspace-size hard limit. The pool-config webhook resolves it to the
	// matching §10.1 tiered-checkpoint-cap (30s for ≤100 MB, 60s for
	// ≤300 MB, 90s for ≤512 MB) when computing the
	// `terminationGracePeriodSeconds` floor; a service-mode pool fans this
	// cap across maxConcurrent slots while a session-mode pool uses a
	// multiplier of 1 (spec/05_runtime-registry-and-pool-model.md §5.2
	// line 516). Unset defaults to the 90s conservative tier per §10.1
	// line 108.
	// +optional
	WorkspaceSizeLimitBytes *int64 `json:"workspaceSizeLimitBytes,omitempty"`

	// CheckpointBarrierAckTimeoutSeconds is the §10.1 wall-clock deadline
	// the gateway waits for `CheckpointBarrierAck` from every coordinated
	// pod during a rolling drain. It sizes the gateway pod's grace period
	// rather than the agent-pod TerminationGracePeriodSeconds floor, which
	// the agent pod's own preStop drain does not incur. The pool-config
	// webhook enforces the independent §10.1 BarrierAck-floor rule
	// (`checkpointBarrierAckTimeoutSeconds ≥ max_tiered_checkpoint_cap`)
	// on this field. Unset defaults to the §10.1 90s. spec: §10.1.
	// +optional
	CheckpointBarrierAckTimeoutSeconds *int64 `json:"checkpointBarrierAckTimeoutSeconds,omitempty"`

	// SessionPolicy parameterizes session-mode pod reuse: simultaneous
	// sessions per pod and the nested recycle block. It is nil on pools
	// that take the default one-session-per-pod configuration.
	// +optional
	SessionPolicy *SessionPolicy `json:"sessionPolicy,omitempty"`

	// AcknowledgeNonceOnlyAuth is the §5.3 deployer opt-in that admits a
	// pool whose runtime sets requireSoPeercred: false into §4.7 nonce-only
	// mode. The PoolScalingController mirrors it from the Postgres pool
	// definition (the §4.6.3 channel for pool configuration to reach the
	// controllers), and the WarmPoolController's render gate requires it
	// before rendering --require-so-peercred=false, so a Runtime CR applied
	// directly cannot put an unacknowledged pool into nonce-only mode.
	// spec: §4.7, §5.3.
	// +optional
	AcknowledgeNonceOnlyAuth bool `json:"acknowledgeNonceOnlyAuth,omitempty"`

	// TopologySpreadConstraints distribute the pool's pods across zones
	// and nodes. The PoolScalingController writes soft per-zone and
	// per-node defaults here; deployers may override them per pool.
	// +optional
	// +listType=atomic
	TopologySpreadConstraints []corev1.TopologySpreadConstraint `json:"topologySpreadConstraints,omitempty"`
}

// SandboxTemplateStatus is the observed state of a SandboxTemplate.
// Per §4.6.3 the WarmPoolController owns status.*: it records pool
// health conditions, including the §5.2 PoolWarmingUp bootstrap
// condition, and the observed generation.
type SandboxTemplateStatus struct {
	// ObservedGeneration is the .metadata.generation the controller
	// last reconciled.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions is the standard Kubernetes condition list. The
	// WarmPoolController sets `PoolWarmingUp` during the §5.2 bootstrap
	// window and may set `SecurityDegradedMode` and `Degraded`.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=sbxt
// +kubebuilder:printcolumn:name="Runtime",type=string,JSONPath=`.spec.runtimeRef`
// +kubebuilder:printcolumn:name="Mode",type=string,JSONPath=`.spec.executionMode`
// +kubebuilder:printcolumn:name="Isolation",type=string,JSONPath=`.spec.isolationProfile`

// SandboxTemplate is the lenny.dev/v1alpha1 declaration of one warmable pool
// (§5.2). The PoolScalingController reconciles it from the Postgres
// pool definitions; the WarmPoolController warms pods against it.
type SandboxTemplate struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SandboxTemplateSpec   `json:"spec,omitempty"`
	Status SandboxTemplateStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// SandboxTemplateList is a list of SandboxTemplate resources.
type SandboxTemplateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SandboxTemplate `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SandboxTemplate{}, &SandboxTemplateList{})
}
