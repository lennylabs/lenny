// SPDX-License-Identifier: MIT

package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ScheduleWindow is a time-of-day override of the pool's warm count
// (§4.6.1). Within the window the pool targets MinWarm idle pods
// instead of the demand-derived target.
type ScheduleWindow struct {
	// Start is the window open time as a 24-hour `HH:MM` clock value.
	// +kubebuilder:validation:Pattern=`^([01][0-9]|2[0-3]):[0-5][0-9]$`
	// +kubebuilder:validation:Required
	Start string `json:"start"`

	// End is the window close time as a 24-hour `HH:MM` clock value.
	// +kubebuilder:validation:Pattern=`^([01][0-9]|2[0-3]):[0-5][0-9]$`
	// +kubebuilder:validation:Required
	End string `json:"end"`

	// MinWarm is the warm-count target enforced inside the window.
	// +kubebuilder:validation:Minimum=0
	MinWarm int32 `json:"minWarm"`
}

// ScalePolicy carries the §4.6.2 scaling-formula tuning inputs and the
// optional time-of-day schedule overrides. Per §4.6.3 the
// PoolScalingController owns it; the WarmPoolController reads it when
// sizing the pool.
type ScalePolicy struct {
	// PodWarmupSecondsBaseline is the pod creation-to-ready time the
	// §4.6.2 scaling formula uses. It is roughly 30 seconds for
	// SDK-warm pools and roughly 10 seconds for pod-warm pools.
	// +optional
	PodWarmupSecondsBaseline int64 `json:"podWarmupSecondsBaseline,omitempty"`

	// SDKWarmCircuitBreakerMinOpenSeconds is how long the SDK-warm
	// circuit breaker stays open once tripped before the
	// PoolScalingController re-evaluates the demotion rate. It defaults
	// to 1800 seconds when unset; 0 disables the grace window.
	// +optional
	SDKWarmCircuitBreakerMinOpenSeconds *int64 `json:"sdkWarmCircuitBreakerMinOpenSeconds,omitempty"`

	// BootstrapMinWarm is the static warm-count target the pool uses
	// while in bootstrap mode, before the scaling formula has enough
	// observed demand to converge.
	// +kubebuilder:validation:Minimum=0
	// +optional
	BootstrapMinWarm int32 `json:"bootstrapMinWarm,omitempty"`

	// Schedules are time-of-day windows that override the
	// demand-derived warm count.
	// +optional
	// +listType=atomic
	Schedules []ScheduleWindow `json:"schedules,omitempty"`

	// ScaleToZero drives the pool to `minWarm: 0` during a recurring
	// off-hours window (§4.6.1). The PoolScalingController evaluates the
	// cron window on every pass and overrides the demand-derived warm
	// floor to zero while the window is open. Sessions arriving in a
	// zero-warm period incur cold-start latency. When unset the pool
	// never scales to zero on a schedule.
	// +optional
	ScaleToZero *ScaleToZeroPolicy `json:"scaleToZero,omitempty"`
}

// ScaleToZeroPolicy is the §4.6.1 off-hours scale-to-zero window. The
// window opens when the Schedule cron fires and closes when the
// ResumeAt cron fires; while open the PoolScalingController sets the
// pool's `minWarm` to 0. Both cron expressions are interpreted in
// Timezone, defaulting to UTC.
type ScaleToZeroPolicy struct {
	// Schedule is the standard five-field cron expression whose firing
	// opens the zero-warm window (for example `0 22 * * *` for 22:00).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Schedule string `json:"schedule"`

	// ResumeAt is the standard five-field cron expression whose firing
	// closes the zero-warm window and restores the demand-derived warm
	// floor (for example `0 6 * * *` for 06:00).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	ResumeAt string `json:"resumeAt"`

	// Timezone is an optional IANA timezone string (for example
	// `America/New_York`) the two cron expressions are interpreted in.
	// Both Schedule and ResumeAt are interpreted as UTC when it is unset.
	// +optional
	Timezone string `json:"timezone,omitempty"`
}

