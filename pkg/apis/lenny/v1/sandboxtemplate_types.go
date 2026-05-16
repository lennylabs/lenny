// SPDX-License-Identifier: MIT

package v1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TaskPolicy configures task-mode pod reuse (§5.2). It is present only
// on pools whose ExecutionMode is `task`. The pool-config validation
// webhook rejects a task-mode pool that omits TaskPolicy or sets
// AcknowledgeBestEffortScrub to false, because the between-task
// workspace scrub is best-effort and is not a tenant isolation
// boundary.
type TaskPolicy struct {
	// AcknowledgeBestEffortScrub records the deployer's acknowledgment
	// that the between-task workspace scrub is best-effort and is not a
	// security boundary between tenants. Task mode is rejected at
	// validation time unless this is true.
	// +kubebuilder:validation:Required
	AcknowledgeBestEffortScrub bool `json:"acknowledgeBestEffortScrub"`

	// AllowCrossTenantReuse permits a task-mode pod to serve more than
	// one tenant. It is valid only when IsolationProfile is `microvm`
	// and the runtime is not workspaceTier T4; the pool-config webhook
	// rejects it on any other pool.
	// +optional
	AllowCrossTenantReuse bool `json:"allowCrossTenantReuse,omitempty"`

	// MicrovmScrubMode selects the cross-tenant scrub variant for
	// microvm pods: `restart` boots a fresh guest VM between tenants,
	// `in-place` reuses the running guest. It is relevant only when
	// AllowCrossTenantReuse is true.
	// +kubebuilder:validation:Enum=restart;in-place
	// +optional
	MicrovmScrubMode string `json:"microvmScrubMode,omitempty"`

	// AcknowledgeMicrovmResidualState records the deployer's
	// acknowledgment that guest-kernel residual state persists across
	// tenants. The pool-config webhook requires it when MicrovmScrubMode
	// is `in-place`.
	// +optional
	AcknowledgeMicrovmResidualState bool `json:"acknowledgeMicrovmResidualState,omitempty"`

	// CleanupCommands are deployer-defined commands run between tasks
	// before the Lenny scrub. They run after the credential file has
	// been purged, so they have no access to the previous task's
	// credentials.
	// +optional
	CleanupCommands []string `json:"cleanupCommands,omitempty"`

	// CleanupTimeoutSeconds bounds the cleanup command execution.
	// +optional
	CleanupTimeoutSeconds int64 `json:"cleanupTimeoutSeconds,omitempty"`

	// OnCleanupFailure selects pod handling when cleanup fails: `warn`
	// returns the pod to the pool with a scrub_warning annotation,
	// `fail` retires and replaces the pod.
	// +kubebuilder:validation:Enum=warn;fail
	// +optional
	OnCleanupFailure string `json:"onCleanupFailure,omitempty"`

	// MaxScrubFailures retires a pod once its cumulative scrub failure
	// count reaches this value. It defaults to 3 when unset.
	// +optional
	MaxScrubFailures *int32 `json:"maxScrubFailures,omitempty"`

	// MaxTasksPerPod retires a pod after this many completed tasks. It
	// is required for task-mode pools; the pool-config webhook rejects
	// a TaskPolicy that omits it, forcing an explicit reuse-limit
	// choice.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	MaxTasksPerPod int32 `json:"maxTasksPerPod"`

	// MaxPodUptimeSeconds retires a pod once its wall-clock uptime
	// exceeds this value. When unset, only MaxTasksPerPod and
	// MaxScrubFailures govern retirement.
	// +optional
	MaxPodUptimeSeconds *int64 `json:"maxPodUptimeSeconds,omitempty"`

	// MaxTaskRetries is the retry count when a pod crashes mid-task. It
	// defaults to 1 (two total attempts) when unset; 0 disables retries.
	// +optional
	MaxTaskRetries *int32 `json:"maxTaskRetries,omitempty"`
}

// ConcurrentWorkspacePolicy configures concurrent-workspace execution
// (§5.2). It is present only on pools whose ExecutionMode is
// `concurrent` and ConcurrencyStyle is `workspace`, where simultaneous
// slots share the pod process namespace.
type ConcurrentWorkspacePolicy struct {
	// AcknowledgeProcessLevelIsolation records the deployer's
	// acknowledgment that concurrent slots share the pod process
	// namespace, /tmp, cgroup memory, network stack, and credential
	// group-read access. Concurrent-workspace mode is rejected at
	// validation time unless this is true.
	// +kubebuilder:validation:Required
	AcknowledgeProcessLevelIsolation bool `json:"acknowledgeProcessLevelIsolation"`

	// CleanupTimeoutSeconds bounds per-slot cleanup. The per-slot
	// budget is CleanupTimeoutSeconds divided by MaxConcurrent; the
	// pool-config webhook rejects a pool where that quotient is below
	// 5 seconds.
	// +optional
	CleanupTimeoutSeconds int64 `json:"cleanupTimeoutSeconds,omitempty"`
}

// SandboxTemplateSpec is the desired state of a SandboxTemplate — the
// §5.2 declaration of one warmable pool. Per §4.6.3 the
// PoolScalingController owns spec.*, reconciling it from the Postgres
// pool definitions that the admin API treats as the source of truth.
type SandboxTemplateSpec struct {
	// RuntimeRef names the lenny.dev/v1 Runtime this pool warms.
	// +kubebuilder:validation:Required
	RuntimeRef string `json:"runtimeRef"`

	// IsolationProfile is the §5.3 isolation profile every pod in the
	// pool is warmed under.
	// +kubebuilder:validation:Enum=standard;sandboxed;microvm
	// +optional
	IsolationProfile string `json:"isolationProfile,omitempty"`

	// ResourceClass is the §5.2 pod resource class.
	// +kubebuilder:validation:Enum=small;medium;large
	// +optional
	ResourceClass string `json:"resourceClass,omitempty"`

	// ExecutionMode is the §5.2 pod-reuse mode. Empty is treated as
	// `session`.
	// +kubebuilder:validation:Enum=session;task;concurrent
	// +optional
	ExecutionMode string `json:"executionMode,omitempty"`

	// ConcurrencyStyle selects the concurrent sub-variant when
	// ExecutionMode is `concurrent`: `stateless` routes through a
	// Service with no workspace materialization, `workspace`
	// materializes a per-slot workspace.
	// +kubebuilder:validation:Enum=stateless;workspace
	// +optional
	ConcurrencyStyle string `json:"concurrencyStyle,omitempty"`

	// MaxConcurrent is the per-pod slot count for concurrent mode.
	// +kubebuilder:validation:Minimum=1
	// +optional
	MaxConcurrent int32 `json:"maxConcurrent,omitempty"`

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

	// TaskPolicy configures task-mode pod reuse. It is required when
	// ExecutionMode is `task`.
	// +optional
	TaskPolicy *TaskPolicy `json:"taskPolicy,omitempty"`

	// ConcurrentWorkspacePolicy configures concurrent-workspace
	// execution. It is required when ExecutionMode is `concurrent` and
	// ConcurrencyStyle is `workspace`.
	// +optional
	ConcurrentWorkspacePolicy *ConcurrentWorkspacePolicy `json:"concurrentWorkspacePolicy,omitempty"`

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

// SandboxTemplate is the lenny.dev/v1 declaration of one warmable pool
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
