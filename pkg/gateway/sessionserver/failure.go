// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"fmt"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/watchdog"
)

// FailureReport is the §7.3 line 401 "Gateway detects session failure"
// payload. It is the single entry-point a pod-failure detector
// (controller-side Sandbox CR watcher, gateway-side adapter-call error
// path, manual operator drain) calls into the session server with;
// ReportSessionFailure runs the §7.3 line 402 classifier and writes the
// matching state-machine edge.
//
// spec: §7.3 lines 399-411.
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

// ReportSessionFailure runs the §7.3 line 402 failure classifier
// against the session's effective retry policy and drives the
// state-machine edge mandated by §7.3 lines 403-411 + §6.2 line 174:
//
//   - Retryable AND RetryCount < effective MaxRetries → resume_pending.
//     The §6.2 line 292 wall-clock cap is then enforced by the watchdog
//     sweep; on retry success the §15.1 /resume handler advances to
//     running. RetryCount is incremented in the same transaction and
//     the §16.1 lenny_session_retry_total counter + §11.7 audit row
//     fire from the existing recordSessionRetry helper.
//
//   - Retryable AND retries exhausted → awaiting_client_action (§7.3
//     line 411 "If retries exhausted → state becomes
//     awaiting_client_action"). The §7.3 line 427 webhook + §11.7
//     audit row fire via emitAwaitingClientActionEntered.
//
//   - NonRetryable from running / suspended / resume_pending → failed
//     (§6.2 line 174 "running ──→ failed (non-retryable error: OOM,
//     workspace validation, policy rejection)"). The terminal pipeline
//     runs (seal, executor release, cascade, billing, audit).
//
//   - NonRetryable from resuming → awaiting_client_action (§6.2 line 250
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
// on a session already in awaiting_client_action, and on a session in
// a pre-running state where the resume cycle has no meaning
// (created/finalizing/ready/starting). The watchdog's pre-running
// timeouts handle those paths via their own sweep.
//
// Best-effort: a store error returns the error so the caller can decide
// whether to retry. The metric / audit / SSE / cascade side effects are
// best-effort and never roll back the state transition. The method is
// safe to call concurrently — the store update is compare-and-swap on
// state so a duplicate report observes the second-write disposition.
//
// spec: §7.3 lines 399-411 (resume flow); §6.2 lines 174, 250 (failure
// branching); §7.3 line 427 (awaiting-action webhook); §16.1 retry
// metric; §11.7 / §16.7 retry audit. F-7.3.4 / F-7.3.5 / F-7.3.16.
func (s *Server) ReportSessionFailure(ctx context.Context, rep FailureReport) (FailureDisposition, error) {
	if rep.TenantID == "" || rep.SessionID == "" || rep.Reason == "" {
		return FailureDisposition{}, fmt.Errorf("%w: tenant=%q session=%q reason=%q",
			ErrFailureReportInvalid, rep.TenantID, rep.SessionID, rep.Reason)
	}
	row, err := s.store.Get(ctx, rep.TenantID, rep.SessionID)
	if err != nil {
		return FailureDisposition{}, err
	}
	disp := FailureDisposition{From: row.State, To: row.State, RetryCount: row.RetryCount}
	if session.IsTerminal(row.State) {
		// Terminal sessions are not re-classified — the §7.3 surface
		// reports failures observed while the row was non-terminal.
		return disp, nil
	}
	if row.State == session.StateAwaitingClientAction {
		// Already in the client-intervention holding state; the
		// reporter races a sibling detector. No further transition.
		return disp, nil
	}
	classification := session.ClassifyFailure(rep.Reason, row.RetryPolicy)
	disp.Classification = classification

	maxRetries := effectiveMaxRetriesForRow(row, s.retryPolicyCaps)
	disp.MaxRetries = maxRetries

	switch row.State {
	case session.StateRunning, session.StateInputRequired, session.StateSuspended,
		session.StateResumePending:
		return s.applyFailureFromActive(ctx, row, rep, classification, maxRetries)
	case session.StateResuming:
		return s.applyFailureFromResuming(ctx, row, rep, classification, maxRetries)
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

// applyFailureFromActive drives the §7.3 line 403/411 edge from a
// running / suspended / resume_pending source state. Retryable causes
// with retry budget remaining write resume_pending and bump the
// retry counter; exhausted budgets write awaiting_client_action;
// non-retryable / unknown causes write failed with the §7.3 failure
// reason. The row's FailureReason is stamped so the §16.1 retry counter
// label and the §7.1 failed-row body carry the cause.
func (s *Server) applyFailureFromActive(ctx context.Context, row sessionstore.Session, rep FailureReport,
	classification session.FailureClassification, maxRetries int,
) (FailureDisposition, error) {
	from := row.State

	retryable := classification == session.FailureRetryable
	budgetLeft := row.RetryCount < int64(maxRetries)

	switch {
	case retryable && budgetLeft:
		return s.transitionToResumePending(ctx, row, rep, classification, maxRetries)
	case retryable && !budgetLeft:
		return s.transitionToAwaitingClientAction(ctx, row, rep, classification, maxRetries, from)
	default:
		// NonRetryable / Unknown / Unclassified from active state →
		// failed per §6.2 line 174. The §7.3 default-platform list calls
		// these out as the "policy rejection" / "workspace validation"
		// terminal causes.
		return s.transitionToFailed(ctx, row, rep, classification, maxRetries, from)
	}
}

// applyFailureFromResuming drives the §6.2 line 250 mid-resume failure
// branching: non-retryable / unknown causes write awaiting_client_action
// (the resume cycle cannot continue); retryable causes with budget
// re-enter resume_pending; retryable-exhausted writes
// awaiting_client_action. The resuming watchdog has the same disposition
// on timeout, but a direct failure report shortcuts the timeout wait.
//
// spec: §7.2 line 214 (a) — every exit from `resuming` that aborts the
// in-flight resume bumps coordination_generation so any stale
// coordinator's subsequent RPC fails the §4.2 CoordinatorFence check.
// Both branches (re-enter resume_pending, write awaiting_client_action)
// abort the in-flight resume's restoration RPCs, so both bump. F-7.1.14.
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
		// to awaiting_client_action per §6.2 line 250.
		disp, err = s.transitionToAwaitingClientAction(ctx, row, rep, classification, maxRetries, from)
	}
	if err == nil && disp.From == session.StateResuming && disp.To != session.StateResuming {
		s.bumpCoordinationGenerationOnSnapshotClose(ctx, row.TenantID, row.ID)
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
		if r.State == session.StateResumePending {
			return nil
		}
		if !legalReportTransition(r.State, session.StateResumePending) {
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
			// A concurrent terminal / awaiting-action write won; report
			// the no-op disposition.
			return FailureDisposition{
				Classification: classification, From: row.State, To: row.State,
				RetryCount: row.RetryCount, MaxRetries: maxRetries,
			}, nil
		}
		return FailureDisposition{}, err
	}
	s.emitStatusChange(updated.TenantID, updated.ID, updated.State)
	s.recordSessionRetry(ctx, updated)
	// spec: §7.2 lines 305-311 — atomically drain the in-memory inbox to
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
// and fires the §7.3 line 427 webhook + §16.6 op event + §11.7 audit
// row via emitAwaitingClientActionEntered.
func (s *Server) transitionToAwaitingClientAction(ctx context.Context, row sessionstore.Session, rep FailureReport,
	classification session.FailureClassification, maxRetries int, from session.State,
) (FailureDisposition, error) {
	updated, err := s.store.Update(ctx, row.TenantID, row.ID, func(r *sessionstore.Session) error {
		if r.State == session.StateAwaitingClientAction {
			return nil
		}
		if !legalReportTransition(r.State, session.StateAwaitingClientAction) {
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
// an active (non-resuming) state per §6.2 line 174. The terminal hook
// pipeline runs via recordSessionCompleted.
func (s *Server) transitionToFailed(ctx context.Context, row sessionstore.Session, rep FailureReport,
	classification session.FailureClassification, maxRetries int, from session.State,
) (FailureDisposition, error) {
	updated, err := s.store.Update(ctx, row.TenantID, row.ID, func(r *sessionstore.Session) error {
		if session.IsTerminal(r.State) {
			return nil
		}
		if !legalReportTransition(r.State, session.StateFailed) {
			return errReportConflict
		}
		r.State = session.StateFailed
		r.FailureReason = rep.Reason
		r.FailureClass = failureClassForReason(rep.Reason)
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
	if updated.State == session.StateFailed {
		// spec: §4.6 — `from` is the pre-terminal state, so the terminal
		// pod-release path can distinguish a pre-running claimed session from
		// a handed-off running/resuming one.
		s.recordSessionCompleted(ctx, from, updated)
	}
	return FailureDisposition{
		Classification: classification, From: from, To: updated.State,
		RetryCount: updated.RetryCount, MaxRetries: maxRetries,
	}, nil
}

// errReportConflict is returned by the store-update closures when a
// concurrent transition has already taken the row to a state from which
// the requested transition is not legal. Callers translate it to a no-op
// FailureDisposition that the caller can log.
var errReportConflict = errors.New("sessionserver: concurrent state transition won")

// legalReportTransition reports whether the §7.2 state machine permits
// from → to. The session.IsValid table is mirrored here as the source
// of truth; using the dedicated state package would force an import
// cycle because the controller-side state package already imports
// pkg/api/v1/session. Keeping the table in api/v1/session as the canonical
// source means we read it through that import here.
func legalReportTransition(from, to session.State) bool {
	switch to {
	case session.StateResumePending:
		switch from {
		case session.StateRunning, session.StateInputRequired,
			session.StateSuspended, session.StateResuming,
			session.StateAwaitingClientAction:
			return true
		}
	case session.StateAwaitingClientAction:
		switch from {
		case session.StateResumePending, session.StateResuming,
			session.StateRunning, session.StateInputRequired,
			session.StateSuspended:
			return true
		}
	case session.StateFailed:
		switch from {
		case session.StateCreated, session.StateFinalizing,
			session.StateReady, session.StateStarting,
			session.StateRunning, session.StateInputRequired,
			session.StateSuspended, session.StateResumePending,
			session.StateResuming:
			return true
		}
	}
	return false
}

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

// effectiveMaxRetriesForRow resolves the §7.3 retry budget for a row.
// The per-session retryPolicy.maxRetries wins when set; otherwise the
// deployer cap; otherwise the §7.3 worked-example default. Mirrors the
// watchdog's effectiveMaxRetries semantics so the resuming-watchdog
// branch and the failure-reporting branch use the same budget.
//
// spec: §7.3 line 382; F-6.2.14.
func effectiveMaxRetriesForRow(row sessionstore.Session, caps session.RetryPolicyCaps) int {
	if row.RetryPolicy != nil && row.RetryPolicy.MaxRetries > 0 {
		return row.RetryPolicy.MaxRetries
	}
	if caps.MaxRetries > 0 {
		return caps.MaxRetries
	}
	return watchdog.DefaultMaxRetries
}
