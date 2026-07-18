// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/observability/tracing"
)

// DefaultWorkspaceSealMaxDuration is the §7.1 line 112
// maxWorkspaceSealDurationSeconds default: the total wall-clock window
// the gateway retries the seal-and-export before giving up and failing
// the session with workspace_seal_timeout. Deployers override it via
// Options.WorkspaceSealMaxDuration.
//
// spec: §7.1 line 112 — "bounded by maxWorkspaceSealDurationSeconds
// (pool-level configuration, default: 300s)".
const DefaultWorkspaceSealMaxDuration = 300 * time.Second

// sealBackoffInitial and sealBackoffCap are the §7.1 line 112 fixed
// exponential-backoff parameters for a failed seal: the first retry
// waits sealBackoffInitial, each subsequent wait doubles, capped per
// attempt at sealBackoffCap. These are spec constants, not deployer
// configuration; only the total window (sealMaxDuration) is tunable.
//
// spec: §7.1 line 112 — "exponential backoff (initial: 5s, factor: 2×,
// cap: 60s per attempt)".
const (
	sealBackoffInitial = 5 * time.Second
	sealBackoffCap     = 60 * time.Second
)

// sealOutcomeSuccess, sealOutcomeTimeout, and sealOutcomePermanent are
// the §7.1 line 112 `outcome` label values on
// lenny_workspace_seal_duration_seconds. `permanent` records a seal that
// failed on an export error the adapter reported as permanent and
// returned immediately without retrying; only the `timeout` arm fires
// the WorkspaceSealStuck alert.
const (
	sealOutcomeSuccess   = "success"
	sealOutcomeTimeout   = "timeout"
	sealOutcomePermanent = "permanent"
)

// isPermanentSealError reports whether err is a gRPC status the seal
// adapter reports as permanent: FailedPrecondition (the §4.4 line 255
// workspace-size probe rejection, which re-probes identically on every
// retry), InvalidArgument, or Unimplemented. A permanent error
// re-produces identically on each attempt, so retrying it for the full
// §7.1 window only delays the identical terminal state.
//
// spec: §7.1 line 112 — "an export failure the adapter reports as
// permanent ends the window on its first observation with the same
// terminal outcome".
func isPermanentSealError(err error) bool {
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
// until it succeeds or the window closes; this loop is that retry. The
// Sealer no-ops (returns nil) for a session with no live workspace, so a
// session that never ran on a pod completes on the first attempt with no
// delay.
//
// spec: §7.1 line 112 — backoff initial 5s, factor 2×, cap 60s per
// attempt, total window maxWorkspaceSealDurationSeconds.
func (s *Server) sealWorkspace(ctx context.Context, sess sessionstore.Session) (retErr error) {
	// spec: §16.3 line 356 — the gateway-side `session.seal_and_export`
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
	start := s.clock()
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
		// spec: §7.1 line 112 — an export failure the adapter reports as
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

// observeWorkspaceSeal records the §7.1 line 112
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

// failWorkspaceSeal applies the §7.1 line 112 seal-timeout terminal
// override: it transitions the session to failed with reason
// workspace_seal_timeout and emits the workspaceSealFailed audit event
// recording the last export error. The pod is terminated anyway by the
// caller's executor.Close, satisfying "terminates the pod anyway". It
// returns the updated session so the caller's downstream side effects
// (billing, archive) observe the corrected terminal state.
//
// The override is skipped when the session is already failed: a session
// that reached failed for another reason keeps its original failure
// class rather than being relabeled by a best-effort seal.
//
// spec: §7.1 line 112; §7.1 line 107 (workspace_seal_timeout failure
// class).
func (s *Server) failWorkspaceSeal(ctx context.Context, sess sessionstore.Session, lastErr error) sessionstore.Session {
	detail := ""
	if lastErr != nil {
		detail = lastErr.Error()
	}
	// spec: §11.7 / §7.1 line 112 — record the audit event under the
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
	updated, err := s.store.Update(ctx, sess.TenantID, sess.ID, func(row *sessionstore.Session) error {
		row.State = session.StateFailed
		row.FailureClass = session.FailureClassWorkspaceSealTimeout
		if row.FailureReason == "" {
			row.FailureReason = "workspace_seal_timeout"
		}
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
