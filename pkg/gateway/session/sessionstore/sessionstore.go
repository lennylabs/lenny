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

	// TerminatedAt and TerminatedReason carry the §7.2 / §8.8 Terminated
	// session-condition fact, relocated off Sandbox.status.conditions by
	// the §5.2 mode collapse: the gateway writes no Sandbox.status field
	// and the WarmPoolController is the sole writer of Sandbox.status
	// (§4.6.3), so the terminal-disposition fact lives on the session row
	// and is read through the session API (§7.2 line 230). TerminatedAt
	// is the wall-clock instant the session reached a terminal state
	// (completed, failed, cancelled, expired); it is the zero time while
	// the session is non-terminal. TerminatedReason is the coded reason
	// string for the terminal disposition. spec: §6.49; §7.2 line 230;
	// §8.8 session-level state mapping.
	TerminatedAt     time.Time
	TerminatedReason string

	// SuspendedAt and SuspendedReason carry the §7.2 interrupt-suspension
	// Suspended session-condition fact, relocated off
	// Sandbox.status.conditions alongside the Terminated fact. SuspendedAt
	// is the wall-clock instant the session entered `suspended`; it is the
	// zero time while the session is not suspended. SuspendedReason is the
	// coded interrupt reason. spec: §6.49; §7.2 line 230; §8.8.
	SuspendedAt     time.Time
	SuspendedReason string

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
	// resolved to at session creation: "session" (default) or "service".
	// Resolved from the pool's SandboxTemplate at
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

	// ConversationContinuity is the §7.1 line 74 sessionIsolationLevel
	// envelope half the create response and GET /v1/sessions/{id}
	// return: "platform" for session mode (the platform binds the
	// session to a pod and preserves conversation context across
	// messages for the session's lifetime) or "none" for service mode
	// (the gateway routes each message to any ready replica and keeps no
	// conversation context between messages, so clients of multi_turn
	// runtimes re-inject context into each message's input). Resolved
	// against the assigned pool at create time and frozen for the
	// session lifetime per §7.1 line 75. Empty when the gateway has not
	// resolved a pool, in which case the read path backfills "platform"
	// for an empty execution_mode and "none" for execution_mode =
	// "service", parallel to the ExecutionMode / ScrubPolicy backfill.
	// spec: §7.1 line 74.
	ConversationContinuity string

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

	// CredentialOriginSessionID identifies the session whose credential
	// pool this session's `inherit` hops draw from — the origin pool the
	// §8.3 cross-environment provider-compatibility check compares against
	// the target runtime's supportedProviders. The delegation Service
	// stamps it at child-row creation: an `inherit` child copies its
	// parent's origin (tracing the same origin pool through contiguous
	// `inherit` hops back to the last `independent` break or the root),
	// while an `independent`, `deny`, root, or top-level session is its
	// own origin. A read-path value equal to the row's own ID (or empty,
	// which collapses to the row's own ID) marks a self-origin, so a
	// session inherited iff this value is a non-empty ancestor id.
	// spec: §8.3 line 472 (origin-pool forwarding); line 488 (independent
	// establishes a new origin); line 474.
	CredentialOriginSessionID string

	// CredentialDeny is true iff the delegate_task hop that created this
	// child set credentialPropagation: deny, meaning the child receives no
	// LLM credentials (§8.3 line 443: "Child receives no LLM credentials").
	// inherit versus independent is already carried by
	// CredentialOriginSessionID (an inherit child copies its parent's
	// origin; an independent child is its own origin), so this field records
	// only the deny case. The finalize-time §4.9 engine reads it to fail a
	// deny child closed at credential assignment, and reads it on an origin
	// row so an inherit hop whose origin traces to a deny session also fails
	// closed. The delegation Service stamps it at child-row creation; the
	// value is invariant once the row is created.
	// spec: §8.3 line 443.
	CredentialDeny bool

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

	// LegalHoldSetBy, LegalHoldSetAt, and LegalHoldNote carry the §15.1
	// line 865 provenance the `GET /v1/admin/legal-holds` list reports:
	// the operator subject who set the hold, the instant it was set, and
	// the required justification note. They are populated when LegalHold
	// flips true and cleared when it flips false. Zero values when no
	// hold is active.
	// spec: §15.1 lines 864-865.
	LegalHoldSetBy string
	LegalHoldSetAt time.Time
	LegalHoldNote  string

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

	// LastAgentActivityAt is the §6.2 lines 273-300 idle-timer anchor:
	// the wall-clock instant of the session's most recent qualifying
	// agent activity (agent_output / tool_use events, an await_children
	// invocation, a proxied LLM chunk, or a direct-mode ReportUsage).
	// The §11.3 line 199 `maxIdleTime` watchdog expires a `running`
	// session whose elapsed time since this instant exceeds its
	// effective `maxIdleTimeSeconds`. Zero when no qualifying event has
	// been recorded yet, in which case the watchdog falls back to
	// UpdatedAt (the running-entry time) so the idle clock is always
	// anchored. The column cannot reuse UpdatedAt, which advances on
	// internal state writes that are not agent activity.
	// spec: §6.2 lines 273-300; §11.3 line 199. F-11.3.7.
	LastAgentActivityAt time.Time

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

	// Labels is the §14 line 311 client-supplied CreateSessionRequest
	// `labels` map — a flat string→string set of caller tags the
	// `GET /v1/sessions` list endpoint filters on (§15.1 line 598). Nil
	// when the caller submitted no labels, in which case the field is
	// omitted from the GET envelope. Distinct from Metadata: labels are
	// the filterable selector set, metadata is opaque annotation. Rides
	// the §14.1 request-envelope JSONB bundle in the Postgres store so it
	// needs no dedicated column. spec: §14 line 311; §15.1 line 598.
	// F-15.1.15.
	Labels map[string]string

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

	// Env is the §14 client-supplied environment-variable map injected
	// into the agent session. The gateway validates every key against the
	// deployer-configured blocklist (exact names and `*` globs) at
	// admission and rejects a blocked key with 400 ENV_VAR_BLOCKLISTED, so
	// a stored Env has already passed the blocklist. Nil when the client
	// supplied none. spec: §14 lines 47-50, 105. F-14.1.12.
	Env map[string]string

	// Pool is the §14 / §14.1 line 311 client-requested target pool from
	// the CreateSessionRequest envelope. Recorded at create so a client
	// that lost the create response can recover its requested pool and so
	// the admission rate-limit scope can key on the explicit pool. It is
	// distinct from PoolRef, which records the pool the gateway actually
	// resolved and scheduled against. Empty when the request named no
	// pool. spec: §14 example; §14.1 line 311. F-14.1.14.
	Pool string

	// Timeouts holds the §14 per-session timeout overrides
	// (maxSessionAgeSeconds, maxIdleSeconds). The gateway validates them
	// against the runtime's limits.maxSessionAge at admission (a session
	// override cannot exceed the runtime cap). Nil when the client
	// supplied none. spec: §14 line 154. F-14.1.14.
	//
	// A §27.3 origin=playground session also lands its §27.6 idle and
	// duration caps here: the create path stamps
	// min(existing, playground cap) onto MaxIdleSeconds /
	// MaxSessionAgeSeconds so the watchdog's maxSessionAge sweep (via the
	// sessionage resolver) enforces the playground duration cap. F-27.6.1
	// / F-27.6.2.
	Timeouts *SessionTimeouts

	// Origin is the §27.3 token-origin label copied from the session
	// bearer's `origin` JWT claim at create time. It is "playground" for
	// every /playground/*-originated session (all three playground auth
	// modes) and empty otherwise. §27.6 line 203 requires the label on
	// every playground session record for the §25.9 audit-query slice and
	// the §27.8 origin=playground dashboards. F-27.6.8.
	Origin string

	// CredentialPolicyOverride is the §14 per-session credentialPolicy
	// hint. A per-session override can only restrict, never expand, the
	// tenant credentialPolicy, so the gateway rejects an override that
	// enables a credential source the tenant policy disallows. Nil when
	// the client supplied none, in which case the tenant policy applies
	// unchanged. spec: §14 credentialPolicy; §4.9 lines 1310, 1336.
	// F-14.1.14.
	CredentialPolicyOverride *CredentialPolicyOverride

	// DelegationLeaseRequest is the §14 client-requested delegation lease
	// bounds {maxDepth, maxChildrenTotal, delegationPolicyRef} carried on
	// the CreateSessionRequest envelope. It is distinct from
	// DelegationLease, the §8.2 granted LeaseSlice the delegation Service
	// stamps on a child it admits. Nil when the client supplied none.
	// spec: §14 lines 75-79. F-14.1.14.
	DelegationLeaseRequest *DelegationLeaseRequest

	// RuntimeOptions is the §14 per-runtime discriminated-union options
	// blob (raw JSON, ≤64 KB). When the target runtime registered a
	// runtimeOptionsSchema the gateway validates this against it at
	// admission (400 RUNTIME_OPTIONS_INVALID on failure); when no schema
	// is registered the options pass through and the gateway emits a
	// RuntimeOptionsUnschematized warning. Nil when the client supplied
	// none. spec: §14 line 155. F-14.1.14 / F-14.1.15.
	RuntimeOptions json.RawMessage

	// CallbackURL is the §14 client-supplied session-terminal webhook
	// endpoint. The gateway validated it against the §14 SSRF mitigations
	// at admission, so a stored value is HTTPS, resolves to a public IP,
	// and (when a deployer allowlist is set) matches it. Empty when the
	// client supplied none. spec: §14 lines 73, 108-112. F-14.1.11.
	CallbackURL string

	// CallbackPinnedIP is the §14 line 110 DNS-pinned IP the gateway
	// resolved for CallbackURL at admission. The delivery transport dials
	// this address directly so a hostname that re-resolves to an internal
	// IP between admission and delivery cannot redirect the callback.
	// Empty when no CallbackURL was supplied. spec: §14 line 110. F-14.1.11.
	CallbackPinnedIP string

	// CallbackSecret is the §14 callbackSecret in its KMS-envelope-sealed
	// form (pkg/kms/envelope.Encode output). It is opaque ciphertext: the
	// lenny_app role can read it but only the gateway with KMS Decrypt can
	// recover the plaintext. The gateway clears it (SQL NULL) once the
	// session reaches a terminal state and every delivery attempt has
	// succeeded or been exhausted. The plaintext is never returned by any
	// API. spec: §14 line 139 (T3, KMS-envelope storage, write-only,
	// NULL-on-terminal). F-14.1.11.
	CallbackSecret []byte

	// WebhookEvents holds the §14 line 150 undelivered callback events
	// after the retry budget is spent, surfaced by
	// GET /v1/sessions/{id}/webhook-events. Nil when no delivery has
	// exhausted its retries. spec: §14 line 150; §15.1 line 678. F-14.1.11.
	WebhookEvents []WebhookEventRecord
}

