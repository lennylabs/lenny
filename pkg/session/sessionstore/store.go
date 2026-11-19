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
	"errors"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
)

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
// spec: §8.5; §8.3. F-8.5.2 / F-8.9.2.
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

// ProjectNodeAttributes builds the §8.9 per-node attribute
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
	//
	// Create runs the creation fence: in the insert transaction it reads
	// the tenant row and the owner's user row under a shared lock and
	// returns *CreationRefusal, inserting nothing, when CreationMarkers.Refusal
	// refuses the row. A marker read error fails the insert. In the same
	// transaction it deletes the reservation whose ID and owner match the
	// row. A missing tenant row reaches the fence only in the in-memory
	// store; in Postgres the sessions.tenant_id foreign key rejects the
	// insert first.
	// spec: §12.8 (Lifecycle fence and drain).
	Create(ctx context.Context, s Session) error

	// CreateIfGeneration is Create fenced on a source session's
	// coordination generation: in the insert transaction it reads the
	// source row (sourceTenantID, sourceID) under a shared lock and, when
	// its coordination_generation differs from wantGen, returns
	// ErrGenerationAdvanced and inserts nothing. A missing source row
	// returns ErrNotFound and inserts nothing. A concurrent write that
	// advances the generation either commits before the read and is seen,
	// or waits on the shared lock until the insert commits, so no row is
	// written at a stale generation. The derive-failure audit row uses it.
	// spec: §7.1 (Derive session semantics).
	CreateIfGeneration(ctx context.Context, s Session, sourceTenantID, sourceID string, wantGen int64) error

	// Reserve runs the creation fence for a row that ownerUserID will own
	// and records sessionID for that owner, in one transaction. It returns
	// *CreationRefusal when the fence refuses, and records nothing. An
	// empty ownerUserID reads only the tenant row and records nothing.
	// delegatedChild is the CreationMarkers.IsDelegatedChild of the row the
	// path will insert. A path that writes data keyed to the new session
	// ID before its insert reserves the ID first, so the owner's erasure
	// reaches that data through ReservedSessionIDs.
	// spec: §12.8 (Lifecycle fence and drain).
	Reserve(ctx context.Context, tenantID, ownerUserID, sessionID string, delegatedChild bool) error

	// ReleaseReservation deletes the reservation of sessionID. It is
	// idempotent. A path releases its reservation only after it deleted
	// what it wrote under the ID.
	// spec: §12.8 (Lifecycle fence and drain).
	ReleaseReservation(ctx context.Context, tenantID, sessionID string) error

	// ReservedSessionIDs lists the session IDs reserved for userID within
	// tenantID, read from the primary. The erasure's session lister returns
	// them with the user's session rows.
	// spec: §12.8 (Lifecycle fence and drain).
	ReservedSessionIDs(ctx context.Context, tenantID, userID string) ([]string, error)

	// CreateReserved is Create for a row whose ID was reserved through
	// Reserve. For a row with an owner it deletes the reservation in the
	// insert transaction and, when no reservation is left to delete,
	// returns ReservationEndedRefusal and inserts nothing. A row with no
	// owner inserts as Create does.
	// spec: §12.8 (Lifecycle fence and drain).
	CreateReserved(ctx context.Context, s Session) error

	// CreateWithTranscript is CreateReserved, plus a copy of the transcript
	// of transcriptSourceID into s.ID, in the insert transaction. The
	// replay that calls it reserves s.ID before it copies the source's
	// workspace snapshot under it, so a row with an owner whose reservation
	// is gone returns ReservationEndedRefusal. A fence refusal, a refused
	// reservation, a copy error, or any other insert error leaves neither
	// the row nor a copied entry. A store built without a transcript copier
	// returns ErrTranscriptCopierUnwired and inserts nothing.
	// spec: §12.8 (Lifecycle fence and drain), §15.1 (REST API).
	CreateWithTranscript(ctx context.Context, s Session, transcriptSourceID string) error

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
	// from a platform-admin-gated path. spec: §24.11.
	GetByID(ctx context.Context, id string) (Session, error)

	// Update writes new state to id within tenantID. Returns
	// ErrNotFound when the row is missing. The store does NOT validate
	// the transition — the caller (sessionserver) drives
	// session.Validate first. A caller that must refuse a stale
	// transition re-checks the state inside mutate against the locked
	// row and returns an error, which aborts the write.
	Update(ctx context.Context, tenantID, id string, mutate func(*Session) error) (Session, error)

	// List returns every session for the tenant, in created-at order
	// (newest first). The filter is applied in-process; the store
	// itself does no indexing in v1.
	List(ctx context.Context, tenantID string, filter ListFilter) ([]Session, error)

	// ListByRoot returns every session whose root_session_id equals
	// rootSessionID within tenantID — the §8.9 single-shard tree
	// projection backed by `idx_sessions_root` (§12.5). The
	// rootSessionID is included in the result when it identifies a
	// row in the tenant. Order is by created_at ascending (older
	// ancestors first) so a caller can rebuild the §8.9 task tree by
	// walking ParentSessionID without a sort pass. An empty
	// rootSessionID returns no rows. spec: §8.9; §12.5. F-8.9.7.
	ListByRoot(ctx context.Context, tenantID, rootSessionID string) ([]Session, error)

	// Delete removes the session row entirely. Returns ErrNotFound
	// when the row is missing. The minimal gateway uses this for the
	// audit-row GC path; production gateways soft-delete instead.
	Delete(ctx context.Context, tenantID, id string) error

	// DeleteByUser removes every session owned by userID within
	// tenantID and returns the count deleted. It is the §12.8
	// GDPR-erasure per-store adapter — erasing a user with no sessions
	// is a no-op that returns (0, nil), never ErrNotFound. In the same
	// transaction it deletes the user's session-ID reservations, which
	// the count does not include.
	//
	// spec: §12.1, §12.8 step 17, §12.8 (Lifecycle fence and drain).
	DeleteByUser(ctx context.Context, tenantID, userID string) (int, error)

	// DeleteByTenant removes every session row belonging to tenantID
	// and returns the count deleted. It is the §12.1 / §12.8 Phase 4
	// tenant-deletion erasure adapter — a tenant with no sessions is
	// a no-op that returns (0, nil), never ErrNotFound. Cross-tenant
	// rows are never touched. In the same transaction it deletes the
	// tenant's session-ID reservations, which the count does not include.
	//
	// spec: §12.1, §12.8 Phase 4, §12.8 (Lifecycle fence and drain).
	DeleteByTenant(ctx context.Context, tenantID string) (int, error)

	// GetActiveSlotsByPod returns the identifiers of the pod's slot
	// holders across every tenant: the sessions whose pod_assignment names
	// podID and whose row is non-terminal or in its §7.1 post-terminal
	// window under the store's cap (HoldsPod; the Postgres store reads the
	// non-terminal arm through the migration 0080 index and the terminal arm
	// through the migration 0191 index). A store built without a cap counts
	// no terminal row. It
	// is the §5.2 "Post-recovery rehydration atomicity" seed source: the
	// gateway adds these identifiers to the pod's set,
	// lenny:pod:{pod_id}:active_slots, and removes none of the identifiers
	// the set already holds. The failure funnel clears
	// pod_assignment on its resume_pending and awaiting_client_action edges,
	// so a session whose slot the funnel released is not returned. The query
	// is pod-scoped rather than tenant-scoped because the rehydration path
	// holds only the pod identity; §5.2 tenant pinning guarantees the rows
	// all belong to one tenant. An empty podID or a pod with no slot holder
	// returns (nil, nil).
	// spec: §5.2 (Post-recovery rehydration atomicity), §7.1 (Post-terminal window).
	GetActiveSlotsByPod(ctx context.Context, podID string) ([]string, error)

	// ListPodsWithActiveSlots returns, once each, the pods that a session
	// row whose scrub_policy is best-effort-per-slot holds by the two arms
	// GetActiveSlotsByPod reads, across tenants. The slot counter's recovery
	// rebuild deletes each listed pod's rehydrated flag when a Redis outage
	// ends, so the pod's next reservation or release rehydrates its set. An
	// empty pod_assignment is never returned.
	// spec: §12.4 (Redis failure behavior), §5.2 (Post-recovery
	// rehydration atomicity), §7.1 (Post-terminal window).
	ListPodsWithActiveSlots(ctx context.Context) ([]string, error)

	// ListStaleTerminalSessions returns the identifiers in ids whose row is
	// in a terminal state with a terminated_at earlier than
	// terminatedBefore, across tenants. A missing row, a non-terminal row, a
	// terminal row with no terminated_at, and an identifier that is not a
	// session identifier are not returned. The stale-slot reaper resolves a
	// pod's slot set members through it: MarkTerminated stamps
	// terminated_at in the first terminal write and keeps it on a relabel,
	// so an eligible member's session ended at least the reaper's terminal
	// age ago.
	// spec: §5.2 (Slot assignment atomicity).
	ListStaleTerminalSessions(ctx context.Context, ids []string, terminatedBefore time.Time) ([]string, error)

	// ListAbsentSessions returns the identifiers in ids that have no session
	// row, across tenants. An identifier the store holds a row for is never
	// returned, whatever its spelling, and an identifier that can name no
	// row in the store is returned as absent. The per-replica budget
	// state sweep resolves the session ids it tracks through it, so the
	// per-session state of an erased session is reclaimed on every replica.
	// spec: §11.2, §10.1.1.
	ListAbsentSessions(ctx context.Context, ids []string) ([]string, error)

	// ReserveSlotUnderLock is the §12.4 Redis-outage capacity gate for the
	// per-pod session-mode slot counter. It serializes the slot-admission
	// decision for podID under a per-pod Postgres advisory lock so two
	// concurrent admissions during a Redis outage cannot both observe the
	// same free slot. Under the lock it counts the pod's occupancy other
	// than sessionID: the sessions that hold podID, non-terminal or in their
	// §7.1 post-terminal window (the same source GetActiveSlotsByPod reads),
	// together with the pod's slot reservations (RecordSlotReservation),
	// each session counted once. It admits only when that count is below
	// maxConcurrent, and an admission records sessionID's slot reservation
	// in the same transaction, so the next gated count observes it before
	// the session row names the pod (F-12.4.38). It returns the
	// post-admission slot count on success (the observed count plus one) and
	// admitted=false with the observed count when the pod is at its bound.
	// An empty podID admits nothing; a sessionID the store cannot key a
	// reservation by is an error.
	// spec: §12.4 (Redis failure behavior), §5.2 (Slot assignment
	// atomicity), §7.1 (Post-terminal window).
	ReserveSlotUnderLock(ctx context.Context, podID, sessionID string, maxConcurrent int32) (count int32, admitted bool, err error)

	// RecordSlotReservation records sessionID's slot reservation on podID
	// outside the advisory lock, for the Redis fast path, which records the
	// reservation before its reservation script runs so that a gate started
	// by a later Redis outage counts it. It reports whether it inserted the
	// record; a record that already existed is kept and reports false.
	// spec: §12.4 (Redis failure behavior), §5.2 (Slot assignment atomicity).
	RecordSlotReservation(ctx context.Context, podID, sessionID string) (inserted bool, err error)

	// DeleteSlotReservation deletes sessionID's slot reservation on podID.
	// The slot release runs it, as does a reservation the Redis script
	// refused. Deleting an absent record is not an error.
	// spec: §12.4 (Redis failure behavior), §5.2 (Slot assignment atomicity).
	DeleteSlotReservation(ctx context.Context, podID, sessionID string) error

	// ReclaimSlotReservations deletes, across tenants, the slot reservations
	// recorded before cutoff whose session row is absent, is terminal with a
	// terminated_at before cutoff, or is non-terminal and names another pod,
	// and returns how many it deleted.
	// The stale-slot reaper runs it with cutoff at claimOrphanTimeout ago,
	// the age after which §4.6.1 treats a claim with no session row as
	// orphaned and §5.2 bounds every window in which work of an ended
	// session can still act on its slot. A record whose session row is
	// non-terminal and names the record's pod or no pod, or terminal for
	// less than the cutoff, is kept, so an unreclaimed record over-counts
	// its pod and never under-counts it.
	// spec: §12.4 (Redis failure behavior), §5.2 (Slot assignment
	// atomicity), §4.6.1 (Orphaned PodClaim detection).
	ReclaimSlotReservations(ctx context.Context, cutoff time.Time) (int, error)

	// PoolDrainStats returns the §15.1 pool-drain accounting for
	// poolRef: the number of live (non-terminal) sessions bound to the
	// pool across every tenant, and the create time of the longest-running
	// such session (the oldest created_at). Drain is a platform-global
	// pool operation, so the count crosses the tenant boundary like
	// GetActiveSlotsByPod; §5.2 pins each pod's sessions to one tenant but
	// a pool may warm pods for several tenants. The returned time is zero
	// when the pool has no live sessions (active is then 0). An empty
	// poolRef matches no session and returns (0, time.Time{}, nil).
	// spec: §15.1.
	PoolDrainStats(ctx context.Context, poolRef string) (active int, oldestCreatedAt time.Time, err error)

	// CountActiveSessions returns the number of non-terminal sessions
	// other than suspended ones (session.CountsTowardConcurrencyCaps)
	// belonging to tenantID. It backs the §11.2 per-tenant
	// concurrent-session quota check on the session-creation path and on
	// every resume of a suspended session, so the gateway counts without
	// materializing every historical row. A tenant with no counted
	// sessions returns (0, nil).
	// spec: §11.1 (Concurrent-session count), §11.2 (Budgets and Quotas)
	CountActiveSessions(ctx context.Context, tenantID string) (int, error)

	// CountActiveSessionsByUser returns the number of non-terminal
	// sessions other than suspended ones (session.CountsTowardConcurrencyCaps)
	// owned by userID within tenantID. It backs the §11.1 per-user
	// concurrent-session admission limit so a single user cannot
	// monopolize the tenant's concurrent-session capacity. An empty
	// tenant or user, or a user with no counted sessions, returns (0, nil).
	// spec: §11.1 (Concurrent-session count)
	CountActiveSessionsByUser(ctx context.Context, tenantID, userID string) (int, error)

	// CountActiveSessionsByRuntime returns the number of non-terminal
	// sessions other than suspended ones (session.CountsTowardConcurrencyCaps)
	// targeting runtimeRef within tenantID. It backs the §11.1 per-runtime
	// concurrent-session admission limit so a single runtime cannot be
	// flooded with concurrent sessions. An empty tenant or runtime, or a
	// runtime with no counted sessions, returns (0, nil).
	// spec: §11.1 (Concurrent-session count)
	CountActiveSessionsByRuntime(ctx context.Context, tenantID, runtimeRef string) (int, error)

	// CountActiveSessionsGlobal returns the number of non-terminal
	// sessions other than suspended ones (session.CountsTowardConcurrencyCaps)
	// across every tenant. It backs the §11.1 global concurrent-session
	// admission limit (the gateway-wide ceiling) and is the denominator of
	// the §16.5 session-availability ratio. The count is cross-tenant by
	// design; the global cap bounds the counted sessions regardless of
	// tenant attribution.
	// spec: §11.1 (Concurrent-session count), §16.5 (Alerting Rules and SLOs)
	CountActiveSessionsGlobal(ctx context.Context) (int, error)

	// CountActiveSessionsInRecoveryGlobal returns the number of live
	// sessions across every tenant currently in a retry/recovery state
	// (resume_pending, resuming, awaiting_client_action). The gateway
	// export loop divides it by CountActiveSessionsGlobal to publish the
	// §16.5 lenny_session_unavailability_ratio SLI the
	// SessionAvailabilityBurnRate alert reads. Every recovery state is a
	// counted state, so the numerator is a subset of the denominator, and
	// a suspended session, which runs no turn and is in no recovery
	// state, is in neither.
	// spec: §16.5 (Alerting Rules and SLOs), §11.1 (Concurrent-session count). F-16.5.3.
	CountActiveSessionsInRecoveryGlobal(ctx context.Context) (int, error)

	// CountActiveDelegatedChildrenByUser returns the number of live
	// (non-terminal) delegated child sessions owned by userID within
	// tenantID — sessions carrying a non-empty ParentSessionID. It backs
	// the §11.1 per-user active-delegated-children admission limit, which
	// bounds the in-flight delegation breadth a single user holds across
	// all their sessions and trees (the per-session bound is the §8.2
	// lease/treebudget machinery). A user with no in-flight children
	// returns (0, nil).
	// spec: §11.1.
	CountActiveDelegatedChildrenByUser(ctx context.Context, tenantID, userID string) (int, error)
}

