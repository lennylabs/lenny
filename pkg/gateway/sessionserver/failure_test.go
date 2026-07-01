// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/uploadtoken"
)

// Helper: build a server with retry caps and a seeded session row.
func failureTestServer(t *testing.T, caps session.RetryPolicyCaps) (*sessionserver.Server, sessionstore.Store) {
	t.Helper()
	clock := func() time.Time { return time.Date(2026, 5, 26, 10, 0, 0, 0, time.UTC) }
	ring := uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "k1", Secret: []byte("test-secret")})
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{
		Clock:             clock,
		UploadTokenIssuer: uploadtoken.NewIssuer(ring, clock),
		RetryPolicyCaps:   caps,
	})
	return srv, store
}

func seedRow(t *testing.T, store sessionstore.Store, row sessionstore.Session) sessionstore.Session {
	t.Helper()
	if err := store.Create(context.Background(), row); err != nil {
		t.Fatalf("seed Create: %v", err)
	}
	got, err := store.Get(context.Background(), row.TenantID, row.ID)
	if err != nil {
		t.Fatalf("seed Get: %v", err)
	}
	return got
}

// spec: §7.3 line 403 — Retryable + budget remaining → resume_pending.
// RetryCount is bumped in the same transaction so the watchdog and
// failure detector agree on the budget. The status_change SSE frame
// fires for the new state. F-7.3.4.
func TestReportSessionFailureRetryableEntersResumePending(t *testing.T) {
	srv, store := failureTestServer(t, session.RetryPolicyCaps{MaxRetries: 2})
	row := sessionstore.Session{
		ID: "sess-r1", TenantID: "acme", RuntimeRef: "claude-code",
		State: session.StateRunning, RetryCount: 0,
	}
	seedRow(t, store, row)
	disp, err := srv.ReportSessionFailure(context.Background(), sessionserver.FailureReport{
		TenantID: "acme", SessionID: "sess-r1", Reason: "pod_evicted",
	})
	if err != nil {
		t.Fatalf("ReportSessionFailure: %v", err)
	}
	if disp.Classification != session.FailureRetryable {
		t.Errorf("Classification = %s, want retryable", disp.Classification)
	}
	if disp.To != session.StateResumePending {
		t.Errorf("To = %q, want resume_pending", disp.To)
	}
	if disp.RetryCount != 1 {
		t.Errorf("RetryCount = %d, want 1", disp.RetryCount)
	}
	got, err := store.Get(context.Background(), "acme", "sess-r1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != session.StateResumePending {
		t.Errorf("row.State = %q, want resume_pending", got.State)
	}
	if got.FailureReason != "pod_evicted" {
		t.Errorf("FailureReason = %q, want pod_evicted", got.FailureReason)
	}
}

// spec: §7.3 line 411 — Retryable but retries exhausted →
// awaiting_client_action. The platform deploys the §7.3 line 427
// webhook + §11.7 audit row via the existing emitAwaitingClientActionEntered
// helper. F-7.3.4 / F-7.3.16.
func TestReportSessionFailureRetryableExhaustedEntersAwaiting(t *testing.T) {
	srv, store := failureTestServer(t, session.RetryPolicyCaps{MaxRetries: 2})
	row := sessionstore.Session{
		ID: "sess-r2", TenantID: "acme", RuntimeRef: "claude-code",
		State: session.StateRunning, RetryCount: 2,
	}
	seedRow(t, store, row)
	disp, err := srv.ReportSessionFailure(context.Background(), sessionserver.FailureReport{
		TenantID: "acme", SessionID: "sess-r2", Reason: "pod_evicted",
	})
	if err != nil {
		t.Fatalf("ReportSessionFailure: %v", err)
	}
	if disp.To != session.StateAwaitingClientAction {
		t.Errorf("To = %q, want awaiting_client_action", disp.To)
	}
	if disp.Classification != session.FailureRetryable {
		t.Errorf("Classification = %s, want retryable", disp.Classification)
	}
	got, err := store.Get(context.Background(), "acme", "sess-r2")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != session.StateAwaitingClientAction {
		t.Errorf("row.State = %q, want awaiting_client_action", got.State)
	}
	// RetryCount is NOT incremented on exhaustion — the spec frames the
	// transition as "retries exhausted", not "one more retry".
	if got.RetryCount != 2 {
		t.Errorf("RetryCount = %d, want 2", got.RetryCount)
	}
}

