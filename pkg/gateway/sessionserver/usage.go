// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"log"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/blobstore/artifactcatalog"
	"github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/gateway/billing/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/billing/usagestore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/errorclassify"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/orphancleanup"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/treearchive"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/treebudget"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessioncallback"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	sessionstate "github.com/lennylabs/lenny/pkg/session/state"
	"github.com/lennylabs/lenny/pkg/sessionrecord"
)

// releaseExecutor tears down a terminal session's executor state. When the
// executor is a §6.2 Sandbox-backed pod executor (SessionReleaser), it records
// the terminal disposition on the Sandbox before draining the pod; otherwise
// it falls back to Close (echo, subprocess, and other executors with no
// Sandbox phase). spec: §6.2.
func releaseExecutor(ctx context.Context, exec executor.Executor, sessionID string, disp executor.Disposition) error {
	return executor.ReleaseSession(ctx, exec, sessionID, disp)
}

// dispositionForState maps a session's terminal §6.2 state to the executor
// Disposition that drives the pod disposition at release time: a clean
// terminal (completed/cancelled/expired) recycles a recycling pod, while
// `failed` retires a pod that serves one session and releases the slot of
// a session on a concurrent pod (§5.2 Failure isolation). A non-terminal state (recordSessionCompleted is
// only called on a terminal transition, so this is defensive) carries no
// disposition and falls back to Close. spec: §6.2.
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
	// spec: §14 — the repeatable `?label=key=value` query scopes
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
			// spec: §14 — denormalize the session's labels so the
			// usage report is filterable by session label. F-14.1.13.
			Labels: cloneMetadata(sess.Labels),
		})
	}
	if s.billing != nil {
		// spec: §10.6 — stamp the session's environment on the
		// billing event so downstream rollups aggregate by environment.
		// F-10.6.9. spec: §11.2 — auto-populate
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
			// spec: §14 — denormalize the session's labels so the
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
// data are derived from the terminal state per the §14 data
// schemas. A session with no dispatcher, no callbackUrl, an unparseable
// pinned IP, or a non-terminal state is a no-op. spec: §14.
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
		// spec: §14 — without a stored pin the gateway cannot
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
// state. spec: §14. F-14.1.11.
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

// expiryReasonForState maps a §6.2 expiry to the §14
// expiryReason enum (max_session_age|max_idle_time). The failure reason
// carries the discriminator; an idle-timeout reason maps to max_idle_time
// and everything else to max_session_age. spec: §14. F-14.1.11.
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

// OnSessionTerminal is the TerminalHook of the writers that force a session
// terminal and go on to end further sessions: the watchdog, the orphan
// reconciler and orphan cleanup, the deadlock detector, the admin adapters
// behind the §24.11 force-terminate and the §12.8 drains, the §11.2 budget
// terminator, and the §8.3 BUDGET_KEYS_EXPIRED root write. It runs the
// funnel's terminal pipeline (recordSessionCompleted: the workspace seal,
// the drain, the terminal events and signals, the pod release, the budget
// forget and return, audit, SSE, billing, and archive), so these writers
// emit the same signals exactly once as a session terminated by REST.
//
// The pipeline runs detached: OnSessionTerminal opens a terminal claim of
// its own for the session, starts the pipeline on a goroutine over a context
// detached from ctx's cancellation, defers that claim's end inside the
// goroutine, and returns without waiting. One session's pipeline, which can
// last the seal window and for a delegated child joins the usage quiescence
// wait, therefore does not delay the caller's next session. The caller wrote
// the row under its own claim and ends that claim on its own path when
// OnSessionTerminal returns; the claim opened here keeps WaitTerminalWork
// and the terminal binding release seeing the pipeline as in progress until
// it returns.
//
// fromState is the session's pre-terminal state, which the writer read on
// the row it forced terminal; the terminal pod-release path keys off it so a
// maxSessionAge-expired running session is released through the §6.2
// executor recycle path rather than the by-name reclaim (§4.6).
//
// spec: §7.1 (Terminal pipeline context), §10.1.7 (Terminal pipelines at
// process exit.), §5.2, §6.2, §11.7, §7.2
func (s *Server) OnSessionTerminal(ctx context.Context, fromState session.State, sess sessionstore.Session) {
	end := s.BeginTerminal(sess.ID)
	hctx := context.WithoutCancel(ctx)
	go func() {
		defer end()
		s.recordSessionCompleted(hctx, fromState, sess)
	}()
}

// OnSessionExpiredFromAwaitingClientAction is the watchdog's
// awaiting-action-specific audit hook (§7.3 entry path). The
// watchdog fires it before the generic OnSessionTerminal so the §11.7
// session.expired_in_awaiting_action row precedes the generic
// session.expired row in the hash-chained audit log. spec: §7.3; §11.7 / §16.7. F-7.3.25.
func (s *Server) OnSessionExpiredFromAwaitingClientAction(ctx context.Context, sess sessionstore.Session) {
	s.emitAwaitingClientActionExpired(ctx, sess)
}

// OnSessionEnteredAwaitingClientAction is the watchdog's
// awaiting-action ENTRY hook (§6.2 — resume_pending
// wall-clock cap and resuming retries-exhausted branch). The hook
// drives the §7.3 session.awaiting_action webhook, the §16.6
// operational event, and the §11.7 / §16.7
// session.awaiting_action_entered audit row through the existing
// emitAwaitingClientActionEntered helper. Best-effort: a nil
// collaborator never rolls back the watchdog's state transition.
// spec: §6.2;
// §7.3; §11.7 / §16.7. F-6.2.14.
func (s *Server) OnSessionEnteredAwaitingClientAction(ctx context.Context, sess sessionstore.Session) {
	s.emitAwaitingClientActionEntered(ctx, sess)
}

// OnSessionRetryAttempt is the watchdog's resuming → resume_pending
// retry hook (§6.2). The watchdog fires it after the row
// update so the §16.1 lenny_session_retry_total counter and the §11.7
// / §16.7 session.retry_attempted audit row count the watchdog's spend
// edge. The other spend edge, a reported failure into resume_pending,
// fires the same pair from transitionToResumePending (failure.go).
// spec: §6.2; §7.3 (Retry and Resume) "Retry accounting.";
// §16.1 lenny_session_retry_total; §11.7 / §16.7 session.retry_attempted.
// F-6.2.14.
func (s *Server) OnSessionRetryAttempt(ctx context.Context, sess sessionstore.Session) {
	s.recordSessionRetry(ctx, sess)
}

// OnSessionExpired is the watchdog's platform-expiry-clock hook (the §6.2
// idle expiry of a session that cannot be idle-suspended, the §6.2
// suspended-session lifetime, the §11.3 maxSessionAge age cap, and the §7.3
// awaiting_client_action wall-clock deadline). The watchdog fires it on every
// `→ expired` transition it drives, with the §16.1.1 reason it resolved from
// the expiry edge, so the §16.1 lenny_session_expiry_total{pool, reason}
// counter sees the termination. Best-effort: a nil counter degrades to a no-op
// without rolling back the watchdog's state transition.
//
// spec: §16.1 (lenny_session_expiry_total{reason}); §16.1.1 (reason
// vocabulary); §6.2 (maxClientIdleSeconds clock); §11.3. F-11.3.7.
func (s *Server) OnSessionExpired(_ context.Context, sess sessionstore.Session, reason string) {
	if s.incSessionExpiry != nil {
		s.incSessionExpiry(sess.PoolRef, reason)
	}
}

// recordSessionCompleted is the terminal funnel of the funnel-path terminal
// writers. It runs the post-terminal hook (AfterTerminalWrite) first, with
// terminalSignals as the hook's signal tail, and then, on the row the hook
// returned, the session-log close hook, the input-wait forget, the quota
// checkpoint, and the billing event. The seal precedes every terminal
// signal, and the pod release follows the operational event, the callback,
// the terminal lifecycle signals, and the archive, child-failed, watermark,
// and cascade steps. All are best-effort: a failure never fails the
// transition that triggered it.
//
// fromState is the session's pre-terminal state, read by the caller on the
// locked row it overwrote. The terminal pod-release path keys off it: a
// session that was created/finalizing/ready before termination never
// launched and holds only a §4.6 claim, so its pod is reclaimed by name; a
// running/resuming session holds a live BindResult and is released through
// the executor path so the §6.2 recycle disposition is preserved (§4.6).
//
// spec: §15.1; §4.6; §7.1 (Normal Flow); §29.4 (Interrupt, terminate, and
// delete).
func (s *Server) recordSessionCompleted(ctx context.Context, fromState session.State, sess sessionstore.Session) {
	// spec: §7.1 (Terminal pipeline context) — one bounded, detached context
	// for the hook body and the session-log close that follows it, so the
	// budget is applied once and the close shares it.
	ctx, cancel := s.terminalContext(ctx, sess.TerminatedAt)
	defer cancel()
	sess = s.afterTerminalWrite(ctx, fromState, sess, s.terminalSignals)
	s.afterTerminalRelease(ctx, sess)
}

// terminalSignals is the funnel writers' terminal-signal tail, which the
// post-terminal hook runs after the terminal events and before the pod
// release, in today's order: the §16.6 operational event, the §14 callback,
// the lifecycle audit and retention roll, the §8.10 archive, the child_failed
// event, the §8.3 watermark, and the cascadeOnFailure evaluation of §29.4
// step 13. spec: §7.2; §29.4 (Interrupt, terminate, and delete).
func (s *Server) terminalSignals(ctx context.Context, sess sessionstore.Session) {
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
	// spec: §14 — POST the §14 CloudEvents callback to the
	// client-supplied callbackUrl when the session reaches a terminal
	// state. Best-effort: the isolated dispatcher runs the retry budget
	// off the transition path and clears the sealed secret when the
	// delivery settles. F-14.1.11.
	s.enqueueTerminalCallback(sess)
	// §7.2 / §11.7 / §7.1: the audit-visible terminal signals (the
	// lifecycle audit event and the retention-window roll).
	s.emitTerminalLifecycle(ctx, sess)
	// §8.10: a child session reaching a terminal state is archived to
	// the session_tree_archive so a resumed parent can replay it.
	s.archiveSettledChild(ctx, sess)
	// §8.10: a delegated child reaching `failed` injects
	// a `child_failed` event into the parent's session stream so the
	// parent agent can re-spawn, continue with partial results, or
	// propagate the failure upward without polling. F-8.10.2.
	s.emitChildFailed(ctx, sess)
	// §8.3: when the tree root settles, the delegation tree is
	// completing — observe the recorded parallel-children high-watermark
	// onto the §16.1 histogram. F-8.9.6.
	s.observeTreeHighWatermark(ctx, sess)
	// §8.10: apply the cascadeOnFailure policy to this session's
	// children now that it has reached a terminal state.
	s.cascadeToChildren(ctx, sess)
}

// afterTerminalRelease runs the funnel's steps that follow the post-terminal
// hook: the session-log close hook, the input-wait forget, the quota
// checkpoint, and the billing event. The budget forget is a step of the hook
// body itself (forgetBudgetState). spec: §4.4; §8.8; §11.2; §10.6.
func (s *Server) afterTerminalRelease(ctx context.Context, sess sessionstore.Session) {
	// §4.4: best-effort session-log close-hook. The hook
	// persists the session log it is handed as the
	// `/{tenant_id}/sessions/{session_id}/stderr.log` artifact. A
	// failure (MinIO unavailable, catalog insert error) logs and
	// drops rather than fail the terminal-state transition.
	// spec: §4.4 — Session logs.
	if s.sessionLogHook != nil {
		if err := s.sessionLogHook.OnSessionTerminal(ctx, sess.TenantID, sess.ID, nil, false); err != nil {
			log.Printf("lenny-gateway: session-log close-hook session=%s: %v", sess.ID, err)
		}
	}
	// spec: §8.8 — drop the per-session input-rounds counter
	// the inputwait Registry tracks for the §8.8 one_shot
	// constraint. The counter is process-local and only needed while
	// the session is non-terminal; reclaiming it here keeps a
	// long-running gateway from accumulating entries for sessions
	// whose rows are gone. F-8.8.10.
	if s.inputWaits != nil {
		s.inputWaits.ForgetSession(sess.ID)
	}
	// spec: §11.2 — on session completion the final cumulative
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
	// spec: §10.6 — environment stamp on terminal-state billing
	// event. F-10.6.9. spec: §11.2 — experiment/variant
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
		// spec: §14 — denormalize the session's labels onto the
		// terminal billing event so the metering stream stays label-
		// filterable across the session's full lifecycle. F-14.1.13.
		Labels: cloneMetadata(sess.Labels),
	})
}

