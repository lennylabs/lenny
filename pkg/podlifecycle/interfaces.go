// SPDX-License-Identifier: MIT

// Package podlifecycle defines the §4.6.1 forward-compatibility
// indirection between Lenny's components and the agent-sandbox CRDs.
//
// spec: spec/04_system-components.md lines 333-363 — "All Lenny
// components interact with pod lifecycle through two interfaces, never
// directly with agent-sandbox CRD types. Both embed a shared read-only
// PoolReader."
//
// The package carries the interface definitions and the default
// AgentSandbox* implementations the v1 gateway and controllers use.
// The indirection is what makes the §4.6 fallback plan (replacing
// agent-sandbox with custom kubebuilder controllers) tractable: a
// breaking upstream change or an alternative backend swaps the
// implementations behind these interfaces, leaving every consumer
// untouched.
//
// The interface method signatures are taken verbatim from §4.6.1; the
// types below carry the spec-named fields with no extras.
package podlifecycle

import (
	"context"
	"time"
)

// PoolReader is the read-only pool view both PodLifecycleManager and
// PoolManager embed. spec: spec/04_system-components.md lines 335-338.
type PoolReader interface {
	// ListPools reports the per-pool health and capacity for every
	// registered pool.
	ListPools(ctx context.Context) ([]PoolStatus, error)
	// GetPoolStatus reports a single pool's status. It returns
	// ErrPoolNotFound when poolName has no SandboxTemplate.
	GetPoolStatus(ctx context.Context, poolName string) (PoolStatus, error)
}

// PodLifecycleManager is the gateway-facing pod-lifecycle surface.
// spec: spec/04_system-components.md lines 340-345.
type PodLifecycleManager interface {
	PoolReader
	// ClaimPod acquires an idle pod from poolName for sessionID,
	// returning the claimed pod's handle. opts.RequiresDemotion = true
	// signals that an SDK-warm pod's adapter must be demoted before
	// the runtime is used (the §6.1 workspace plan includes
	// sdkWarmBlockingPaths). opts.Priority is the §5.2 scheduling
	// priority class hint; opts.ClusterID is reserved for the
	// multi-cluster v2 path.
	ClaimPod(ctx context.Context, poolName, sessionID string, opts ClaimOpts) (PodHandle, error)
	// ReleasePod releases a pod after its session ends. Concurrent
	// failures (e.g., a pod already released) are not surfaced as
	// errors — release is idempotent.
	ReleasePod(ctx context.Context, handle PodHandle) error
	// DrainPod gracefully terminates the pod. When checkpointFirst is
	// true the drain quiesces in-flight work and runs the §7.1
	// seal-and-export sequence before tearing the pod down.
	DrainPod(ctx context.Context, handle PodHandle, checkpointFirst bool) (DrainResult, error)
	// GetPodStatus reads the pod's authoritative state machine (§6.2),
	// health, and certificate expiry.
	GetPodStatus(ctx context.Context, handle PodHandle) (PodStatus, error)
}

// PoolManager is the controller-facing pool-management surface.
// spec: spec/04_system-components.md lines 347-357.
type PoolManager interface {
	PoolReader
	// ReconcilePool ensures the pool's runtime CRDs (SandboxTemplate +
	// SandboxWarmPool) match the desired configuration.
	ReconcilePool(ctx context.Context, poolConfig PoolConfig) error
	// ApplyPoolDefinition is the CRUD surface for pool definitions. It
	// creates, updates, or deletes the pool's SandboxTemplate and
	// SandboxWarmPool CRDs. A poolDef with Deleted = true tears the
	// pool down.
	ApplyPoolDefinition(ctx context.Context, poolDef PoolDefinition) error
	// ReplacePod proactively replaces a pod (cert-expiry refresh,
	// health-failure recovery). reason is recorded in the §16.5
	// replacement metric.
	ReplacePod(ctx context.Context, handle PodHandle, reason string) error
	// TransitionPodState writes a §6.2 phase transition on the pod's
	// Sandbox status. It rejects transitions the state machine does
	// not allow (TransitionRejectedError).
	TransitionPodState(ctx context.Context, handle PodHandle, from, to PodState) error
	// GarbageCollect runs the §4.6.1 orphan-detection sweep: orphaned
	// Sandboxes (no matching pool) and orphaned SandboxClaims (no
	// matching active session).
	GarbageCollect(ctx context.Context) ([]OrphanResult, error)
	// ManageFinalizer adds or removes the §4.6.1 lenny.dev/session-
	// cleanup finalizer on the pod's Sandbox.
	ManageFinalizer(ctx context.Context, handle PodHandle, action FinalizerAction) error
	// ManagePDB creates, updates, or deletes the pool's
	// PodDisruptionBudget per config.
	ManagePDB(ctx context.Context, poolName string, config PDBConfig) error
	// DrainPool drains every pod in poolName. When checkpointFirst is
	// true each drain runs the §7.1 seal-and-export sequence.
	DrainPool(ctx context.Context, poolName string, checkpointFirst bool) error
	// SetPoolCondition writes a status condition on the pool's
	// SandboxTemplate (e.g., Degraded when the RuntimeClass is
	// missing). reason is the condition's free-form reason field.
	SetPoolCondition(ctx context.Context, poolName string, condition PoolCondition, reason string) error
}