// spec: §6.2 line 174 — Non-retryable from running → failed. The
// terminal pipeline runs (the row carries the FailureReason for the
// failed-row body). F-7.3.4 / F-7.3.5.
func TestReportSessionFailureNonRetryableFromRunningGoesFailed(t *testing.T) {
	srv, store := failureTestServer(t, session.RetryPolicyCaps{MaxRetries: 2})
	row := sessionstore.Session{
		ID: "sess-r3", TenantID: "acme", RuntimeRef: "claude-code",
		State: session.StateRunning, RetryCount: 0,
	}
	seedRow(t, store, row)
	disp, err := srv.ReportSessionFailure(context.Background(), sessionserver.FailureReport{
		TenantID: "acme", SessionID: "sess-r3", Reason: "workspace_validation_failed",
	})
	if err != nil {
		t.Fatalf("ReportSessionFailure: %v", err)
	}
	if disp.To != session.StateFailed {
		t.Errorf("To = %q, want failed", disp.To)
	}
	if disp.Classification != session.FailureNonRetryable {
		t.Errorf("Classification = %s, want non_retryable", disp.Classification)
	}
	got, err := store.Get(context.Background(), "acme", "sess-r3")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != session.StateFailed {
		t.Errorf("row.State = %q, want failed", got.State)
	}
	if got.FailureReason != "workspace_validation_failed" {
		t.Errorf("FailureReason = %q", got.FailureReason)
	}
}

// spec: §6.2 line 250 — Non-retryable during resuming →
// awaiting_client_action (regardless of retry budget). F-7.3.4 / F-7.3.5.
func TestReportSessionFailureNonRetryableDuringResumingEntersAwaiting(t *testing.T) {
	srv, store := failureTestServer(t, session.RetryPolicyCaps{MaxRetries: 5})
	row := sessionstore.Session{
		ID: "sess-r4", TenantID: "acme", RuntimeRef: "claude-code",
		State: session.StateResuming, RetryCount: 0,
	}
	seedRow(t, store, row)
	disp, err := srv.ReportSessionFailure(context.Background(), sessionserver.FailureReport{
		TenantID: "acme", SessionID: "sess-r4", Reason: "workspace_validation_failed",
	})
	if err != nil {
		t.Fatalf("ReportSessionFailure: %v", err)
	}
	if disp.To != session.StateAwaitingClientAction {
		t.Errorf("To = %q, want awaiting_client_action", disp.To)
	}
}

// Unknown reasons degrade to non-retryable so an untagged cause cannot
// consume retry budget. From running, the row goes to failed (not
// awaiting_client_action). F-7.3.5.
func TestReportSessionFailureUnknownReasonDegradesNonRetryable(t *testing.T) {
	srv, store := failureTestServer(t, session.RetryPolicyCaps{MaxRetries: 5})
	row := sessionstore.Session{
		ID: "sess-r5", TenantID: "acme", RuntimeRef: "claude-code",
		State: session.StateRunning, RetryCount: 0,
	}
	seedRow(t, store, row)
	disp, err := srv.ReportSessionFailure(context.Background(), sessionserver.FailureReport{
		TenantID: "acme", SessionID: "sess-r5", Reason: "custom_disk_full",
	})
	if err != nil {
		t.Fatalf("ReportSessionFailure: %v", err)
	}
	if disp.Classification != session.FailureUnknown {
		t.Errorf("Classification = %s, want unknown", disp.Classification)
	}
	if disp.To != session.StateFailed {
		t.Errorf("To = %q, want failed (unknown degrades non-retryable)", disp.To)
	}
}

