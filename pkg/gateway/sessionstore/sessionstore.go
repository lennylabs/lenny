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

	// RootSessionID is the §8.9 / §12.5 delegation-tree root: every
	// session in a delegation tree carries the same RootSessionID,
	// equal to the session at the tree's apex. A standalone session
	// (one without a parent) is its own root. Children inherit their
	// parent's RootSessionID, not the parent's id, so the column
	// identifies the entire tree by a single value. The §12.5
	// `idx_sessions_root` index supports single-shard tree-scoped
	// queries (`WHERE root_session_id = $1`) so a tree-walker reads
	// O(tree size) rather than O(tenant size) rows. Empty on the read
	// path collapses to the row's own ID. spec: §8.9 line 1010; §12.5
	// line 101. F-8.9.7 / F-8.9.8.
	RootSessionID string

	// DelegationDepth is the session's depth in its delegation tree: 0
	// for a root (standalone) session, parent.DelegationDepth+1 for a
	// delegated child. The §8.2 delegation Service stamps it on every
	// child it admits; the value is invariant once the row is created.
	// The §10.7 built-in eval endpoint copies it onto each EvalResult so
	// the Results API `?delegation_depth=` filter and
	// `?breakdown_by=delegation_depth` operate on truthful data. spec:
	// §10.7 lines 868, 905. F-10.7.5.
	DelegationDepth uint32

	// DelegationLease is the §8.2 LeaseSlice granted to this session at
	// delegation admission: the per-subtree resource ceiling the §8.2
	// `lease_slice` parameter carried (maxTokenBudget, maxChildrenTotal,
	// maxTreeSize, maxParallelChildren, perChildMaxAge). The §8.2
	// delegation Service validates a child's requested slice against this
	// granted slice (a child can only tighten, never widen, the parent's
	// budget) and stamps the child's resolved slice here so the child's
	// own descendants are bound in turn. Nil for a root/standalone
	// session and for any child whose lease declared no slice — both mean
	// "no explicit budget binding at this scope" and ValidateChildSlice
	// admits any child against a zero parent axis. The value is invariant
	// once the row is created. spec: §8.2 lines 38-48, 127. F-8.2.2.
	DelegationLease *DelegationLease

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

	// WorkspaceRoot is the §7.3 line 408 absolute cwd path the original
	// bind's adapter reported on the §15.5 version handshake. The
	// gateway captures it once on the first non-empty Bind and never
	// rewrites it — a subsequent Resume passes the recorded value to
	// the replacement pod's adapter for the "same absolute cwd path"
	// assertion, so a SandboxTemplate change between the original and
	// replacement pods cannot silently restore into the wrong path.
	// Empty when the session never reached a §15.5-capable adapter (a
	// pre-bind session row or an older adapter that does not report
	// the field), in which case the assertion is skipped.
	// spec: §7.3 line 408 step (d). F-7.3.15.
	WorkspaceRoot string

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

	// SetupOutput carries the §7.5 line 475 captured stdout/stderr/exit
	// for each setup command the adapter ran (success or failure), plus
	// the §7.5 line 488 synthetic rejection-reason entries the gateway
	// records when it rejects a command at admission. Nil when the
	// session ran no setup commands. F-7.5.4 / F-7.5.11.
	// spec: §7.5 lines 475, 488.
	SetupOutput []SetupCommandOutput

	// TreeVisibility is the §8.5 / §8.3 delegation-lease visibility
	// boundary realised on the session row (the §4.2 line 161 design
	// clarification: in v1 the delegation lease is the child session
	// row). It scopes what lenny/get_task_tree and
	// GET /v1/sessions/{id}/tree return for this session: `full` (the
	// entire tree rooted at the apex), `parent-and-self`, or `self-only`.
	// Empty resolves to the §8.5 default `full` on the read path; the
	// §8.2 delegation Service stamps the monotonically-resolved value
	// onto every child it admits (a child may narrow but never widen the
	// parent's effective visibility). spec: §8.5 line 540; §8.3 lines
	// 311-319. F-8.5.2 / F-8.9.2 / F-13.5.8.
	TreeVisibility session.TreeVisibility
}

