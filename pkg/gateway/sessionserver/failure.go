// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/watchdog"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// FailureReport is the §7.3
// payload. It is the single entry-point a pod-failure detector
// (controller-side Sandbox CR watcher, gateway-side adapter-call error
// path, manual operator drain) calls into the session server with;
// ReportSessionFailure runs the §7.3 classifier and writes the
// matching state-machine edge.
//
// spec: §7.3.
type FailureReport struct {
	// TenantID scopes the session lookup.
	TenantID string

	// SessionID names the failed session.
	SessionID string

	// Reason is the §7.3 failure label — one of the platform-default
	// retryable causes (pod_evicted, node_lost, runtime_crash), one of
	// the platform-default non-retryable causes (workspace_validation_
	// failed, setup_command_failed), or a deployer-specific extension.
	// An empty reason is rejected as a programming error.
	Reason string

	// PodAssignment carries the failed pod's sandbox name when the
	// reporter knows it. Stamped onto the session row's PodAssignment
	// so a follow-on /resume can correlate the new pod's claim. An empty
	// value leaves the existing assignment in place.
	PodAssignment string

	// ExpectedCoordinationGeneration is the resuming incarnation the report
	// belongs to: the resume driver sets it to its entry generation G. A
	// report on a resuming row writes only when the locked row is still at
	// this generation, so a report from an incarnation a terminal write, the
	// resuming watchdog, or a later attempt already ended writes nothing. A
	// zero value never matches a resuming row. spec: §7.3 (Resume driver),
	// §6.2 (Pod State Machine).
	ExpectedCoordinationGeneration int64
}

// FailureDisposition captures the post-classification edge that
// ReportSessionFailure took. Reporters log it so operators can correlate
// the §16.1 metric increment with the row state without re-reading the
// store.
type FailureDisposition struct {
	// Classification is the §7.3 classifier output for the supplied
	// reason against the session's effective retry policy.
	Classification session.FailureClassification
	// From is the row state observed by the call (may differ from the
	// reporter's view if a concurrent transition won).
	From session.State
	// To is the state the row reached. Equal to From when the call was
	// a no-op (already terminal, already in awaiting_client_action,
	// concurrent transition lost).
	To session.State
	// RetryCount is the row's RetryCount after the update.
	RetryCount int64
	// MaxRetries is the effective per-session retry budget the call
	// compared RetryCount against.
	MaxRetries int
}

// ErrFailureReportInvalid is returned by ReportSessionFailure for a
// malformed report payload (missing tenant, session, or reason). The
// store is not consulted on a malformed report — the caller is buggy.
var ErrFailureReportInvalid = errors.New("sessionserver: failure report missing required field")

