// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/events"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
)

// DefaultArtifactRetention is the §7.1 line 77 default artifact
// retention window. Session artifacts (workspace snapshots, logs,
// transcripts) remain eligible for retrieval until this long after the
// session reaches a terminal state, after which the §4.5 retention GC
// is eligible to delete them. Deployers override the window via
// Options.DefaultRetention.
//
// spec: §7.1 line 77 — "Session artifacts ... are retained for a
// configurable TTL (default: 7 days, deployer-configurable)".
const DefaultArtifactRetention = 7 * 24 * time.Hour

// DefaultExpectedWorkspaceRoot is the §7.3 line 408 / §6.1 platform
// convention for the absolute `cwd` path mounted into every agent pod.
// The lenny-adapter's `--workspace-root` flag defaults to this same
// value; the gateway passes it on Resume so the adapter can assert that
// the replacement pod was provisioned with an identical mount path
// before extracting any checkpoint bytes. A deployer that customises
// the flag on either side will see the §7.3 step (d) assertion fire.
//
// spec: §7.3 line 408 — "Recreate same absolute `cwd` path";
// §6.1 — workspace volume convention. F-7.3.15.
const DefaultExpectedWorkspaceRoot = "/workspace/current"

// LifecycleAuditSink receives the §7.1 / §16.6 session-lifecycle audit
// events the gateway writes to the §11.7 hash-chained audit log. The
// server composes with the audit subsystem through this interface
// rather than importing it directly, mirroring DeriveAuditSink.
// Implementations must be non-blocking — the lifecycle paths emit
// best-effort and never wait for delivery.
type LifecycleAuditSink interface {
	// EmitSessionLifecycle records one session-lifecycle audit event.
	// The event type is one of the §11.7 session lifecycle event types
	// (session.created, session.completed, session.failed,
	// session.cancelled, session.expired).
	EmitSessionLifecycle(ctx context.Context, ev SessionLifecycleEvent)
}

// SessionLifecycleEvent is the §11.7 audit payload for a session
// lifecycle transition. The audit row's tenant scope is the session's
// own TenantID per the §11.7 line 428 write-time tenant-validation
// rule (the gateway derives it from session context, never from client
// input). Field names feed the §16.6 OCSF mapping.
type SessionLifecycleEvent struct {
	// EventType is the §11.7 audit event_type, e.g. "session.created".
	EventType string
	TenantID  string
	SessionID string
	UserID    string
	// RuntimeRef is the runtime the session targets, recorded for audit
	// correlation.
	RuntimeRef string
	// State is the session state at the time of the event.
	State string
	// FailureClass is the §7.1 coarse failure classification; populated
	// only for the session.failed terminal event.
	FailureClass string
	// Detail carries an event-specific human-readable note, e.g. the last
	// MinIO error recorded on a workspaceSealFailed event (§7.1 line 112).
	// Empty for events that need no detail.
	Detail string
	// At is the event wall-clock time (the gateway clock).
	At time.Time
}