// WebhookEventRecord is one §14 callback event that exhausted its
// delivery retry budget. It mirrors the dispatcher's delivery record so
// GET /v1/sessions/{id}/webhook-events can report the undelivered event,
// its CloudEvents id, and the last failure. spec: §14 line 150; §15.1
// line 678. F-14.1.11.
type WebhookEventRecord struct {
	EventID     string    `json:"eventId"`
	EventType   string    `json:"eventType"`
	CallbackURL string    `json:"callbackUrl"`
	Body        []byte    `json:"body,omitempty"`
	Attempts    int       `json:"attempts"`
	LastError   string    `json:"lastError,omitempty"`
	LastStatus  int       `json:"lastStatus,omitempty"`
	FailedAt    time.Time `json:"failedAt"`
}

// SessionTimeouts is the §14 per-session timeouts envelope: the
// maxSessionAge / maxIdle overrides a client may request, each capped by
// deployer policy and bounded above by the runtime's limits.maxSessionAge.
// A zero field means the client requested no override for that axis.
// spec: §14 line 154. F-14.1.14.
type SessionTimeouts struct {
	MaxSessionAgeSeconds int64 `json:"maxSessionAgeSeconds,omitempty"`
	MaxIdleSeconds       int64 `json:"maxIdleSeconds,omitempty"`
}