// ReportSessionFailure runs the §7.3 failure classifier
// against the session's effective retry policy and drives the
// state-machine edge mandated by §7.3 + §6.2:
//
//   - Retryable AND RetryCount < effective MaxRetries → resume_pending.
//     The §6.2 wall-clock cap is then enforced by the watchdog
//     sweep; on retry success the §15.1 /resume handler advances to
//     running. RetryCount is incremented in the same transaction and
//     the §16.1 lenny_session_retry_total counter + §11.7 audit row
//     fire from the existing recordSessionRetry helper.
//
//   - Retryable AND retries exhausted → awaiting_client_action (§7.3). The §7.3 webhook + §11.7
//     audit row fire via emitAwaitingClientActionEntered.
//
//   - NonRetryable from running / input_required / suspended → failed
//     (§6.2). The terminal pipeline
//     runs (seal, executor release, cascade, billing, audit).
//
//   - NonRetryable from resuming → awaiting_client_action (§6.2
//     "Non-retryable errors during resuming ... transition directly to
//     awaiting_client_action regardless of retry count").
//
//   - Unknown (reason not on either list) → treated as NonRetryable.
//     The §7.3 platform-default lists are the closed set the gateway
//     recognises; an unenumerated label cannot consume retry budget.
//     The disposition is logged with Classification=Unknown so an
//     operator notices an untagged failure cause and can extend the
//     per-runtime policy.
//
// The session's current state determines which transitions are
// admissible. ReportSessionFailure is a no-op on a terminal session,
// on a session already in awaiting_client_action or resume_pending, and
// on a session in a pre-running state where the resume cycle has no
// meaning (created/finalizing/ready/starting). §7.2 gives resume_pending
// no failure edge: a report on such a row duplicates the report that
// entered it. The watchdog's pre-running timeouts handle the pre-running
// paths via their own sweep.
//
// On a committed edge out of an active state the session's pod binding is
// released with the failed disposition, after a failed slot is counted
// toward the pod's §5.2 whole-pod replacement trigger (see
// applyFailureFromActive).
//
// Best-effort: a store error returns the error so the caller can decide
// whether to retry. The metric / audit / SSE / cascade side effects are
// best-effort and never roll back the state transition. The method is
// safe to call concurrently — the store update is compare-and-swap on
// the state the call read, so a report that loses to any concurrent
// state change, a duplicate report included, gets the no-op disposition.
//
// spec: §7.3; §6.2; §7.3; §16.1 retry
// metric; §11.7 / §16.7 retry audit. F-7.3.4 / F-7.3.5 / F-7.3.16.
func (s *Server) ReportSessionFailure(ctx context.Context, rep FailureReport) (FailureDisposition, error) {
	if rep.TenantID == "" || rep.SessionID == "" || rep.Reason == "" {
		return FailureDisposition{}, fmt.Errorf("%w: tenant=%q session=%q reason=%q",
			ErrFailureReportInvalid, rep.TenantID, rep.SessionID, rep.Reason)
	}
	// spec: §12.3 (Postgres HA Requirements) — the edge is decided on the
	// primary row, so a lagging read-pool row that still shows an earlier
	// state does not drop a report the primary row admits.
	row, err := s.readPrimary(ctx, rep.TenantID, rep.SessionID)
	if err != nil {
		return FailureDisposition{}, err
	}
	disp := FailureDisposition{From: row.State, To: row.State, RetryCount: row.RetryCount}
	if session.IsTerminal(row.State) {
		// Terminal sessions are not re-classified — the §7.3 surface
		// reports failures observed while the row was non-terminal.
		return disp, nil
	}
	if rep.ExpectedCoordinationGeneration != 0 && row.State != session.StateResuming {
		// spec: §7.3 (Resume driver) — a report from a resume attempt
		// belongs to its resuming incarnation; once the row left resuming,
		// the incarnation ended and the report writes nothing.
		return disp, nil
	}
	if row.State == session.StateAwaitingClientAction || row.State == session.StateResumePending {
		// Already in the client-intervention holding state or the
		// recovering state a prior report entered; the reporter races a
		// sibling detector. §7.2 gives neither state a failure edge.
		return disp, nil
	}
	classification := session.ClassifyFailure(rep.Reason, row.RetryPolicy)
	disp.Classification = classification

	maxRetries := EffectiveMaxRetriesForRow(row, s.retryPolicyCaps)
	disp.MaxRetries = maxRetries

	switch row.State {
	case session.StateRunning, session.StateInputRequired, session.StateSuspended:
		disp, err := s.applyFailureFromActive(ctx, row, rep, classification, maxRetries)
		s.afterRecoveryEdge(ctx, row, disp, err)
		return disp, err
	case session.StateResuming:
		disp, err := s.applyFailureFromResuming(ctx, row, rep, classification, maxRetries)
		s.afterRecoveryEdge(ctx, row, disp, err)
		return disp, err
	case session.StateCreated, session.StateFinalizing, session.StateReady,
		session.StateStarting:
		// Pre-running states are bounded by the watchdog's
		// MaxCreated/Finalizing/Ready/StartingSeconds sweep — the §7.3
		// resume flow models post-start failures only. A report on a
		// pre-running row is a no-op so a controller that races the
		// pre-running watchdog does not double-transition.
		return disp, nil
	default:
		// Defensive: unrecognised non-terminal state. Do not transition.
		return disp, nil
	}
}

// afterRecoveryEdge runs the effects of a committed failure edge into
// resume_pending or awaiting_client_action: the session left its pod, so the
// elicitations it raised expire, and an edge out of `suspended` re-arms the
// tree's delegation budget keys. A failed disposition expires through the
// post-terminal hook its write runs, and a no-op or conflict disposition, or
// an error, runs neither.
//
// spec: §9.2 (Elicitation Chain), §7.3 (Retry and Resume), §8.3 (Delegation
// Policy and Lease)
func (s *Server) afterRecoveryEdge(ctx context.Context, row sessionstore.Session, disp FailureDisposition, err error) {
	if err != nil || disp.To == disp.From {
		return
	}
	if disp.To != session.StateResumePending && disp.To != session.StateAwaitingClientAction {
		return
	}
	s.expireRaisedElicitations(ctx, row)
	if disp.From == session.StateSuspended {
		s.rearmBudgetKeys(ctx, row)
	}
}