// isPreRunningClaimState reports whether st is one of the §4.6 pre-running
// states (created/finalizing/ready/starting) in which a session holds a pod
// claimed at /create, so the durable binding is the persisted SandboxName. A
// `starting` row is mid-launch: the replica that launched it holds the live
// BindResult once it publishes, and terminalReclaimPreRunning defers to that
// binding through the executor release, while a replica without the binding
// (the starting watchdog, full_revoke, or a failed launch elsewhere)
// reclaims the claim by name. spec: §4.6 (durable binding), §7.2 (Session
// state machine: starting → failed).
func isPreRunningClaimState(st session.State) bool {
	switch st {
	case session.StateCreated, session.StateFinalizing, session.StateReady, session.StateStarting:
		return true
	}
	return false
}

// lookupBinding reads this replica's binding for the session, reporting none
// when no pod registry is wired.
func (s *Server) lookupBinding(sessionID string) (*podsession.BindResult, bool) {
	if s.podRegistry == nil {
		return nil, false
	}
	return s.podRegistry.Get(sessionID)
}

// terminalReclaimPreRunning releases the pod a session claimed at /create
// and revokes any lease the finalize barrier assigned when the session
// reaches a terminal state before it ever launched
// (created/finalizing/ready/starting) on a replica that holds no binding for
// it. Such a session registered no live BindResult here, so the executor release path
// (PodExecutor.Release) keys off the empty registry and returns early, leaking
// the claim and the lease. It reports true when it ran the reclaim so the
// caller skips the no-bind executor release.
//
// The discriminator is the pre-terminal state, not the mere absence of a local
// BindResult: the reclaim is scoped to created/finalizing/ready/starting
// (fromState),
// where the §4.6 durable binding is the persisted SandboxName alone. A
// running/resuming session is excluded even when this replica holds no live
// BindResult: the per-replica registry (registry.go) misses a
// coordinator-handed-off running session, and a running session always
// carries a persisted PodAssignment, so keying on the absent BindResult alone
// would route a clean-terminating running session through the pre-running
// reclaim, bypassing the §6.2 recycle disposition and the adapter scrub the
// binder's Release performs. When the gateway runs without a pod binder
// (in-memory mode) or the row carries no pod binding, the reclaim cannot apply
// and it reports false so the caller falls through to the executor path.
//
// ReclaimClaimed ends the occupancy by pool kind, which the row's frozen
// scrubPolicy decides: on a concurrent-workspace pool it releases the
// session's slot member and deletes the shared claim only when that release
// empties the set, so a live sibling keeps its pod; on an exclusive pool it
// deletes the claim by name. The terminal writers that reach this call are
// DELETE, terminate, cancel_child, the watchdog's created, finalizing, ready,
// and starting timeouts, the finalize failure write, and the /start failure
// write. The lease revoke inside
// ReclaimClaimed is a no-op for a created session that never assigned one and
// mandatory for a finalizing/ready session that always holds one (§7.1 step
// 23).
//
// spec: §15.1; §4.6 (durable binding, created/finalizing/ready/starting
// scope); §4.6.3
// (SandboxClaim: a release that ends the occupancy without reuse); §5.2 (Slot
// assignment atomicity); §6.2 (pre-attached disposition); §7.1 step 23
// (lease release).
func (s *Server) terminalReclaimPreRunning(ctx context.Context, fromState session.State, sess sessionstore.Session) bool {
	if !isPreRunningClaimState(fromState) {
		return false
	}
	if s.podBinder == nil || sess.PodAssignment == "" {
		return false
	}
	// With no pod registry wired this replica holds no binding, so the
	// claim is reclaimed by name.
	if _, bound := s.lookupBinding(sess.ID); bound {
		// A created/finalizing/ready session that nonetheless holds a live
		// BindResult on this replica (an SDK-warm preConnect pod attached at
		// finalize, for example) is released through the executor path so its
		// lease and pod follow the binder's Release rather than the by-name
		// claim DELETE.
		return false
	}
	if err := s.podBinder.ReclaimClaimed(ctx, sess.PodAssignment, sess.ID, sess.ScrubPolicy == ScrubPolicyPerSlot); err != nil {
		// Best-effort, like the rest of recordSessionCompleted: a release error
		// does not unwind the terminal-state transition. The §4.6.1 orphan-claim
		// GC backstops a leaked claim, and logging surfaces the failure so an
		// operator notices the pool not draining.
		log.Printf("lenny-gateway: reclaim claimed pod %s for pre-running terminal session %s: %v",
			sess.PodAssignment, sess.ID, err)
	}
	// spec: §7.1 (Post-terminal window) — the reclaim ran, so record the
	// release whatever its error.
	s.recordPodRelease(ctx, sess)
	return true
}

