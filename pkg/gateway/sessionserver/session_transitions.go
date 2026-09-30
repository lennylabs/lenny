// SPDX-License-Identifier: MIT

package sessionserver

import (
	"errors"
	"net/http"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// handleDelete implements DELETE /v1/sessions/{id} per §15.1: every
// non-terminal state transitions to cancelled.
func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
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
		Endpoint:     session.EndpointDelete,
		CurrentState: row.State,
	}); err != nil {
		s.writePreconditionError(w, err)
		return
	}
	fromState := row.State
	updated, err := s.store.Update(r.Context(), tenantID, id, func(row *sessionstore.Session) error {
		row.State = session.StateCancelled
		return nil
	})
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	// spec: §7.2 (a) — DELETE during the internal `resuming`
	// transient is the canonical resuming → cancelled snapshot-close
	// edge (§7.2). Bump coordination_generation in the same
	// logical write so any stale coordinator's subsequent RPC fails
	// the §4.2 CoordinatorFence check. F-7.1.14.
	if fromState == session.StateResuming {
		s.bumpCoordinationGenerationOnSnapshotClose(r.Context(), tenantID, id)
	}
	s.recordSessionCompleted(r.Context(), fromState, updated)
	s.writeSession(w, http.StatusOK, updated)
}

// handleTransition is the shared handler shape for every
// state-mutating endpoint that does not carry a body (finalize,
// start, interrupt, terminate, resume). The supplied transition
// function captures the next state.
func (s *Server) handleTransition(endpoint session.Endpoint, transition func(*sessionstore.Session)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
			Endpoint:     endpoint,
			CurrentState: row.State,
		}); err != nil {
			s.writePreconditionError(w, err)
			return
		}
		fromState := row.State
		updated, err := s.store.Update(r.Context(), tenantID, id, func(row *sessionstore.Session) error {
			transition(row)
			return nil
		})
		if err != nil {
			s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
			return
		}
		if session.IsTerminal(updated.State) {
			// spec: §7.2 (a) — when the terminal write
			// collapses an in-flight resume (resuming → cancelled /
			// completed / failed), bump coordination_generation in the
			// same logical write so any stale coordinator's subsequent
			// RPC fails the CoordinatorFence check. The pre-attach
			// counterparts (resume_pending → cancelled / completed,
			// §7.2) intentionally do NOT bump because no
			// pod is attached and no CoordinatorFence round-trip is
			// pending. F-7.1.14.
			if fromState == session.StateResuming {
				s.bumpCoordinationGenerationOnSnapshotClose(r.Context(), tenantID, id)
			}
			s.recordSessionCompleted(r.Context(), fromState, updated)
		} else {
			// spec: §7.2 — surface a non-terminal transition
			// (e.g. interrupt → suspended) on the SSE stream. Terminal
			// transitions emit status_change from recordSessionCompleted
			// so every terminal caller is covered uniformly.
			s.emitStatusChange(updated.TenantID, updated.ID, updated.State)
		}
		s.writeSession(w, http.StatusOK, updated)
	}
}

// transitionStart: per §15.1, /start transitions ready → starting →
// running. Short-circuits to running.
func transitionStart(row *sessionstore.Session) { row.State = session.StateRunning }

// transitionInterrupt: per §15.1, /interrupt transitions running →
// suspended.
func transitionInterrupt(row *sessionstore.Session) { row.State = session.StateSuspended }

// transitionTerminate: per §15.1, /terminate transitions any
// non-terminal → completed.
func transitionTerminate(row *sessionstore.Session) { row.State = session.StateCompleted }

// transitionResume: per §15.1, /resume transitions
// awaiting_client_action → resume_pending → running. The minimal
// gateway short-circuits to running.
func transitionResume(row *sessionstore.Session) { row.State = session.StateRunning }