// ListFilter narrows the List result. Empty fields mean "no filter".
// UserID narrows to the named §11.4 invalidation subject; the
// Postgres-backed store pushes the filter to SQL so a `full_revoke` in
// a tenant with many sessions reads O(user's sessions) rows instead of
// O(tenant). spec: §11.4.
//
// spec: §12.8 (Lifecycle fence and drain), §12.3 (Read replicas).
type ListFilter struct {
	State        session.State
	RuntimeRef   string
	FailureClass session.FailureClass
	UserID       string

	// Labels narrows to rows whose §14 Labels map contains every
	// key=value pair listed here (AND-containment). Empty means no label
	// filter. spec: §15.1 — "filterable by ... labels".
	// F-15.1.15.
	Labels map[string]string

	// ExcludeDeriveFailures drops terminal `failed` rows whose
	// FailureClass is `derive_failure` from the result. The §15.1 list
	// includes these audit rows by default; the `?includeDeriveFailures=false`
	// query param sets this flag. spec: §15.1. F-15.1.14.
	ExcludeDeriveFailures bool

	// Primary reads from the Postgres primary rather than the §12.3 read
	// pool. Lifecycle decisions set it: the drain of a lifecycle operation
	// lists its scope from the primary, because a replica can omit a row
	// the creation fence admitted before the marker committed. A store
	// with a single backing (memstore) ignores it.
	Primary bool
}