// archiveSettledChild records a terminal child session in the §8.10
// session_tree_archive under its recorded root. A session with no parent is
// the tree root and is not archived. Archiving is best-effort: a failure
// never fails the transition that triggered it. The archive returns no
// budget; the post-terminal hook's return step does (returnTreeBudget).
// spec: §8.10, §8.3 (Return on completion)
func (s *Server) archiveSettledChild(ctx context.Context, sess sessionstore.Session) {
	if s.treeArchive == nil || sess.ParentSessionID == "" {
		return
	}
	// spec: §8.8 — the envelope schemaVersion is immutable
	// once the first writer sets it. The archive is the durable carrier of
	// the §8.8 body, so this read-modify-write site preserves any version
	// a prior archive of the same node already recorded (a re-archive on
	// cascade, or a settle after a partial write). A rolling upgrade where
	// a newer replica knows a later schema therefore cannot silently
	// rewrite a record an older replica created. F-8.8.11.
	existingVer := 0
	if prev, err := s.treeArchive.GetByNode(ctx, sess.TenantID, sess.ID); err == nil {
		var prevResult sessionrecord.Result
		if json.Unmarshal([]byte(prev.Result), &prevResult) == nil {
			existingVer = prevResult.SchemaVersion
		}
	}
	result, _ := json.Marshal(s.materializeTaskResult(ctx, sess, existingVer))
	_ = s.treeArchive.Archive(ctx, treearchive.ArchivedNode{
		TenantID:        sess.TenantID,
		RootSessionID:   sess.RootSessionID,
		NodeSessionID:   sess.ID,
		ParentSessionID: sess.ParentSessionID,
		State:           string(sess.State),
		Result:          string(result),
		SettledAt:       s.clock(),
	})
}