// SDKWarmCircuitBreakerStatus persists the SDK-warm circuit-breaker
// open/closed decision (§6.1). It is the §4.6.3 ownership carve-out:
// the PoolScalingController owns these fields within the otherwise
// WarmPoolController-owned status, so the breaker decision survives a
// PoolScalingController leader failover.
type SDKWarmCircuitBreakerStatus struct {
	// OpenedAt is when the PoolScalingController tripped the breaker.
	// It is cleared when the breaker closes.
	// +optional
	OpenedAt *metav1.Time `json:"openedAt,omitempty"`

	// OpenedReason is a machine-readable trip code, for example
	// `demotion_rate_exceeded` or `operator_manual`.
	// +optional
	OpenedReason string `json:"openedReason,omitempty"`

	// MinOpenUntil is the earliest time the breaker may auto-close. It
	// is OpenedAt plus the configured minimum open duration; the
	// breaker is held open until this time even across a
	// PoolScalingController failover.
	// +optional
	MinOpenUntil *metav1.Time `json:"minOpenUntil,omitempty"`
}

// SandboxWarmPoolSpec is the desired state of a SandboxWarmPool — the
// §5.2 warm-count management for one pool. Per §4.6.3 the
// PoolScalingController owns spec.*, deriving the scaling parameters
// from the Postgres pool definitions.
type SandboxWarmPoolSpec struct {
	// TemplateRef names the SandboxTemplate whose pods this warm pool
	// manages.
	// +kubebuilder:validation:Required
	TemplateRef string `json:"templateRef"`

	// MinWarm is the floor on idle ready pods the WarmPoolController
	// maintains. A value of 0 lets the pool scale to zero with a
	// documented cold-start penalty.
	// +kubebuilder:validation:Minimum=0
	MinWarm int32 `json:"minWarm"`

	// MaxWarm is the ceiling on idle pods the pool warms ahead of
	// demand.
	// +kubebuilder:validation:Minimum=0
	MaxWarm int32 `json:"maxWarm"`

	// ScalePolicy carries the scaling-formula tuning inputs and the
	// time-of-day schedule overrides.
	// +optional
	ScalePolicy *ScalePolicy `json:"scalePolicy,omitempty"`

	// SDKWarmDisabled is the SDK-warm circuit-breaker flag. The
	// PoolScalingController sets it true to stop warming pods to
	// SDK-warm state when the demotion rate stays too high.
	// +optional
	SDKWarmDisabled bool `json:"sdkWarmDisabled,omitempty"`
}

// SandboxWarmPoolStatus is the observed state of a SandboxWarmPool.
// Per §4.6.3 the WarmPoolController owns status.* except the
// SDKWarmCircuitBreaker carve-out, which the PoolScalingController
// owns.
type SandboxWarmPoolStatus struct {
	// WarmCount is the current number of idle pods held in the pool.
	// +optional
	WarmCount int32 `json:"warmCount,omitempty"`

	// ReadyCount is the number of idle pods that have reached a
	// claimable ready state.
	// +optional
	ReadyCount int32 `json:"readyCount,omitempty"`

	// SDKWarmCircuitBreaker persists the SDK-warm circuit-breaker
	// state. Per §4.6.3 it is owned by the PoolScalingController, not
	// the WarmPoolController.
	// +optional
	SDKWarmCircuitBreaker *SDKWarmCircuitBreakerStatus `json:"sdkWarmCircuitBreaker,omitempty"`

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
// +kubebuilder:resource:shortName=sbxwp
// +kubebuilder:printcolumn:name="Template",type=string,JSONPath=`.spec.templateRef`
// +kubebuilder:printcolumn:name="MinWarm",type=integer,JSONPath=`.spec.minWarm`
// +kubebuilder:printcolumn:name="MaxWarm",type=integer,JSONPath=`.spec.maxWarm`
// +kubebuilder:printcolumn:name="Warm",type=integer,JSONPath=`.status.warmCount`
// +kubebuilder:printcolumn:name="Ready",type=integer,JSONPath=`.status.readyCount`

// SandboxWarmPool is the lenny.dev/v1 record of warm-count management
// for one pool (§5.2). The PoolScalingController owns the scaling
// parameters; the WarmPoolController reconciles warm pods toward them.
type SandboxWarmPool struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SandboxWarmPoolSpec   `json:"spec,omitempty"`
	Status SandboxWarmPoolStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// SandboxWarmPoolList is a list of SandboxWarmPool resources.
type SandboxWarmPoolList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SandboxWarmPool `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SandboxWarmPool{}, &SandboxWarmPoolList{})
}
