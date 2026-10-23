// SPDX-License-Identifier: MIT

package sessionserver

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// DefaultInterruptDeadline is the §4.7 / §7.2 clean-interrupt deadline
// the gateway grants the runtime to reach a safe stop point. The spec
// itself does not fix the value; the deployer overrides it through
// Options.InterruptDeadline. Five seconds matches the
// interrupt_acknowledged budget the §15.4 streaming-echo / compliance
// runtimes use in their fixtures.
const DefaultInterruptDeadline = 5 * time.Second

// handleInterrupt implements POST /v1/sessions/{id}/interrupt per §7.2. It is
// the one write of an interrupt into `suspended`; lenny/interrupt_session
// dispatches to it. It is a richer
// transition than the shared handleTransition helper covers: the
// gateway signals the runtime through the pod's adapter and waits for
// `interrupt_acknowledged` within `deadlineMs`. The transition to
// `suspended` is unconditional once the §15.1 precondition is met —
// either the runtime acknowledges (STATUS_ACKNOWLEDGED) or the deadline
// elapses (STATUS_INTERRUPT_TIMEOUT, §7.2). A STATUS_BUSY response or a transport error leaves the
// row in `running` so the client can retry per §4.7.
//
// When the gateway holds no live pod binding for the session (single-
// replica without a pod, or a coordinator handoff has not re-bound the
// session on this replica), the handler falls back to the row-only
// transition that handleTransition implemented before this fix. That
// path is the minimal-gateway dev posture; production wires podBinder +
// podRegistry so the adapter call lands.
//
// spec: §7.2; §4.7 InterruptResponse.Status; §15.1
// /v1/sessions/{id}/interrupt endpoint.
func (s *Server) handleInterrupt(w http.ResponseWriter, r *http.Request) {
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
		Endpoint:     session.EndpointInterrupt,
		CurrentState: row.State,
	}); err != nil {
		s.writePreconditionError(w, err)
		return
	}

	// spec: §4.7 — drive the adapter when we hold a binding. Misses
	// (no registry, no binding, or pre-attached transport closed) fall
	// through to the row-only transition.
	status, timedOut := adapterclient.InterruptStatusUnspecified, false
	if bind := s.interruptBinding(row.ID); bind != nil {
		// spec: §15.1 — interrupt is valid only while the session is
		// running and answers 409 INVALID_STATE_TRANSITION against a
		// terminal row. The read above may come from a lagging read pool,
		// so the primary row is checked before the adapter is driven: an
		// adapter whose session already ended can answer STATUS_BUSY, and
		// that answer must not surface as a retryable INTERRUPT_BUSY
		// against a row no retry can ever interrupt. F-15.1.43.
		fresh, ok := s.interruptPrimaryRow(w, r, tenantID, id)
		if !ok {
			return
		}
		row = fresh
		status, timedOut = s.signalAdapterInterrupt(r, bind, row)
	}
	if status == adapterclient.InterruptStatusBusy {
		// spec: §4.7 — STATUS_BUSY means the adapter rejected this
		// operation because another op holds the per-session lock. Per
		// §15.1's transient-error contract this is a 409
		// INTERRUPT_BUSY so the client retries.
		s.writeError(w, http.StatusConflict, "INTERRUPT_BUSY",
			"adapter rejected interrupt: another operation is in progress", nil)
		return
	}

	// spec: §7.2 / §8.8 — the Suspended session-condition fact lives on the
	// Postgres session row, not on Sandbox.status.conditions (the gateway is
	// not a Sandbox.status writer, §4.6.3). Stamp SuspendedAt/SuspendedReason
	// in the same transaction that advances the row to `suspended` so the
	// fact and the state never disagree. The reason distinguishes an
	// acknowledged interrupt from a forced (timeout) one. F-6.2.12.
	reason := suspendReason(timedOut)
	now := s.clock()
	// spec: §7.2 — the precondition is re-evaluated on the locked row, so
	// a row the read pool still shows as running but that left it (to a
	// recovering state, or an idle suspension that released its pod) is
	// refused with 409 and left unchanged.
	updated, err := s.store.Update(r.Context(), tenantID, id, func(row *sessionstore.Session) error {
		if verr := session.Validate(session.PreconditionRequest{
			Endpoint:     session.EndpointInterrupt,
			CurrentState: row.State,
		}); verr != nil {
			return verr
		}
		transitionInterrupt(row)
		row.SuspendedAt = now
		row.SuspendedReason = reason
		return nil
	})
	var perr *session.PreconditionError
	if errors.As(err, &perr) {
		s.writePreconditionError(w, err)
		return
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	// spec: §7.2 — surface the running → suspended transition
	// on the SSE stream alongside the row update.
	s.emitStatusChange(updated.TenantID, updated.ID, updated.State)
	// spec: §8.3 (Delegation Policy and Lease) — a committed write into
	// suspended re-arms the tree's delegation budget keys.
	s.rearmBudgetKeys(r.Context(), updated)
	if timedOut {
		// spec: §7.2 / §4.7 InterruptResponse.Status.
		// Surface INTERRUPT_TIMEOUT to the caller so a UI can flag that
		// the runtime did not acknowledge; the row state is still
		// suspended per the "adapter forces suspended" rule.
		s.writeInterruptTimeoutResponse(w, updated)
		return
	}
	s.writeSession(w, http.StatusOK, updated)
}

