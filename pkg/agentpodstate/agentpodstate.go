// SPDX-License-Identifier: MIT

// Package agentpodstate is the §4.6.1 Postgres-side mirror of Sandbox
// CRD status. The WarmPoolController writes the mirror; the gateway's
// fallback claim path reads it when the Kubernetes API watch stream is
// degraded.
//
// The mirror is a read-optimized copy of the authoritative Sandbox
// status subresource, never a source of truth. The agent_pod_state
// table is platform-global (§12.6): tenant_id is a denormalized
// convenience column set on claim, not an isolation boundary, so the
// table carries no RLS and operations run as plain queries without an
// app.current_tenant context.
package agentpodstate

import (
	"context"
	"errors"
)

// PodState is one agent_pod_state row: the mirrored status of a single
// Sandbox. The field set matches the table created by migration 0001.
type PodState struct {
	// PodID is the Sandbox name, the agent_pod_state primary key.
	PodID string

	// PoolID is the SandboxWarmPool the pod belongs to.
	PoolID string

	// State is the observed §6.2 pod lifecycle phase.
	State string

	// TenantID is the claiming tenant. Empty for an idle or warm pod
	// that carries no session.
	TenantID string

	// SessionID is the claiming session. Empty for an idle or warm pod
	// that carries no session.
	SessionID string

	// IsolationProfile is the §5.3 isolation profile the pod was warmed
	// under.
	IsolationProfile string

	// ExecutionMode is the §5.2 pod-reuse mode.
	ExecutionMode string

	// ResourceVersion is the Sandbox metadata.resourceVersion the
	// mirror row was derived from, parsed to an integer.
	ResourceVersion int64

	// NodeName is the host node the backing pod is scheduled on. Empty
	// until the scheduler binds the pod.
	NodeName string
}

// Store is the §4.6.1 agent_pod_state mirror contract. The mirror is a
// read-optimized copy of Sandbox status; the authoritative store is the
// Sandbox CRD status subresource.
type Store interface {
	// Sync converges the mirror for poolID to the observed set: every
	// row in observed is bulk-UPSERTed keyed on pod_id, and any
	// agent_pod_state row for poolID whose pod_id is absent from
	// observed is deleted. The whole convergence runs in one
	// transaction. Sync is pool-scoped: it never touches rows belonging
	// to another pool.
	Sync(ctx context.Context, poolID string, observed []PodState) error

	// MirrorLagSeconds returns now() - max(updated_at) across the rows
	// for poolID, the staleness of the mirror for that pool. It returns
	// 0 when the pool has no rows.
	MirrorLagSeconds(ctx context.Context, poolID string) (float64, error)

	// ClaimIdle is the §4.6.1 Postgres-backed fallback claim. In one
	// transaction it selects the oldest idle row for poolID with
	// SELECT ... FOR UPDATE SKIP LOCKED, then, if a row is found, marks
	// it claimed for sessionID and tenantID and returns (podState, true,
	// nil). When the pool has no idle row it returns (PodState{}, false,
	// nil). SKIP LOCKED makes two concurrent ClaimIdle calls claim
	// distinct pods: the second call skips the row the first holds
	// locked. The mirror is a read-optimized copy, so a successful claim
	// here is provisional until the caller flips the authoritative
	// Sandbox CRD phase and creates the binding SandboxClaim.
	ClaimIdle(ctx context.Context, poolID, sessionID, tenantID string) (PodState, bool, error)
}

// ErrEmptyPoolID is returned by Sync, MirrorLagSeconds, and ClaimIdle
// when poolID is empty. A pool-scoped operation with no pool key would
// delete unrelated rows, report meaningless lag, or claim a pod from an
// unintended pool.
var ErrEmptyPoolID = errors.New("agentpodstate: poolID is required")