// DefaultUsageQuiescenceTimeout is the §11.3 default of
// delegation.usageQuiescenceTimeoutSeconds, which a non-positive
// Options.UsageQuiescenceTimeout selects.
const DefaultUsageQuiescenceTimeout = 5 * time.Second

// UsageQuiescenceTimeoutFor resolves the usage quiescence timeout in effect
// for a configured value: the value when positive, and the §11.3 default
// otherwise. The server and the startup check resolve it the same way.
// spec: §11.3, §8.3 (Usage quiescence before budget return)
func UsageQuiescenceTimeoutFor(v time.Duration) time.Duration {
	if v <= 0 {
		return DefaultUsageQuiescenceTimeout
	}
	return v
}

// treeBudgetReturnAdmitted reports whether the post-terminal hook runs the
// §8.3 budget return for the row it received. It admits a delegated child
// (DelegationDepth > 0) on a server with a TreeBudgetReturner. A delegated
// row whose recorded root is its own id, which is how an empty root column
// reads back, is logged and skipped, because its return would address the
// child's own key set.
// spec: §8.3 (Return on completion), §7.1 (Terminal pipeline context)
func (s *Server) treeBudgetReturnAdmitted(sess sessionstore.Session) bool {
	if s.treeBudgetReturner == nil || sess.DelegationDepth == 0 {
		return false
	}
	if sess.RootSessionID == "" || sess.RootSessionID == sess.ID {
		slog.Warn("sessionserver: delegated child records no distinct tree root; budget return skipped",
			"session_id", sess.ID, "tenant_id", sess.TenantID, "root_session_id", sess.RootSessionID)
		return false
	}
	return true
}