// spec: §7.3 line 382 — client_only mode disables auto-retry; every
// classifiable cause goes straight to awaiting_client_action when
// reached from a running source (no resume budget consumed). F-7.3.5.
func TestReportSessionFailureClientOnlyModeBypassesRetry(t *testing.T) {
	srv, store := failureTestServer(t, session.RetryPolicyCaps{MaxRetries: 5})
	row := sessionstore.Session{
		ID: "sess-r6", TenantID: "acme", RuntimeRef: "claude-code",
		State: session.StateRunning, RetryCount: 0,
		RetryPolicy: &session.RetryPolicy{Mode: session.RetryModeClientOnly},
	}
	seedRow(t, store, row)
	disp, err := srv.ReportSessionFailure(context.Background(), sessionserver.FailureReport{
		TenantID: "acme", SessionID: "sess-r6", Reason: "pod_evicted",
	})
	if err != nil {
		t.Fatalf("ReportSessionFailure: %v", err)
	}
	// client_only forces non-retryable. From running → failed per §6.2 line 174.
	if disp.To != session.StateFailed {
		t.Errorf("To = %q, want failed", disp.To)
	}
}

// ReportSessionFailure is a no-op on a session already in
// awaiting_client_action or any terminal state — a concurrent reporter
// must not double-transition.
func TestReportSessionFailureNoOpInHoldingOrTerminalState(t *testing.T) {
	cases := []session.State{
		session.StateAwaitingClientAction, session.StateCompleted,
		session.StateFailed, session.StateCancelled, session.StateExpired,
	}
	srv, store := failureTestServer(t, session.RetryPolicyCaps{MaxRetries: 5})
	for i, st := range cases {
		row := sessionstore.Session{
			ID: "noop-" + string(st), TenantID: "acme",
			RuntimeRef: "claude-code", State: st,
		}
		seedRow(t, store, row)
		disp, err := srv.ReportSessionFailure(context.Background(), sessionserver.FailureReport{
			TenantID: "acme", SessionID: row.ID, Reason: "pod_evicted",
		})
		if err != nil {
			t.Fatalf("%d: %v", i, err)
		}
		if disp.To != st {
			t.Errorf("%d (%s): To = %q, want unchanged %q", i, st, disp.To, st)
		}
		got, err := store.Get(context.Background(), "acme", row.ID)
		if err != nil {
			t.Fatalf("%d: Get: %v", i, err)
		}
		if got.State != st {
			t.Errorf("%d (%s): row.State = %q, want unchanged", i, st, got.State)
		}
	}
}

// A failure report on a pre-running row is a no-op so a controller that
// races the watchdog's pre-running sweeps does not double-transition.
func TestReportSessionFailureIgnoredOnPreRunningState(t *testing.T) {
	srv, store := failureTestServer(t, session.RetryPolicyCaps{MaxRetries: 5})
	for _, st := range []session.State{
		session.StateCreated, session.StateFinalizing,
		session.StateReady, session.StateStarting,
	} {
		row := sessionstore.Session{
			ID: "pre-" + string(st), TenantID: "acme",
			RuntimeRef: "claude-code", State: st,
		}
		seedRow(t, store, row)
		disp, err := srv.ReportSessionFailure(context.Background(), sessionserver.FailureReport{
			TenantID: "acme", SessionID: row.ID, Reason: "pod_evicted",
		})
		if err != nil {
			t.Fatalf("ReportSessionFailure(%s): %v", st, err)
		}
		if disp.To != st {
			t.Errorf("%s: To = %q, want unchanged %q", st, disp.To, st)
		}
	}
}