// SetupCommandOutput is one §7.5 line 475 setup-command record retained
// on the session row: the submitted command, captured stdout and stderr,
// exit code, duration, whether the streams were truncated, and (when the
// gateway rejected the command at admission per §7.5 line 488) a
// machine-readable rejection reason. Synthetic entries (Rejected=true)
// are produced by the gateway and carry RejectionReason; executed entries
// (Rejected=false) carry adapter-captured runtime data. spec: §7.5 lines
// 475, 488 — F-7.5.4 / F-7.5.11.
type SetupCommandOutput struct {
	// Cmd is the submitted setup-command text.
	Cmd string
	// ExitCode is the captured process exit code (0 = success; non-zero
	// = failure). Zero is also reported for a §7.5 line 488 rejected
	// entry that never executed; Rejected disambiguates.
	ExitCode int32
	// Stdout is the adapter-captured stdout, truncated to a per-stream
	// budget. Empty for a §7.5 line 488 rejection record.
	Stdout string
	// Stderr is the adapter-captured stderr, truncated to a per-stream
	// budget. Empty for a §7.5 line 488 rejection record.
	Stderr string
	// DurationMs is the wall-clock execution time in milliseconds.
	// Zero for a rejected entry that never executed.
	DurationMs int64
	// Truncated reports whether the adapter truncated stdout or stderr
	// against its per-stream byte budget.
	Truncated bool
	// Rejected is true when the gateway rejected this command at
	// admission per §7.5 line 488 (the command never reached the pod).
	Rejected bool
	// RejectionReason is the machine-readable §7.5 line 488 rejection
	// reason (e.g. `setup_command_policy_violation`,
	// `setup_commands_max_exceeded`). Empty for executed entries.
	RejectionReason string
}

// VisibleTree computes the §8.5 treeVisibility-scoped projection of a
// delegation tree for a caller session. Given the caller row, the full
// set of rows in the caller's tree (as returned by ListByRoot on the
// caller's RootSessionID), and the caller's effective visibility, it
// returns the session that roots the response and a predicate set of
// session IDs that may appear in the response.
//
//   - full: the response is rooted at the tree apex (RootSessionID) and
//     no node is filtered (a nil allowed set). The caller sees the
//     entire tree including siblings and their descendants.
//   - parent-and-self: the response is rooted at the caller's direct
//     parent and contains exactly the parent and the caller. When the
//     caller is the tree root (no parent), it degenerates to self-only.
//   - self-only: the response is rooted at the caller and contains only
//     the caller.
//
// A nil allowed set means "no restriction"; a non-nil set restricts the
// walk to exactly its members. The caller threads allowed into its tree
// walker so descent skips any child outside the set. Keeping the
// decision here (rather than duplicated in the MCP and REST walkers)
// keeps the two §15.2.1 projections of the operation in lockstep.
//
// spec: §8.5 line 540; §8.3 lines 311-319. F-8.5.2 / F-8.9.2.
func VisibleTree(caller Session, all []Session, vis session.TreeVisibility) (Session, map[string]bool) {
	switch vis.OrDefault() {
	case session.VisibilitySelfOnly:
		return caller, map[string]bool{caller.ID: true}
	case session.VisibilityParentAndSelf:
		if caller.ParentSessionID == "" {
			// The tree root has no parent; parent-and-self degenerates to
			// self-only rather than fabricating a parent node.
			return caller, map[string]bool{caller.ID: true}
		}
		for _, s := range all {
			if s.ID == caller.ParentSessionID {
				return s, map[string]bool{s.ID: true, caller.ID: true}
			}
		}
		// Parent row absent from the tree set (GC'd or a cross-tree
		// pointer); fall back to self-only so the caller never observes
		// more than its own node.
		return caller, map[string]bool{caller.ID: true}
	default: // full
		apexID := caller.RootSessionID
		if apexID == "" {
			apexID = caller.ID
		}
		for _, s := range all {
			if s.ID == apexID {
				return s, nil
			}
		}
		// Apex row absent (a legacy row without root_session_id, or the
		// caller is its own root); root the response at the caller.
		return caller, nil
	}
}