// returnTreeBudget is the post-terminal hook's §8.3 budget return for an
// admitted delegated child. It waits for usage quiescence
// (awaitUsageQuiescence), reads the subtree's consumption, and returns the
// child's reservation on its recorded root: one tree_size unit, the
// per-node tree_memory share, the parent's parallel_children slot, and the
// unused tokens max(0, slice − subtree consumption), where the slice is the
// child's granted MaxTokenBudget. A consumption read error credits no
// tokens and still returns the other three deltas.
//
// A context that ends during the wait skips the read and the Return, and
// nothing retries them. A Return error, ErrBudgetKeysExpired included, is
// logged and leaves the reservation counted; a return that finds the
// sentinel absent is not a detection, so it calls no cleanup and counts no
// metric.
//
// spec: §7.1 (Terminal pipeline context), §8.3 (Return on completion), §8.3
// (Usage quiescence before budget return)
func (s *Server) returnTreeBudget(ctx context.Context, sess sessionstore.Session) {
	if !s.awaitUsageQuiescence(ctx, sess.TerminatedAt) {
		slog.Warn("sessionserver: terminal pipeline ended before the budget return",
			"session_id", sess.ID, "root_session_id", sess.RootSessionID, "error", ctx.Err())
		return
	}
	r := treebudget.Reservation{
		RootSessionID:         sess.RootSessionID,
		ParentSessionID:       sess.ParentSessionID,
		TreeSizeDelta:         1,
		TreeMemoryDelta:       treebudget.PerNodeMemoryBytes,
		ParallelChildrenDelta: 1,
		TokenDelta:            s.unusedTokens(ctx, sess),
	}
	if err := s.treeBudgetReturner.Return(ctx, r); err != nil {
		slog.Warn("sessionserver: delegation tree budget return failed; the reservation stays counted",
			"session_id", sess.ID, "root_session_id", sess.RootSessionID, "error", err)
	}
}

// unusedTokens is the token credit of a child's budget return:
// max(0, slice − subtree consumption). A zero slice, or a consumption read
// that fails, credits zero.
// spec: §8.3 (Return on completion)
func (s *Server) unusedTokens(ctx context.Context, sess sessionstore.Session) int64 {
	if sess.DelegationLease == nil || sess.DelegationLease.MaxTokenBudget <= 0 {
		return 0
	}
	used, err := s.taskUsage.SubtreeTokens(ctx, sess)
	if err != nil {
		slog.Warn("sessionserver: subtree token read failed; the budget return credits no tokens",
			"session_id", sess.ID, "root_session_id", sess.RootSessionID, "error", err)
		return 0
	}
	return max(0, sess.DelegationLease.MaxTokenBudget-used)
}

