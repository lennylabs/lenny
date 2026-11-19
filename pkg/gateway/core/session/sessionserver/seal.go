// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/observability/tracing"
)

// DefaultWorkspaceSealMaxDuration is the §7.1
// maxWorkspaceSealDurationSeconds default: the total wall-clock window
// the gateway retries the seal-and-export before giving up and failing
// the session with workspace_seal_timeout. Deployers override it via
// Options.WorkspaceSealMaxDuration.
//
// spec: §7.1 — "bounded by maxWorkspaceSealDurationSeconds
// (pool-level configuration, default: 300s)".
const DefaultWorkspaceSealMaxDuration = 300 * time.Second

// sealBackoffInitial and sealBackoffCap are the §7.1 fixed
// exponential-backoff parameters for a failed seal: the first retry
// waits sealBackoffInitial, each subsequent wait doubles, capped per
// attempt at sealBackoffCap. These are spec constants, not deployer
// configuration; only the total window (sealMaxDuration) is tunable.
//
// spec: §7.1 — "exponential backoff (initial: 5s, factor: 2×,
// cap: 60s per attempt)".
const (
	sealBackoffInitial = 5 * time.Second
	sealBackoffCap     = 60 * time.Second
)

// sealOutcomeSuccess, sealOutcomeTimeout, and sealOutcomePermanent are
// the §7.1 label values on
// lenny_workspace_seal_duration_seconds. `permanent` records a seal that
// ended on a condition the §7.1 Seal-and-export invariant classifies as
// permanent (an export error the adapter reports as permanent, a binding
// lost during the seal, or a session row absent on the primary) and
// returned immediately without retrying; only the `timeout` arm fires
// the WorkspaceSealStuck alert.
//
// spec: §7.1 (Seal-and-export invariant)
const (
	sealOutcomeSuccess   = "success"
	sealOutcomeTimeout   = "timeout"
	sealOutcomePermanent = "permanent"
)

// isPermanentSealError reports whether err ends the seal window on its
// first observation. Two conditions qualify. The first is a gRPC status the
// seal adapter reports as permanent: FailedPrecondition (the §4.4
// workspace-size probe rejection, which re-probes identically on every
// retry), InvalidArgument, or Unimplemented. The second is
// sessionstore.ErrNotFound, which the Checkpointer returns when the
// session's row is absent on the primary: the gateway drives no export for a
// session whose row no longer exists. Either condition re-produces
// identically on each attempt, so retrying it for the full §7.1 window only
// delays the identical terminal state. An adapter gRPC NotFound stays
// transient, because its meaning at the adapter is ambiguous.
//
// spec: §7.1 (Seal-and-export invariant) — "an export failure the adapter
// reports as permanent ends the window on its first observation with the
// same terminal outcome".
func isPermanentSealError(err error) bool {
	if errors.Is(err, sessionstore.ErrNotFound) {
		return true
	}
	switch status.Code(err) {
	case codes.FailedPrecondition, codes.InvalidArgument, codes.Unimplemented:
		return true
	default:
		return false
	}
}

