// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/netip"
	"strings"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/blobstore/artifactcatalog"
	"github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/gateway/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/delegationtree/treearchive"
	"github.com/lennylabs/lenny/pkg/gateway/delegationtree/treebudget"
	"github.com/lennylabs/lenny/pkg/gateway/errorclassify"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessioncallback"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/usagestore"
	sessionstate "github.com/lennylabs/lenny/pkg/session/state"
	"github.com/lennylabs/lenny/pkg/sessionrecord"
)

// releaseExecutor tears down a terminal session's executor state. When the
// executor is a §6.2 Sandbox-backed pod executor (SessionReleaser), it records
// the terminal disposition on the Sandbox before draining the pod; otherwise
// it falls back to Close (echo, subprocess, and other executors with no
// Sandbox phase). spec: §6.2 lines 105-117, 305.
func releaseExecutor(ctx context.Context, exec executor.Executor, sessionID string, disp executor.Disposition) error {
	return executor.ReleaseSession(ctx, exec, sessionID, disp)
}

// dispositionForState maps a session's terminal §6.2 state to the executor
// Disposition that drives the §3.4 pod disposition at release time: a clean
// terminal (completed/cancelled/expired) recycles a recycling pod, while
// `failed` always retires it. A non-terminal state (recordSessionCompleted is
// only called on a terminal transition, so this is defensive) carries no
// disposition and falls back to Close. spec: §3.4; §6.2 lines 24, 105-117, 157.
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

// terminatedReasonForState maps a session's terminal §6.2 state to the coded
// reason stamped on the session row's TerminatedReason field. It returns "" for
// a non-terminal state, which stampTerminatedCondition treats as "no fact to
// record". spec: §7.2 / §8.8 (Terminated session-condition fact).
func terminatedReasonForState(st session.State) string {
	switch st {
	case session.StateCompleted:
		return "Completed"
	case session.StateFailed:
		return "Failed"
	case session.StateCancelled:
		return "Cancelled"
	case session.StateExpired:
		return "Expired"
	default:
		return ""
	}
}