// CredentialPolicyOverride is the §14 per-session credentialPolicy
// override carried on the CreateSessionRequest envelope. v1 carries the
// preferredSource hint; per §14 it may only restrict the tenant policy.
// spec: §14 credentialPolicy; §4.9 lines 1310, 1336. F-14.1.14.
type CredentialPolicyOverride struct {
	PreferredSource string `json:"preferredSource,omitempty"`
}

// DelegationLeaseRequest is the §14 client-requested delegation lease
// bounds on the CreateSessionRequest envelope. MaxDepth and
// MaxChildrenTotal are pointers so an explicit zero ("no further
// delegation") is distinguishable from an unset bound. spec: §14 lines
// 75-79. F-14.1.14.
type DelegationLeaseRequest struct {
	MaxDepth            *int   `json:"maxDepth,omitempty"`
	MaxChildrenTotal    *int   `json:"maxChildrenTotal,omitempty"`
	DelegationPolicyRef string `json:"delegationPolicyRef,omitempty"`
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
	// MaxTreeMemoryBytes caps the delegation tree's aggregate gateway
	// memory footprint (§8.2 default 2 MB).
	MaxTreeMemoryBytes int64 `json:"maxTreeMemoryBytes,omitempty"`
	// MaxParallelChildren caps concurrent in-flight children.
	MaxParallelChildren int `json:"maxParallelChildren,omitempty"`
	// PerChildMaxAge is the wall-clock seconds budget per descendant.
	PerChildMaxAge int `json:"perChildMaxAge,omitempty"`

	// The fields below are the §8.10 lines 1044-1049 lease-scoped policy
	// record the gateway captures at original `delegate_task` approval
	// time and persists alongside the resource slice. Tree recovery MUST
	// bring a node back up against these snapshotted values rather than
	// re-evaluating the live policy state, so policy narrowing applied
	// between original approval and recovery cannot retroactively
	// invalidate a node's lease. They ride the same `delegation_lease`
	// JSONB column as the resource axes above. spec: §8.10 lines 1044-1049.
	// F-8.10.5.

	// DelegationPolicyRef is the §5.1 runtime `delegationPolicyRef` that
	// scoped this node's delegation at approval time. Empty when the
	// target runtime named no policy.
	DelegationPolicyRef string `json:"delegationPolicyRef,omitempty"`
	// MaxDelegationPolicy is the node's effective `maxDelegationPolicy`
	// after §8.3 inheritance resolution — in v1 the resolved
	// DelegationPolicy name the gateway evaluated this delegation against.
	MaxDelegationPolicy string `json:"maxDelegationPolicy,omitempty"`
	// ContentPolicyRef is the §8.3 `contentPolicy.interceptorRef` in
	// effect at approval. Empty when the policy declared no interceptor.
	// The lease-scoped `minIsolationProfile` the spec also names is
	// already persisted as the session's first-class IsolationProfile
	// column, so it is not duplicated here; recovery reads it from the
	// session row directly.
	ContentPolicyRef string `json:"contentPolicyRef,omitempty"`
	// ContentMaxInputSize, ContentScanExportedFiles, and
	// ContentMaxExportedFileSize are the remaining three §8.3 line-157
	// `contentPolicy` axes the gateway resolves to their
	// transitively-narrowest effective value at `delegate_task` approval
	// time (alongside ContentPolicyRef above). They are stamped so the
	// next delegation hop inherits the narrowest cap seen anywhere on the
	// path from root to parent (§8.3 line 240) and so the four-axis
	// monotonicity check on the child can read the parent's effective
	// policy from the lease rather than re-resolving the chain. A zero
	// size field means "the §8.3 platform default" (128 KiB input /
	// 10 MiB exported file); only a tightened value below the default, a
	// set interceptor, or `scanExportedFiles: true` is persisted, so a
	// default-only effective policy leaves the lease unchanged. F-13.5.10.
	ContentMaxInputSize        int   `json:"contentMaxInputSize,omitempty"`
	ContentScanExportedFiles   bool  `json:"contentScanExportedFiles,omitempty"`
	ContentMaxExportedFileSize int64 `json:"contentMaxExportedFileSize,omitempty"`
	// SnapshottedPoolIDs is the §8.3 line 208 `snapshotted_pool_ids` set,
	// populated only when the root lease was issued with
	// `snapshotPolicyAtLease: true`. Empty under the v1 default (live pool
	// labels), so recovery falls through to live evaluation for new
	// post-recovery delegations exactly as a pre-failure call would.
	SnapshottedPoolIDs []string `json:"snapshottedPoolIds,omitempty"`
}