// spec: §7.3 line 401 — the gateway is the failure-detector entry; a
// malformed report is rejected so a programming error never silently
// loses a failure signal.
func TestReportSessionFailureRejectsMalformedReports(t *testing.T) {
	srv, _ := failureTestServer(t, session.RetryPolicyCaps{})
	cases := []sessionserver.FailureReport{
		{TenantID: "", SessionID: "s", Reason: "x"},
		{TenantID: "t", SessionID: "", Reason: "x"},
		{TenantID: "t", SessionID: "s", Reason: ""},
	}
	for i, rep := range cases {
		_, err := srv.ReportSessionFailure(context.Background(), rep)
		if !errors.Is(err, sessionserver.ErrFailureReportInvalid) {
			t.Errorf("%d: err = %v, want ErrFailureReportInvalid", i, err)
		}
	}
}

// Per-session retryPolicy.MaxRetries overrides the deployer cap when
// resolving the budget. A row with maxRetries=0 falls through to caps.
func TestReportSessionFailureUsesPerSessionMaxRetries(t *testing.T) {
	// Deployer cap=5; per-session policy=1; RetryCount=1 → exhausted.
	srv, store := failureTestServer(t, session.RetryPolicyCaps{MaxRetries: 5})
	row := sessionstore.Session{
		ID: "sess-r7", TenantID: "acme", RuntimeRef: "claude-code",
		State: session.StateRunning, RetryCount: 1,
		RetryPolicy: &session.RetryPolicy{MaxRetries: 1},
	}
	seedRow(t, store, row)
	disp, err := srv.ReportSessionFailure(context.Background(), sessionserver.FailureReport{
		TenantID: "acme", SessionID: "sess-r7", Reason: "pod_evicted",
	})
	if err != nil {
		t.Fatalf("ReportSessionFailure: %v", err)
	}
	if disp.MaxRetries != 1 {
		t.Errorf("MaxRetries = %d, want 1 (per-session)", disp.MaxRetries)
	}
	if disp.To != session.StateAwaitingClientAction {
		t.Errorf("To = %q, want awaiting_client_action (per-session budget exhausted)", disp.To)
	}
}

// PodAssignment is stamped onto the row when supplied so the §15.1
// /resume handler can correlate the new pod's claim with the failed
// pod's name on the audit chain.
func TestReportSessionFailureStampsPodAssignment(t *testing.T) {
	srv, store := failureTestServer(t, session.RetryPolicyCaps{MaxRetries: 2})
	row := sessionstore.Session{
		ID: "sess-r8", TenantID: "acme", RuntimeRef: "claude-code",
		State: session.StateRunning, PodAssignment: "old-pod",
	}
	seedRow(t, store, row)
	_, err := srv.ReportSessionFailure(context.Background(), sessionserver.FailureReport{
		TenantID: "acme", SessionID: "sess-r8", Reason: "pod_evicted",
		PodAssignment: "evicted-pod-1",
	})
	if err != nil {
		t.Fatalf("ReportSessionFailure: %v", err)
	}
	got, err := store.Get(context.Background(), "acme", "sess-r8")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.PodAssignment != "evicted-pod-1" {
		t.Errorf("PodAssignment = %q, want evicted-pod-1", got.PodAssignment)
	}
}

