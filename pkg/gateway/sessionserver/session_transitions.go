// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"net/http"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// handleDelete implements DELETE /v1/sessions/{id} per §15.1: every
// non-terminal state, resuming included, transitions to cancelled. The entry
// read answers 404 and the precondition from the read pool; the write
// re-validates on the locked row, so a row another writer made terminal after
// the read answers the precondition error and runs no post-terminal hook. The
// write records the Terminated fact, and a delete of a resuming row bumps
// coordination_generation in the same write, so the in-flight restore's
// stale RPCs fail their fence. fromState is the locked row's state.
//
// spec: §7.2 (Interactive Session Model), §15.1 (REST API)
func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	s.handleTerminalWrite(w, r, session.EndpointDelete, func(row *sessionstore.Session) {
		row.State = session.StateCancelled
	})
}

// handleTransition is the shared handler for a state-mutating endpoint that
// carries no body. Only POST /terminate is registered on it today. It
// answers 404 and the precondition from the entry read, re-validates the
// endpoint's precondition on the locked row inside the Update, and on a
// terminal write records the Terminated fact and runs the post-terminal hook
// with fromState from the locked row. Terminate admits every non-terminal
// state except resuming, so a restore that moved the row to resuming after
// the entry read gets no terminate write.
//
// spec: §7.2 (Interactive Session Model), §15.1 (REST API)
func (s *Server) handleTransition(endpoint session.Endpoint, transition func(*sessionstore.Session)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.handleTerminalWrite(w, r, endpoint, transition)
	}
}

// handleTerminalWrite runs one REST state write: the entry read, the guarded
// Update that re-validates the endpoint on the locked row, and the
// post-terminal hook when the write made the row terminal. The terminal
// claim opens before the Update and ends after the hook, or at once on a
// refusal or store error.
func (s *Server) handleTerminalWrite(w http.ResponseWriter, r *http.Request, endpoint session.Endpoint, transition func(*sessionstore.Session)) {
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
	end := s.BeginTerminal(id)
	defer end()
	var fromState session.State
	now := s.clock()
	updated, err := s.store.Update(r.Context(), tenantID, id, func(locked *sessionstore.Session) error {
		if verr := session.Validate(session.PreconditionRequest{
			Endpoint:     endpoint,
			CurrentState: locked.State,
		}); verr != nil {
			return verr
		}
		fromState = locked.State
		transition(locked)
		sessionstore.MarkTerminated(locked, fromState, now)
		return nil
	})
	if err != nil {
		var pe *session.PreconditionError
		if errors.As(err, &pe) {
			s.writePreconditionError(w, err)
			return
		}
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if session.IsTerminal(updated.State) {
		s.recordSessionCompleted(context.WithoutCancel(r.Context()), fromState, updated)
	} else {
		// spec: §7.2 — surface a non-terminal transition on the SSE stream.
		// Terminal transitions emit status_change from the post-terminal hook
		// so every terminal caller is covered uniformly.
		s.emitStatusChange(updated.TenantID, updated.ID, updated.State)
	}
	s.writeSession(w, http.StatusOK, updated)
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
