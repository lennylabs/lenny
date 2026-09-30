// SPDX-License-Identifier: MIT

package sessionserver

import (
	"errors"
	"log"
	"net/http"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// transitionFinalizing: per §15.1, /finalize begins the §7.1 steps 11-13
// prepare phase by transitioning created → finalizing. The workspace
// materialization, setup commands, and credential-lease assignment then
// run while the row is `finalizing`; handleFinalize transitions
// finalizing → ready only once the session is fully prepared. The caller
// applies it inside the locked Update, after re-checking the finalize
// precondition against the locked row.
// spec: §7.1 steps 11-13, §15.1
func transitionFinalizing(row *sessionstore.Session) { row.State = session.StateFinalizing }

// transitionReady: per §15.1, /finalize transitions finalizing → ready
// once the §7.1 steps 11-13 prepare phase (workspace materialization,
// setup commands, and credential assignment) has completed; the session
// then awaits /start.
// spec: §7.1 steps 11-13, §15.1
func transitionReady(row *sessionstore.Session) { row.State = session.StateReady }

// handleFinalize implements POST /v1/sessions/{id}/finalize. After binding
// the optional §14 WorkspacePlan and transitioning created → finalizing, it
// reconnects to the pod claimed at
// /create and runs the §7.1 step 11-13 prepare phase against it:
// PrepareWorkspace streams the buffered lenny-blob:// upload content into
// the session's staging tree, FinalizeWorkspace materializes that
// session's current tree with the §7.4 post-promotion symlink re-validation, RunSetup runs the
// plan's setup commands, and AssignCredentials delivers the §4.9
// credential lease. Only when the session is fully prepared does it
// transition finalizing → ready and return.
//
// Admission is a compare-and-swap: the created → finalizing write re-checks
// the §15.1 finalize precondition against the locked row inside the store
// Update, so of overlapping calls that all read `created` before either
// write, exactly one commits `finalizing` and every other call answers
// 409 INVALID_STATE_TRANSITION before any pod RPC, reclaim, failure write,
// or WorkspacePlan write. The pre-lock Get and Validate remain as an early
// rejection, because resolveFinalizePlan needs the row.
//
// A failure in any prepare step reclaims the claimed pod via the §6.2
// pre-attached disposition (the binder's failPhase or reconnect reclaim, or
// prepareAtFinalize's own reclaim before Prepare runs), revokes any lease
// recorded under the session, and surfaces the failure through
// writePodClaimError (§6.2 client visibility). A
// deterministic setup-command exit surfaces as the non-retryable 422
// SETUP_COMMAND_FAILED, and a transient setup-window failure as the
// retryable fallback. A credential failure surfaces its own envelope:
// a lease-assignment failure, and a finalize-time credential availability
// miss that mapFinalizeCredentialMismatch remaps to the §4.9
// check-to-assignment mismatch, surface as CREDENTIAL_POOL_EXHAUSTED. A
// workspace-materialization failure surfaces as the retryable 503
// SESSION_CREATION_FAILED fallback with Retry-After, except a §13.4 archive
// validator violation, which surfaces as the non-retryable 413
// UPLOAD_ARCHIVE_LIMIT_EXCEEDED. The row transitions
// finalizing → failed so a client cannot retry finalize against a pod
// that no longer exists.
//
// The finalizing → ready write and every failure write admit only a row
// that is still `finalizing`. A terminate, DELETE, or the finalizing
// watchdog can end the session while the prepare phase runs; the call then
// keeps that terminal state, revokes any lease recorded under the session,
// deletes no claim (the terminal writer's reclaim owns the pod), and
// answers 409 INVALID_STATE_TRANSITION with the terminal state, even when
// its own prepare phase also failed. A ready write that fails with a store
// error after AssignCredentials succeeded reclaims the pod and revokes the
// lease, so a post-assignment finalize failure does not leak the lease.
//
// The single-use uploadToken invalidation, upload-channel/limits close,
// SSE status-change, plan-warning publish, and the §16.6 finalize audit
// row run once the row reaches ready, as before. The minimal gateway (no
// pod binder) finalizes by the plain created → finalizing → ready
// transition with no pod work.
//
// spec: §7.1 steps 11-13; §7.1 step 23 (lease release); §7.4; §15.1
// (finalize precondition, State-mutating endpoint preconditions); §6.2
// (finalize timeout); §7.2 (terminal states); §4.9 (finalize lease
// assignment).
func (s *Server) handleFinalize(w http.ResponseWriter, r *http.Request) {
	tenantID := s.resolveTenant(r)
	id := r.PathValue("id")
	row, err := s.store.Get(r.Context(), tenantID, id)
	if err != nil {
		if errors.Is(err, sessionstore.ErrNotFound) {
			s.writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "session not found", nil)
			return
		}
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if err := session.Validate(session.PreconditionRequest{
		Endpoint:     session.EndpointFinalize,
		CurrentState: row.State,
	}); err != nil {
		s.writePreconditionError(w, err)
		return
	}
	// spec: §7.1 step 11 (FinalizeWorkspace); §26.2 — the
	// §14 WorkspacePlan referencing this session's staged uploadArchive
	// blob is bound here in the decomposed create → upload → finalize
	// flow, because the create-time plan is immutable and cannot name an
	// uploadRef minted only after the session exists. A no-body finalize
	// keeps the existing plan (or empty workspace). F-24.17.4 / F-26.2.4.
	planJSON, planWarnings, hasPlan, planOK := s.resolveFinalizePlan(w, r, tenantID, row)
	if !planOK {
		return
	}
	// spec: §15.1 (finalize precondition); §7.1 steps 11-13. The pre-lock
	// Validate above is an early rejection. This check against the locked row
	// is authoritative, so of overlapping calls that both read `created`,
	// exactly one commits `finalizing`, binding the finalize plan in the same
	// write. The prepare phase runs while the row is `finalizing`.
	updated, err := s.store.Update(r.Context(), tenantID, id, func(row *sessionstore.Session) error {
		if err := session.Validate(session.PreconditionRequest{
			Endpoint:     session.EndpointFinalize,
			CurrentState: row.State,
		}); err != nil {
			return err
		}
		transitionFinalizing(row)
		if hasPlan {
			row.WorkspacePlan = planJSON
		}
		return nil
	})
	if errors.Is(err, sessionstore.ErrNotFound) {
		s.writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "session not found", nil)
		return
	}
	if err != nil {
		// A refused compare-and-swap renders 409 with the locked state; any
		// other store error renders 500 and leaves the row `created`.
		s.writePreconditionError(w, err)
		return
	}
	// spec: §7.1 steps 11-13 — run the prepare phase against the pod
	// claimed at /create: stream the buffered uploads into the session's
	// staging tree, materialize its current tree, run setup commands, and assign the §4.9
	// credential lease. prepareAtFinalize returns (nil, nil) for the
	// dispositions that materialize nothing at finalize (no binder, service
	// mode, a concurrent-workspace slot, or a row with no live binding).
	plan, perr := storedWorkspacePlanForFinalize(updated, hasPlan, planJSON)
	if perr != nil {
		s.finalizePlanParseFailed(w, r, tenantID, id, updated.PodAssignment, perr)
		return
	}
	prep, err := s.prepareAtFinalize(r.Context(), updated, plan)
	if err != nil {
		s.finalizePrepareFailed(w, r, tenantID, id, err)
		return
	}
	// spec: §7.1 steps 11-13 — only after the prepare phase succeeds does the
	// finalizing → ready write run. Persist the §7.5 setup-command trail and
	// the §7.3 negotiated workspace root the prepare phase produced. These
	// persists write no state, so they stay unguarded.
	if prep != nil {
		s.applyFinalizePrepareResult(r.Context(), tenantID, id, updated.TenantID, updated.ID, prep)
	}
	// Capture the pod↔session binding from the finalizing-write result before
	// the ready write below: a failed Update returns the zero Session, so
	// reading PodAssignment off its result would lose the binding the
	// store-error branch reclaims.
	podAssignment := updated.PodAssignment
	uploadTokenDigest := updated.UploadTokenDigest
	uploadTokenExpiry := updated.UploadTokenExpiry
	// spec: §15.1 (finalize row), §6.2 (finalize timeout), §7.2 (terminal
	// states) — finalizing → ready admits only a row that is still
	// `finalizing`, so a terminal state another writer committed while the
	// prepare phase ran is kept.
	updated, err = s.store.Update(r.Context(), tenantID, id, func(row *sessionstore.Session) error {
		if err := finalizingPrecondition(row); err != nil {
			return err
		}
		transitionReady(row)
		return nil
	})
	if err != nil {
		s.finalizeReadyWriteFailed(w, r, tenantID, id, podAssignment, prep != nil, err)
		return
	}
	// spec: §7.1 (single-use uploadToken invalidation) — once the upload
	// window closes, the digest cannot mint another upload. ConsumeDigest
	// fails only when the digest is already invalidated, so a failure is
	// logged and the call continues: the session is `ready`, and a token left
	// unconsumed mints no upload because uploads are admitted only in
	// `created`.
	if s.uploadVerifier != nil && uploadTokenDigest != "" {
		if cerr := s.uploadVerifier.ConsumeDigest(uploadTokenDigest, uploadTokenExpiry); cerr != nil {
			log.Printf("sessionserver: consume upload token for finalized session %s: %v", id, cerr)
		}
	}
	// §7.4: close the upload channel — abort any in-flight
	// /upload stream for this session so it surfaces
	// UPLOAD_CHANNEL_CLOSED and its staged blob is rolled back. A
	// late /upload register that races finalize gets an
	// already-closed abort signal on the next Read. F-7.4.16.
	s.uploadAborts.closeSession(updated.ID)
	// §11.1: the upload window has closed, so drop the
	// per-session cumulative upload-byte total. In-flight concurrency
	// slots self-release; only the byte total is freed here. F-11.1.6.
	s.uploadLimits.closeSession(updated.ID)
	// spec: §7.2 — surface the finalizing → ready transition that
	// closed the §7.1 steps 11-13 prepare phase.
	s.emitStatusChange(updated.TenantID, updated.ID, updated.State)
	// spec: §14 — surface any consumer-advisory parse
	// warnings the finalize-bound plan raised on the same per-session SSE
	// bus the create path uses. F-24.17.4 / F-26.2.4.
	if hasPlan {
		s.publishParsePlanWarnings(updated.TenantID, updated.ID, planWarnings)
	}
	// F-7.4.17: §16.6 session.finalize_workspace audit row. The row
	// records the finalize transition and the consumption of the
	// single-use uploadToken so SIEM post-incident review can join the
	// upload-token consumption event to the session lifecycle. Detail
	// carries the persisted digest so SOC analysts can correlate the
	// audit row with the rejected /upload calls that follow it.
	// spec: §16.6; §7.1; §11.7. F-7.4.17.
	if s.lifecycleAudit != nil {
		s.lifecycleAudit.EmitSessionLifecycle(r.Context(), SessionLifecycleEvent{
			EventType:  auditSessionWorkspaceFinalized,
			TenantID:   updated.TenantID,
			SessionID:  updated.ID,
			UserID:     updated.UserID,
			RuntimeRef: updated.RuntimeRef,
			State:      string(updated.State),
			Detail:     updated.UploadTokenDigest,
			At:         s.clock(),
		})
	}
	s.writeSession(w, http.StatusOK, updated)
}