// sleepWithContext is the production seal-retry wait: it blocks for d or
// until ctx is cancelled, returning true when the full delay elapsed and
// false when ctx ended first (so the caller stops retrying).
func sleepWithContext(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// sealWorkspace runs the §7.1 seal-and-export with the spec's bounded
// exponential backoff. On a successful seal it observes
// lenny_workspace_seal_duration_seconds{outcome="success"} and returns
// nil. When the retry window (sealMaxDuration) is exhausted, or ctx is
// cancelled mid-backoff, it observes outcome="timeout" and returns the
// last export error so the caller transitions the session to
// workspace_seal_timeout and terminates the pod anyway.
//
// The §7.1 invariant holds the pod in draining and retries the export
// until it succeeds or the window closes; this loop is that retry. A
// session with no binding on this replica (or a server with no pod
// registry) has no live workspace here, so sealWorkspace skips the Sealer
// and returns nil with no delay. With a binding, it marks the binding
// closing and evicts the cached Attach stream before the first attempt, so
// no turn and no non-seal checkpoint runs while the export reads the
// workspace; the export itself uses the separate Checkpoint RPC, which the
// eviction does not disturb. An attempt that fails after the binding was
// removed from the registry has not exported the workspace and ends the
// window with the permanent outcome.
//
// spec: §7.1 (Seal-and-export invariant) — backoff initial 5s, factor 2×,
// cap 60s per attempt, total window maxWorkspaceSealDurationSeconds; §7.1
// (Delivery and checkpoints during the seal).
func (s *Server) sealWorkspace(ctx context.Context, sess sessionstore.Session) (retErr error) {
	// spec: §16.3 — the gateway-side `session.seal_and_export`
	// span covers the whole bounded-backoff retry loop, so a trace shows
	// the full seal window and its terminal outcome. A seal timeout records
	// the last export error on the span (TRANSIENT: a MinIO outage is a
	// retryable downstream condition the loop already exhausted). The
	// pod-side span stitches under it via the inherited trace context.
	ctx, span := tracing.NewTracer(nil).Start(ctx, tracing.SpanSessionSealAndExport)
	defer func() {
		tracing.RecordError(span, tracing.CategorizeError(retErr, tracing.CategoryTransient))
		span.End()
	}()
	// The clock-derived deadline below bounds the retries on the injected
	// clock, which tests fake. This context bounds the window in real time
	// as well, so a Seal call that never returns cannot hold the caller past
	// it: on a stream-failure report's failed edge the release, the parent
	// notification, and the cascade still run on the report's remaining
	// time. spec: §7.1; §28.5.1 (CH-ATTACH Degradation.).
	ctx, cancel := context.WithTimeout(ctx, s.sealMaxDuration)
	defer cancel()
	start := s.clock()
	if !s.beginSealClosing(sess.ID) {
		// spec: §7.1 (Seal-and-export invariant) — no binding on this
		// replica, so there is no workspace here to export; the outcome is
		// the one a no-op seal records.
		s.observeWorkspaceSeal(sess, start, sealOutcomeSuccess)
		return nil
	}
	deadline := start.Add(s.sealMaxDuration)
	backoff := sealBackoffInitial
	var lastErr error
	for {
		err := s.sealer.Seal(ctx, sess.TenantID, sess.ID)
		if err == nil {
			s.observeWorkspaceSeal(sess, start, sealOutcomeSuccess)
			return nil
		}
		lastErr = err
		// spec: §7.1 (Seal-and-export invariant) — a seal whose binding on
		// this replica was removed after the seal began has not exported
		// the workspace, and no later attempt can, so the window ends on
		// the first observation with the permanent outcome.
		if _, bound := s.podRegistry.Get(sess.ID); !bound {
			s.observeWorkspaceSeal(sess, start, sealOutcomePermanent)
			return lastErr
		}
		// spec: §7.1 — an export failure the adapter reports as
		// permanent ends the window on its first observation with the same
		// terminal outcome. The workspace-size probe rejection re-produces
		// identically on every retry, so retrying it for the full window
		// only delays the identical failed/workspace_seal_timeout state and
		// would fire the WorkspaceSealStuck MinIO-unavailability alert on a
		// tenant configuration condition with no MinIO involvement.
		if isPermanentSealError(err) {
			s.observeWorkspaceSeal(sess, start, sealOutcomePermanent)
			return lastErr
		}
		// Stop when the next attempt would start at or past the window;
		// retrying past maxWorkspaceSealDurationSeconds is forbidden so a
		// permanent MinIO outage cannot hold the pod in draining forever.
		now := s.clock()
		if !now.Add(backoff).Before(deadline) {
			s.observeWorkspaceSeal(sess, start, sealOutcomeTimeout)
			return lastErr
		}
		if !s.sealSleep(ctx, backoff) {
			// ctx ended before the backoff elapsed; treat as timeout so the
			// pod is still released rather than held on a cancelled teardown.
			s.observeWorkspaceSeal(sess, start, sealOutcomeTimeout)
			return lastErr
		}
		backoff *= 2
		if backoff > sealBackoffCap {
			backoff = sealBackoffCap
		}
	}
}

// beginSealClosing starts the terminal seal on this replica's binding for
// the session. It reports false, touching nothing, when the server has no
// pod registry or the registry holds no binding for the session. Otherwise
// it marks the binding closing, so the executor opens no stream over it and
// the Checkpointer starts no non-seal attempt on it, and then evicts the
// session's cached Attach stream, so a stream opened before the mark ends.
// The mark precedes the eviction: a Send that misses the cache after the
// eviction reads the mark and refuses. spec: §7.1 (Delivery and checkpoints
// during the seal), §28.5.1 (Gateway-to-pod).
func (s *Server) beginSealClosing(sessionID string) bool {
	if s.podRegistry == nil {
		return false
	}
	bind, ok := s.podRegistry.Get(sessionID)
	if !ok {
		return false
	}
	bind.MarkClosing()
	if se, ok := s.executor.(streamEvicter); ok {
		se.EvictStream(sessionID)
	}
	return true
}

// observeWorkspaceSeal records the §7.1
// lenny_workspace_seal_duration_seconds{pool,outcome} histogram for one
// completed seal attempt. The pool label is the session's runtime
// reference, the closest stable pool identity the gateway holds at
// teardown. A nil observer skips the emission.
func (s *Server) observeWorkspaceSeal(sess sessionstore.Session, start time.Time, outcome string) {
	if s.observeSealDuration == nil {
		return
	}
	s.observeSealDuration(sess.RuntimeRef, outcome, s.clock().Sub(start).Seconds())
}

// failWorkspaceSeal applies the §7.1 seal-timeout terminal
// override: it relabels the session's committed terminal state as failed
// with reason workspace_seal_timeout and emits the workspaceSealFailed
// audit event recording the last export error. The hook's release then
// terminates the pod, or runs the slot cleanup and release, with the failed
// disposition. It returns the updated session so the caller's downstream
// side effects (billing, archive) observe the corrected terminal state.
//
// The relabel is skipped only when the session is already failed with
// reason workspace_seal_timeout; any other terminal state, failed included,
// is relabelled, as the §7.1 Seal-and-export invariant states.
//
// spec: §7.1 (Seal-and-export invariant)
func (s *Server) failWorkspaceSeal(ctx context.Context, sess sessionstore.Session, lastErr error) sessionstore.Session {
	detail := ""
	if lastErr != nil {
		detail = lastErr.Error()
	}
	// spec: §11.7 / §7.1 — record the audit event under the
	// session's own tenant with the last MinIO error in the detail.
	if s.lifecycleAudit != nil {
		s.lifecycleAudit.EmitSessionLifecycle(ctx, SessionLifecycleEvent{
			EventType:    auditWorkspaceSealFailed,
			TenantID:     sess.TenantID,
			SessionID:    sess.ID,
			UserID:       sess.UserID,
			RuntimeRef:   sess.RuntimeRef,
			State:        string(session.StateFailed),
			FailureClass: string(session.FailureClassWorkspaceSealTimeout),
			Detail:       detail,
			At:           s.clock(),
		})
	}
	if sess.State == session.StateFailed && sess.FailureClass == session.FailureClassWorkspaceSealTimeout {
		return sess
	}
	now := s.clock()
	updated, err := s.store.Update(ctx, sess.TenantID, sess.ID, func(row *sessionstore.Session) error {
		from := row.State
		row.State = session.StateFailed
		row.FailureClass = session.FailureClassWorkspaceSealTimeout
		if row.FailureReason == "" {
			row.FailureReason = "workspace_seal_timeout"
		}
		// spec: §7.2, §8.8 — the relabel keeps the first TerminatedAt and
		// rewrites the reason; from is terminal, so no generation bump.
		sessionstore.MarkTerminated(row, from, now)
		return nil
	})
	if err != nil {
		// The store write failed; the terminal state already triggered
		// recordSessionCompleted, so return the input row and let the pod
		// teardown proceed. The audit event above still records the failure.
		return sess
	}
	return updated
}