// IsZero reports whether every axis is unset, in which case the lease
// imposes no budget binding and carries no §8.10 policy record, so the
// store omits the persisted column.
func (l *DelegationLease) IsZero() bool {
	if l == nil {
		return true
	}
	return l.MaxTokenBudget == 0 && l.MaxChildrenTotal == 0 && l.MaxTreeSize == 0 &&
		l.MaxTreeMemoryBytes == 0 && l.MaxParallelChildren == 0 && l.PerChildMaxAge == 0 &&
		l.DelegationPolicyRef == "" && l.MaxDelegationPolicy == "" &&
		l.ContentPolicyRef == "" && l.ContentMaxInputSize == 0 &&
		!l.ContentScanExportedFiles && l.ContentMaxExportedFileSize == 0 &&
		len(l.SnapshottedPoolIDs) == 0
}

// NodeAttributes is the §8.9 line 1010 per-node tracking projection.
// The spec states each task-tree node tracks "session_id, generation,
// pod, state, lease, budget consumed, failure history". session_id and
// state ride on the enclosing tree node (TaskID / State); this struct
// carries the remaining row-persisted attributes so a parent agent or
// an operator can inspect a child's recovery generation, pod
// assignment, granted resource lease, and failure history directly
// from `lenny/get_task_tree` and `GET /v1/sessions/{id}/tree` without a
// second per-node lookup. Live budget-consumption accounting against
// the lease is the §8.3 Redis counter surfaced through the §8.8 usage
// rollup and the §16 billing surface, not this static projection; the
// Lease field carries the granted ceiling the consumption is measured
// against. spec: §8.9 line 1010. F-8.9.1.
type NodeAttributes struct {
	// Generation is the §4.2 recovery counter (RecoveryGeneration),
	// incremented on each pod recovery. spec: §8.9 line 1010 ("generation").
	Generation int64 `json:"generation"`
	// Pod is the §4.2 pod-to-session binding (PodAssignment); empty when
	// the node is not currently bound to a pod. spec: §8.9 line 1010 ("pod").
	Pod string `json:"pod,omitempty"`
	// Lease is the §8.2 granted resource ceiling for the node's subtree,
	// nil for a root/standalone node or one admitted with no slice.
	// spec: §8.9 line 1010 ("lease").
	Lease *DelegationLease `json:"lease,omitempty"`
	// FailureHistory carries the node's §4.2 retry counter and the §7.1
	// terminal failure cause, nil while the node has neither retried nor
	// failed. spec: §8.9 line 1010 ("failure history").
	FailureHistory *FailureHistory `json:"failureHistory,omitempty"`
}