// DelegationLease is the §8.2 lease-slice subset persisted on a session
// row: the resource ceiling a delegation granted to the session's
// subtree. It mirrors pkg/delegation/lease.LeaseSlice field-for-field;
// the store stays free of a dependency on the lease package, and the
// §8.2 delegation Service translates between the two. A zero field means
// "no limit set at this scope". spec: §8.2 lines 38-48. F-8.2.2.
type DelegationLease struct {
	// MaxTokenBudget is the LLM token cap for the entire subtree.
	MaxTokenBudget int64 `json:"maxTokenBudget,omitempty"`
	// MaxChildrenTotal caps the total descendants the subtree may spawn.
	MaxChildrenTotal int `json:"maxChildrenTotal,omitempty"`
	// MaxTreeSize caps this branch's contribution to the tree-wide pod cap.
	MaxTreeSize int `json:"maxTreeSize,omitempty"`
	// MaxParallelChildren caps concurrent in-flight children.
	MaxParallelChildren int `json:"maxParallelChildren,omitempty"`
	// PerChildMaxAge is the wall-clock seconds budget per descendant.
	PerChildMaxAge int `json:"perChildMaxAge,omitempty"`
}

// IsZero reports whether every axis is unset, in which case the lease
// imposes no budget binding and the store omits the persisted column.
func (l *DelegationLease) IsZero() bool {
	if l == nil {
		return true
	}
	return l.MaxTokenBudget == 0 && l.MaxChildrenTotal == 0 && l.MaxTreeSize == 0 &&
		l.MaxParallelChildren == 0 && l.PerChildMaxAge == 0
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

// Enrollment returns the experiment and variant ids carried by the
// context, or empty strings when the context is nil. Billing events
// auto-populate the §11.2.1 experiment_id/variant_id fields from it so
// per-experiment and per-variant cost attribution works without joining
// the session row. Safe to call on a nil receiver (an unenrolled
// session). spec: §11.2 lines 87-88. F-11.2.13.
func (c *ExperimentContext) Enrollment() (experimentID, variantID string) {
	if c == nil {
		return "", ""
	}
	return c.ExperimentID, c.VariantID
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

	// ListByRoot returns every session whose root_session_id equals
	// rootSessionID within tenantID — the §8.9 single-shard tree
	// projection backed by `idx_sessions_root` (§12.5 line 101). The
	// rootSessionID is included in the result when it identifies a
	// row in the tenant. Order is by created_at ascending (older
	// ancestors first) so a caller can rebuild the §8.9 task tree by
	// walking ParentSessionID without a sort pass. An empty
	// rootSessionID returns no rows. spec: §8.9 line 1010; §12.5 line
	// 101. F-8.9.7.
	ListByRoot(ctx context.Context, tenantID, rootSessionID string) ([]Session, error)

	// Delete removes the session row entirely. Returns ErrNotFound
	// when the row is missing. The minimal gateway uses this for the
	// audit-row GC path; production gateways soft-delete instead.
	Delete(ctx context.Context, tenantID, id string) error

	// DeleteByUser removes every session owned by userID within
	// tenantID and returns the count deleted. It is the §12.8
	// GDPR-erasure per-store adapter — erasing a user with no sessions
	// is a no-op that returns (0, nil), never ErrNotFound.
	//
	// spec: §12.1 line 5, §12.8 step 17.
	DeleteByUser(ctx context.Context, tenantID, userID string) (int, error)

	// DeleteByTenant removes every session row belonging to tenantID
	// and returns the count deleted. It is the §12.1 / §12.8 Phase 4
	// tenant-deletion erasure adapter — a tenant with no sessions is
	// a no-op that returns (0, nil), never ErrNotFound. Cross-tenant
	// rows are never touched.
	//
	// spec: §12.1 line 5, §12.8 Phase 4.
	DeleteByTenant(ctx context.Context, tenantID string) (int, error)

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
// UserID narrows to the named §11.4 invalidation subject; the
// Postgres-backed store pushes the filter to SQL so a `full_revoke` in
// a tenant with many sessions reads O(user's sessions) rows instead of
// O(tenant). spec: §11.4 line 256 (full_revoke step 1).
type ListFilter struct {
	State        session.State
	RuntimeRef   string
	FailureClass session.FailureClass
	UserID       string
}

// Sentinel errors. The sessionserver maps these to the §15.1 error
// envelope: ErrNotFound → 404, ErrAlreadyExists → 409.
var (
	ErrNotFound      = errors.New("sessionstore: session not found")
	ErrAlreadyExists = errors.New("sessionstore: session already exists")
)
