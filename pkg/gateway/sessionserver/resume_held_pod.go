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

// resumeHeldPod performs the §7.2 path-6 pod-held resume: it transitions the
// caller's suspended row, whose pod is still held, from `suspended` to
// `running`, reusing the bound pod (no fresh warm-pod claim). The caller
// delivers held messages and the new message after it returns.
//
// It fails closed atomically: the state guard runs inside the store.Update
// mutator, which returns errResumeHeldPodNotSuspended when the locked row is
// not session.StateSuspended, so a concurrent terminal transition on the
// suspended row (DELETE, the watchdog, a parent cascade) cannot slip between
// the guard and the write and resurrect a terminal session to running.
//
// On the state write it emits the session.resumed lifecycle audit row and the
// resume status-change. It returns the running row.
//
// spec: §7.2 path 6.
func (s *Server) resumeHeldPod(ctx context.Context, row sessionstore.Session) (sessionstore.Session, error) {
	updated, err := s.store.Update(ctx, row.TenantID, row.ID, func(r *sessionstore.Session) error {
		if r.State != session.StateSuspended {
			return errResumeHeldPodNotSuspended
		}
		transitionResume(r)
		return nil
	})
	if err != nil {
		return sessionstore.Session{}, fmt.Errorf("resume held pod %s: %w", row.ID, err)
	}
	// spec: §11.7 / §16.7 — append the session.resumed audit row after the
	// successful resume, then surface the resume status-change on the
	// session's SSE stream.
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
	return updated, nil
}