// Session lifecycle audit event types written to the §11.7 audit log.
// The strings match the §16.6 OCSF mapping prefixes (session.created →
// API Activity Create 6003; the session. prefix maps the rest).
const (
	auditSessionCreated   = "session.created"
	auditSessionCompleted = "session.completed"
	auditSessionFailed    = "session.failed"
	auditSessionCancelled = "session.cancelled"
	auditSessionExpired   = "session.expired"
	// auditWorkspaceSealFailed is the §7.1 line 112 audit event emitted
	// when the seal-and-export retry window is exhausted. It records the
	// last export error in Detail. spec: §7.1 line 112 — "emits a
	// workspaceSealFailed audit event (recording the last MinIO error)".
	auditWorkspaceSealFailed = "session.workspace_seal_failed"

	// §7.3 retry/resume lifecycle audit event types. F-7.3.25 catalogs
	// the matching closed §16.7 constants in pkg/observability/audit;
	// these mirrors are the on-row strings the gateway emits.
	auditSessionResumed                 = "session.resumed"
	auditSessionRetryAttempted          = "session.retry_attempted"
	auditSessionAwaitingActionEntered   = "session.awaiting_action_entered"
	auditSessionExpiredInAwaitingAction = "session.expired_in_awaiting_action"
	auditSessionCascadeApplied          = "session.cascade_applied"

	// Interaction-resolution audit event types written by the §15.1
	// approve/deny/respond/dismiss endpoints. The strings match the
	// §16.7 catalog. spec: §7.2 lines 124-127; §11.7; §16.7. F-7.2.8.
	auditToolUseApproved      = "tool_use.approved"
	auditToolUseDenied        = "tool_use.denied"
	auditElicitationResponded = "elicitation.responded"
	auditElicitationDismissed = "elicitation.dismissed"

	// §7.4 / §16.6 workspace lifecycle audit event types. The strings
	// follow the §16.6 OCSF mapping prefixes — `session.upload` (line
	// 338) covers the `POST /v1/sessions/{id}/upload` and
	// `/upload-archive` blob-commit boundary; `session.finalize_workspace`
	// (line 339) covers the `POST /v1/sessions/{id}/finalize` transition
	// that consumes the upload token's digest. F-7.4.17.
	auditSessionUpload             = "session.upload"
	auditSessionWorkspaceFinalized = "session.finalize_workspace"
)

// InteractionAuditSink receives the §7.2 / §11.7 / §16.7
// interaction-resolution audit events the gateway writes when a user
// decision (tool-use approve/deny, elicitation respond/dismiss)
// resolves a pending interaction. It is a separate sink from
// LifecycleAuditSink because the event payload (the resolved
// interaction id, the resolution phase, an optional dismissal reason)
// is distinct from a session lifecycle transition. Implementations
// must be non-blocking — the resolution path emits best-effort and
// never waits for delivery.
//
// spec: §7.2 table lines 124-127 (resolution endpoints); §11.7 (the
// audit row contract); §16.7 (the OCSF mapping). F-7.2.8.
type InteractionAuditSink interface {
	EmitInteractionResolution(ctx context.Context, ev InteractionResolutionEvent)
}

// InteractionResolutionEvent is the §11.7 audit payload for one
// resolved interaction. The audit row's tenant scope is the session's
// own TenantID per the §11.7 line 428 write-time tenant-validation
// rule. Field names feed the §16.7 OCSF mapping.
//
// spec: §7.2 table lines 124-127; §11.7; §16.7. F-7.2.8.
type InteractionResolutionEvent struct {
	// EventType is one of auditToolUseApproved, auditToolUseDenied,
	// auditElicitationResponded, auditElicitationDismissed.
	EventType string
	TenantID  string
	SessionID string
	// UserID is the resolving user's subject — the authenticated caller
	// per the §15.1 (session_id, user_id, interaction_id) triple.
	UserID string
	// InteractionID is the tool_call_id (for tool-use approve/deny) or
	// elicitation_id (for elicitation respond/dismiss) the resolution
	// targets. Recording it is the post-incident link from the audit
	// row back to the interaction row.
	InteractionID string
	// Phase is the new interaction phase ("approved" / "denied" /
	// "responded" / "dismissed").
	Phase string
	// Reason carries the optional dismissal reason supplied by the
	// /deny body. Empty for the other three resolution paths.
	Reason string
	// At is the resolution wall-clock time.
	At time.Time
}

// auditEventTypeForTerminal maps a terminal session state to its §11.7
// session lifecycle audit event type. ok is false for a non-terminal
// state. Terminate transitions a session to Completed (§6.2), so no
// state maps to a distinct "terminated" audit type — completion covers
// it, matching the §16.6 operational-event treatment in usage.go.
func auditEventTypeForTerminal(st session.State) (eventType string, ok bool) {
	switch st {
	case session.StateCompleted:
		return auditSessionCompleted, true
	case session.StateFailed:
		return auditSessionFailed, true
	case session.StateCancelled:
		return auditSessionCancelled, true
	case session.StateExpired:
		return auditSessionExpired, true
	default:
		return "", false
	}
}

