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

	// Environment is the §10.6 environment the session was created in,
	// recorded at create time and immutable thereafter. Empty when the
	// session is not scoped to an environment. It bounds the §10.6
	// effective delegation scope for the session's delegations.
	Environment string

	// IsolationProfile is the §5.3 sandbox isolation profile the
	// session is bound to. Used by the §7.1 derive monotonicity check,
	// the §8.3 delegation monotonicity check, and the §15.1 replay
	// monotonicity check. Empty for sessions whose pool has not yet
	// been resolved.
	IsolationProfile isolation.Profile

	// ExecutionMode is the §5.2 pool execution mode the assigned pool
	// resolved to at session creation: "session" (default), "task", or
	// "concurrent". Resolved from the pool's SandboxTemplate at
	// /v1/sessions and frozen for the session lifetime per §7.1 line 75
	// so GET /v1/sessions/{id} returns the same envelope a client
	// received from the create response. Empty when the gateway has not
	// resolved a pool (Postgres-only posture, no PodBinder), in which
	// case the §7.1 sessionIsolationLevel falls back to session-mode.
	ExecutionMode string

	// ScrubPolicy is the §7.1 line 72 scrub-policy string for the
	// session's assigned pool: "" for session-mode pools, or one of
	// "best-effort" / "vm-restart" / "best-effort-in-place" /
	// "best-effort-per-slot" / "none" for the §5.2 reuse modes.
	// Resolved at create time and frozen for the session lifetime per
	// §7.1 line 75. Empty for the session-mode default.
	ScrubPolicy string

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

	// ExperimentContext is the §10.7 experiment enrollment the
	// ExperimentRouter assigned at session creation. Nil when the
	// session is not enrolled in any experiment.
	ExperimentContext *ExperimentContext

	// Cwd is the §4.2 / §4.7 session working directory. Recorded for
	// audit reconstruction; left empty until the runtime adapter
	// materialises the workspace.
	// spec: §4.2 line 156 — "Session records (..., cwd, ...)".
	Cwd string

	// PodAssignment is the §4.2 persistent pod-to-session binding.
	// The gateway's in-memory Registry is a hot cache; this field is
	// the cross-replica source of truth so a fresh replica can resume
	// the binding after a coordinator handoff per §7.2. Empty when the
	// session is not currently bound to a pod (created but not yet
	// started, or already drained).
	// spec: §4.2 line 160 — "Pod-to-session binding".
	PodAssignment string

	// RecoveryGeneration is the §4.2 recovery counter, incremented on
	// each pod recovery. Visible to clients via the session API and the
	// `session.resumed` events. Monotonically non-decreasing — never
	// rolled back, never reset. A mid-resume terminal collapse freezes
	// it at its current value per §7.2 snapshot-close semantics.
	// spec: §4.2 line 156.
	RecoveryGeneration int64

	// CoordinationGeneration is the §4.2 coordinator-handoff counter,
	// incremented when a different gateway replica becomes the
	// authoritative coordinator for this session. Internal-only —
	// used for split-brain fencing. Monotonically non-decreasing.
	// Bumped under a mid-resume terminal write per §7.2 to fence any
	// stale coordinator still attempting resume.
	// spec: §4.2 line 156.
	CoordinationGeneration int64

	// SchemaVersion is the §4.2 row schema version. v1 sessions are
	// written at schema version 1; later migrations may evolve the
	// row layout while leaving the gateway's read path stable.
	// spec: §4.2 line 156.
	SchemaVersion int32

	// RetryCount is the §4.2 line 158 retry counter the Session
	// Manager tracks. Incremented by the coordinator / watchdog on
	// each retry of this logical session (pod recovery, coordinator
	// handoff retry). Monotonically non-decreasing across every
	// transition; the pgstore enforces the floor on Update and the
	// DB CHECK constraint catches the impossible negative.
	// spec: §4.2 line 158 — "Retry counters and policy enforcement".
	RetryCount int64

	// PolicyEnforcementState is the §4.2 line 158 policy-enforcement
	// state payload. Schemaless JSON the Session Manager uses to
	// record per-session policy decisions (delegation bookkeeping,
	// rate-limit decision audit, last circuit-breaker decision) and
	// extend without a migration per field. Nil maps to the empty
	// object `{}` in the row.
	// spec: §4.2 line 158 — "Retry counters and policy enforcement".
	PolicyEnforcementState json.RawMessage

	// ResumeEligibleUntil is the §4.2 line 159 resume-window
	// deadline. A session may be resumed up to this UTC instant,
	// after which the watchdog forces the session to a terminal
	// state. Zero when the session has no resume budget (e.g.,
	// already-terminal sessions, sessions created without a resume
	// window).
	// spec: §4.2 line 159 — "Resume eligibility and window".
	ResumeEligibleUntil time.Time

	// LastSuccessfulCheckpointAt is the wall-clock instant the
	// gateway recorded its most recent successful checkpoint for
	// this session — regardless of trigger (periodic, eviction,
	// pre-drain). Zero when the session has never been checkpointed.
	// The §4.4 freshness gauge / `lenny_checkpoint_stale_sessions`
	// reaper reads this to compute the per-pool staleness count.
	// spec: §4.4 line 258 — "The gateway tracks
	// last_successful_checkpoint_at on the session record in
	// Postgres, updated on every successful checkpoint regardless
	// of trigger (periodic, eviction, pre-drain)".
	LastSuccessfulCheckpointAt time.Time

	// Metadata is the §7.1 line 6 client-supplied
	// CreateSession(..., metadata) payload — a flat string→string map
	// of caller annotations preserved verbatim for the session
	// lifetime. Nil when the caller submitted no payload, in which
	// case the field is omitted from the GET envelope. Non-string
	// values are rejected at the gateway decode boundary so the on-row
	// shape stays bounded. F-7.3.20.
	// spec: §7.1 line 6 — "CreateSession(runtime, pool, retryPolicy,
	// metadata)"; §15.1 GET /v1/sessions/{id} surface.
	Metadata map[string]string

	// RetryPolicy is the §7.3 client-supplied retry policy, clamped at
	// admission against the deployer caps. Nil when the caller did not
	// override the deployer defaults; the watchdog and retry-evaluator
	// paths then fall back to their respective config values. F-7.3.1.
	// spec: §7.3 lines 377-393.
	RetryPolicy *session.RetryPolicy

	// LastSeq is the §7.3 line 397 sessions.last_seq durable counter —
	// the authoritative per-session monotonic SessionEvent.SeqNum value.
	// The gateway advances it atomically with each persisted event so
	// the counter survives coordinator handoff, replica restart, and
	// resume_pending → resuming → running recovery without rewinds.
	// Monotonically non-decreasing; the pgstore enforces the floor on
	// Update via GREATEST and the DB CHECK constraint catches the
	// impossible negative. Coordinator-local Bus counters are advisory
	// caches primed from this column at handoff step 0. F-7.3.3.
	// spec: §7.3 line 397; §10.4 coordinator-handoff replay; §15 SSE.
	LastSeq int64
}