// awaitUsageQuiescence waits until terminatedAt plus the usage quiescence
// timeout on the server clock, through the server's timer seam. A zero
// terminatedAt counts the timeout from the call, and a point already passed
// waits not at all. The gateway has no consumer of the child's final usage
// report, so the wait always ends at the timeout. It reports false when ctx
// ends first.
// spec: §8.3 (Usage quiescence before budget return)
func (s *Server) awaitUsageQuiescence(ctx context.Context, terminatedAt time.Time) bool {
	wait := UsageQuiescenceTimeoutFor(s.usageQuiescenceTimeout)
	if !terminatedAt.IsZero() {
		wait -= s.clock().Sub(terminatedAt)
	}
	timer := s.quiescenceTimer
	if timer == nil {
		timer = time.After
	}
	if wait > 0 {
		select {
		case <-ctx.Done():
			return false
		case <-timer(wait):
		}
	}
	return ctx.Err() == nil
}

// observeTreeHighWatermark records the §8.3 per-tree
// parallel-children high-watermark onto the §16.1 histogram when the
// tree root settles. It fires only for a true tree root — a session
// whose RootSessionID is empty or equal to its own id and which has no
// parent — so the observation is sampled once per tree at completion
// rather than per settling child. Reading the counter clears it
// (GETDEL), so a re-settle of the same root never double-counts. A tree
// that admitted no delegation has no recorded watermark and is skipped.
// Best-effort: a Redis or read failure never fails the transition that
// triggered it. spec: §8.3; §16.1. F-8.9.6.
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
// (failure class and reason, and the §8.8 error code, category, message,
// and retryable flag the child's TaskResult carries for the same
// failure), and whether the child's retry budget was exhausted, the
// fields §8.10 enumerates, so the parent agent can decide
// to re-spawn a replacement, continue with partial results, or propagate
// the failure upward without polling or re-issuing await_children.
//
// The classification and retries_exhausted are independent. The
// classification records whether §7.3 classified the failure reason as
// retryable. retries_exhausted records whether the row's RetryCount
// reached the effective §7.3 retry budget, the same comparison the §8.8
// TaskResult error block makes. A child with a non-retryable final
// failure can still have spent its budget on earlier retryable failures,
// and a child with a retryable final failure can have budget left, so
// the event derives neither value from the other.
//
// Only the `failed` terminal state injects the event; a `cancelled` or
// `expired` child is a cascade or deadline outcome rather than a child
// failure the parent decides on. Best-effort: a nil event sink or
// marshal error never fails the transition that triggered it.
//
// spec: §8.10 (Delegation Tree Recovery);
// §8.8 (TaskRecord and TaskResult Schema); §7.3. F-8.10.2.
func (s *Server) emitChildFailed(ctx context.Context, sess sessionstore.Session) {
	if s.events == nil || sess.ParentSessionID == "" || sess.State != session.StateFailed {
		return
	}
	transient := session.ClassifyFailure(sess.FailureReason, sess.RetryPolicy) == session.FailureRetryable
	classification := "permanent"
	if transient {
		classification = "transient"
	}
	// spec: §8.10, §8.8, §15.2 — the error details carry the code and
	// category the child's TaskResult error block reports for the same
	// failure, so a parent reading child_failed sees DEADLOCK_TIMEOUT
	// rather than the stored reason. The code, category, message, and
	// retryable keys are the shared error taxonomy the MCP
	// notifications/lenny/error frame projects this event into.
	code := session.TaskErrorCode(sess.FailureReason, sess.State)
	category, retryable := errorclassify.Classify(code)
	data, err := json.Marshal(struct {
		ChildTaskID      string `json:"child_task_id"`
		Classification   string `json:"classification"`
		FailureClass     string `json:"failure_class,omitempty"`
		FailureReason    string `json:"failure_reason,omitempty"`
		Code             string `json:"code"`
		Category         string `json:"category"`
		Message          string `json:"message"`
		Retryable        bool   `json:"retryable"`
		RetriesExhausted bool   `json:"retries_exhausted"`
	}{
		ChildTaskID:    sess.ID,
		Classification: classification,
		FailureClass:   string(sess.FailureClass),
		FailureReason:  sess.FailureReason,
		Code:           code,
		Category:       string(category),
		Message:        "child session ended in state " + string(sess.State),
		Retryable:      retryable,
		// spec: §8.8 (TaskRecord and TaskResult Schema) — the spent
		// budget, independent of the retryable classification above.
		RetriesExhausted: sessionrecord.RetriesExhausted(sess.RetryCount, s.EffectiveMaxRetries(sess)),
	})
	if err != nil {
		return
	}
	s.events.PublishForTenant(sess.TenantID, sess.ParentSessionID, "child_failed", string(data), s.clock())
}

