// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/events"
	"github.com/lennylabs/lenny/pkg/gateway/executor"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/treearchive"
	"github.com/lennylabs/lenny/pkg/gateway/usagestore"
)

// releaseExecutor tears down a terminal session's executor state. When the
// executor is a §6.2 Sandbox-backed pod executor (SessionReleaser), it records
// the terminal disposition on the Sandbox before draining the pod; otherwise
// it falls back to Close (echo, subprocess, and other executors with no
// Sandbox phase). spec: §6.2 lines 105-117, 305.
func releaseExecutor(ctx context.Context, exec executor.Executor, sessionID string, disp executor.Disposition) error {
	if r, ok := exec.(executor.SessionReleaser); ok {
		return r.Release(ctx, sessionID, disp)
	}
	return exec.Close(ctx, sessionID)
}

// dispositionForState maps a session's terminal §6.2 state to the executor
// Disposition recorded on the backing Sandbox at release time
// (attached → completed/failed/cancelled/expired). A non-terminal state
// (recordSessionCompleted is only called on a terminal transition, so this is
// defensive) carries no disposition and skips the terminal-phase write.
// spec: §6.2 lines 105-117, 269.
func dispositionForState(st session.State) executor.Disposition {
	switch st {
	case session.StateCompleted:
		return executor.DispositionCompleted
	case session.StateFailed:
		return executor.DispositionFailed
	case session.StateCancelled:
		return executor.DispositionCancelled
	case session.StateExpired:
		return executor.DispositionExpired
	default:
		return ""
	}
}

// handleUsage implements GET /v1/usage per §15.1 — the aggregated
// usage report. The §15.1 contract: this is a single aggregated
// object, not a paginated list.
//
// The endpoint requires the §10.2 view_usage permission (held by
// platform-admin, tenant-admin, tenant-viewer, and billing-viewer; the
// `user` role does not hold it). A platform-admin caller may scope the
// report to one tenant via ?tenantId=; every other caller is scoped to
// their own tenant.
func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	principal, ok := getPrincipal(r)
	if !ok || !pkgauth.RolesGrant(principal.Roles, pkgauth.PermViewUsage) {
		s.writeError(w, http.StatusForbidden, "FORBIDDEN",
			"usage reports require the view_usage permission", nil)
		return
	}
	if s.usage == nil {
		// Metering disabled — return an empty report rather than an
		// error so dashboards do not break.
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(usagestore.Report{
			ByTenant:  []usagestore.TenantUsage{},
			ByRuntime: []usagestore.RuntimeUsage{},
		})
		return
	}
	// The caller's resolved tenant scopes the report; a non-empty
	// ?tenantId= is honoured only when it matches the caller's
	// tenant (cross-tenant usage reads require platform-admin, which
	// the minimal gateway does not yet distinguish — scope to the
	// caller's tenant unconditionally).
	tenant := s.resolveTenant(r)
	report, err := s.usage.Aggregate(r.Context(), tenant)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(report)
}

// recordSessionCreated records the §15.1 usage event and the §11.2.1
// `session.created` billing event for a new session. Both writes are
// best-effort: a metering or billing failure is never allowed to fail
// the session create.
func (s *Server) recordSessionCreated(ctx context.Context, sess sessionstore.Session) {
	if s.usage != nil {
		_ = s.usage.Record(ctx, usagestore.Record{
			TenantID: sess.TenantID,
			Runtime:  sess.RuntimeRef,
			Sessions: 1,
		})
	}
	if s.billing != nil {
		_, _ = s.billing.Append(ctx, billingstore.Event{
			TenantID:  sess.TenantID,
			UserID:    sess.UserID,
			SessionID: sess.ID,
			EventType: billingstore.EventSessionCreated,
		})
	}
	// §7.1 / §16.6: write the `session.created` event to the §11.7
	// hash-chained audit log so the lifecycle has a tamper-evident
	// record distinct from the billing event above. Best-effort.
	if s.lifecycleAudit != nil {
		s.lifecycleAudit.EmitSessionLifecycle(ctx, SessionLifecycleEvent{
			EventType:  auditSessionCreated,
			TenantID:   sess.TenantID,
			SessionID:  sess.ID,
			UserID:     sess.UserID,
			RuntimeRef: sess.RuntimeRef,
			State:      string(sess.State),
			At:         s.clock(),
		})
	}
}

