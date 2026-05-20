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
	"errors"
)

// PodID identifies an agent pod. The CRD-backed implementation maps
// this to the Sandbox metadata.name; the Postgres-backed
// implementation maps it to agent_pod_state.pod_id.
type PodID string

// PoolID identifies the warm pool a pod belongs to.
type PoolID string

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
	ActiveSlots     int32
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
// the session id that will run on the pod.
type ClaimOpts struct {
	PoolID    PoolID
	TenantID  string
	SessionID string
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
// the new Sandbox.
type PodSpec struct {
	PoolID           PoolID
	IsolationProfile string
	ExecutionMode    string
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