// ExperimentContext is the §10.7 experiment enrollment recorded on a
// session: the experiment and variant the ExperimentRouter assigned,
// and whether the enrollment was inherited from a delegating parent.
type ExperimentContext struct {
	// ExperimentID is the enrolled experiment.
	ExperimentID string
	// VariantID is the assigned variant.
	VariantID string
	// Inherited is true when the context was propagated from a parent
	// session rather than independently assigned.
	Inherited bool
}

// WorkspaceSnapshot describes a stored workspace artifact attached to
// a session. Mirrors the §7.1 derive response fields
// (`workspaceSnapshotSource`, `workspaceSnapshotTimestamp`,
// `workspaceSnapshotContentHash`) plus the underlying object
// reference the gateway uses to materialise the snapshot onto a new
// pod.
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

	// ContentHash is the §4.5 ll. 311 content-addressed identity of
	// the snapshot — SHA-256 of the workspace tar archive, hex-
	// encoded with no `sha256:` prefix. Empty when the snapshot
	// predates the hash column or no archive content is available.
	// The §15.1 derive response surfaces it as
	// `workspaceSnapshotContentHash` so clients can verify the
	// derived session owns the parent bytes.
	//
	// spec: §4.5 line 311 — "Each workspace snapshot is immutable
	// and identified by a content-addressed hash (SHA-256 of the
	// tar archive)".
	ContentHash string

	// Bytes is the §7.3 line 397 / §10.1 coordinator-handoff
	// `last_checkpoint_workspace_bytes` value: the compressed size of
	// the stored workspace archive reported by the adapter at
	// checkpoint time. Zero when the adapter did not report a size
	// (legacy snapshots or the seal-on-never-ran fast path); the §10.1
	// preStop tiered-cap selection treats zero the same as a NULL
	// column and falls back to the conservative 90s tier.
	// spec: §7.3 line 397; §10.1 preStop Stage 2 session enumeration.
	Bytes int64
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

	// GetActiveSlotsByPod returns the number of live (non-terminal)
	// sessions bound to podID across every tenant. It is the §5.2
	// "Post-recovery rehydration atomicity" seed source: after a Redis
	// restart the gateway reads this count to rehydrate the pod's
	// lenny:pod:{pod_id}:active_slots counter before allowing any new
	// slot allocation on the pod. The query is pod-scoped rather than
	// tenant-scoped because the rehydration path holds only the pod
	// identity; §5.2 tenant pinning guarantees the rows it counts all
	// belong to one tenant. A pod with no live sessions returns
	// (0, nil).
	// spec: §5.2 line 521 (post-recovery rehydration; GetActiveSlotsByPod).
	GetActiveSlotsByPod(ctx context.Context, podID string) (int, error)
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