// stampTerminatedCondition records the §7.2 / §8.8 Terminated session-condition
// fact (TerminatedAt + TerminatedReason) on the session row when the row has
// reached a terminal state and the fact has not already been stamped. The
// gateway writes no Sandbox.status field for the terminal disposition (§4.6.3);
// the fact lives on the session row and is read through the session API (§7.2
// line 230). The write is best-effort and idempotent: a row already carrying a
// TerminatedAt is returned unchanged so a re-entrant terminal funnel does not
// churn the stamp, and a store error is logged but never rolls back the
// terminal transition. spec: §7.2 line 230; §8.8.
func (s *Server) stampTerminatedCondition(ctx context.Context, sess sessionstore.Session) sessionstore.Session {
	reason := terminatedReasonForState(sess.State)
	if reason == "" || !sess.TerminatedAt.IsZero() {
		return sess
	}
	now := s.clock()
	updated, err := s.store.Update(ctx, sess.TenantID, sess.ID, func(r *sessionstore.Session) error {
		if !r.TerminatedAt.IsZero() {
			return nil
		}
		r.TerminatedAt = now
		r.TerminatedReason = reason
		return nil
	})
	if err != nil {
		log.Printf("lenny-gateway: stamp terminated condition session=%s: %v", sess.ID, err)
		return sess
	}
	return updated
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
	// spec: §14 line 106 — the repeatable `?label=key=value` query scopes
	// the usage report to sessions carrying every requested label.
	// F-14.1.13.
	labelFilter := parseLabelFilter(r.URL.Query()["label"])
	report, err := s.usage.Aggregate(r.Context(), tenant, labelFilter)
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
			// spec: §14 line 106 — denormalize the session's labels so the
			// usage report is filterable by session label. F-14.1.13.
			Labels: cloneMetadata(sess.Labels),
		})
	}
	if s.billing != nil {
		// spec: §10.6 line 663 — stamp the session's environment on the
		// billing event so downstream rollups aggregate by environment.
		// F-10.6.9. spec: §11.2 lines 87-88 — auto-populate
		// experiment_id/variant_id from the session's experimentContext
		// for per-experiment / per-variant cost attribution. F-11.2.13.
		expID, varID := sess.ExperimentContext.Enrollment()
		_, _ = s.billing.Append(ctx, billingstore.Event{
			TenantID:      sess.TenantID,
			UserID:        sess.UserID,
			SessionID:     sess.ID,
			EventType:     billingstore.EventSessionCreated,
			EnvironmentID: sess.Environment,
			ExperimentID:  expID,
			VariantID:     varID,
			// spec: §14 line 106 — denormalize the session's labels so the
			// metering stream is filterable by session label. F-14.1.13.
			Labels: cloneMetadata(sess.Labels),
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

// enqueueTerminalCallback schedules the §14 session-completion webhook
// when the session carried a client callbackUrl. The CloudEvents type and
// data are derived from the terminal state per the §14 line 142-146 data
// schemas. A session with no dispatcher, no callbackUrl, an unparseable
// pinned IP, or a non-terminal state is a no-op. spec: §14 lines 108-150.
// F-14.1.11.
func (s *Server) enqueueTerminalCallback(sess sessionstore.Session) {
	if s.callbackDispatcher == nil || sess.CallbackURL == "" {
		return
	}
	shortName, data, ok := terminalCallbackEvent(sess)
	if !ok {
		return
	}
	pinned, err := netip.ParseAddr(sess.CallbackPinnedIP)
	if err != nil {
		// spec: §14 line 110 — without a stored pin the gateway cannot
		// dial the registration-time IP, so it does not deliver rather
		// than re-resolve and risk a rebind to an internal address.
		return
	}
	s.callbackDispatcher.Enqueue(sessioncallback.Job{
		TenantID:      sess.TenantID,
		SessionID:     sess.ID,
		RootSessionID: sess.RootSessionID,
		CallbackURL:   sess.CallbackURL,
		PinnedIP:      pinned,
		SealedSecret:  sess.CallbackSecret,
		ShortName:     shortName,
		Subject:       "session/" + sess.ID,
		Data:          data,
	})
}

// terminalCallbackEvent maps a terminal session state to its §14 webhook
// CloudEvents short name and data payload. ok is false for a non-terminal
// state. spec: §14 lines 142-146. F-14.1.11.
func terminalCallbackEvent(sess sessionstore.Session) (string, json.RawMessage, bool) {
	info := sessioncallback.SessionInfo{SessionID: sess.ID}
	switch sess.State {
	case session.StateCompleted:
		return sessioncallback.EventSessionCompleted, sessioncallback.CompletedData(info), true
	case session.StateFailed:
		info.ErrorCode = string(sess.FailureClass)
		info.ErrorMessage = sess.FailureReason
		return sessioncallback.EventSessionFailed, sessioncallback.FailedData(info), true
	case session.StateCancelled:
		info.Reason = sess.FailureReason
		return sessioncallback.EventSessionCancelled, sessioncallback.CancelledData(info), true
	case session.StateExpired:
		info.ExpiryReason = expiryReasonForState(sess)
		return sessioncallback.EventSessionExpired, sessioncallback.ExpiredData(info), true
	default:
		return "", nil, false
	}
}

// expiryReasonForState maps a §6.2 expiry to the §14 line 146
// expiryReason enum (max_session_age|max_idle_time). The failure reason
// carries the discriminator; an idle-timeout reason maps to max_idle_time
// and everything else to max_session_age. spec: §14 line 146. F-14.1.11.
func expiryReasonForState(sess sessionstore.Session) string {
	if strings.Contains(strings.ToLower(sess.FailureReason), "idle") {
		return "max_idle_time"
	}
	return "max_session_age"
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
// REST. fromState is the session's pre-terminal state, which the sweeper
// captured before it forced the row terminal; the terminal pod-release path
// keys off it so a maxSessionAge-expired running session is released through
// the §6.2 executor recycle path rather than the by-name reclaim (§4.6).
// spec: §5.2 line 519; §6.2 lines 105-117; §11.7; §7.2 lines 137,
// 141. Closes F-5.2.26.
func (s *Server) OnSessionTerminal(ctx context.Context, fromState session.State, sess sessionstore.Session) {
	s.recordSessionCompleted(ctx, fromState, sess)
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

// OnSessionExpired is the watchdog's platform-expiry-clock hook (§6.2
// maxClientIdleSeconds idle clock, §11.3 maxSessionAge age cap, §7.3
// awaiting_client_action wall-clock deadline). The watchdog fires it on every
// `→ expired` transition it drives, with the §16.1.1 reason it resolved from
// the expiry edge, so the §16.1 lenny_session_expiry_total{pool, reason}
// counter sees the termination. Best-effort: a nil counter degrades to a no-op
// without rolling back the watchdog's state transition.
//
// spec: §16.1 (lenny_session_expiry_total{reason}); §16.1.1 (reason
// vocabulary); §6.2 (maxClientIdleSeconds clock); §11.3 line 199 (max client
// idle row). F-11.3.7.
func (s *Server) OnSessionExpired(_ context.Context, sess sessionstore.Session, reason string) {
	if s.incSessionExpiry != nil {
		s.incSessionExpiry(sess.PoolRef, reason)
	}
}

// recordSessionCompleted runs the side effects of a session reaching a
// terminal state: it takes the §7.1 final workspace snapshot, releases
// the session's executor state — for a pod-backed session this shuts
// the runtime down and reclaims the pod — and emits the §11.2.1
// `session.completed` billing event. All are best-effort: a failure
// never fails the transition that triggered it.
//
// fromState is the session's pre-terminal state, captured by the caller
// before it overwrote the row with the terminal value. The terminal
// pod-release path keys off it: a session that was created/finalizing/ready
// before termination never launched and holds only a §4.6 claim, so its pod
// is reclaimed by name; a running/resuming session holds a live BindResult
// and is released through the executor path so the §6.2 recycle disposition
// is preserved (§4.6). spec: §15.1 line 620; §4.6.
func (s *Server) recordSessionCompleted(ctx context.Context, fromState session.State, sess sessionstore.Session) {
	// spec: §7.2 / §8.8 — record the Terminated session-condition fact on the
	// Postgres session row (the gateway is not a Sandbox.status writer, §4.6.3,
	// so the terminal disposition is a session-row field rather than a
	// Sandbox.status.conditions entry). recordSessionCompleted is the single
	// terminal funnel, so stamping here covers every terminal path (REST
	// terminate, watchdog force-terminate, cascade). Best-effort and
	// idempotent: a row already carrying the fact is left unchanged, and a
	// failed stamp never rolls back the terminal transition. F-6.2.12.
	sess = s.stampTerminatedCondition(ctx, sess)
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
			_ = s.opsEmitter.Emit(ctx, events.OperationalEvent{
				Source:          "/v1/sessions",
				Type:            et.CloudEventsType(),
				Severity:        severity,
				DataContentType: "application/json",
				Data:            data,
			})
		}
	}
	// spec: §14 lines 108-150 — POST the §14 CloudEvents callback to the
	// client-supplied callbackUrl when the session reaches a terminal
	// state. Best-effort: the isolated dispatcher runs the retry budget
	// off the transition path and clears the sealed secret when the
	// delivery settles. F-14.1.11.
	s.enqueueTerminalCallback(sess)
	// §7.2 lines 137, 141 / §11.7 / §7.1 line 77: the client- and
	// audit-visible terminal signals (status_change, session_complete,
	// the lifecycle audit event, and the retention-window roll). Shared
	// with failSession so the start-path failure emits the same signals.
	s.emitTerminalLifecycle(ctx, sess)
	// spec: §15.1 line 620 (/terminate releases the pod), §4.6 (durable
	// binding), §6.2 (pre-attached disposition), §7.1 step 23 (lease release)
	// — a session terminated before it ever launched (created/finalizing/ready)
	// holds a pod claimed at /create but registered no live BindResult, so the
	// executor release below keys off the empty registry and returns early
	// (PodExecutor.Release no-op), leaking both the claim and any lease the
	// finalize barrier assigned. Reclaim the claimed pod and revoke the lease
	// from the persisted binding instead, the same claimless reclaim the §4.5
	// created-expiry sweeper runs. A running/resuming session is released through
	// the executor path so its pod follows the §6.2 recycle disposition that
	// the binder's Release applies; the by-name reclaim is scoped to the
	// pre-running states (§4.6) by gating on fromState rather than on the mere
	// absence of a local BindResult, because a coordinator-handed-off running
	// session also lacks a local BindResult yet must not be reclaimed by name.
	// terminalReclaimPreRunning reports true only when it ran the reclaim, so the
	// executor release (the no-bind no-op for a pre-running session) is skipped
	// in that case.
	if !s.terminalReclaimPreRunning(ctx, fromState, sess) && s.executor != nil {
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
	// §8.10 lines 1080-1089: a delegated child reaching `failed` injects
	// a `child_failed` event into the parent's session stream so the
	// parent agent can re-spawn, continue with partial results, or
	// propagate the failure upward without polling. F-8.10.2.
	s.emitChildFailed(ctx, sess)
	// §8.3 line 379: when the tree root settles, the delegation tree is
	// completing — observe the recorded parallel-children high-watermark
	// onto the §16.1 histogram. F-8.9.6.
	s.observeTreeHighWatermark(ctx, sess)
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
	// spec: §8.8 line 869 — drop the per-session input-rounds counter
	// the inputwait Registry tracks for the §8.8 line 869 one_shot
	// constraint. The counter is process-local and only needed while
	// the session is non-terminal; reclaiming it here keeps a
	// long-running gateway from accumulating entries for sessions
	// whose rows are gone. F-8.8.10.
	if s.inputWaits != nil {
		s.inputWaits.ForgetSession(sess.ID)
	}
	// spec: §11.2 — drop the settled session's mid-session token-budget
	// accounting from the §4.9 LLM-proxy enforcer so the per-session map
	// does not retain an entry for a session whose row is gone. Runs for
	// every terminal (natural completion, failure, budget expiry, force
	// terminate) since recordSessionCompleted is the single terminal
	// funnel.
	if s.budgetForget != nil {
		s.budgetForget(sess.ID)
	}
	// spec: §11.2 line 44 — on session completion the final cumulative
	// token usage is written to Postgres as the authoritative value so a
	// subsequent Redis-recovery reconstruction has an accurate baseline.
	// Best-effort: a checkpoint failure never unwinds the terminal-state
	// transition. F-11.2.4.
	if s.quotaCheckpointer != nil && sess.UserID != "" {
		if err := s.quotaCheckpointer.CheckpointSubject(ctx, sess.TenantID, sess.UserID); err != nil {
			log.Printf("lenny-gateway: quota final checkpoint session=%s: %v", sess.ID, err)
		}
	}
	if s.billing == nil {
		return
	}
	// spec: §10.6 line 663 — environment stamp on terminal-state billing
	// event. F-10.6.9. spec: §11.2 lines 87-88 — experiment/variant
	// auto-population. F-11.2.13.
	expID, varID := sess.ExperimentContext.Enrollment()
	_, _ = s.billing.Append(ctx, billingstore.Event{
		TenantID:      sess.TenantID,
		UserID:        sess.UserID,
		SessionID:     sess.ID,
		EventType:     billingstore.EventSessionCompleted,
		EnvironmentID: sess.Environment,
		ExperimentID:  expID,
		VariantID:     varID,
		// spec: §14 line 106 — denormalize the session's labels onto the
		// terminal billing event so the metering stream stays label-
		// filterable across the session's full lifecycle. F-14.1.13.
		Labels: cloneMetadata(sess.Labels),
	})
}

// isPreRunningClaimState reports whether st is one of the §4.6 pre-running
// states (created/finalizing/ready) in which a session holds a pod claimed at
// /create but never launched a runtime, so the durable binding is the persisted
// SandboxName alone. It deliberately excludes `starting`: a `starting` session
// is mid-launch and the launching replica holds (or is establishing) a live
// BindResult, so its teardown follows the §6.2 executor-release recycle path
// rather than the by-name reclaim. spec: §4.6 (durable binding, scoped to
// created/finalizing/ready).
func isPreRunningClaimState(st session.State) bool {
	switch st {
	case session.StateCreated, session.StateFinalizing, session.StateReady:
		return true
	}
	return false
}

// terminalReclaimPreRunning releases the pod a session claimed at /create
// and revokes any lease the finalize barrier assigned when the session
// reaches a terminal state before it ever launched (created/finalizing/ready).
// Such a session registered no live BindResult, so the executor release path
// (PodExecutor.Release) keys off the empty registry and returns early, leaking
// the claim and the lease. It reports true when it ran the reclaim so the
// caller skips the no-bind executor release.
//
// The discriminator is the pre-terminal state, not the mere absence of a local
// BindResult: the by-name reclaim is scoped to created/finalizing/ready
// (fromState), where the §4.6 durable binding is the persisted SandboxName
// alone and ReclaimClaimed releases it by name (deleting the per-pod
// SandboxClaim and revoking the lease keyed by sessionID). A running/resuming
// session is excluded even when this replica holds no live BindResult: the
// per-replica registry (registry.go) misses a coordinator-handed-off running
// session, and a running session always carries a persisted PodAssignment, so
// keying on the absent BindResult alone would route a clean-terminating running
// session through the by-name claim DELETE, bypassing the §6.2 recycle
// disposition and the adapter scrub the binder's Release performs. Gating on
// fromState keeps the running-session teardown on the executor path (§10.1
// handoff coverage, the binder's Release). When the gateway runs without a pod
// binder or registry (in-memory mode) or the row carries no pod binding, the
// reclaim cannot apply and it reports false so the caller falls through to the
// executor path. The lease revoke inside ReclaimClaimed is a no-op for a
// created session that never assigned one and mandatory for a finalizing/ready
// session that always holds one (§7.1 step 23).
//
// spec: §15.1 line 620 (/terminate releases the pod); §4.6 (durable binding,
// created/finalizing/ready scope); §6.2 (pre-attached disposition); §7.1 step
// 23 (lease release); §4.5 (proposal, the claimless reclaim the created-expiry
// sweeper shares).
func (s *Server) terminalReclaimPreRunning(ctx context.Context, fromState session.State, sess sessionstore.Session) bool {
	if !isPreRunningClaimState(fromState) {
		return false
	}
	if s.podBinder == nil || s.podRegistry == nil || sess.PodAssignment == "" {
		return false
	}
	if _, bound := s.podRegistry.Get(sess.ID); bound {
		// A created/finalizing/ready session that nonetheless holds a live
		// BindResult on this replica (an SDK-warm preConnect pod attached at
		// finalize, for example) is released through the executor path so its
		// lease and pod follow the binder's Release rather than the by-name
		// claim DELETE.
		return false
	}
	if err := s.podBinder.ReclaimClaimed(ctx, sess.PodAssignment, sess.ID); err != nil {
		// Best-effort, like the rest of recordSessionCompleted: a release error
		// does not unwind the terminal-state transition. The §4.6.1 orphan-claim
		// GC backstops a leaked claim, and logging surfaces the failure so an
		// operator notices the pool not draining.
		log.Printf("lenny-gateway: reclaim claimed pod %s for pre-running terminal session %s: %v",
			sess.PodAssignment, sess.ID, err)
	}
	return true
}

// archiveSettledChild records a terminal child session in the §8.10
// session_tree_archive. A session with no parent is the tree root and
// is not archived. Archiving is best-effort: a failure never fails the
// transition that triggered it.
func (s *Server) archiveSettledChild(ctx context.Context, sess sessionstore.Session) {
	if s.treeArchive == nil || sess.ParentSessionID == "" {
		return
	}
	// spec: §8.8 lines 825-827 — the envelope schemaVersion is immutable
	// once the first writer sets it. The archive is the durable carrier of
	// the §8.8 body, so this read-modify-write site preserves any version
	// a prior archive of the same node already recorded (a re-archive on
	// cascade, or a settle after a partial write). A rolling upgrade where
	// a newer replica knows a later schema therefore cannot silently
	// rewrite a record an older replica created. F-8.8.11.
	existingVer := 0
	alreadyArchived := false
	if prev, err := s.treeArchive.GetByNode(ctx, sess.TenantID, sess.ID); err == nil {
		alreadyArchived = true
		var prevResult sessionrecord.Result
		if json.Unmarshal([]byte(prev.Result), &prevResult) == nil {
			existingVer = prevResult.SchemaVersion
		}
	}
	root := s.treeRoot(ctx, sess)
	result, _ := json.Marshal(s.materializeTaskResult(ctx, sess, existingVer))
	_ = s.treeArchive.Archive(ctx, treearchive.ArchivedNode{
		TenantID:        sess.TenantID,
		RootSessionID:   root,
		NodeSessionID:   sess.ID,
		ParentSessionID: sess.ParentSessionID,
		State:           string(sess.State),
		Result:          string(result),
		SettledAt:       s.clock(),
	})
	// spec: §8.2 line 130 — completed-subtree offload returns the §12.4
	// tree budget the node held: maxTreeMemoryBytes (the node's in-memory
	// footprint moves to Postgres) and the parent's parallel_children
	// slot (the child stopped running). Guarded on the first archive so a
	// re-archive on cascade does not double-return; the returnScript also
	// clamps at zero as a backstop.
	if !alreadyArchived {
		s.returnTreeBudget(ctx, sess, root)
	}
}

// returnTreeBudget releases the §12.4 budget a settled child consumed.
// It decrements the tree's maxTreeMemoryBytes counter by the per-node
// footprint and the per-parent parallel_children counter by one. Best
// effort: a return failure leaks budget conservatively (the §12.4 TTL
// reclaims it) and never fails the transition that triggered it.
// spec: §8.2 line 130; §12.4 line 193.
func (s *Server) returnTreeBudget(ctx context.Context, sess sessionstore.Session, rootSessionID string) {
	if s.treeBudgetReturner == nil {
		return
	}
	_ = s.treeBudgetReturner.Return(ctx, treebudget.Reservation{
		RootSessionID:         rootSessionID,
		ParentSessionID:       sess.ParentSessionID,
		TreeMemoryDelta:       treebudget.PerNodeMemoryBytes,
		ParallelChildrenDelta: 1,
	})
}

// observeTreeHighWatermark records the §8.3 line 379 per-tree
// parallel-children high-watermark onto the §16.1 histogram when the
// tree root settles. It fires only for a true tree root — a session
// whose RootSessionID is empty or equal to its own id and which has no
// parent — so the observation is sampled once per tree at completion
// rather than per settling child. Reading the counter clears it
// (GETDEL), so a re-settle of the same root never double-counts. A tree
// that admitted no delegation has no recorded watermark and is skipped.
// Best-effort: a Redis or read failure never fails the transition that
// triggered it. spec: §8.3 line 379; §16.1 line 73. F-8.9.6.
func (s *Server) observeTreeHighWatermark(ctx context.Context, sess sessionstore.Session) {
	if s.hwmReader == nil || s.hwmObserver == nil {
		return
	}
	// A tree root is a parentless session that is its own root. A
	// delegated child (or a session whose RootSessionID points elsewhere)
	// is not the tree apex, so its settle does not signal tree completion.
	if sess.ParentSessionID != "" {
		return
	}
	if sess.RootSessionID != "" && sess.RootSessionID != sess.ID {
		return
	}
	value, found, err := s.hwmReader.ObserveHighWatermark(ctx, sess.ID)
	if err != nil || !found {
		return
	}
	s.hwmObserver.ObserveDelegationParallelChildrenHighWatermark(sess.PoolRef, sess.TenantID, value)
}

// emitChildFailed injects the §8.10 `child_failed` event into the
// parent's session stream when a delegated child reaches the `failed`
// terminal state. The payload carries the child task id, the
// transient/permanent failure classification, the coded error details
// (failure class and reason), and whether the gateway's retry budget for
// the child was exhausted — the four fields the spec enumerates — so the
// parent agent can decide to re-spawn a replacement, continue with
// partial results, or propagate the failure upward without polling or
// re-issuing await_children.
//
// A child reaching `failed` after a transient (retryable) cause means
// its per-child retry budget was exhausted; a permanent
// (non-retryable / unknown / unclassified) cause short-circuits to
// `failed` without consuming retries, so retries_exhausted is false in
// that case. Only the `failed` terminal state injects the event — a
// `cancelled` or `expired` child is a cascade / deadline outcome, not a
// child failure the parent decides on. Best-effort: a nil event sink or
// marshal error never fails the transition that triggered it.
//
// spec: §8.10 lines 1080-1089 (child_failed notification);
// §7.3 lines 285-326 (transient/permanent classification). F-8.10.2.
func (s *Server) emitChildFailed(ctx context.Context, sess sessionstore.Session) {
	if s.events == nil || sess.ParentSessionID == "" || sess.State != session.StateFailed {
		return
	}
	transient := session.ClassifyFailure(sess.FailureReason, sess.RetryPolicy) == session.FailureRetryable
	classification := "permanent"
	if transient {
		classification = "transient"
	}
	data, err := json.Marshal(struct {
		ChildTaskID      string `json:"child_task_id"`
		Classification   string `json:"classification"`
		FailureClass     string `json:"failure_class,omitempty"`
		FailureReason    string `json:"failure_reason,omitempty"`
		RetriesExhausted bool   `json:"retries_exhausted"`
	}{
		ChildTaskID:      sess.ID,
		Classification:   classification,
		FailureClass:     string(sess.FailureClass),
		FailureReason:    sess.FailureReason,
		RetriesExhausted: transient,
	})
	if err != nil {
		return
	}
	s.events.PublishForTenant(sess.TenantID, sess.ParentSessionID, "child_failed", string(data), s.clock())
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
	originalPolicy := sess.CascadeOnFailure.Resolve()
	policy := originalPolicy
	orphanCapFallback := false
	if policy == session.CascadeDetach && s.detachExceedsOrphanCap(ctx, sess.TenantID) {
		// spec: §8.10 line 1103 — maxOrphanTasksPerTenant fallback. Emit
		// the §16.7 `session.cascade_applied` audit row, a structured log
		// line, and a §7.2 status_change on the parent's SSE event stream
		// so the orchestrator that configured `detach` sees the override
		// reason. F-8.10.8.
		policy = session.CascadeCancelAll
		orphanCapFallback = true
		s.recordCascadeApplied(ctx, sess, originalPolicy, policy, "orphan_cap_fallback")
	}
	if policy != session.CascadeCancelAll {
		return
	}
	_ = orphanCapFallback // referenced via recordCascadeApplied above
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
		// spec: §11.3 line 236 / §11.4 line 258 — a cascaded cancel must
		// drain the descendant's pod, not merely flip its row. Releasing the
		// executor records the §6.2 cancelled disposition and triggers the
		// adapter's graceful shutdown so the descendant runtime stops holding
		// tokens, executing tool calls, and charging its credential lease.
		// Without this the only sink for a cascaded cancel is the watchdog's
		// maxSessionAge clock (hours later) and every descendant pod leaks.
		// Best-effort: a teardown failure never unwinds the cancel. F-11.3.9.
		if s.executor != nil {
			if err := releaseExecutor(ctx, s.executor, cur.ID, executor.DispositionCancelled); err != nil {
				log.Printf("lenny-gateway: cascade executor release session=%s: %v", cur.ID, err)
			}
		}
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

// subtreeSessionIDs returns the session's own id plus every descendant
// session id in its §8 delegation subtree. It unions the live session
// store (running and not-yet-GC'd rows) with the §8.10 tree archive
// (settled children whose live rows may already be reclaimed) so a
// tree-aggregated usage total reflects the whole subtree rather than only
// the sessions that happen to still be resident. The returned slice
// always contains root.ID; a leaf session with no descendants returns a
// single-element slice. spec: §15.1 line 676 (tree-aggregated usage
// including all descendant tasks). F-15.1.31.
func (s *Server) subtreeSessionIDs(ctx context.Context, tenantID string, root sessionstore.Session) []string {
	children := map[string][]string{}
	addEdge := func(parent, child string) {
		if parent == "" || child == "" || parent == child {
			return
		}
		children[parent] = append(children[parent], child)
	}
	if rows, err := s.store.List(ctx, tenantID, sessionstore.ListFilter{}); err == nil {
		for _, row := range rows {
			addEdge(row.ParentSessionID, row.ID)
		}
	}
	if s.treeArchive != nil {
		// The archive is keyed by the tree's root session, so resolve the
		// apex before replaying: a delegated child carries its root id
		// directly, and a root resolves to itself via the live lineage walk.
		// Replaying the apex yields every archived node in the whole tree;
		// the breadth-first walk below restricts the result to the subtree
		// under root.ID, so the handler works for a mid-tree node too.
		rootID := root.RootSessionID
		if rootID == "" {
			rootID = s.treeRoot(ctx, root)
		}
		if nodes, err := s.treeArchive.Replay(ctx, tenantID, rootID); err == nil {
			for _, n := range nodes {
				addEdge(n.ParentSessionID, n.NodeSessionID)
			}
		}
	}
	seen := map[string]bool{root.ID: true}
	ids := []string{root.ID}
	queue := []string{root.ID}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, child := range children[cur] {
			if seen[child] {
				continue
			}
			seen[child] = true
			ids = append(ids, child)
			queue = append(queue, child)
		}
	}
	return ids
}

// materializeTaskResult builds the §8.8 TaskResult body the tree archive
// stores for a settled child. This is the single materialization site
// for the rich body: the gateway has the session row, its transcript,
// and the §12.5 artifact catalog in scope here, so it populates
// output.parts (the child's final emitted turn) and output.artifactRefs
// (the child's catalogued `lenny-blob://` artifacts) for a completed
// child, and the error block (code, category, retriesExhausted) for a
// terminal failure. When a §8.8 usage Builder is wired it also stamps
// usage (this task's own consumption) and treeUsage (the subtree rollup,
// null until every descendant settles per §8.8 line 917).
// existingSchemaVersion carries the version a prior archive of this node
// already recorded (0 when none); the body preserves it per the §8.8
// immutability rule. The `state` field uses the §8.8 line 857 MCP
// protocol spelling so a resumed parent replaying the archive sees the
// same value a live row would yield.
// spec: §8.8 lines 855-940. F-8.8.2 / F-8.8.3 / F-8.8.4 / F-8.8.7 / F-8.8.11.
func (s *Server) materializeTaskResult(ctx context.Context, sess sessionstore.Session, existingSchemaVersion int) sessionrecord.Result {
	res := sessionrecord.Result{
		SchemaVersion: sessionrecord.ReconcileSchemaVersion(existingSchemaVersion, sessionrecord.SchemaVersion),
		TaskID:        sess.ID,
		State:         mcpStateForSession(sess.State),
	}
	if sess.State == session.StateCompleted {
		res.Output = s.buildTaskOutput(ctx, sess)
	} else {
		res.Error = taskErrorForSession(sess)
	}
	if s.taskUsage != nil {
		res.Usage = s.taskUsage.Usage(ctx, sess)
		res.TreeUsage = s.taskUsage.TreeUsage(ctx, sess, res.Usage)
	}
	return res
}

// buildTaskOutput projects the §8.8 TaskResult.output block for a
// completed child: the child's final emitted part (the last non-caller
// turn of its transcript, mirroring the §8.8 line 815 "terminal state on
// the final agent turn" projection) plus every deliverable
// `lenny-blob://` artifact the child catalogued. Both arrays are always
// present (possibly empty) when output is set, per the §8.8 / §15.4.1
// contract.
// spec: §8.8 lines 888-896; §15.4.1. F-8.8.2.
func (s *Server) buildTaskOutput(ctx context.Context, sess sessionstore.Session) *sessionrecord.Output {
	out := &sessionrecord.Output{Parts: []sessionrecord.MessagePart{}, ArtifactRefs: []string{}}
	if s.transcripts != nil {
		if entries, err := s.transcripts.Get(ctx, sess.TenantID, sess.ID); err == nil {
			for i := len(entries) - 1; i >= 0; i-- {
				// spec: §8.8 lines 810-817 — the transcript's user role maps
				// to caller; assistant/system map to agent. The child's
				// emitted result is its final agent turn.
				if entries[i].Role != "user" {
					out.Parts = append(out.Parts, sessionrecord.TextPart(entries[i].Content))
					break
				}
			}
		}
	}
	if s.artifacts != nil {
		if rows, err := s.artifacts.ListBySession(ctx, sess.TenantID, sess.ID); err == nil {
			for _, r := range rows {
				if isDeliverableArtifact(r) {
					out.ArtifactRefs = append(out.ArtifactRefs, r.URI)
				}
			}
		}
	}
	return out
}

// isDeliverableArtifact reports whether a §12.5 catalog row is a child's
// deliverable output: a live (non-soft-deleted, non-tombstoned)
// workspace or export artifact. Internal artifact kinds (checkpoint,
// eviction_context, session_log) are gateway bookkeeping rather than the
// child's emitted output and are excluded from artifactRefs. The empty
// ArtifactType defaults to workspace per the §12.5 catalog Insert.
// spec: §8.8 lines 888-896; §12.5 lines 309-321. F-8.8.2.
func isDeliverableArtifact(r artifactcatalog.Record) bool {
	if r.State != artifactcatalog.StateLive {
		return false
	}
	switch r.ArtifactType {
	case artifactcatalog.ArtifactTypeWorkspace, artifactcatalog.ArtifactTypeExport, "":
		return true
	default:
		return false
	}
}

// taskErrorForSession builds the §8.8 TaskResult.error block for a
// non-completed terminal child from the session row. The code falls back
// to the per-state CHILD_<STATE> literal when no FailureReason is set;
// the category routes through the shared §15.2.1 classifier so the value
// matches the REST and MCP error envelopes for the same code; and
// retriesExhausted reports whether the gateway consumed the row's
// automatic-recovery budget. This mirrors the mcptools row-only fallback
// so the await path sees identical error blocks whether it reads the
// archived body or the live row.
// spec: §8.8 lines 922-940; §15.2.1. F-8.8.4.
func taskErrorForSession(sess sessionstore.Session) *sessionrecord.Error {
	code := sess.FailureReason
	if code == "" {
		code = "CHILD_" + strings.ToUpper(string(sess.State))
	}
	cat, _ := errorclassify.Classify(code)
	maxRetries := 0
	if sess.RetryPolicy != nil {
		maxRetries = sess.RetryPolicy.MaxRetries
	}
	return &sessionrecord.Error{
		Code:             code,
		Category:         string(cat),
		Message:          "child session ended in state " + string(sess.State),
		RetriesExhausted: sessionrecord.RetriesExhausted(sess.RetryCount, maxRetries),
	}
}

// mcpStateForSession mirrors the §8.8 MCP projection used by the MCP
// taskResult builder so an archived row produces the same protocol
// state string a live row produces. §8.8 defines the canonical task
// machine as the §7.2 session machine, so every state routes through
// the single sessionstate.MCPProtocolState projection; the metadata
// annotations are discarded on this single-string path. The terminal
// and input_required spellings are byte-identical to the former
// task-level table. F-8.8.7.
// spec: §8.8 lines 855-883, §7.2.
func mcpStateForSession(s session.State) string {
	proto, _ := sessionstate.MCPProtocolState(sessionstate.State(s))
	return proto
}