// Sentinel errors. The sessionserver maps these to the §15.1 error
// envelope: ErrNotFound → 404, ErrAlreadyExists → 409.
var (
	ErrNotFound      = errors.New("sessionstore: session not found")
	ErrAlreadyExists = errors.New("sessionstore: session already exists")
	// ErrGenerationAdvanced is returned by CreateIfGeneration when the
	// source row's coordination generation is no longer the one the caller
	// admitted against; nothing was inserted. spec: §7.1 (Derive session
	// semantics).
	ErrGenerationAdvanced = errors.New("sessionstore: source coordination generation advanced")
)

// SessionAgeCounts reports whether the §6.2 maxSessionAge timer runs in st.
// The timer runs in starting (as a secondary bound behind the starting
// watchdog), running, and input_required. It is paused in suspended and in
// the recovery states (resume_pending, resuming, awaiting_client_action),
// does not run in the pre-running states created, finalizing, and ready, and
// is stopped in the terminal states.
// spec: §6.2 (maxSessionAge timer behavior across states).
func SessionAgeCounts(st session.State) bool {
	switch st {
	case session.StateStarting, session.StateRunning, session.StateInputRequired:
		return true
	default:
		return false
	}
}

// AccrueSessionAge adds to row.AccumulatedSessionAgeSeconds the active time
// between the previous write and now, when the row was in a counting state
// (starting, running, or input_required) at the previous write. prev and
// prevUpdatedAt are the state and UpdatedAt the row carried before the write;
// now is the instant the write stamps as the new UpdatedAt. A now at or before
// prevUpdatedAt accrues nothing.
//
// Both stores call it inside Update, after the mutator returns and before the
// UpdatedAt stamp, with the same now the stamp uses. Because UpdatedAt
// strictly advances on every write, the column then sums exactly the
// intervals the row spent in a counting state. A write that leaves the row in
// a counting state still accrues, because the row stayed in that state across
// the interval; a write that moves the row out of a counting state accrues
// the interval up to the transition and nothing after it.
// spec: §6.2 (maxSessionAge timer behavior across states).
func AccrueSessionAge(prev session.State, prevUpdatedAt time.Time, row *Session, now time.Time) {
	if !SessionAgeCounts(prev) || !now.After(prevUpdatedAt) {
		return
	}
	row.AccumulatedSessionAgeSeconds += now.Sub(prevUpdatedAt).Seconds()
}