// applyFailureFromActive drives the §7.3 edge from a running,
// input_required, or suspended source state. Retryable causes with retry
// budget remaining write resume_pending and bump the retry counter;
// exhausted budgets write awaiting_client_action; non-retryable / unknown
// causes write failed with the §7.3 failure reason. The row's FailureReason
// is stamped so the §16.1 retry counter label and the §7.1 failed-row body
// carry the cause.
//
// It is the one place a failure report releases the session's pod binding,
// so every reporter gets the release. It takes the session's
// slot-accounting lock on both pool kinds before the edge and holds it
// through the release, so on this replica the funnel's identity check and
// release of the reported binding are one step against a resume attempt's
// releaseEarlierBinding, claim, and commit, and the funnel never tears down
// a replacement binding. Under the lock it snapshots the binding. On a
// committed edge it first counts a slot binding toward the pod's §5.2
// whole-pod replacement trigger, which can request the drain, and then
// releases with the failed disposition, so a drain is stamped before
// ReleaseSlot can reach the occupancy-zero recycle edge. The failed edge
// releases through the post-terminal hook after the lock is released. The
// resume_pending and awaiting_client_action edges release here, and write no
// coordination lease: the coordinating replica keeps REG-COORDLEASE, which
// its Sweeper renews in those states. A conflict or no-op runs neither step.
//
// spec: §7.3 (resume flow after pod failure; Credential leases on the
// replacement pod); §6.2 (pod crash during an active session); §5.2 (Failure
// isolation, whole-pod replacement trigger); §10.1.1 (one coordinating
// replica per session); §28.5.1 (CH-ATTACH Degradation).
func (s *Server) applyFailureFromActive(ctx context.Context, row sessionstore.Session, rep FailureReport,
	classification session.FailureClassification, maxRetries int,
) (FailureDisposition, error) {
	from := row.State
	unlock, err := s.slotAccountLocks.Lock(ctx, row.ID)
	if err != nil {
		return FailureDisposition{}, fmt.Errorf("sessionserver: wait for slot accounting of session %s: %w", row.ID, err)
	}
	defer unlock()
	bind := s.snapshotBinding(row.ID)
	slot := slotBinding(bind)

	retryable := classification == session.FailureRetryable
	budgetLeft := row.RetryCount < int64(maxRetries)

	var disp FailureDisposition
	switch {
	case retryable && budgetLeft:
		disp, err = s.transitionToResumePending(ctx, row, rep, classification, maxRetries)
	case retryable && !budgetLeft:
		disp, err = s.transitionToAwaitingClientAction(ctx, row, rep, classification, maxRetries, from)
	default:
		// NonRetryable / Unknown / Unclassified from active state →
		// failed per §6.2. The §7.3 default-platform list calls
		// these out as the "policy rejection" / "workspace validation"
		// terminal causes. The post-terminal hook releases the binding.
		return s.transitionToFailed(ctx, row, rep, classification, maxRetries, from, slot, unlock)
	}
	// No snapshot state equals a target state, so the edge committed
	// exactly when the row left the snapshot state.
	if err != nil || disp.To == from {
		return disp, err
	}
	s.accountFailedSlot(ctx, row, slot, rep.Reason, classification)
	s.releaseReportedBinding(ctx, row.ID, bind, executor.DispositionFailed)
	return disp, nil
}

// reportGuard is the failure funnel's locked-row guard. The row must still be
// in the report's source state. A suspended source must still name the
// reported pod and hold it, so a report on a suspended session whose pod a
// suspension released is a no-op. A resuming source must still be at the
// report's expected generation, so a report from an ended incarnation is a
// no-op. spec: §6.2 (Pod failure while suspended-without-pod is impossible);
// §7.3 (Resume driver).
func (s *Server) reportGuard(r sessionstore.Session, row sessionstore.Session, rep FailureReport) bool {
	if r.State != row.State {
		return false
	}
	switch row.State {
	case session.StateSuspended:
		return r.PodAssignment == row.PodAssignment && s.holdsPod(r)
	case session.StateResuming:
		return rep.ExpectedCoordinationGeneration != 0 && r.CoordinationGeneration == rep.ExpectedCoordinationGeneration
	default:
		return true
	}
}

