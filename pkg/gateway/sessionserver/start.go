// SPDX-License-Identifier: MIT

package sessionserver

import (
	"errors"
	"net/http"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// handleStart implements POST /v1/sessions/{id}/start per §15.1: the
// explicit launch transition of the two-step create → finalize → start
// lifecycle. It transitions a ready session to running and, when the gateway
// is wired with a pod binder, launches the runtime on the pod prepared at
// /finalize using the §14 WorkspacePlan stored at create.
//
// Per the proposal §4.4, /start is launch-only: the §4.3 preparation barrier
// (staging, workspace materialization, setup commands, and credential-lease
// assignment) already ran at /finalize, so /start neither claims a pod nor
// re-runs any of that work. It reconnects to the prepared pod and runs only
// the §6.3 agent_session_start phase (StartSession for a pod-warm pod,
// ConfigureWorkspace for an SDK-warm one) through launchOnPod. The exception
// is a concurrent-workspace pool, whose reserved slot materializes and
// launches together at /start (§5.2); launchOnPod owns that distinction.
//
// handleStart is a dedicated handler rather than a generic handleTransition
// because the start transition carries the extra launch step — the same
// reason handleFinalize is dedicated for the finalize transition. The launch
// runs before the row transitions: a launch failure leaves the row ready so
// the client can retry POST /start.
//
// spec: §4.4, §4.6 (proposal); §15.1 (/start precondition); §6.1.
func (s *Server) handleStart(w http.ResponseWriter, r *http.Request) {
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
		Endpoint:     session.EndpointStart,
		CurrentState: row.State,
	}); err != nil {
		s.writePreconditionError(w, err)
		return
	}

	if s.podBinder != nil {
		plan, err := storedWorkspacePlan(row)
		if err != nil {
			s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
				"stored workspace plan could not be parsed: "+err.Error(), nil)
			return
		}
		// spec: §4.4, §4.6 (proposal) — /start is launch-only: launchOnPod
		// reconnects to the pod prepared at /finalize and runs only the §6.3
		// agent_session_start phase (StartSession or ConfigureWorkspace),
		// without re-staging, re-materializing, re-running setup, or
		// re-assigning the credential lease, and without any §4.9 credential
		// resolution. The exclusive-pool launch needs no credential inputs (the
		// lease was assigned at /finalize); only the concurrent-pool slot
		// launch, which materializes at /start, resolves them.
		result, err := s.launchOnPod(r.Context(), row, plan)
		if err != nil {
			// spec: §7.1 / §6.2 — `POST /v1/sessions/{id}/start`
			// returns `STARTING_FAILED` when the §7.1 atomic-creation unit
			// fails on the explicit launch half. The row stays `ready` so the
			// client can retry; the binder already reclaimed the prepared pod
			// (and the lease assigned at /finalize) on the launch failure path.
			s.writePodClaimError(w, err, "STARTING_FAILED", "could not place the session on a warm pod")
			return
		}
		// spec: §4.6.1 (coordinating replica holds the lease), §10.1
		// (per-session coordination lease) — the row is still `ready` here
		// (the running-commit follows below), so the at-bind acquire inside
		// registerBinding precedes the running-commit. On ErrHeld a live
		// foreign holder still coordinates this session, so publish nothing,
		// release this replica's freshly launched pod, and fail the start
		// closed rather than double-bind. The binding carries the generation
		// of the row read above (spec: §10.1.1, §10.1.5).
		stampBindingGeneration(result, row.CoordinationGeneration)
		if err := s.registerBinding(r.Context(), result); err != nil {
			s.rollbackBinding(r.Context(), result)
			s.writePodClaimError(w, err, "STARTING_FAILED", "could not place the session on a warm pod")
			return
		}
		// spec: §7.5 — persist the per-command setup output a
		// concurrent-workspace slot launch captured so a subsequent GET
		// /v1/sessions/{id} can surface it. The exclusive-pool launch-only path
		// produces no setup output (setup ran at /finalize, persisted there by
		// applyFinalizePrepareResult), so this fires only for the concurrent
		// slot launch that materializes at /start. F-7.5.4.
		if result != nil && len(result.SetupOutputs) > 0 {
			outs := setupOutputsFromBind(result.SetupOutputs)
			if _, uerr := s.store.Update(r.Context(), tenantID, id, func(rr *sessionstore.Session) error {
				rr.SetupOutput = outs
				return nil
			}); uerr != nil {
				// Persistence failure is non-fatal; the §7.5 trail is
				// best-effort. Log via the diagnostics path.
				s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", uerr.Error(), nil)
				return
			}
		}
	}

	updated, err := s.store.Update(r.Context(), tenantID, id, func(row *sessionstore.Session) error {
		transitionStart(row)
		return nil
	})
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	// spec: §7.2 — surface the ready → running transition.
	s.emitStatusChange(updated.TenantID, updated.ID, updated.State)
	s.writeSession(w, http.StatusOK, updated)
}