// emitStatusChange publishes the §7.2 line 137 status_change(state)
// event on the session's SSE stream. It is the platform-emitted signal
// that the session reached a new state; emitting it on every observable
// transition lets clients subscribed to GET /v1/sessions/{id}/events
// observe lifecycle changes live instead of polling
// GET /v1/sessions/{id}.
//
// spec: §7.2 line 137 — "status_change(state) | Session state
// transition (including suspended and input_required)".
func (s *Server) emitStatusChange(tenantID, sessionID string, st session.State) {
	s.publishEvent(tenantID, sessionID, "status_change", map[string]any{"state": string(st)})
}

// emitSessionComplete publishes the §7.2 line 141 session_complete(result)
// event once a session reaches a terminal state. The payload is the
// §8.8 TaskResult body (schemaVersion, taskId, state, and an error
// object for a non-completed terminal state), reusing the same encoder
// the §8.10 tree archive writes so the on-stream result matches the
// archived result.
//
// spec: §7.2 line 141 — "session_complete(result) | Session finished,
// result available".
func (s *Server) emitSessionComplete(sess sessionstore.Session) {
	s.publishEvent(sess.TenantID, sess.ID, "session_complete", archivedTaskResult(sess))
}

// emitTerminalLifecycle fires the client- and audit-visible signals
// every terminal session transition must produce, independent of the
// heavier teardown (seal, executor close, cascade, billing) that only
// the full completion path runs: the §7.2 status_change(state) and
// session_complete(result) SSE events, the §11.7 session lifecycle
// audit event, and the §7.1 line 77 retention-window roll. It is the
// shared hook for both terminal paths — recordSessionCompleted (the
// REST transitions, cancel cascade, and watchdog expiry) and
// failSession (the start-path claim/credential failure) — so a failed
// session emits the same lifecycle signals as a completed one. Every
// step is best-effort. The caller must only invoke this for a terminal
// state.
//
// spec: §7.2 lines 137, 141; §7.1 line 77; §16.6.
func (s *Server) emitTerminalLifecycle(ctx context.Context, sess sessionstore.Session) {
	if !session.IsTerminal(sess.State) {
		// Defensive: the terminal signals (notably session_complete and
		// the retention roll) are meaningless for a non-terminal state.
		// recordSessionCompleted is only invoked for terminal states in
		// production, but the guard keeps the helper self-consistent.
		return
	}
	s.emitStatusChange(sess.TenantID, sess.ID, sess.State)
	s.emitSessionComplete(sess)
	if s.lifecycleAudit != nil {
		if et, ok := auditEventTypeForTerminal(sess.State); ok {
			s.lifecycleAudit.EmitSessionLifecycle(ctx, SessionLifecycleEvent{
				EventType:    et,
				TenantID:     sess.TenantID,
				SessionID:    sess.ID,
				UserID:       sess.UserID,
				RuntimeRef:   sess.RuntimeRef,
				State:        string(sess.State),
				FailureClass: string(sess.FailureClass),
				At:           s.clock(),
			})
		}
	}
	s.rollRetentionOnTerminal(ctx, sess)
	// spec: §6.3 line 356 — the §6.3 TTFT tracker is in-memory only.
	// On terminal, drop the entry so the map size scales with
	// concurrently-streaming sessions rather than lifetime sessions.
	s.firstTokenObserved.Delete(sess.ID)
}

