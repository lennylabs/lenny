// SPDX-License-Identifier: MIT

// Package sessionstore is the §4.2 SessionStore contract. The gateway
// reads and writes session rows through this interface; production
// uses a Postgres-backed implementation, tests use the in-memory
// implementation from sub-package memstore.
//
// The store is tenant-scoped per §4.2: every Get / Update / List /
// Delete call carries the tenant_id and stores assert that the
// record's tenant_id matches before returning. Cross-tenant reads
// return ErrNotFound — the store never leaks the existence of a
// session in another tenant.
package sessionstore

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// Session is the per-row payload the store persists. Mirrors the
// subset of the §15.1 GET /v1/sessions/{id} envelope the minimal
// gateway populates.
type Session struct {
	ID        string
	TenantID  string
	UserID    string
	State     session.State
	CreatedAt time.Time
	UpdatedAt time.Time

	// FailureClass is populated when State == failed per §7.1; nil
	// otherwise.
	FailureClass session.FailureClass

	// FailureReason is the coded reason string the gateway attaches
	// when the session is transitioned to `failed`. Per §6.2 / §11.3
	// these include `CREATED_TIMEOUT`, `FINALIZE_TIMEOUT`,
	// `READY_TIMEOUT`, `STARTING_TIMEOUT`, plus the §7.1 failure
	// causes. Distinct from FailureClass: the class is a coarse
	// audit bucket, the reason is a specific cause string.
	FailureReason string

	// RuntimeRef identifies the runtime this session targets. Stored
	// at create-time and immutable across the session lifetime.
	RuntimeRef string

	// PoolRef identifies the `SandboxWarmPool` this session was
	// scheduled against. Optional; left blank when the row is created
	// before the gateway has resolved a pool (every primary
	// `POST /v1/sessions` row populates this at admission).
	PoolRef string

	// IsolationProfile is the §5.3 sandbox isolation profile the
	// session is bound to. Used by the §7.1 derive monotonicity check,
	// the §8.3 delegation monotonicity check, and the §15.1 replay
	// monotonicity check. Empty for sessions whose pool has not yet
	// been resolved.
	IsolationProfile isolation.Profile

	// WorkspacePlan is the raw §14 WorkspacePlan JSON submitted with
	// POST /v1/sessions or POST /v1/sessions/start. It is stored at
	// create so the finalize and start handlers can materialize the
	// workspace onto the claimed pod, and GET /v1/sessions/{id} can
	// return the stored plan per §15.1. Nil when the session was
	// created without a workspace plan.
	WorkspacePlan json.RawMessage

	// WorkspaceSnapshot is the §7.1 / §15.1 workspace snapshot ref
	// resolved for this session — nil when no snapshot exists yet
	// (e.g., a `created` session before finalize, or a session whose
	// pod never sealed a workspace). Populated by checkpoint, seal, or
	// derive-copy success paths.
	WorkspaceSnapshot *WorkspaceSnapshot

	// ParentSessionID is the source session a derive copied from, per
	// §7.1 derive copy semantics. Set only on derived sessions.
	ParentSessionID string

	// ParentWorkspaceRef is the §4.5 metadata lineage pointer to the
	// parent session's workspace object. Audit / observability only;
	// not a reference-counted dependency.
	ParentWorkspaceRef string

	// TracingContext is the §8.3 delegation tracingContext: opaque
	// tracing identifiers a runtime registers via
	// lenny/set_tracing_context. The gateway copies a parent's
	// TracingContext onto each child it delegates so native traces
	// stitch into the parent's trace tree. Nil when no context was
	// registered.
	TracingContext map[string]string

	// CascadeOnFailure is the §8.10 policy governing the fate of this
	// session's children when it reaches a terminal state. Empty
	// resolves to the §8.10 default (`cancel_all`).
	CascadeOnFailure session.CascadePolicy

	// RetentionExpiresAt is the §7.1 artifact retention deadline at
	// which the background GC job is eligible to delete this
	// session's workspace, transcript, and logs. Zero when retention
	// has not been set or has been cleared. Updated by
	// `POST /v1/sessions/{id}/extend-retention` per §15.1.
	RetentionExpiresAt time.Time

	// UploadTokenDigest is the SHA-256 digest of the §7.1 uploadToken
	// minted at session creation. The gateway records it so the
	// `finalize` handler can mark the digest consumed in the
	// single-use ConsumedTracker per §7.1. Empty when no upload token
	// was issued for this session (a tested-only path).
	UploadTokenDigest string

	// UploadTokenExpiry is the absolute expiry of the uploadToken
	// minted at session creation. Recorded alongside the digest so
	// the consumed-tracker entry can be GC'd after expiry.
	UploadTokenExpiry time.Time

	// LegalHold is the §12.8 legal-hold flag. When true, the artifact
	// retention GC suspends all rotation for the session and a §12.8
	// GDPR erasure of the session's owner is blocked by the legal-hold
	// preflight. Set and cleared via POST /v1/admin/legal-hold.
	LegalHold bool
}