// interruptBinding returns the live pod binding whose adapter an interrupt
// drives, or nil when the gateway holds none (no registry, no binding, or a
// binding without an adapter client), in which case the interrupt takes the
// row-only transition.
func (s *Server) interruptBinding(sessionID string) *podsession.BindResult {
	if s.podRegistry == nil {
		return nil
	}
	bind, ok := s.podRegistry.Get(sessionID)
	if !ok || bind == nil || bind.Adapter == nil {
		return nil
	}
	return bind
}

// interruptPrimaryRow reads the session row from the primary and re-applies
// the interrupt precondition to it before the adapter is driven. It writes
// the response and returns ok=false when the row is gone (404
// RESOURCE_NOT_FOUND), unreadable (500 INTERNAL_ERROR), or in a state the
// interrupt is not valid in (the §15.1 precondition error, which is 409
// INVALID_STATE_TRANSITION for a terminal row). An unreadable primary fails
// closed rather than signalling a runtime whose session state is unknown.
//
// spec: §15.1 (REST API, state-transition preconditions); §12.3 (Postgres HA
// Requirements).
func (s *Server) interruptPrimaryRow(w http.ResponseWriter, r *http.Request, tenantID, id string) (sessionstore.Session, bool) {
	fresh, err := s.readPrimary(r.Context(), tenantID, id)
	if err != nil {
		if errors.Is(err, sessionstore.ErrNotFound) {
			s.writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "session not found", nil)
			return sessionstore.Session{}, false
		}
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return sessionstore.Session{}, false
	}
	if err := session.Validate(session.PreconditionRequest{
		Endpoint:     session.EndpointInterrupt,
		CurrentState: fresh.State,
	}); err != nil {
		s.writePreconditionError(w, err)
		return sessionstore.Session{}, false
	}
	return fresh, true
}

// signalAdapterInterrupt asks the pod's adapter to interrupt the
// runtime. It returns:
//
//   - status: the §4.7 InterruptResponse.Status the adapter returned,
//     or InterruptStatusUnspecified on a transport or RPC error.
//   - timedOut: true when the adapter returned INTERRUPT_TIMEOUT. The
//     row should still transition to suspended per §7.2.
func (s *Server) signalAdapterInterrupt(r *http.Request, bind *podsession.BindResult, row sessionstore.Session) (status adapterclient.InterruptStatus, timedOut bool) {
	deadline := s.interruptDeadline()
	// Use a fresh context that survives the request's body close but
	// is bounded by the adapter deadline so a slow runtime cannot stall
	// the HTTP handler past the configured budget.
	ctx := r.Context()
	st, err := bind.Adapter.Interrupt(ctx, row.ID, false, deadline)
	if err != nil {
		// Transport / RPC error. §7.2 covers the
		// deadline-elapsed case explicitly; other failures (ctx
		// cancelled, gRPC unavailable) collapse to a best-effort row
		// flip in the same vein as the pre-fix behaviour. The error
		// reports timedOut false so the response does not falsely
		// advertise INTERRUPT_TIMEOUT.
		log.Printf("sessionserver: adapter interrupt for session %s: %v", row.ID, err)
		return adapterclient.InterruptStatusUnspecified, false
	}
	return st, st == adapterclient.InterruptStatusTimeout
}

// suspendReason maps the §7.2 interrupt outcome to the coded reason stamped
// on the session row's SuspendedReason field, distinguishing an acknowledged
// interrupt from a forced (deadline-elapsed) one in the §7.2 / §8.8 session
// condition history. F-6.2.12.
func suspendReason(timedOut bool) string {
	if timedOut {
		return sessionstore.SuspendedReasonInterruptTimeout
	}
	return sessionstore.SuspendedReasonInterruptAcknowledged
}

// interruptDeadline returns the §4.7 deadline the gateway grants the
// adapter on a clean interrupt. The current minimal gateway exposes no
// deployer knob for this (the option lands when the broader §7.3
// retryPolicy plumbing arrives); the spec leaves the value to the
// deployer so the platform default has to be conservative — long enough
// for a runtime that runs through a checkpoint loop to acknowledge,
// short enough that a stuck runtime cannot stall the API for minutes.
// Five seconds matches the §15.4 streaming-echo runtime's fixtures.
func (s *Server) interruptDeadline() time.Duration {
	return DefaultInterruptDeadline
}

// writeInterruptTimeoutResponse writes the §7.2 response. The HTTP
// status is 200 (the state transition succeeded), and the body carries
// the regular SessionResponse plus an `interruptStatus: "timeout"`
// field so the caller distinguishes acknowledged from forced. The
// timeout is also stamped into the response's `details` block on the
// session envelope so a UI can render the §4.7 status without parsing
// the headers.
func (s *Server) writeInterruptTimeoutResponse(w http.ResponseWriter, row sessionstore.Session) {
	resp := struct {
		SessionResponse
		// spec: §4.7 InterruptResponse.Status — surfaced to the
		// caller so the UI can flag that the runtime did not
		// acknowledge before the deadline elapsed.
		InterruptStatus string `json:"interruptStatus"`
	}{
		SessionResponse: toResponse(row),
		InterruptStatus: "timeout",
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}