// cascadeToChildren applies the §8.10 cascadeOnFailure policy when
// sess reaches a terminal state. Under the default `cancel_all` policy
// the gateway cancels every descendant; `await_completion` and
// `detach` leave the children running. Every node's policy, the top
// session's and each cancelled descendant's, is resolved through
// effectiveCascade, which applies the §8.10 maxOrphanTasksPerTenant
// fallback, so a `detach` descendant shields its own subtree only while the
// tenant is within the cap. Each cancel is a hook-path terminal write whose
// post-terminal hook releases the descendant's pod. Best-effort: a failure
// never fails the transition that triggered it.
func (s *Server) cascadeToChildren(ctx context.Context, sess sessionstore.Session) {
	if s.effectiveCascade(ctx, sess) != session.CascadeCancelAll {
		return
	}
	// spec: §12.8 (Lifecycle fence and drain), §8.10 — breadth-first, one
	// level at a time. Each level's children come from a listing of the
	// primary taken after the parent's terminal commit, or after the
	// previous level's terminal writes returned, so a child the creation
	// fence admitted before its parent's terminal write is never missed. A
	// node's children are queued only when the node's own terminal write
	// applied and its effective policy, evaluated on the row that write
	// committed, is `cancel_all`: a `detach` node within the orphan cap keeps
	// its descendants alive.
	seen := map[string]bool{sess.ID: true}
	level := []string{sess.ID}
	for len(level) > 0 {
		byParent, err := sessionstore.ChildrenByParent(ctx, s.store, sess.TenantID)
		if err != nil {
			return
		}
		var next []string
		for _, parentID := range level {
			for _, cur := range byParent[parentID] {
				if seen[cur.ID] {
					continue
				}
				seen[cur.ID] = true
				updated, applied := s.cancelNodeGuarded(ctx, sess.TenantID, cur)
				if applied && s.effectiveCascade(ctx, updated) == session.CascadeCancelAll {
					next = append(next, cur.ID)
				}
			}
		}
		level = next
	}
}

// effectiveCascade resolves sess's cascadeOnFailure policy with the §8.10
// maxOrphanTasksPerTenant fallback applied: a `detach` session whose tenant
// is over the cap cascades as `cancel_all`, and the override is recorded as
// a `session.cascade_applied` audit row with reason `orphan_cap_fallback` and a
// structured log line, so the orchestrator that configured `detach` sees why.
// The caller passes a row that is already terminal, so that session's own live children count
// as orphans.
//
// spec: §8.10 (maxOrphanTasksPerTenant fallback); §11.7, §16.7
// session.cascade_applied.
func (s *Server) effectiveCascade(ctx context.Context, sess sessionstore.Session) session.CascadePolicy {
	policy := sess.CascadeOnFailure.Resolve()
	if policy != session.CascadeDetach || !s.detachExceedsOrphanCap(ctx, sess.TenantID) {
		return policy
	}
	s.recordCascadeApplied(ctx, sess, policy, session.CascadeCancelAll, "orphan_cap_fallback")
	return session.CascadeCancelAll
}

// cancelNodeGuarded makes the hook-path terminal write that cancels one
// delegation node and returns the row that write committed and whether it
// applied. cascadeToChildren calls it for each descendant it reaches, and the
// §8.3 BUDGET_KEYS_EXPIRED cleanup calls it for each node of the root's tree.
// An already-terminal row is skipped: its own terminal pipeline ran when it
// settled.
//
// spec: §7.2, §8.10, §8.3 — the claim opens before the Update, the write
// applies only over a non-terminal locked row and records the Terminated
// fact, and the post-terminal hook (cancel, seal, drain, the terminal events,
// the archive, the pod release with the cancelled disposition, and a
// delegated child's budget return) runs on a
// goroutine on a detached context, so one node's seal window does not delay
// the next. A node another writer already ended reports false, so the
// cascade evaluates no policy for it and queues none of its children.
func (s *Server) cancelNodeGuarded(ctx context.Context, tenantID string, cur sessionstore.Session) (sessionstore.Session, bool) {
	if session.IsTerminal(cur.State) {
		return sessionstore.Session{}, false
	}
	end := s.BeginTerminal(cur.ID)
	from, updated, err := s.writeTerminal(ctx, tenantID, cur.ID, session.StateCancelled, nonTerminalGuard)
	if err != nil {
		end()
		return sessionstore.Session{}, false
	}
	hctx := context.WithoutCancel(ctx)
	go func() {
		defer end()
		s.AfterTerminalWrite(hctx, from, updated, s.archiveSettledChild)
	}()
	return updated, true
}

