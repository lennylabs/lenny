// SPDX-License-Identifier: MIT

// Package podregistry is the §12.6 PodRegistry data-access layer
// for agent pods. The §4.6.1 PodLifecycleManager and PoolManager
// delegate their state reads and writes here; the v1 implementation
// (CRDPodRegistry, in this package) reads and writes Sandbox CRD
// status via the Kubernetes API. Tier-4 deploys may swap in
// PostgresPodRegistry against the agent_pod_state table without
// restructuring the §4.6.1 business logic.
package podregistry

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/lennylabs/lenny/pkg/platform/store"
)

// The shared ID types live in pkg/platform/store (§12.6 shared type
// definitions block). They are aliased here so the §4.6.1 callers keep
// the podregistry.PodID / PoolID / ClusterID spelling while there is a
// single canonical definition shared with storerouter and eventbus.
type (
	// PodID identifies an agent pod. The CRD-backed implementation maps
	// this to the Sandbox metadata.name; the Postgres-backed
	// implementation maps it to agent_pod_state.pod_id.
	PodID = store.PodID

	// PoolID identifies the warm pool a pod belongs to.
	PoolID = store.PoolID

	// ClusterID identifies a Kubernetes cluster in a multi-cluster
	// topology. spec: §12.6 line 373. It is always nil on ClaimOpts in
	// v1 (single cluster); the §12.6 ClusterRegistry populates it when
	// routing a claim to a remote cluster.
	ClusterID = store.ClusterID
)

// PodRecord is the §12.6 view of one agent pod's authoritative
// state. The fields mirror the §6.2 Sandbox.status subresource and
// the agent_pod_state Postgres table.
type PodRecord struct {
	PodID            PodID
	PoolID           PoolID
	State            string
	TenantID         string
	SessionID        string
	IsolationProfile string
	ExecutionMode    string
	// ResourceVersion is the CRD generation used for optimistic
	// locking on UpdatePodState. The §4.6.1 CAS loop carries the
	// last-observed value forward and retries on a mismatch.
	ResourceVersion string
	NodeName        string
	PodIP           string
	PodName         string
}

// StateTransition is the §6.2 state-machine edge a write requests.
// The CRD-backed implementation validates From against the current
// Sandbox.status.phase before writing To.
type StateTransition struct {
	From string
	To   string
}

// ClaimOpts describes a §4.6.1 ClaimPod request: the pool to claim
// from, the tenant to pin the pod to (concurrent / task modes), and
// the session id that will run on the pod. spec: §12.6 line 424.
type ClaimOpts struct {
	PoolID    PoolID
	TenantID  string
	SessionID string

	// RequiresDemotion asks for an SDK-warm pod to be demoted to
	// pod-warm before the claim is satisfied (§6 SDK-warm → pod-warm
	// demotion, which feeds lenny_warmpool_sdk_demotions_total). v1
	// callers leave it false.
	RequiresDemotion bool

	// Priority is the admission-control claim priority. nil means
	// unset, the §12.6 line 424 default; a future priority-aware
	// admission path branches on a non-nil value.
	Priority *int32

	// ClusterID selects a remote cluster for the claim. It is always
	// nil in v1 (single cluster); the §12.6 LocalClusterRegistry
	// propagates a non-nil value in a multi-cluster topology.
	ClusterID *ClusterID
}

// ReleaseReason explains why a session returned a pod to its pool.
// The §4.6.1 manager records it on the released pod's status for
// observability.
type ReleaseReason string

const (
	ReleaseCompleted ReleaseReason = "completed"
	ReleaseFailed    ReleaseReason = "failed"
	ReleaseCancelled ReleaseReason = "cancelled"
)

// PodFilter narrows ListPodsByPool. An empty filter returns every
// pod in the pool.
type PodFilter struct {
	// State, when non-empty, restricts results to pods whose phase
	// matches.
	State string
}