// ClaimOpts carries the per-claim parameters from §4.6.1 line 342.
type ClaimOpts struct {
	// RequiresDemotion signals that an SDK-warm pod must be demoted
	// before use (§6.1 sdkWarmBlockingPaths).
	RequiresDemotion bool
	// Priority is the §5.2 scheduling priority class hint. An empty
	// string defers to the pool's default.
	Priority string
	// ClusterID is the reserved multi-cluster v2 field. Empty for v1.
	ClusterID string
}

// PodHandle is the opaque pod reference returned by ClaimPod and
// carried through Release/Drain/Get/Replace. The fields are the §4.6.1
// "PodHandle carries metadata: warmMode, certExpiresAt, adapterEndpoint"
// surface plus the namespace+name lookup keys the implementations need.
type PodHandle struct {
	// SandboxName names the Sandbox CRD claimed for the session.
	SandboxName string
	// Namespace is the agent namespace the Sandbox lives in.
	Namespace string
	// SessionID is the session the pod is claimed for.
	SessionID string
	// PoolName names the pool the pod was claimed from.
	PoolName string
	// WarmMode is the §6.1 warm mode of the claimed pod
	// (pod_warm | sdk_warm).
	WarmMode WarmMode
	// CertExpiresAt is the §13.x adapter certificate expiry the
	// gateway uses to time proactive cert refreshes.
	CertExpiresAt time.Time
	// AdapterEndpoint is the host:port the gateway dials to reach the
	// pod's §4.7 adapter.
	AdapterEndpoint string
}

// PodStatus is the read-only pod-status response returned by
// GetPodStatus. spec: spec/04_system-components.md line 345.
type PodStatus struct {
	// Phase is the §6.2 phase from Sandbox.status.phase.
	Phase PodState
	// PodIP is the cluster IP of the backing Pod.
	PodIP string
	// PodName is the underlying Pod backing the Sandbox.
	PodName string
	// NodeName is the host node the pod is scheduled on.
	NodeName string
	// CertExpiresAt is the adapter cert expiry on the Sandbox.
	CertExpiresAt time.Time
	// TenantID is the §5.2 tenant pinning, empty for an unassigned
	// pod. The per-pod slot count lives in the Redis counter (§5.2),
	// no longer on Sandbox.status, so it is not surfaced here.
	TenantID string
}

// PoolStatus is the read-only pool view returned by ListPools and
// GetPoolStatus. spec: spec/04_system-components.md line 337.
type PoolStatus struct {
	// Name is the pool name.
	Name string
	// Namespace is the agent namespace the pool's SandboxTemplate
	// lives in.
	Namespace string
	// MinWarm is the spec'd minimum warm-pod count.
	MinWarm int32
	// MaxWarm is the spec'd maximum warm-pod count.
	MaxWarm int32
	// WarmCount is the observed ready warm-pod count.
	WarmCount int32
	// IdleCount is the observed idle pod count (warm, unclaimed).
	IdleCount int32
	// ClaimedCount is the observed claimed pod count.
	ClaimedCount int32
	// IsolationProfile is the §5.3 sandbox isolation profile pods are
	// warmed under.
	IsolationProfile string
	// Conditions are the pool's §4.6.1 status conditions
	// (PoolWarmingUp, Degraded, …).
	Conditions []PoolCondition
}

// DrainResult is the response from DrainPod. spec:
// spec/04_system-components.md line 344 — "DrainResult includes retry
// state for seal-and-export hold semantics."
type DrainResult struct {
	// SealRetried reports whether the §7.1 seal-and-export retry loop
	// ran for a checkpointFirst drain.
	SealRetried bool
	// SealExhausted reports whether the seal-and-export retry budget
	// was exhausted (the pod was torn down anyway, per §7.1).
	SealExhausted bool
	// TornDown reports whether the underlying pod was actually
	// deleted. A drain of an already-deleted Sandbox is a no-op.
	TornDown bool
}