// detachExceedsOrphanCap reports whether the tenant's active orphan
// count is over the §8.10 maxOrphanTasksPerTenant cap. The count is the
// orphan-cleanup selection itself: every non-terminal delegated session with
// an ancestor that is terminal or no longer exists, evaluated over one
// listing of the tenant, so the cap, the active-orphan gauges, and the sweep
// describe one set. The caller invokes this after a `detach`-policy session
// has terminated, so that session's own children are already counted. The
// listing reads the primary, as the cascade walk's does: the terminal write
// the caller just committed is not yet on a lagging §12.3 read replica, and a
// replica that still shows the session live hides its children from the
// count. Best-effort: a store error reports "not over cap" so a transient
// failure does not silently escalate detach to cancel_all.
//
// spec: §8.10 (Orphan cleanup), §12.3 (Read replicas).
func (s *Server) detachExceedsOrphanCap(ctx context.Context, tenantID string) bool {
	all, err := s.store.List(ctx, tenantID, sessionstore.ListFilter{Primary: true})
	if err != nil {
		return false
	}
	byID := orphancleanup.IndexByID(all)
	orphans := 0
	for _, r := range all {
		if _, ok := orphancleanup.Orphaned(r, byID); ok {
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
// single-element slice. spec: §15.1. F-15.1.31.
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
// null until every descendant settles per §8.8).
// existingSchemaVersion carries the version a prior archive of this node
// already recorded (0 when none); the body preserves it per the §8.8
// immutability rule. The `state` field uses the §8.8 MCP
// protocol spelling so a resumed parent replaying the archive sees the
// same value a live row would yield.
// spec: §8.8. F-8.8.2 / F-8.8.3 / F-8.8.4 / F-8.8.7 / F-8.8.11.
func (s *Server) materializeTaskResult(ctx context.Context, sess sessionstore.Session, existingSchemaVersion int) sessionrecord.Result {
	res := sessionrecord.Result{
		SchemaVersion: sessionrecord.ReconcileSchemaVersion(existingSchemaVersion, sessionrecord.SchemaVersion),
		TaskID:        sess.ID,
		State:         mcpStateForSession(sess.State),
	}
	if sess.State == session.StateCompleted {
		res.Output = s.buildTaskOutput(ctx, sess)
	} else {
		res.Error = taskErrorForSession(sess, s.EffectiveMaxRetries(sess))
	}
	if s.taskUsage != nil {
		res.Usage = s.taskUsage.Usage(ctx, sess)
		res.TreeUsage = s.taskUsage.TreeUsage(ctx, sess, res.Usage)
	}
	return res
}

// buildTaskOutput projects the §8.8 TaskResult.output block for a
// completed child: the child's final emitted part (the last non-caller
// turn of its transcript, mirroring the §8.8 projection) plus every deliverable
// `lenny-blob://` artifact the child catalogued. Both arrays are always
// present (possibly empty) when output is set, per the §8.8 / §28.5.3
// contract.
// spec: §8.8; §28.5.3. F-8.8.2.
func (s *Server) buildTaskOutput(ctx context.Context, sess sessionstore.Session) *sessionrecord.Output {
	out := &sessionrecord.Output{Parts: []sessionrecord.MessagePart{}, ArtifactRefs: []string{}}
	if s.transcripts != nil {
		if entries, err := s.transcripts.Get(ctx, sess.TenantID, sess.ID); err == nil {
			for i := len(entries) - 1; i >= 0; i-- {
				// spec: §8.8 — the transcript's user role maps
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
// spec: §8.8; §12.5. F-8.8.2.
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
// retriesExhausted reports whether the row's RetryCount reached
// maxRetries, the effective §7.3 retry budget the caller resolves with
// EffectiveMaxRetriesForRow. This mirrors the mcptools row-only fallback
// so the await path sees identical error blocks whether it reads the
// archived body or the live row.
// spec: §8.8; §15.2.1. F-8.8.4.
func taskErrorForSession(sess sessionstore.Session, maxRetries int) *sessionrecord.Error {
	// spec: §8.8 — the stored reason maps to its catalog code, such as
	// deadlock_timeout to DEADLOCK_TIMEOUT. F-8.8.6.
	code := session.TaskErrorCode(sess.FailureReason, sess.State)
	cat, _ := errorclassify.Classify(code)
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
// spec: §8.8, §7.2.
func mcpStateForSession(s session.State) string {
	proto, _ := sessionstate.MCPProtocolState(sessionstate.State(s))
	return proto
}