// emitAwaitingClientActionEntered fires the §16.6 / §11.7 / §7.2 signals
// every transition into `awaiting_client_action` must produce:
//   - the §7.2 line 137 status_change(awaiting_client_action) SSE frame,
//   - the §16.6 EventSessionAwaitingAction operational event (delivered to
//     a §14 callbackUrl webhook and the §25.5 subscription stream so CI
//     systems can react without polling per §7.3 line 427), and
//   - the §11.7 / §16.7 session.awaiting_action_entered audit row.
//
// Every step is best-effort: a nil collaborator or marshal failure must
// never roll back the state transition that triggered it. The caller
// must only invoke this when sess.State == StateAwaitingClientAction.
//
// spec: §7.3 line 427 (callbackUrl session.awaiting_action emission);
// §16.6 (operational event); §11.7 / §16.7 audit row; §7.2 line 137
// status_change. F-7.3.13 / F-7.3.25 (audit row).
func (s *Server) emitAwaitingClientActionEntered(ctx context.Context, sess sessionstore.Session) {
	if sess.State != session.StateAwaitingClientAction {
		// Defensive — the helper is only meaningful when the row is in
		// the awaiting state. Callers in production already guard, but
		// the no-op keeps the helper self-consistent if a future caller
		// invokes it from a state mutation that ran a concurrent compare-
		// and-swap and lost.
		return
	}
	s.emitStatusChange(sess.TenantID, sess.ID, sess.State)
	if s.opsEmitter != nil {
		payload := map[string]any{
			"sessionId": sess.ID,
			"runtime":   sess.RuntimeRef,
			"tenantId":  sess.TenantID,
		}
		data, _ := json.Marshal(payload)
		_, _ = s.opsEmitter.Emit(ctx, events.OperationalEvent{
			Source:          "/v1/sessions",
			Type:            events.EventSessionAwaitingAction.CloudEventsType(),
			Severity:        "warning",
			DataContentType: "application/json",
			Data:            data,
		})
	}
	if s.lifecycleAudit != nil {
		s.lifecycleAudit.EmitSessionLifecycle(ctx, SessionLifecycleEvent{
			EventType:  auditSessionAwaitingActionEntered,
			TenantID:   sess.TenantID,
			SessionID:  sess.ID,
			UserID:     sess.UserID,
			RuntimeRef: sess.RuntimeRef,
			State:      string(sess.State),
			At:         s.clock(),
		})
	}
}

// emitAwaitingClientActionExpired writes the §11.7 audit row for the
// `awaiting_client_action → expired` edge — the §7.3 line 423 entry path
// the watchdog drives on inactivity. The terminal lifecycle helpers
// (emitTerminalLifecycle, recordSessionCompleted) already cover the
// status_change(expired) / session_complete / session.expired writes;
// this helper adds only the distinct §7.3 audit row so SIEM/SOC
// dashboards can filter on the cause of the expiry.
//
// spec: §7.3 line 423 (awaiting_client_action → expired entry path);
// §11.7 / §16.7 audit row. F-7.3.25.
func (s *Server) emitAwaitingClientActionExpired(ctx context.Context, sess sessionstore.Session) {
	if s.lifecycleAudit == nil {
		return
	}
	s.lifecycleAudit.EmitSessionLifecycle(ctx, SessionLifecycleEvent{
		EventType:  auditSessionExpiredInAwaitingAction,
		TenantID:   sess.TenantID,
		SessionID:  sess.ID,
		UserID:     sess.UserID,
		RuntimeRef: sess.RuntimeRef,
		State:      string(sess.State),
		At:         s.clock(),
	})
}

// rollRetentionOnTerminal applies the §7.1 line 77 default artifact-
// retention window measured from the terminal transition. The window
// starts when the session settles, so the deadline is rolled forward
// to terminal_time + DefaultRetention. A client that already extended
// retention past that instant via POST /extend-retention keeps the
// longer deadline — the roll never shortens an explicit extension. The
// re-check inside the store mutation closes the race with a concurrent
// extend-retention write. Best-effort: a store failure leaves the
// create-time deadline in place rather than fail the terminal
// transition.
//
// spec: §7.1 line 77 (default 7-day retention) and §7.1.16 — the
// window starts at the terminal transition.
func (s *Server) rollRetentionOnTerminal(ctx context.Context, sess sessionstore.Session) {
	deadline := s.clock().Add(s.defaultRetention)
	if sess.RetentionExpiresAt.After(deadline) {
		// The client extended retention beyond the default window; do
		// not shorten it.
		return
	}
	_, _ = s.store.Update(ctx, sess.TenantID, sess.ID, func(row *sessionstore.Session) error {
		if row.RetentionExpiresAt.After(deadline) {
			// A concurrent extend-retention won the race.
			return nil
		}
		row.RetentionExpiresAt = deadline
		return nil
	})
}
