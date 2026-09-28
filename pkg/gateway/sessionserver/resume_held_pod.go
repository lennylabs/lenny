// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"fmt"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// errResumeHeldPodNotSuspended is returned by the resumeHeldPod store
// mutator when the target row is no longer session.StateSuspended. The
// guard runs inside the store.Update mutator so the check and the
// suspended → running write commit as one critical section under the
// pgstore FOR UPDATE row lock / memstore mutex; returning this sentinel
// leaves the row unchanged. The caller (deliverMessageBatch) maps it to
// the §7.2 path-6 `queued` fail-closed fallback rather than resurrecting
// a terminal row to running. spec: §7.2 path 6.
var errResumeHeldPodNotSuspended = errors.New("resume held pod: session is not suspended")

// resumeHeldPod performs the §7.2 path-6 pod-held resume: it transitions a
// suspended session whose pod is still held from `suspended` to `running`,
// reusing any bound pod (no fresh warm-pod claim). It is the coordinator-side
// half of the atomic resume-and-deliver; the caller (deliverMessageBatch)
// delivers the message via executor.Send after this returns.
//
// It fails closed atomically: the state guard runs inside the store.Update
// mutator, which returns errResumeHeldPodNotSuspended when the row is not
// session.StateSuspended before calling transitionResume. Under the pgstore
// SELECT ... FOR UPDATE row lock and the memstore mutex the check and the
// transition commit as one critical section, so a concurrent terminal
// transition on the suspended row (Suspended → Cancelled via DELETE, Suspended
// → Expired via the watchdog, Suspended → Completed/Failed via parent cascade,
// all legal in the state machine) cannot slip between the guard and the write
// and resurrect a terminal session to running. Neither store validates
// transition legality, so the in-mutator guard is the only thing preventing an
// illegal terminal → running write. A misroute (a non-suspended row) surfaces
// the sentinel to the caller, which maps it to a `queued` fallback rather than
// a silent fresh claim.
//
// The guard does not require a non-empty PodAssignment: no §6.2 sweep releases
// a suspended pod today, so every `suspended` row is pod-held in the currently
// reachable state set, and in --dev-mode the gateway builds no pod binder so
// PodAssignment is empty while the session still holds its in-process executor.
// §6.2 keeps a released session in `suspended` (podless) with no state change;
// when the deferred §6.2 sweep lands the guard must additionally distinguish a
// pod-held row from a podless-suspended row and route the podless case to
// `resume_pending`/`queued` per §7.2.
//
// On the state write it reuses transitionResume (the {Suspended, Running} edge
// is legal) and emits the same resume status-change (emitStatusChange) and
// session.resumed lifecycle audit that handleResume emits. For the in-process
// echo executor there is no pod adapter to signal, so the method returns after
// the state write; the runtime delivery is the caller's executor.Send. The
// real-pod in-place adapter-resume signal against a still-held pod (waiting for
// the §4.7 ready_for_input signal) is deferred.
//
// spec: §7.2 path 6.
func (s *Server) resumeHeldPod(ctx context.Context, tenantID, id string) error {
	updated, err := s.store.Update(ctx, tenantID, id, func(row *sessionstore.Session) error {
		if row.State != session.StateSuspended {
			return errResumeHeldPodNotSuspended
		}
		transitionResume(row)
		return nil
	})
	if err != nil {
		return fmt.Errorf("resume held pod %s: %w", id, err)
	}
	// spec: §11.7 / §16.7 — mirror handleResume: append the session.resumed
	// audit row after the successful resume, then surface the resume
	// status-change on the session's SSE stream.
	if s.lifecycleAudit != nil {
		s.lifecycleAudit.EmitSessionLifecycle(ctx, SessionLifecycleEvent{
			EventType:  auditSessionResumed,
			TenantID:   updated.TenantID,
			SessionID:  updated.ID,
			UserID:     updated.UserID,
			RuntimeRef: updated.RuntimeRef,
			State:      string(updated.State),
			At:         s.clock(),
		})
	}
	s.emitStatusChange(updated.TenantID, updated.ID, updated.State)
	return nil
}
