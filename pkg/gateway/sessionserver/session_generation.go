// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"log"

	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// bumpCoordinationGenerationOnSnapshotClose increments the §4.2
// coordination_generation counter on a session row as part of the §7.2 snapshot-close terminal-collapse sequence. The bump runs in
// the same store update that writes the terminal state, fencing any
// stale coordinator still attempting resume against the prior
// generation — per §4.2 CoordinatorFence preconditions, any subsequent
// operational RPC carrying a lower coordination_generation is rejected.
// recovery_generation is intentionally left untouched: the interrupted
// resume attempt is recorded as failed-by-terminal and is not retried,
// so no new recovery is minted (§7.2 (b)).
//
// This helper is the gateway's authoritative CAS-fence primitive for
// the resuming → {cancelled, completed, failed} edges (§7.2). The store's monotonicity floor (sessionstore/pgstore guards
// + memstore guards) blocks any update that tries to decrement the
// counter, so a duplicate concurrent transition observes the second
// generation rather than the first.
//
// The bump is best-effort: a store error is logged and the caller's
// terminal write is already durable, so the only consequence is that
// a stale coordinator's next RPC might pass the CG check (degrading to
// the next layer of defence). The session's state is the authoritative
// barrier; the bump is the §7.2 belt-and-braces fence.
//
// Returns whether the bump succeeded. Callers may use the boolean for
// metric / audit emission; v1 only logs on failure.
//
// spec: §7.2 (a) — snapshot-close coordination_generation bump.
// spec: §4.2 — CoordinatorFence preconditions. F-7.1.14.
func (s *Server) bumpCoordinationGenerationOnSnapshotClose(ctx context.Context, tenantID, sessionID string) bool {
	_, err := s.store.Update(ctx, tenantID, sessionID, func(row *sessionstore.Session) error {
		row.CoordinationGeneration++
		return nil
	})
	if err != nil {
		log.Printf("sessionserver: bump coordination_generation for session %s: %v", sessionID, err)
		return false
	}
	return true
}

// bumpRecoveryGeneration increments the §4.2 recovery_generation
// counter and persists the recovered pod assignment in the same
// transaction. The store's monotonicity floor ensures the counter
// only advances; the in-memory Registry already holds the new BindResult
// on success.
//
// A pod recovery is also a §4.2 retry — the Session Manager
// is responsible for both counters, and a recovery onto a fresh pod
// is the v1 retry path. retry_count is bumped in the same
// transaction; the store enforces monotonicity on both columns.
//
// On a successful bump, the §16.1 lenny_session_retry_total counter is
// incremented with the row's FailureClass as the label (or "unknown"
// when no class is recorded), and the §11.7 / §16.7
// session.retry_attempted audit row is appended. Both side effects are
// best-effort and gated on their respective hooks being wired.
//
// spec: §4.2 — "incremented on each pod recovery".
// spec: §4.2 — "Retry counters and policy enforcement".
// spec: §16.1 catalog — lenny_session_retry_total. F-7.3.10.
// spec: §11.7 / §16.7 — session.retry_attempted audit. F-7.3.18.
func (s *Server) bumpRecoveryGeneration(ctx context.Context, tenantID, sessionID, podAssignment string) {
	updated, err := s.store.Update(ctx, tenantID, sessionID, func(row *sessionstore.Session) error {
		row.RecoveryGeneration++
		row.RetryCount++
		if podAssignment != "" {
			row.PodAssignment = podAssignment
		}
		return nil
	})
	if err != nil {
		log.Printf("sessionserver: bump recovery_generation for session %s: %v", sessionID, err)
		return
	}
	s.recordSessionRetry(ctx, updated)
}

// recordSessionRetry fires the §16.1 lenny_session_retry_total metric
// and the §11.7 / §16.7 session.retry_attempted audit row for one
// retry attempt. Best-effort: a nil hook degrades to a no-op without
// rolling back the row update that triggered it.
//
// spec: §16.1 catalog (F-7.3.10); §11.7 / §16.7 (F-7.3.18).
func (s *Server) recordSessionRetry(ctx context.Context, row sessionstore.Session) {
	failureClass := string(row.FailureClass)
	if failureClass == "" {
		failureClass = "unknown"
	}
	if s.incSessionRetry != nil {
		s.incSessionRetry(failureClass)
	}
	if s.lifecycleAudit != nil {
		s.lifecycleAudit.EmitSessionLifecycle(ctx, SessionLifecycleEvent{
			EventType:    auditSessionRetryAttempted,
			TenantID:     row.TenantID,
			SessionID:    row.ID,
			UserID:       row.UserID,
			RuntimeRef:   row.RuntimeRef,
			State:        string(row.State),
			FailureClass: failureClass,
			At:           s.clock(),
		})
	}
}