// WorkspaceSnapshot describes a stored workspace artifact attached to
// a session. Mirrors the §7.1 derive response fields
// (`workspaceSnapshotSource`, `workspaceSnapshotTimestamp`) plus the
// underlying object reference the gateway uses to materialise the
// snapshot onto a new pod.
type WorkspaceSnapshot struct {
	// Ref is the object-store key (§4.5 MinIO path
	// `/{tenant_id}/{object_type}/{session_id}/...`) the snapshot
	// resolves to. Empty when no snapshot exists.
	Ref string

	// Source records how the snapshot was produced: `sealed`,
	// `checkpoint`, or `live`. The §7.1 derive response echoes this
	// value as `workspaceSnapshotSource`.
	Source WorkspaceSnapshotSource

	// Timestamp is the moment the snapshot was committed to object
	// storage. The §7.1 derive response echoes this as
	// `workspaceSnapshotTimestamp`.
	Timestamp time.Time
}

// WorkspaceSnapshotSource is the closed §7.1 enum recording how a
// workspace snapshot was produced.
type WorkspaceSnapshotSource string

const (
	// WorkspaceSnapshotSealed is the post-completion seal artifact.
	WorkspaceSnapshotSealed WorkspaceSnapshotSource = "sealed"
	// WorkspaceSnapshotCheckpoint is the latest periodic checkpoint
	// artifact (the §7.1 `allowStale` derive path resolves to this).
	WorkspaceSnapshotCheckpoint WorkspaceSnapshotSource = "checkpoint"
	// WorkspaceSnapshotLive is the live-pod export path. Only valid
	// when the source pod is currently attached; the derive code path
	// never observes this value because derive runs against
	// at-rest snapshots only.
	WorkspaceSnapshotLive WorkspaceSnapshotSource = "live"
)

// Store is the §4.2 SessionStore interface. Every method is
// goroutine-safe. The context is used for cancellation only; production
// Postgres implementations also use it for tracing propagation.
type Store interface {
	// Create persists a fresh session row. Returns ErrAlreadyExists
	// if a session with the same ID already exists.
	Create(ctx context.Context, s Session) error

	// Get returns the session row whose ID equals id within tenantID.
	// Returns ErrNotFound when no matching row exists (including
	// cross-tenant misses — the store never leaks foreign sessions).
	Get(ctx context.Context, tenantID, id string) (Session, error)

	// Update writes new state to id within tenantID. Returns
	// ErrNotFound when the row is missing. The store does NOT validate
	// the transition — the caller (sessionserver) drives
	// session.Validate first.
	Update(ctx context.Context, tenantID, id string, mutate func(*Session) error) (Session, error)

	// List returns every session for the tenant, in created-at order
	// (newest first). The filter is applied in-process; the store
	// itself does no indexing in v1.
	List(ctx context.Context, tenantID string, filter ListFilter) ([]Session, error)

	// Delete removes the session row entirely. Returns ErrNotFound
	// when the row is missing. The minimal gateway uses this for the
	// audit-row GC path; production gateways soft-delete instead.
	Delete(ctx context.Context, tenantID, id string) error

	// DeleteByUser removes every session owned by userID within
	// tenantID and returns the count deleted. It is the §12.8
	// GDPR-erasure per-store adapter — erasing a user with no sessions
	// is a no-op that returns (0, nil), never ErrNotFound.
	DeleteByUser(ctx context.Context, tenantID, userID string) (int, error)
}

// ListFilter narrows the List result. Empty fields mean "no filter".
type ListFilter struct {
	State        session.State
	RuntimeRef   string
	FailureClass session.FailureClass
}

// Sentinel errors. The sessionserver maps these to the §15.1 error
// envelope: ErrNotFound → 404, ErrAlreadyExists → 409.
var (
	ErrNotFound      = errors.New("sessionstore: session not found")
	ErrAlreadyExists = errors.New("sessionstore: session already exists")
)