// spec: §7.2 line 214 (a) — every exit from `resuming` that aborts
// the in-flight resume bumps coordination_generation by exactly one in
// the same logical write that records the new state. The bump fences
// any stale coordinator still mid-restore against the prior generation
// so its next operational RPC fails the §4.2 CoordinatorFence check.
// recovery_generation is frozen — the interrupted resume is not retried
// (the next /resume call mints a fresh one). F-7.1.14.
func TestReportSessionFailureBumpsCoordinationOnExitFromResuming_spec_7_2_F_7_1_14(t *testing.T) {
	srv, store := failureTestServer(t, session.RetryPolicyCaps{MaxRetries: 5})
	row := sessionstore.Session{
		ID: "sess-cg1", TenantID: "acme", RuntimeRef: "claude-code",
		State: session.StateResuming, RetryCount: 0,
		CoordinationGeneration: 7, RecoveryGeneration: 3,
	}
	seedRow(t, store, row)
	disp, err := srv.ReportSessionFailure(context.Background(), sessionserver.FailureReport{
		TenantID: "acme", SessionID: "sess-cg1", Reason: "workspace_validation_failed",
	})
	if err != nil {
		t.Fatalf("ReportSessionFailure: %v", err)
	}
	if disp.To != session.StateAwaitingClientAction {
		t.Fatalf("To = %q, want awaiting_client_action", disp.To)
	}
	got, err := store.Get(context.Background(), "acme", "sess-cg1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.CoordinationGeneration != 8 {
		t.Errorf("CoordinationGeneration = %d, want 8 (bumped by 1)", got.CoordinationGeneration)
	}
	if got.RecoveryGeneration != 3 {
		t.Errorf("RecoveryGeneration = %d, want 3 (frozen on snapshot-close)", got.RecoveryGeneration)
	}
}

// spec: §7.2 line 214 — the bump also fires on the retryable+budget
// branch (resuming → resume_pending) because the in-flight resume is
// aborted before the next attempt; without the fence, a stale
// coordinator could still race the new resume_pending → resuming
// transition. F-7.1.14.
func TestReportSessionFailureBumpsCoordinationOnResumingToResumePending_spec_7_2_F_7_1_14(t *testing.T) {
	srv, store := failureTestServer(t, session.RetryPolicyCaps{MaxRetries: 5})
	row := sessionstore.Session{
		ID: "sess-cg2", TenantID: "acme", RuntimeRef: "claude-code",
		State: session.StateResuming, RetryCount: 0,
		CoordinationGeneration: 1,
	}
	seedRow(t, store, row)
	disp, err := srv.ReportSessionFailure(context.Background(), sessionserver.FailureReport{
		TenantID: "acme", SessionID: "sess-cg2", Reason: "pod_evicted",
	})
	if err != nil {
		t.Fatalf("ReportSessionFailure: %v", err)
	}
	if disp.To != session.StateResumePending {
		t.Fatalf("To = %q, want resume_pending", disp.To)
	}
	got, err := store.Get(context.Background(), "acme", "sess-cg2")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.CoordinationGeneration != 2 {
		t.Errorf("CoordinationGeneration = %d, want 2 (bumped on resuming → resume_pending)", got.CoordinationGeneration)
	}
}

// spec: §7.2 line 219-225 — the pre-attach terminal-collapse path
// (resume_pending → cancelled/completed) intentionally does NOT bump
// coordination_generation: no pod is attached, no CoordinatorFence
// round-trip is pending, so the bump is unnecessary. Verify that a
// failure report from resume_pending bumps RetryCount but leaves the
// CG counter untouched. F-7.1.14.
func TestReportSessionFailureDoesNotBumpCoordinationFromResumePending_spec_7_2_F_7_1_14(t *testing.T) {
	srv, store := failureTestServer(t, session.RetryPolicyCaps{MaxRetries: 5})
	row := sessionstore.Session{
		ID: "sess-cg3", TenantID: "acme", RuntimeRef: "claude-code",
		State: session.StateResumePending, RetryCount: 0,
		CoordinationGeneration: 11,
	}
	seedRow(t, store, row)
	disp, err := srv.ReportSessionFailure(context.Background(), sessionserver.FailureReport{
		TenantID: "acme", SessionID: "sess-cg3", Reason: "pod_evicted",
	})
	if err != nil {
		t.Fatalf("ReportSessionFailure: %v", err)
	}
	if disp.From != session.StateResumePending {
		t.Fatalf("From = %q, want resume_pending", disp.From)
	}
	got, err := store.Get(context.Background(), "acme", "sess-cg3")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.CoordinationGeneration != 11 {
		t.Errorf("CoordinationGeneration = %d, want 11 (pre-attach collapse must NOT bump)", got.CoordinationGeneration)
	}
}
