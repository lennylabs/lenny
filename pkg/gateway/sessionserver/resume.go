// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// handleResume implements POST /v1/sessions/{id}/resume. The endpoint admits
// awaiting_client_action and suspended. It decides on the primary row:
//
//   - From awaiting_client_action, and from a suspended session that holds no
//     pod, the call is a store write any replica serves: it writes
//     resume_pending with RetryCount unchanged and answers 200 with the
//     updated row. The resume driver restores the session on the replica
//     that coordinates it, and the restore's outcome arrives on the event
//     stream.
//   - From a suspended session that holds its pod, the coordinating replica
//     resumes the session on that pod and delivers its held messages under
//     the session's delivery lock, answering 200 with the row in running. A
//     replica that does not coordinate the session writes nothing and
//     answers 409 TARGET_NOT_READY, because no inter-replica carrier for the
//     forward exists.
//
// The call claims no pod and runs no post-commit effect itself.
//
// spec: §7.3 (Retry and Resume), §15.1 (REST API), §29.3 (Interactive
// message send), §29.6 (Restore and resume)
func (s *Server) handleResume(w http.ResponseWriter, r *http.Request) {
	tenantID := s.resolveTenant(r)
	id := r.PathValue("id")
	row, err := s.readPrimary(r.Context(), tenantID, id)
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
	if row.State == session.StateSuspended && s.holdsPod(row) {
		s.resumeHeldPodRequest(w, r, row)
		return
	}
	updated, err := s.enterResumePending(r.Context(), row)
	if err != nil {
		s.writePreconditionError(w, err)
		return
	}
	s.writeSession(w, http.StatusOK, updated)
}

// resumeHeldPodRequest serves POST /resume on a suspended session that holds
// its pod. Only the coordinating replica resumes it; any other replica writes
// nothing and answers 409 TARGET_NOT_READY with the current state. On the
// coordinating replica the resume and the delivery of held messages run
// under the session's delivery lock. A delivery failure after the running
// write still answers 200 with running. A row that left suspended before the
// write is handled as the state it reached: from awaiting_client_action the
// call writes resume_pending, and from any other state it answers the
// precondition error.
//
// spec: §29.3 (Interactive message send), §7.2 (Message delivery routing —
// seven paths, path 6), §7.3 (Retry and Resume)
func (s *Server) resumeHeldPodRequest(w http.ResponseWriter, r *http.Request, row sessionstore.Session) {
	if !s.coordinatesSession(r.Context(), row.TenantID, row.ID) {
		s.writeError(w, http.StatusConflict, "TARGET_NOT_READY",
			"the session's pod is coordinated by another gateway replica; retry the request",
			map[string]any{"currentState": string(row.State)})
		return
	}
	unlock, err := s.deliveryLocks.Lock(r.Context(), row.ID)
	if err != nil {
		s.writeError(w, http.StatusServiceUnavailable, "TARGET_NOT_READY", err.Error(),
			map[string]any{"currentState": string(row.State)})
		return
	}
	updated, err := s.resumeHeldAndDrain(r.Context(), row)
	unlock()
	switch {
	case err == nil, errors.Is(err, errHeldDeliveryFailed):
		s.writeSession(w, http.StatusOK, updated)
	case errors.Is(err, errResumeHeldPodReleased):
		// spec: §7.2 — a suspension release cleared the pod after the
		// read; the call is the podless store write.
		pending, perr := s.enterResumePending(r.Context(), row)
		if perr != nil {
			s.writePreconditionError(w, perr)
			return
		}
		s.writeSession(w, http.StatusOK, pending)
	case errors.Is(err, errResumeHeldPodNotSuspended):
		moved := row
		moved.State = session.StateAwaitingClientAction
		pending, perr := s.enterResumePending(r.Context(), moved)
		if perr != nil {
			s.writePreconditionError(w, perr)
			return
		}
		s.writeSession(w, http.StatusOK, pending)
	default:
		s.writePreconditionError(w, err)
	}
}

// errHeldDeliveryFailed wraps a held-message delivery error that followed a
// held-pod resume's running write, so a caller distinguishes it from a
// resume error that left the row suspended.
var errHeldDeliveryFailed = errors.New("sessionserver: held-message delivery failed after the resume")

// resumeHeldAndDrain resumes a suspended session on its held pod and then
// delivers its held messages. It is the one implementation of the held-pod
// resume edge. Its callers are the REST message path, ResumeSuspended, and
// handleResume, each holding the session's delivery lock and each only when
// this replica coordinates the session. It returns the running row; a
// delivery error comes after the running write and is returned wrapped in
// errHeldDeliveryFailed with that row. A row whose pod a suspension release
// cleared returns errResumeHeldPodReleased, and the caller moves the session
// to resume_pending.
//
// spec: §7.2 (Message delivery routing — seven paths, path 6)
func (s *Server) resumeHeldAndDrain(ctx context.Context, row sessionstore.Session) (sessionstore.Session, error) {
	updated, err := s.resumeHeldPod(ctx, row)
	if err != nil {
		return sessionstore.Session{}, err
	}
	if derr := s.deliverBuffered(ctx, updated); derr != nil {
		return updated, fmt.Errorf("%w: %w", errHeldDeliveryFailed, derr)
	}
	return updated, nil
}

// holdsPod reports whether the session row names its pod, or the server runs
// with no pod binder, where every session holds its in-process executor.
// spec: §6.2 (Pod State Machine).
func (s *Server) holdsPod(row sessionstore.Session) bool {
	return row.PodAssignment != "" || s.podBinder == nil
}