// FailureHistory is the §8.9 line 1010 per-node failure projection. The
// v1 session row tracks the retry counter plus the most recent terminal
// failure class and coded reason rather than a chronological list; a
// future iteration that records a per-attempt audit replaces this with
// the full list behind the same field. spec: §8.9 line 1010; §4.2 line
// 158; §7.1. F-8.9.1.
type FailureHistory struct {
	// RetryCount is the §4.2 line 158 monotonic retry counter.
	RetryCount int64 `json:"retryCount"`
	// FailureClass is the §7.1 coarse audit bucket, empty unless the node
	// reached `failed`.
	FailureClass string `json:"failureClass,omitempty"`
	// FailureReason is the §7.1 specific coded cause, empty unless the
	// node reached `failed`.
	FailureReason string `json:"failureReason,omitempty"`
}

// ProjectNodeAttributes builds the §8.9 line 1010 per-node attribute
// projection from a session row. FailureHistory is omitted when the
// node has neither retried nor recorded a terminal failure, so a clean
// node serializes without an empty history object. F-8.9.1.
func ProjectNodeAttributes(s Session) NodeAttributes {
	attrs := NodeAttributes{
		Generation: s.RecoveryGeneration,
		Pod:        s.PodAssignment,
		Lease:      s.DelegationLease,
	}
	if s.RetryCount > 0 || s.FailureClass != "" || s.FailureReason != "" {
		attrs.FailureHistory = &FailureHistory{
			RetryCount:    s.RetryCount,
			FailureClass:  string(s.FailureClass),
			FailureReason: s.FailureReason,
		}
	}
	return attrs
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

	// GetByID returns the session row whose ID equals id, regardless of
	// tenant. The session id is a globally-unique UUID, so this resolves
	// at most one row. It backs the §24.11 platform-admin session
	// investigation surface (`GET /v1/admin/sessions/{id}`,
	// force-terminate), where the operator supplies only the session id.
	// Returns ErrNotFound when no row exists. Unlike Get, this method
	// deliberately crosses the tenant boundary and MUST only be reached
	// from a platform-admin-gated path. spec: §24.11 lines 135-136.
	GetByID(ctx context.Context, id string) (Session, error)

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

	// ReserveSlotUnderLock is the §12.4 Redis-outage capacity gate for the
	// per-pod session-mode slot counter. It serializes the slot-admission
	// decision for podID under a per-pod Postgres advisory lock so two
	// concurrent admissions during a Redis outage cannot both observe the
	// same free slot: under the lock it counts live (non-terminal) sessions
	// bound to podID (the same source GetActiveSlotsByPod reads) and admits
	// only when that count is below maxConcurrent. It returns the
	// post-admission slot count on success (the observed count plus one) and
	// ErrSlotsExhausted-equivalent semantics via admitted=false when the pod
	// is already at its bound. The lock is held only for the duration of the
	// count-and-decide; the §12.4 posture documents the fallback as a
	// degraded Postgres-latency path bounded by slotCounterPostgresFallbackMaxSeconds.
	// spec: §12.4 line 208 (Postgres fallback under a per-pod advisory lock);
	// §5.2 line 541 (intra-pod capacity gate during a Redis outage).
	ReserveSlotUnderLock(ctx context.Context, podID string, maxConcurrent int32) (count int32, admitted bool, err error)

	// PoolDrainStats returns the §15.1 line 797 pool-drain accounting for
	// poolRef: the number of live (non-terminal) sessions bound to the
	// pool across every tenant, and the create time of the longest-running
	// such session (the oldest created_at). Drain is a platform-global
	// pool operation, so the count crosses the tenant boundary like
	// GetActiveSlotsByPod; §5.2 pins each pod's sessions to one tenant but
	// a pool may warm pods for several tenants. The returned time is zero
	// when the pool has no live sessions (active is then 0). An empty
	// poolRef matches no session and returns (0, time.Time{}, nil).
	// spec: §15.1 line 797 (drain backpressure, activeSessions, Retry-After).
	PoolDrainStats(ctx context.Context, poolRef string) (active int, oldestCreatedAt time.Time, err error)

	// CountActiveSessions returns the number of live (non-terminal)
	// sessions belonging to tenantID. It backs the §11.2 per-tenant
	// concurrent-session quota check on the session-creation path so the
	// gateway counts active sessions without materializing every
	// historical row. A tenant with no live sessions returns (0, nil).
	// spec: §11.2 (per-tenant concurrent-session quota with hard
	// rejection).
	CountActiveSessions(ctx context.Context, tenantID string) (int, error)

	// CountActiveSessionsByUser returns the number of live (non-terminal)
	// sessions owned by userID within tenantID. It backs the §11.1
	// per-user concurrent-session admission limit so a single user cannot
	// monopolize the tenant's concurrent-session capacity. An empty
	// tenant or user, or a user with no live sessions, returns (0, nil).
	// spec: §11.1 line 8 (Concurrency limits — per-user).
	CountActiveSessionsByUser(ctx context.Context, tenantID, userID string) (int, error)

	// CountActiveSessionsByRuntime returns the number of live
	// (non-terminal) sessions targeting runtimeRef within tenantID. It
	// backs the §11.1 per-runtime concurrent-session admission limit so a
	// single runtime cannot be flooded with concurrent sessions. An empty
	// tenant or runtime, or a runtime with no live sessions, returns
	// (0, nil).
	// spec: §11.1 line 8 (Concurrency limits — per-runtime).
	CountActiveSessionsByRuntime(ctx context.Context, tenantID, runtimeRef string) (int, error)

	// CountActiveSessionsGlobal returns the number of live (non-terminal)
	// sessions across every tenant. It backs the §11.1 global
	// concurrent-session admission limit (the gateway-wide ceiling). The
	// count is cross-tenant by design; the global cap bounds total live
	// sessions regardless of tenant attribution.
	// spec: §11.1 line 8 (Concurrency limits — global).
	CountActiveSessionsGlobal(ctx context.Context) (int, error)

	// CountActiveSessionsInRecoveryGlobal returns the number of live
	// sessions across every tenant currently in a retry/recovery state
	// (resume_pending, resuming, awaiting_client_action). The gateway
	// export loop divides it by CountActiveSessionsGlobal to publish the
	// §16.5 lenny_session_unavailability_ratio SLI the
	// SessionAvailabilityBurnRate alert reads.
	// spec: §16.5 line 616 (Session availability SLO). F-16.5.3.
	CountActiveSessionsInRecoveryGlobal(ctx context.Context) (int, error)

	// CountActiveDelegatedChildrenByUser returns the number of live
	// (non-terminal) delegated child sessions owned by userID within
	// tenantID — sessions carrying a non-empty ParentSessionID. It backs
	// the §11.1 per-user active-delegated-children admission limit, which
	// bounds the in-flight delegation breadth a single user holds across
	// all their sessions and trees (the per-session bound is the §8.2
	// lease/treebudget machinery). A user with no in-flight children
	// returns (0, nil).
	// spec: §11.1 line 9 (Active delegated children — per-user).
	CountActiveDelegatedChildrenByUser(ctx context.Context, tenantID, userID string) (int, error)
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

	// Labels narrows to rows whose §14 Labels map contains every
	// key=value pair listed here (AND-containment). Empty means no label
	// filter. spec: §15.1 line 598 — "filterable by ... labels".
	// F-15.1.15.
	Labels map[string]string

	// ExcludeDeriveFailures drops terminal `failed` rows whose
	// FailureClass is `derive_failure` from the result. The §15.1 list
	// includes these audit rows by default; the `?includeDeriveFailures=false`
	// query param sets this flag. spec: §15.1 lines 652, 661 (derive-failure
	// reachability). F-15.1.14.
	ExcludeDeriveFailures bool
}

// Sentinel errors. The sessionserver maps these to the §15.1 error
// envelope: ErrNotFound → 404, ErrAlreadyExists → 409.
var (
	ErrNotFound      = errors.New("sessionstore: session not found")
	ErrAlreadyExists = errors.New("sessionstore: session already exists")
)
