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

// errResumeHeldPodReleased is returned by the resumeHeldPod mutator when the
// locked row is still suspended but no longer holds its pod: a suspension
// release cleared it after the caller read the row. The row is unchanged,
// and the caller moves the session to resume_pending, so the resume driver
// restores it on a new pod. spec: §7.2 (Message delivery routing — seven
// paths, path 6).
var errResumeHeldPodReleased = errors.New("resume held pod: the suspended session released its pod")

// resumeHeldPod performs the §7.2 path-6 pod-held resume: it transitions the
// caller's suspended row, whose pod is still held, from `suspended` to
// `running`, reusing the bound pod (no fresh warm-pod claim). The caller
// delivers held messages and the new message after it returns.
//
// It fails closed atomically: the state guard runs inside the store.Update
// mutator, which returns errResumeHeldPodNotSuspended when the locked row is
// not session.StateSuspended, so a concurrent terminal transition on the
// suspended row (DELETE, the watchdog, a parent cascade) cannot slip between
// the guard and the write and resurrect a terminal session to running. A
// still-suspended row that no longer holds its pod returns
// errResumeHeldPodReleased. The write stamps LastAgentActivityAt, so the
// idle clock starts fresh in running. resumeHeldAndDrain is its only caller.
//
// On the state write it emits the session.resumed lifecycle audit row and the
// resume status-change. It returns the running row.
//
// Before the write it runs the resume admission check on row; a *CapRefusal
// or a lookup or count error is returned unchanged, and the row stays
// suspended.
//
// spec: §7.2 path 6, §11.1 (Resume admission).
func (s *Server) resumeHeldPod(ctx context.Context, row sessionstore.Session) (sessionstore.Session, error) {
	if err := s.admitResume(ctx, row); err != nil {
		return sessionstore.Session{}, err
	}
	now := s.clock()
	updated, err := s.store.Update(ctx, row.TenantID, row.ID, func(r *sessionstore.Session) error {
		if r.State != session.StateSuspended {
			return errResumeHeldPodNotSuspended
		}
		if !s.holdsPod(*r) {
			return errResumeHeldPodReleased
		}
		transitionResume(r)
		// spec: §6.2 (maxClientIdleSeconds clock behavior across states) —
		// the idle clock starts fresh on entry to running.
		r.LastAgentActivityAt = now
		return nil
	})
	if err != nil {
		return sessionstore.Session{}, fmt.Errorf("resume held pod %s: %w", row.ID, err)
	}
	// spec: §8.3 (Delegation Policy and Lease) — a committed write out of
	// suspended re-arms the tree's delegation budget keys.
	s.rearmBudgetKeys(ctx, updated)
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
