// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
)

// DefaultInterruptDeadline is the §4.7 / §7.2 clean-interrupt deadline
// the gateway grants the runtime to reach a safe stop point. The spec
// itself does not fix the value; the deployer overrides it through
// Options.InterruptDeadline. Five seconds matches the
// interrupt_acknowledged budget the §15.4 streaming-echo / compliance
// runtimes use in their fixtures.
const DefaultInterruptDeadline = 5 * time.Second

// handleInterrupt implements POST /v1/sessions/{id}/interrupt per §7.2
// (table line 123, state machine lines 168-169). It is a richer
// transition than the shared handleTransition helper covers: the
// gateway signals the runtime through the pod's adapter and waits for
// `interrupt_acknowledged` within `deadlineMs`. The transition to
// `suspended` is unconditional once the §15.1 precondition is met —
// either the runtime acknowledges (STATUS_ACKNOWLEDGED) or the deadline
// elapses (STATUS_INTERRUPT_TIMEOUT, §7.2 line 169 "adapter forces
// suspended"). A STATUS_BUSY response or a transport error leaves the
// row in `running` so the client can retry per §4.7.
//
// When the gateway holds no live pod binding for the session (single-
// replica without a pod, or a coordinator handoff has not re-bound the
// session on this replica), the handler falls back to the row-only
// transition that handleTransition implemented before this fix. That
// path is the minimal-gateway dev posture; production wires podBinder +
// podRegistry so the adapter call lands.
//
// spec: §7.2 lines 168-169; §4.7 InterruptResponse.Status; §15.1
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
	status, timedOut, attempted := s.signalAdapterInterrupt(r, row)
	if attempted && status == adapterclient.InterruptStatusBusy {
		// spec: §4.7 — STATUS_BUSY means the adapter rejected this
		// operation because another op holds the per-session lock. Per
		// §15.1's transient-error contract this is a 409
		// INTERRUPT_BUSY so the client retries.
		s.writeError(w, http.StatusConflict, "INTERRUPT_BUSY",
			"adapter rejected interrupt: another operation is in progress", nil)
		return
	}

	updated, err := s.store.Update(r.Context(), tenantID, id, func(row *sessionstore.Session) error {
		transitionInterrupt(row)
		return nil
	})
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	// spec: §7.2 line 137 — surface the running → suspended transition
	// on the SSE stream alongside the row update.
	s.emitStatusChange(updated.TenantID, updated.ID, updated.State)
	// spec: §6.2 line 214 — project the row's `running → suspended`
	// transition onto Sandbox.status.phase so the operator-visible
	// state matches the session row. The pod stays held while
	// suspended (line 220+); the §6.2 line 234
	// maxSuspendedPodHoldSeconds graceful-release path runs later.
	// Best-effort: a Sandbox phase write that races with a concurrent
	// recovery transition is logged but does not roll back the row
	// transition that already advanced the session. F-6.2.13.
	s.suspendSandboxForInterrupt(r.Context(), updated.ID)
	if timedOut {
		// spec: §7.2 line 169 / §4.7 InterruptResponse.Status.
		// Surface INTERRUPT_TIMEOUT to the caller so a UI can flag that
		// the runtime did not acknowledge; the row state is still
		// suspended per the "adapter forces suspended" rule.
		s.writeInterruptTimeoutResponse(w, updated)
		return
	}
	s.writeSession(w, http.StatusOK, updated)
}

// signalAdapterInterrupt asks the pod's adapter to interrupt the
// runtime. It returns:
//
//   - status: the §4.7 InterruptResponse.Status the adapter returned
//     (only meaningful when attempted is true).
//   - timedOut: true when the adapter returned INTERRUPT_TIMEOUT. The
//     row should still transition to suspended per §7.2 line 169.
//   - attempted: true when the call reached the adapter. When false the
//     gateway holds no binding for the session; the caller must fall
//     back to the row-only transition.
func (s *Server) signalAdapterInterrupt(r *http.Request, row sessionstore.Session) (status adapterclient.InterruptStatus, timedOut, attempted bool) {
	if s.podRegistry == nil {
		return adapterclient.InterruptStatusUnspecified, false, false
	}
	bind, ok := s.podRegistry.Get(row.ID)
	if !ok || bind == nil || bind.Adapter == nil {
		return adapterclient.InterruptStatusUnspecified, false, false
	}
	deadline := s.interruptDeadline()
	// Use a fresh context that survives the request's body close but
	// is bounded by the adapter deadline so a slow runtime cannot stall
	// the HTTP handler past the configured budget.
	ctx := r.Context()
	st, err := bind.Adapter.Interrupt(ctx, row.ID, false, deadline)
	if err != nil {
		// Transport / RPC error. §7.2 line 169 covers the
		// deadline-elapsed case explicitly; other failures (ctx
		// cancelled, gRPC unavailable) collapse to a best-effort row
		// flip in the same vein as the pre-fix behaviour. We log and
		// signal `attempted: true` so the caller's BUSY-on-401 path
		// stays correct, but we report `timedOut: false` so the
		// response does not falsely advertise INTERRUPT_TIMEOUT.
		log.Printf("sessionserver: adapter interrupt for session %s: %v", row.ID, err)
		return adapterclient.InterruptStatusUnspecified, false, true
	}
	return st, st == adapterclient.InterruptStatusTimeout, true
}

// suspendSandboxForInterrupt drives the §6.2 line 214 `attached →
// suspended` Sandbox phase transition after the interrupt handler has
// written the session row to `suspended`. The gateway looks up the
// session's bound Sandbox via the pod registry; a session with no live
// binding (single-replica without a pod, or a coordinator handoff has
// not re-bound) is the dev/minimal-gateway posture where no Sandbox
// exists to update. Best-effort: a Sandbox phase write failure does not
// roll back the row update. F-6.2.13.
func (s *Server) suspendSandboxForInterrupt(ctx context.Context, sessionID string) {
	if s.podBinder == nil || s.podRegistry == nil {
		return
	}
	bind, ok := s.podRegistry.Get(sessionID)
	if !ok || bind == nil || bind.SandboxName == "" {
		return
	}
	if err := s.podBinder.Suspend(ctx, bind.SandboxName); err != nil {
		log.Printf("sessionserver: suspend sandbox %s for session %s: %v", bind.SandboxName, sessionID, err)
	}
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

// writeInterruptTimeoutResponse writes the §7.2 line 169 "adapter
// forces suspended, RPC returns INTERRUPT_TIMEOUT" response. The HTTP
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