// StampStateEntry sets row.StateEnteredAt to now when the write changes the
// session's state, and restores the stored value otherwise, so a mutator can
// neither forget nor forge the state-entry instant. prev and prevEntered are
// the state and StateEnteredAt the row carried before the write.
//
// Both stores call it inside Update, after the mutator returns, with the same
// now the UpdatedAt stamp and AccrueSessionAge use.
// spec: §6.2 (Pod State Machine)
func StampStateEntry(prev session.State, prevEntered time.Time, row *Session, now time.Time) {
	if row.State != prev {
		row.StateEnteredAt = now
		return
	}
	row.StateEnteredAt = prevEntered
}

// ActiveAge returns the §6.2 active session age of row at now: the
// accumulated value plus, when the row is in a counting state, the time
// elapsed since its last write. The elapsed part is clamped at zero, so a now
// that precedes the row's UpdatedAt (a sweep instant taken before a
// concurrent write) adds nothing. This is the value the maxSessionAge timer
// compares with the effective cap, periodically and on entry to running.
// spec: §6.2 (maxSessionAge timer behavior across states).
func ActiveAge(row Session, now time.Time) time.Duration {
	age := time.Duration(row.AccumulatedSessionAgeSeconds * float64(time.Second))
	if SessionAgeCounts(row.State) && now.After(row.UpdatedAt) {
		age += now.Sub(row.UpdatedAt)
	}
	return age
}

// Suspension reasons recorded in Session.SuspendedReason. An interrupt
// suspension keeps its pod for the hold window (§6.2
// maxSuspendedPodHoldSeconds); an idle suspension releases its pod in the
// same sequence that writes it, so an Idle row names no pod.
//
// spec: §6.2 (Pod State Machine), §7.2 (Interactive Session Model).
const (
	// SuspendedReasonInterruptAcknowledged is an interrupt the runtime
	// acknowledged before the deadline.
	SuspendedReasonInterruptAcknowledged = "InterruptAcknowledged"
	// SuspendedReasonInterruptTimeout is an interrupt whose deadline elapsed
	// without an acknowledgement.
	SuspendedReasonInterruptTimeout = "InterruptTimeout"
	// SuspendedReasonIdle is an idle suspension (§6.2 Idle suspension.).
	SuspendedReasonIdle = "Idle"
)