// snapshotBinding reads the session's pod binding before the failure edge,
// or nil when the server has no pod registry or the session is unbound.
func (s *Server) snapshotBinding(sessionID string) *podsession.BindResult {
	if s.podRegistry == nil {
		return nil
	}
	bind, ok := s.podRegistry.Get(sessionID)
	if !ok {
		return nil
	}
	return bind
}

// slotBinding returns bind when it is a §5.2 concurrent-workspace slot
// binding, and nil otherwise.
func slotBinding(bind *podsession.BindResult) *podsession.BindResult {
	if bind == nil || bind.SlotID == "" {
		return nil
	}
	return bind
}

// releaseReportedBinding releases the binding bind, with disposition. With a
// pod registry it releases only when the registry still returns bind, so a
// binding a later bind of the session published, or a binding another path
// already removed, is left alone. Without a registry the executor owns the
// whole binding and is released unconditionally. It reports true only when it
// called the executor's release and that call returned nil. A release error
// is logged: the row has already committed and the release is best-effort.
// Its callers are the failure funnel (failed disposition), the suspension
// release, and the terminal binding release.
//
// spec: §7.3 (resume flow after pod failure); §5.2 (Failure isolation);
// §28.5.1 (CH-ATTACH Degradation).
func (s *Server) releaseReportedBinding(ctx context.Context, sessionID string, bind *podsession.BindResult, disposition executor.Disposition) bool {
	if s.podRegistry != nil {
		if bind == nil {
			return false
		}
		if cur, ok := s.podRegistry.Get(sessionID); !ok || cur != bind {
			return false
		}
	}
	if err := releaseExecutor(ctx, s.executor, sessionID, disposition); err != nil {
		log.Printf("sessionserver: release session %s binding: %v", sessionID, err)
		return false
	}
	return true
}

// applyFailureFromResuming drives the §6.2 mid-resume failure
// branching: non-retryable / unknown causes write awaiting_client_action
// (the resume cycle cannot continue); retryable causes with budget
// re-enter resume_pending; retryable-exhausted writes
// awaiting_client_action. The resuming watchdog has the same disposition
// on timeout, but a direct failure report shortcuts the timeout wait.
//
// Each exit takes effect only on the resuming incarnation the report names
// (FailureReport.ExpectedCoordinationGeneration) and writes no
// coordination_generation: the entry write of the next incarnation advances
// it. spec: §7.3 (Resume driver); §6.2 (resuming failure transitions).
func (s *Server) applyFailureFromResuming(ctx context.Context, row sessionstore.Session, rep FailureReport,
	classification session.FailureClassification, maxRetries int,
) (FailureDisposition, error) {
	from := row.State
	retryable := classification == session.FailureRetryable
	budgetLeft := row.RetryCount < int64(maxRetries)
	var disp FailureDisposition
	var err error
	switch {
	case retryable && budgetLeft:
		disp, err = s.transitionToResumePending(ctx, row, rep, classification, maxRetries)
	default:
		// Both retryable-exhausted and non-retryable from resuming go
		// to awaiting_client_action per §6.2.
		disp, err = s.transitionToAwaitingClientAction(ctx, row, rep, classification, maxRetries, from)
	}
	return disp, err
}