// PoolConfig is the desired-state input to ReconcilePool. spec:
// spec/04_system-components.md line 349.
type PoolConfig struct {
	Name             string
	Namespace        string
	RuntimeRef       string
	MinWarm          int32
	MaxWarm          int32
	IsolationProfile string
	ResourceClass    string
	Image            string
}

// PoolDefinition is the CRUD input to ApplyPoolDefinition. spec:
// spec/04_system-components.md line 350.
type PoolDefinition struct {
	// Spec carries the pool's desired configuration when Deleted is
	// false. When Deleted is true Spec is ignored.
	Spec PoolConfig
	// Deleted = true tears the pool down (the implementation deletes
	// the SandboxTemplate + SandboxWarmPool CRDs).
	Deleted bool
}

// OrphanResult is one row of the GarbageCollect sweep. spec:
// spec/04_system-components.md line 353.
type OrphanResult struct {
	// Kind names the orphan resource: "Sandbox" or "SandboxClaim".
	Kind string
	// Namespace + Name identify the orphan.
	Namespace string
	Name      string
	// Reason explains why the resource was considered orphaned.
	Reason string
	// Action records what GarbageCollect did with the orphan.
	Action OrphanAction
}

// OrphanAction is the disposition the GC chose for an orphan.
type OrphanAction string

const (
	OrphanActionDeleted  OrphanAction = "deleted"
	OrphanActionRetained OrphanAction = "retained"
)

// PoolCondition is one §4.6.1 status condition on a pool.
type PoolCondition struct {
	Type    PoolConditionType
	Status  ConditionStatus
	Reason  string
	Message string
}

// PoolConditionType enumerates the §4.6.1 pool condition types. The
// spec writes these as kebab-case PoolWarmingUp, Degraded, etc.
type PoolConditionType string

const (
	PoolConditionWarmingUp PoolConditionType = "PoolWarmingUp"
	PoolConditionDegraded  PoolConditionType = "Degraded"
	PoolConditionDraining  PoolConditionType = "Draining"
	PoolConditionReady     PoolConditionType = "Ready"
)

// ConditionStatus is the standard Kubernetes condition status.
type ConditionStatus string

const (
	ConditionTrue    ConditionStatus = "True"
	ConditionFalse   ConditionStatus = "False"
	ConditionUnknown ConditionStatus = "Unknown"
)

// WarmMode is the §6.1 warm mode reported on PodHandle.
type WarmMode string

const (
	WarmModePodWarm WarmMode = "pod_warm"
	WarmModeSDKWarm WarmMode = "sdk_warm"
)

// PodState mirrors the §6.2 coarse pod-occupancy phase written into
// Sandbox.status.phase. The named constants here are the only values the
// WarmPoolController projects onto the CRD; the fine session-lifecycle
// states live in the Postgres session model and are not mirrored here
// (spec: §6.2, §6.37). This block stays in lockstep with
// pkg/sandbox/state.All().
// spec: spec/06_warm-pod-model.md §6.2; spec/04_system-components.md
// lines 340-358 (the read/write surface that consumes them).
type PodState string

const (
	PodStateWarming       PodState = "warming"
	PodStateSDKConnecting PodState = "sdk_connecting"
	PodStateIdle          PodState = "idle"
	// PodStateReserved is the §6.2 coarse occupancy phase a recycled pod
	// projects while its claim is held for the pinned tenant through the
	// hold window. It is excluded from idle inventory.
	PodStateReserved   PodState = "reserved"
	PodStateClaimed    PodState = "claimed"
	PodStateFailed     PodState = "failed"
	PodStateDraining   PodState = "draining"
	PodStateTerminated PodState = "terminated"
)

// FinalizerAction is the action ManageFinalizer takes on the
// lenny.dev/session-cleanup finalizer. spec:
// spec/04_system-components.md line 354.
type FinalizerAction string

const (
	FinalizerAdd    FinalizerAction = "Add"
	FinalizerRemove FinalizerAction = "Remove"
)

// PDBConfig is the desired PodDisruptionBudget for a pool. spec:
// spec/04_system-components.md line 355.
type PDBConfig struct {
	// MinAvailable, when non-nil, fixes the PDB's minAvailable.
	MinAvailable *int32
	// MaxUnavailable, when non-nil, fixes the PDB's maxUnavailable.
	MaxUnavailable *int32
	// Deleted = true removes the pool's PDB.
	Deleted bool
}