// terminalSessionEvent maps a terminal session state to its §16.6
// operational-event type and severity. ok is false for a non-terminal
// state. Terminate is modeled as StateCompleted, so no session state
// maps to the session_terminated catalogue entry.
func terminalSessionEvent(st session.State) (et events.EventType, severity string, ok bool) {
	switch st {
	case session.StateCompleted:
		return events.EventSessionCompleted, "info", true
	case session.StateFailed:
		return events.EventSessionFailed, "error", true
	case session.StateCancelled:
		return events.EventSessionCancelled, "info", true
	case session.StateExpired:
		return events.EventSessionExpired, "warning", true
	default:
		return "", "", false
	}
}

// OnSessionTerminal is the watchdog / orphancleanup TerminalHook that
// runs the full terminal-side-effects pipeline (workspace seal, executor
// release — which drains the pod and releases concurrent-mode slots per
// §5.2 line 519 — audit, SSE, billing, archive). It adapts the private
// recordSessionCompleted so background sweepers force-terminating a
// session emit the same signals exactly once as a session terminated by
// REST. spec: §5.2 line 519; §6.2 lines 105-117; §11.7; §7.2 lines 137,
// 141. Closes F-5.2.26.
func (s *Server) OnSessionTerminal(ctx context.Context, sess sessionstore.Session) {
	s.recordSessionCompleted(ctx, sess)
}

// OnSessionExpiredFromAwaitingClientAction is the watchdog's
// awaiting-action-specific audit hook (§7.3 line 423 entry path). The
// watchdog fires it before the generic OnSessionTerminal so the §11.7
// session.expired_in_awaiting_action row precedes the generic
// session.expired row in the hash-chained audit log. spec: §7.3 line
// 423; §11.7 / §16.7. F-7.3.25.
func (s *Server) OnSessionExpiredFromAwaitingClientAction(ctx context.Context, sess sessionstore.Session) {
	s.emitAwaitingClientActionExpired(ctx, sess)
}

// OnSessionEnteredAwaitingClientAction is the watchdog's
// awaiting-action ENTRY hook (§6.2 lines 249, 292 — resume_pending
// wall-clock cap and resuming retries-exhausted branch). The hook
// drives the §7.3 line 427 session.awaiting_action webhook, the §16.6
// operational event, and the §11.7 / §16.7
// session.awaiting_action_entered audit row through the existing
// emitAwaitingClientActionEntered helper. Best-effort: a nil
// collaborator never rolls back the watchdog's state transition.
// spec: §6.2 line 249/292 (entry from resuming/resume_pending);
// §7.3 line 427 (webhook); §11.7 / §16.7. F-6.2.14.
func (s *Server) OnSessionEnteredAwaitingClientAction(ctx context.Context, sess sessionstore.Session) {
	s.emitAwaitingClientActionEntered(ctx, sess)
}

// OnSessionRetryAttempt is the watchdog's resuming → resume_pending
// retry hook (§6.2 line 249). The watchdog fires it after the row
// update so the §16.1 lenny_session_retry_total counter and the §11.7
// / §16.7 session.retry_attempted audit row see the watchdog-initiated
// retry alongside the gateway-initiated retries the
// bumpRecoveryGeneration path already covers.
// spec: §6.2 line 249 (resuming → resume_pending retry path);
// §16.1 lenny_session_retry_total; §11.7 / §16.7 session.retry_attempted.
// F-6.2.14.
func (s *Server) OnSessionRetryAttempt(ctx context.Context, sess sessionstore.Session) {
	s.recordSessionRetry(ctx, sess)
}

