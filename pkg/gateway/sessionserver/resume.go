// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"log"
	"net/http"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// handleResume implements POST /v1/sessions/{id}/resume per §15.1 and
// §7.1. The endpoint is valid only from `awaiting_client_action` — the
// state a session reaches after automatic resume retries are exhausted
// or the resume window elapses (§7.2). A session in that state has no
// live pod, so the handler restores the session onto a fresh §5 warm
// pod from its latest §7.1 WorkspaceSnapshot before the row
// transitions to running. The API-reported transition is
// `resume_pending` → `running`; the `resume_pending` and `resuming`
// states between are internal transients.
//
// handleResume is a dedicated handler rather than a generic
// handleTransition because the resume carries the extra pod-claim and
// workspace-restore step.
func (s *Server) handleResume(w http.ResponseWriter, r *http.Request) {
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
		Endpoint:     session.EndpointResume,
		CurrentState: row.State,
	}); err != nil {
		s.writePreconditionError(w, err)
		return
	}

	// A session in `awaiting_client_action` has no live pod: it reached
	// that state after its pod failed and automatic recovery was
	// abandoned. When the gateway is wired with a pod binder, restore
	// the session onto a fresh pod before the row transitions to
	// running. A claim failure is reported as a retryable 503. Transient
	// pool/credential exhaustion keeps the row in awaiting_client_action
	// so the client can re-issue `POST /resume` once pods are available;
	// only a non-retryable cause demotes the row to failed. F-7.3.23.
	//
	// spec: §7.3 — internally the row traverses
	// `awaiting_client_action → resume_pending → resuming → running`;
	// the API view collapses to `awaiting_client_action → running`.
	// Writing the `resuming` transient before resumeOnPod makes the
	// §7.2 mid-resume terminal-collapse edges
	// (resuming → cancelled, resuming → completed) reachable by
	// concurrent DELETE / cascade / failure-report observers, and the
	// §6.2 watchdog uses it as the entry signal for the
	// resuming wall-clock timeout. F-7.3.8.
	if s.podBinder != nil {
		if _, err := s.store.Update(r.Context(), tenantID, id, func(row *sessionstore.Session) error {
			row.State = session.StateResuming
			return nil
		}); err != nil {
			s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
			return
		}
	}
	var adapterReportedResumeMode string
	if s.podBinder != nil {
		mode, err := s.resumeOnPod(r.Context(), row)
		if err != nil {
			// spec: §16.1 catalog — record the failed resume attempt
			// before unwinding so the {pool, outcome="failure"} counter
			// advances even when the row transitions straight to failed.
			// F-7.3.10.
			if s.incSessionResumeAttempt != nil {
				s.incSessionResumeAttempt(row.PoolRef, "failure")
			}
			// spec: §7.2 (a) — the row was in `resuming` when
			// the resumeOnPod call started; bump the
			// coordination_generation before unwinding so any stale
			// coordinator's subsequent RPC fails the §4.2
			// CoordinatorFence check. F-7.1.14 / F-7.3.8.
			s.bumpCoordinationGenerationOnSnapshotClose(r.Context(), tenantID, id)
			s.holdOrFailOnResumeError(r.Context(), tenantID, id, err)
			// spec: §7.3 — a resume claim failure surfaces as a retryable
			// 503; the row was already persisted (resume requires it), so
			// `RESUME_FAILED` is the analogous spec-named fallback to
			// `SESSION_CREATION_FAILED` and `STARTING_FAILED`. The
			// underlying transient codes (WARM_POOL_EXHAUSTED,
			// CREDENTIAL_POOL_EXHAUSTED, etc.) are surfaced directly so
			// the client receives a spec-defined retry hint.
			s.writePodClaimError(w, err, "RESUME_FAILED", "could not resume the session on a warm pod")
			return
		}
		adapterReportedResumeMode = mode
	}

	updated, err := s.store.Update(r.Context(), tenantID, id, func(row *sessionstore.Session) error {
		transitionResume(row)
		return nil
	})
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	// spec: §16.1 catalog — every resume call increments the
	// `lenny_session_resume_attempts_total{pool, outcome="success"}`
	// counter; the matching {outcome="failure"} branch fires above on
	// the resumeOnPod error path. F-7.3.10.
	if s.incSessionResumeAttempt != nil {
		s.incSessionResumeAttempt(updated.PoolRef, "success")
	}
	// spec: §11.7 / §16.7 — the session.resumed audit row is appended
	// after every successful resume so SIEM dashboards can filter on
	// the §7.3 recovery surface. The matching SSE `session.resumed`
	// event below is the client-facing signal. F-7.3.18.
	if s.lifecycleAudit != nil {
		s.lifecycleAudit.EmitSessionLifecycle(r.Context(), SessionLifecycleEvent{
			EventType:  auditSessionResumed,
			TenantID:   updated.TenantID,
			SessionID:  updated.ID,
			UserID:     updated.UserID,
			RuntimeRef: updated.RuntimeRef,
			State:      string(updated.State),
			At:         s.clock(),
		})
	}
	// spec: §7.2 — surface the resume transition (the
	// resume_pending → resuming → running chain collapses to the
	// resolved state) before the richer session.resumed event below.
	s.emitStatusChange(updated.TenantID, updated.ID, updated.State)
	// spec: §7.2 — the resume completes the v1 coordinator
	// re-acquisition of a recovering session, so the gateway emits
	// `inbox_cleared` on the target's own stream: the in-memory inbox from
	// the prior coordinator is gone and the client learns how many messages
	// survived in the DLQ. F-7.2.12.
	s.clearInboxOnResume(r.Context(), updated)
	// spec: §4.4 — partial-manifest cleanup runs on every
	// resume regardless of whether the underlying reassembly
	// succeeded. The cleaner deletes the chunk objects and
	// soft-deletes the manifest row under the `deleted_at IS NULL`
	// guard; failures leave the row active for the §12.5 backstop
	// sweep to retry.
	if s.partialManifestCleaner != nil {
		if cerr := s.partialManifestCleaner.CleanupAfterResume(r.Context(), tenantID, id); cerr != nil {
			// Cleanup failure is non-fatal: the resume already
			// completed and the row stays active for the
			// backstop sweep. Surface the error in logs only.
			log.Printf("sessionserver: partial-manifest cleanup for session %s failed: %v", id, cerr)
		}
	}
	// spec: §7.2 — `session.resumed` precedes
	// `children_reattached`. The event fires from the resume handler
	// (rather than from resumeOnPod) so dev-mode / unit-test
	// deployments without a pod binder still emit the event. The
	// adapter-reported mode (when present) is fed into classifyResume so
	// gateway-side eviction / partial-manifest state can still upgrade
	// it to a stronger label, but a plain `full` adapter signal cannot
	// silently override a `conversation_only` gateway classification.
	// F-7.3.22.
	mode := s.classifyResumeWithAdapter(r.Context(), updated, adapterReportedResumeMode)
	s.emitResumedEvent(r.Context(), updated, mode)
	s.emitChildrenReattached(r.Context(), tenantID, id)
	// spec: §8.10 — recover the resumed tree's orphaned
	// descendants bottom-up so that "by the time a parent resumes, its
	// children are already in a known state". Detached from the request
	// because the traversal is bounded by maxTreeRecoverySeconds, not by
	// the HTTP deadline. A leaf resume (no descendants) is a cheap
	// no-op.
	s.recoverDelegationTree(r.Context(), tenantID, s.treeRoot(r.Context(), updated))
	s.writeSession(w, http.StatusOK, updated)
}

// holdOrFailOnResumeError reconciles the §7.2 `resuming` row with the
// resume failure: a transient cause (per isTransientPodClaimError) reverts
// the row to `awaiting_client_action` so the explicit `POST /resume` retry
// can succeed once the condition clears, while a non-retryable cause demotes
// the row to terminal `failed`. The boundary is the same
// codes.FailedPrecondition split writeSetupCommandError uses for the wire
// envelope, so a row that returns the retryable RESUME_FAILED envelope is
// never demoted to a terminal state the retry would be rejected against.
// spec: §7.3, §6.2 (transient
// setup failure retried on a fresh pod). F-7.3.23.
func (s *Server) holdOrFailOnResumeError(ctx context.Context, tenantID, id string, err error) {
	if isTransientPodClaimError(err) {
		if _, uerr := s.store.Update(ctx, tenantID, id, func(row *sessionstore.Session) error {
			row.State = session.StateAwaitingClientAction
			return nil
		}); uerr != nil {
			log.Printf("sessionserver: revert resuming → awaiting_client_action for session %s: %v", id, uerr)
		}
		return
	}
	s.failSession(ctx, tenantID, id)
}