// transitionToResumePending writes from → resume_pending, bumps
// RetryCount, stamps the failure reason on the row, and fires the
// §16.1 retry counter + §11.7 audit row via recordSessionRetry.
func (s *Server) transitionToResumePending(ctx context.Context, row sessionstore.Session, rep FailureReport,
	classification session.FailureClassification, maxRetries int,
) (FailureDisposition, error) {
	updated, err := s.store.Update(ctx, row.TenantID, row.ID, func(r *sessionstore.Session) error {
		if !s.reportGuard(*r, row, rep) {
			return errReportConflict
		}
		r.State = session.StateResumePending
		r.RetryCount++
		r.FailureReason = rep.Reason
		if rep.PodAssignment != "" {
			r.PodAssignment = rep.PodAssignment
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, errReportConflict) {
			// A concurrent state change won; report the no-op
			// disposition.
			return FailureDisposition{
				Classification: classification, From: row.State, To: row.State,
				RetryCount: row.RetryCount, MaxRetries: maxRetries,
			}, nil
		}
		return FailureDisposition{}, err
	}
	s.emitStatusChange(updated.TenantID, updated.ID, updated.State)
	s.recordSessionRetry(ctx, updated)
	// spec: §7.2 — atomically drain the in-memory inbox to
	// the DLQ now that the session is recovering, so messages buffered
	// for it survive a coordinator crash during the resume window. In
	// durable mode the Redis inbox is left in place with an EXPIRE.
	// F-7.2.4.
	s.migrateInboxOnResumePending(ctx, updated)
	return FailureDisposition{
		Classification: classification, From: row.State, To: updated.State,
		RetryCount: updated.RetryCount, MaxRetries: maxRetries,
	}, nil
}

// transitionToAwaitingClientAction writes from → awaiting_client_action
// and fires the §7.3 webhook + §16.6 op event + §11.7 audit
// row via emitAwaitingClientActionEntered.
func (s *Server) transitionToAwaitingClientAction(ctx context.Context, row sessionstore.Session, rep FailureReport,
	classification session.FailureClassification, maxRetries int, from session.State,
) (FailureDisposition, error) {
	updated, err := s.store.Update(ctx, row.TenantID, row.ID, func(r *sessionstore.Session) error {
		if !s.reportGuard(*r, row, rep) {
			return errReportConflict
		}
		r.State = session.StateAwaitingClientAction
		r.FailureReason = rep.Reason
		if rep.PodAssignment != "" {
			r.PodAssignment = rep.PodAssignment
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, errReportConflict) {
			return FailureDisposition{
				Classification: classification, From: from, To: row.State,
				RetryCount: row.RetryCount, MaxRetries: maxRetries,
			}, nil
		}
		return FailureDisposition{}, err
	}
	s.emitAwaitingClientActionEntered(ctx, updated)
	return FailureDisposition{
		Classification: classification, From: from, To: updated.State,
		RetryCount: updated.RetryCount, MaxRetries: maxRetries,
	}, nil
}

// transitionToFailed writes from → failed for a non-retryable cause from
// an active (non-resuming) state per §6.2. It is a funnel terminal writer:
// it opens its terminal claim under the funnel's slot-accounting lock before
// its Update, its mutator applies the funnel's guard to the locked row and
// records the Terminated fact, and on a committed write the post-terminal
// hook runs through recordSessionCompleted, which releases the binding with
// the failed disposition. bind is the failure funnel's snapshot of a slot
// binding, or nil; a slot is counted toward the pod's §5.2 whole-pod
// replacement trigger after the commit and before that release.
//
// unlock releases the funnel's per-session slot-accounting lock. It runs
// once the accounting returns and before the post-terminal hook, so the hook
// never waits while holding that lock. unlock is idempotent; the funnel's
// deferred call covers the conflict and error returns. The claim ends on
// every path after the Update returns.
//
// spec: §6.2 (Pod State Machine); §7.2 (Interactive Session Model).
func (s *Server) transitionToFailed(ctx context.Context, row sessionstore.Session, rep FailureReport,
	classification session.FailureClassification, maxRetries int, from session.State,
	bind *podsession.BindResult, unlock func(),
) (FailureDisposition, error) {
	end := s.BeginTerminal(row.ID)
	defer end()
	now := s.clock()
	updated, err := s.store.Update(ctx, row.TenantID, row.ID, func(r *sessionstore.Session) error {
		if !s.reportGuard(*r, row, rep) {
			return errReportConflict
		}
		r.State = session.StateFailed
		r.FailureReason = rep.Reason
		r.FailureClass = failureClassForReason(rep.Reason)
		if rep.PodAssignment != "" {
			r.PodAssignment = rep.PodAssignment
		}
		sessionstore.MarkTerminated(r, from, now)
		return nil
	})
	if err != nil {
		if errors.Is(err, errReportConflict) {
			return FailureDisposition{
				Classification: classification, From: from, To: row.State,
				RetryCount: row.RetryCount, MaxRetries: maxRetries,
			}, nil
		}
		return FailureDisposition{}, err
	}
	// spec: §5.2 (whole-pod replacement trigger) — count the failed slot
	// and stamp any drain before recordSessionCompleted releases it.
	s.accountFailedSlot(ctx, row, bind, rep.Reason, classification)
	unlock()
	// spec: §4.6 — `from` is the pre-terminal state, so the terminal
	// pod-release path can distinguish a pre-running claimed session from
	// a handed-off running/resuming one.
	s.recordSessionCompleted(ctx, from, updated)
	return FailureDisposition{
		Classification: classification, From: from, To: updated.State,
		RetryCount: updated.RetryCount, MaxRetries: maxRetries,
	}, nil
}

// enterResumePending writes a recovering or podless suspended session into
// resume_pending with one Update guarded on the expected source state,
// row.State, and for suspended also on the locked row holding no pod. It
// leaves RetryCount unchanged: no retry is spent on a client resume or a
// message. It then migrates the session's inbox to the DLQ and emits the
// status change. A guard miss returns the §15.1 precondition error for the
// resume endpoint, with the locked row's state.
//
// spec: §7.3 (Retry and Resume; "Retry accounting."), §7.2 (Interactive
// Session Model)
func (s *Server) enterResumePending(ctx context.Context, row sessionstore.Session) (sessionstore.Session, error) {
	updated, err := s.store.Update(ctx, row.TenantID, row.ID, func(r *sessionstore.Session) error {
		if r.State != row.State || (r.State == session.StateSuspended && s.holdsPod(*r)) {
			return &session.PreconditionError{
				Endpoint:      session.EndpointResume,
				CurrentState:  r.State,
				AllowedStates: session.AllowedStates(session.EndpointResume, nil),
			}
		}
		r.State = session.StateResumePending
		return nil
	})
	if err != nil {
		return sessionstore.Session{}, err
	}
	s.migrateInboxOnResumePending(ctx, updated)
	s.emitStatusChange(updated.TenantID, updated.ID, updated.State)
	// spec: §8.3 (Delegation Policy and Lease) — every committed
	// enterResumePending re-arms the tree's delegation budget keys.
	s.rearmBudgetKeys(ctx, updated)
	return updated, nil
}

// errReportConflict is returned by the store-update closures when the
// locked row is no longer in the state the report read. The closures
// commit by compare-and-swap on that snapshot state, so a report that
// loses to any concurrent transition, a duplicate report included, gets
// the no-op FailureDisposition rather than re-running the edge's side
// effects. spec: §7.3, §7.2.
var errReportConflict = errors.New("sessionserver: concurrent state transition won")

// failureClassForReason maps the §7.3 failure label to the §7.1
// FailureClass enum that appears on the failed-row body. The mapping
// is coarse — the §7.1 enum is the closed catalogue, while the §7.3
// labels are the open per-runtime list. Reasons outside the closed
// catalogue degrade to FailureClassRuntime (the catch-all for in-pod
// runtime failure).
//
// spec: §7.1 failure-class table.
func failureClassForReason(reason string) session.FailureClass {
	switch reason {
	case string(session.FailureRuntimeCrash), string(session.FailurePodEvicted),
		string(session.FailureNodeLost):
		return session.FailureClassRuntime
	case string(session.FailureWorkspaceValidationFailed):
		return session.FailureClassWorkspaceSealTimeout
	case string(session.FailureSetupCommandFailed):
		return session.FailureClassRuntime
	default:
		return session.FailureClassRuntime
	}
}

// EffectiveMaxRetriesForRow resolves the §7.3 retry budget for a row.
// The per-session retryPolicy.maxRetries wins when set; otherwise the
// deployer cap; otherwise the §7.3 worked-example default. Mirrors the
// watchdog's effectiveMaxRetries semantics so the resuming-watchdog
// branch and the failure-reporting branch use the same budget. It is
// exported so every surface that reports retriesExhausted (the §8.8
// TaskResult error block and the §8.10 child_failed event) compares the
// row's RetryCount against the same budget the failure path enforced.
//
// spec: §7.3; F-6.2.14.
func EffectiveMaxRetriesForRow(row sessionstore.Session, caps session.RetryPolicyCaps) int {
	if row.RetryPolicy != nil && row.RetryPolicy.MaxRetries > 0 {
		return row.RetryPolicy.MaxRetries
	}
	if caps.MaxRetries > 0 {
		return caps.MaxRetries
	}
	return watchdog.DefaultMaxRetries
}

// EffectiveMaxRetries resolves a row's §7.3 retry budget against this
// server's deployer caps. It lets a surface outside this package, such
// as the MCP await path, report retriesExhausted against the budget the
// failure path enforces without holding its own copy of the caps.
//
// spec: §8.8 (TaskRecord and TaskResult Schema); §7.3.
func (s *Server) EffectiveMaxRetries(row sessionstore.Session) int {
	return EffectiveMaxRetriesForRow(row, s.retryPolicyCaps)
}