// recordSessionCompleted runs the side effects of a session reaching a
// terminal state: it takes the §7.1 final workspace snapshot, releases
// the session's executor state — for a pod-backed session this shuts
// the runtime down and reclaims the pod — and emits the §11.2.1
// `session.completed` billing event. All are best-effort: a failure
// never fails the transition that triggered it.
func (s *Server) recordSessionCompleted(ctx context.Context, sess sessionstore.Session) {
	// §7.1 seal-and-export (steps 20-23): export the final workspace
	// before the pod is released and before the client- and audit-visible
	// terminal signals fire below. The seal runs with the §7.1 line 112
	// bounded exponential backoff; when the retry window is exhausted the
	// session is re-labeled failed/workspace_seal_timeout so the
	// session_complete event and audit row report the true outcome rather
	// than a clean completion that silently lost its workspace. The pod is
	// still torn down by the executor release below ("terminates the pod
	// anyway"). The Sealer no-ops for a session that never ran on a pod.
	if s.sealer != nil {
		if err := s.sealWorkspace(ctx, sess); err != nil {
			sess = s.failWorkspaceSeal(ctx, sess, err)
		}
	}
	// §16.6 / §25.3: a session reaching a terminal state emits the
	// matching session lifecycle operational event so an ops agent
	// observes it through the event buffer. Best-effort — a nil emitter
	// or marshal error never fails the transition.
	if s.opsEmitter != nil {
		if et, severity, ok := terminalSessionEvent(sess.State); ok {
			payload := map[string]any{
				"sessionId": sess.ID,
				"runtime":   sess.RuntimeRef,
			}
			if sess.FailureClass != "" {
				payload["failureClass"] = string(sess.FailureClass)
			}
			data, _ := json.Marshal(payload)
			_, _ = s.opsEmitter.Emit(ctx, events.OperationalEvent{
				Source:          "/v1/sessions",
				Type:            et.CloudEventsType(),
				Severity:        severity,
				DataContentType: "application/json",
				Data:            data,
			})
		}
	}
	// §7.2 lines 137, 141 / §11.7 / §7.1 line 77: the client- and
	// audit-visible terminal signals (status_change, session_complete,
	// the lifecycle audit event, and the retention-window roll). Shared
	// with failSession so the start-path failure emits the same signals.
	s.emitTerminalLifecycle(ctx, sess)
	if s.executor != nil {
		if err := releaseExecutor(ctx, s.executor, sess.ID, dispositionForState(sess.State)); err != nil {
			// recordSessionCompleted is best-effort by design (a failed
			// teardown does not unwind the terminal-state transition), but
			// silently dropping the error has bitten us: a 403 on
			// SandboxClaim Delete or an SSA conflict on the
			// activeSlots decrement leaves a slot stuck and the pool
			// drains its capacity unobserved. Logging makes the failure
			// surface in the gateway log so an operator notices.
			log.Printf("lenny-gateway: executor.Close session=%s: %v", sess.ID, err)
		}
	}
	// §8.10: a child session reaching a terminal state is archived to
	// the session_tree_archive so a resumed parent can replay it.
	s.archiveSettledChild(ctx, sess)
	// §8.10: apply the cascadeOnFailure policy to this session's
	// children now that it has reached a terminal state.
	s.cascadeToChildren(ctx, sess)
	// §4.4 line 226: best-effort session-log close-hook. The hook
	// captures the buffered runtime stderr and persists it as the
	// `/{tenant_id}/sessions/{session_id}/stderr.log` artifact. A
	// failure (MinIO unavailable, catalog insert error) logs and
	// drops rather than fail the terminal-state transition.
	// spec: §4.4 line 226 — Session logs and runtime stderr.
	if s.sessionLogHook != nil {
		if err := s.sessionLogHook.OnSessionTerminal(ctx, sess.TenantID, sess.ID, nil, false); err != nil {
			log.Printf("lenny-gateway: session-log close-hook session=%s: %v", sess.ID, err)
		}
	}
	if s.billing == nil {
		return
	}
	_, _ = s.billing.Append(ctx, billingstore.Event{
		TenantID:  sess.TenantID,
		UserID:    sess.UserID,
		SessionID: sess.ID,
		EventType: billingstore.EventSessionCompleted,
	})
}

// archiveSettledChild records a terminal child session in the §8.10
// session_tree_archive. A session with no parent is the tree root and
// is not archived. Archiving is best-effort: a failure never fails the
// transition that triggered it.
func (s *Server) archiveSettledChild(ctx context.Context, sess sessionstore.Session) {
	if s.treeArchive == nil || sess.ParentSessionID == "" {
		return
	}
	result, _ := json.Marshal(archivedTaskResult(sess))
	_ = s.treeArchive.Archive(ctx, treearchive.ArchivedNode{
		TenantID:        sess.TenantID,
		RootSessionID:   s.treeRoot(ctx, sess),
		NodeSessionID:   sess.ID,
		ParentSessionID: sess.ParentSessionID,
		State:           string(sess.State),
		Result:          string(result),
		SettledAt:       s.clock(),
	})
}