// StateCounts is the §6.2 pod-state histogram CountByState returns
// for the §4.6.2 PoolScalingController.
type StateCounts map[string]int

// PodSpec is the input to CreatePod: the pool the new pod belongs
// to and the per-pod fields the §4.6.1 lifecycle manager sets on
// the new Sandbox. spec: §12.6 line 422 — the key fields are
// RuntimeDefinitionRef, WorkspacePlan, IsolationProfile, ExecutionMode,
// and resource limits (modeled here as ResourceClass, per §5.1/§5.2).
type PodSpec struct {
	PoolID PoolID

	// RuntimeDefinitionRef names the lenny.dev/v1alpha1 Runtime the new pod
	// runs. CreatePod stamps it onto Sandbox.spec.runtimeRef, which is a
	// required field — a pod created without it fails CRD validation.
	RuntimeDefinitionRef string

	IsolationProfile string
	ExecutionMode    string

	// ResourceClass is the §5.1/§5.2 resource class the pod's CPU/memory
	// limits resolve from (the §12.6 "resource limits" PodSpec field,
	// modeled as a named class in this codebase). Empty selects the
	// pool/runtime default.
	ResourceClass string

	// WorkspacePlan is the serialized §14 WorkspacePlan the pod is
	// created for (the §12.6 PodSpec WorkspacePlan field). It is empty
	// for a warm pod, whose workspace is materialized at session claim;
	// the Tier-4+ gateway-creates-a-pod path passes the resolved plan
	// here so CreatePod records it on the Sandbox. The registry treats
	// the bytes as opaque.
	WorkspacePlan json.RawMessage
}

// PodEvent is one frame of the WatchPods stream.
type PodEvent struct {
	PodID     PodID
	EventType string
	PodRecord PodRecord
}

// EventType values for PodEvent.EventType.
const (
	EventCreated = "created"
	EventUpdated = "updated"
	EventDeleted = "deleted"
	// EventResync is the §12.6 line 482 synthetic backpressure signal:
	// when WatchPods detects the channel has fallen behind, it emits a
	// resync frame carrying no PodRecord so the consumer re-reads its
	// authoritative state via ListPodsByPool.
	EventResync = "resync"
)

// Sentinel errors. The §4.6.1 CAS loop branches on ErrResourceConflict;
// the §4.6.1 lifecycle manager surfaces ErrNotFound as
// PodNotRegistered.
var (
	ErrNotFound          = errors.New("podregistry: pod not found")
	ErrInvalidTransition = errors.New("podregistry: invalid state transition")
	ErrPoolExhausted     = errors.New("podregistry: no idle pod available in pool")
	ErrResourceConflict  = errors.New("podregistry: resource version conflict")
)

// PodRegistry is the §12.6 data-access layer. The v1 implementation
// (CRDPodRegistry) reads and writes Sandbox CRD status; the Tier-4
// PostgresPodRegistry replaces CRD status writes with
// agent_pod_state. The §4.6.1 business interfaces
// (PodLifecycleManager, PoolManager) delegate their state primitives
// here.
type PodRegistry interface {
	GetPod(ctx context.Context, podID PodID) (*PodRecord, error)
	UpdatePodState(ctx context.Context, podID PodID, transition StateTransition) error
	ClaimPod(ctx context.Context, opts ClaimOpts) (*PodRecord, error)
	ReleasePod(ctx context.Context, podID PodID, reason ReleaseReason) error
	ListPodsByPool(ctx context.Context, poolID PoolID, filter PodFilter) ([]PodRecord, error)
	CountByState(ctx context.Context, poolID PoolID) (StateCounts, error)
	CreatePod(ctx context.Context, poolID PoolID, spec PodSpec) (*PodRecord, error)
	DeletePod(ctx context.Context, podID PodID) error
	WatchPods(ctx context.Context, poolID PoolID) (<-chan PodEvent, error)
}