// cascadeToChildren applies the §8.10 cascadeOnFailure policy when
// sess reaches a terminal state. Under the default `cancel_all` policy
// the gateway cancels every descendant; `await_completion` and
// `detach` leave the children running. A `detach` cascade is capped
// per §8.10: when the tenant is already over maxOrphanTasksPerTenant
// the gateway falls back to `cancel_all` so orphans cannot accumulate
// without bound. Each cancelled descendant is itself governed by its
// own cascade policy, so a `detach` child shields its own subtree.
// Best-effort: a failure never fails the transition that triggered it.
func (s *Server) cascadeToChildren(ctx context.Context, sess sessionstore.Session) {
	policy := sess.CascadeOnFailure.Resolve()
	if policy == session.CascadeDetach && s.detachExceedsOrphanCap(ctx, sess.TenantID) {
		// §8.10 maxOrphanTasksPerTenant fallback.
		policy = session.CascadeCancelAll
	}
	if policy != session.CascadeCancelAll {
		return
	}
	all, err := s.store.List(ctx, sess.TenantID, sessionstore.ListFilter{})
	if err != nil {
		return
	}
	byParent := map[string][]sessionstore.Session{}
	for _, row := range all {
		if row.ParentSessionID != "" {
			byParent[row.ParentSessionID] = append(byParent[row.ParentSessionID], row)
		}
	}
	// Breadth-first over the subtree. A node is cancelled, then its own
	// children are cascaded only when the cancelled node's own policy
	// is `cancel_all` — a `detach` node keeps its descendants alive.
	seen := map[string]bool{sess.ID: true}
	queue := append([]sessionstore.Session(nil), byParent[sess.ID]...)
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if seen[cur.ID] {
			continue
		}
		seen[cur.ID] = true
		if session.IsTerminal(cur.State) {
			// Already settled — its own cascade ran when it settled.
			continue
		}
		updated, err := s.store.Update(ctx, sess.TenantID, cur.ID, func(row *sessionstore.Session) error {
			row.State = session.StateCancelled
			return nil
		})
		if err != nil {
			continue
		}
		s.archiveSettledChild(ctx, updated)
		if cur.CascadeOnFailure.Resolve() == session.CascadeCancelAll {
			queue = append(queue, byParent[cur.ID]...)
		}
	}
}

// detachExceedsOrphanCap reports whether the tenant's active orphan
// count is over the §8.10 maxOrphanTasksPerTenant cap. An orphan is a
// non-terminal child session whose delegation tree root has reached a
// terminal state — the caller invokes this after a `detach`-policy
// session has terminated, so that session's own children are already
// counted. Best-effort: a store error reports "not over cap" so a
// transient failure does not silently escalate detach to cancel_all.
func (s *Server) detachExceedsOrphanCap(ctx context.Context, tenantID string) bool {
	all, err := s.store.List(ctx, tenantID, sessionstore.ListFilter{})
	if err != nil {
		return false
	}
	orphans := 0
	for _, r := range all {
		if session.IsTerminal(r.State) || r.ParentSessionID == "" {
			continue
		}
		root, err := s.store.Get(ctx, tenantID, s.treeRoot(ctx, r))
		if err != nil {
			continue
		}
		if session.IsTerminal(root.State) {
			orphans++
		}
	}
	return orphans > s.maxOrphanTasks
}

// treeRoot returns the id of the delegation tree's root by walking the
// ParentSessionID chain up from sess. The visited set guards against a
// malformed cyclic chain, and an ancestor that has been GC'd ends the
// walk at the deepest reachable node.
func (s *Server) treeRoot(ctx context.Context, sess sessionstore.Session) string {
	cur := sess
	visited := map[string]bool{}
	for cur.ParentSessionID != "" && !visited[cur.ID] {
		visited[cur.ID] = true
		parent, err := s.store.Get(ctx, cur.TenantID, cur.ParentSessionID)
		if err != nil {
			break
		}
		cur = parent
	}
	return cur.ID
}

// archivedTaskResult is the §8.8 TaskResult body the tree archive
// stores for a settled child session.
type archivedTaskResult sessionstore.Session

// MarshalJSON renders the §8.8 TaskResult fields v1 records: identity,
// terminal state, and an error code for a non-completed terminal state.
func (a archivedTaskResult) MarshalJSON() ([]byte, error) {
	body := map[string]any{
		"schemaVersion": 1,
		"taskId":        a.ID,
		"state":         string(a.State),
	}
	if a.State != session.StateCompleted {
		code := a.FailureReason
		if code == "" {
			code = "CHILD_" + strings.ToUpper(string(a.State))
		}
		body["error"] = map[string]any{
			"code":    code,
			"message": "child session ended in state " + string(a.State),
		}
	}
	return json.Marshal(body)
}
