// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podclaim"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/slothealth"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/sandbox/slotstate"
)

// seedResumingRow inserts a row in the §7.2 internal `resuming` transient, as
// the resume driver's entry write leaves it, and returns the stored row with
// its coordination generation, the attempt's G.
func seedResumingRow(t *testing.T, store *memstore.Store, id string) sessionstore.Session {
	t.Helper()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID:         id,
		TenantID:   "acme",
		UserID:     "alice@acme.com",
		RuntimeRef: "echo",
		State:      session.StateResuming,
	}); err != nil {
		t.Fatalf("seed resuming row: %v", err)
	}
	row, err := store.Get(context.Background(), "acme", id)
	if err != nil {
		t.Fatalf("read seeded row: %v", err)
	}
	return row
}

// spec: §7.3 (Resume driver; Retry and Resume), §15.1 (SETUP_COMMAND_FAILED),
// §6.2 (Pod State Machine).
// diagnosis: a failure after the resume driver's entry write is reported once
// through the failure funnel at the attempt's generation. The deterministic
// codes.FailedPrecondition setup-command exit is reported as
// setup_command_failed, which the default policy treats as non-retryable, so
// the row leaves resuming for awaiting_client_action with RetryCount
// unchanged; every other setup-time cause is reported as runtime_crash, which
// spends one retry and re-enters resume_pending. No cause demotes the row to
// failed. A failure here means a recoverable session was abandoned, or a
// deterministic setup failure was retried on every attempt.
func TestReportResumeFailureSetupCommand_spec_7_3(t *testing.T) {
	cases := []struct {
		name       string
		cause      error
		wantState  session.State
		wantReason session.FailureReason
		wantRetry  int64
	}{
		{
			"deterministic FailedPrecondition is non-retryable",
			status.Error(codes.FailedPrecondition, "run setup commands: exit 3"),
			session.StateAwaitingClientAction, session.FailureSetupCommandFailed, 0,
		},
		{
			"transient Unavailable re-enters resume_pending",
			status.Error(codes.Unavailable, "pod unreachable"),
			session.StateResumePending, session.FailureRuntimeCrash, 1,
		},
		{
			"transient DeadlineExceeded re-enters resume_pending",
			status.Error(codes.DeadlineExceeded, "setup timed out"),
			session.StateResumePending, session.FailureRuntimeCrash, 1,
		},
		{
			"transient Internal re-enters resume_pending",
			status.Error(codes.Internal, "adapter internal error"),
			session.StateResumePending, session.FailureRuntimeCrash, 1,
		},
		{
			"non-status cause re-enters resume_pending",
			errors.New("wrapped non-status transport boom"),
			session.StateResumePending, session.FailureRuntimeCrash, 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := memstore.New()
			s := New(store, Options{})
			row := seedResumingRow(t, store, "sess-resume-report")
			err := fmt.Errorf("podsession: resume: %w",
				&podsession.SetupCommandFailure{Pod: "sbx-1", Cause: tc.cause})
			if exit := s.reportResumeFailure(row, row.CoordinationGeneration, err); exit != resumeExitReported {
				t.Fatalf("exit = %v, want resumeExitReported", exit)
			}
			got, gerr := store.Get(context.Background(), "acme", row.ID)
			if gerr != nil {
				t.Fatalf("get session: %v", gerr)
			}
			if got.State != tc.wantState || got.FailureReason != string(tc.wantReason) || got.RetryCount != tc.wantRetry {
				t.Errorf("row = %q/%q/retry %d, want %q/%q/retry %d",
					got.State, got.FailureReason, got.RetryCount, tc.wantState, tc.wantReason, tc.wantRetry)
			}
			if got.CoordinationGeneration != row.CoordinationGeneration {
				t.Errorf("coordination_generation = %d, want %d unchanged by the exit", got.CoordinationGeneration, row.CoordinationGeneration)
			}
		})
	}
}

// resumeSlotBindErr wraps cause in the *podsession.SlotBindError Binder.Resume
// returns at the "resume" stage, as the §7.3 re-attach carries a refusal.
func resumeSlotBindErr(cause error) error {
	return fmt.Errorf("podsession: resume session on pod sbx-1: %w",
		&podsession.SlotBindError{Pod: "sbx-1", SlotID: "sess-1", Stage: "resume", Err: cause})
}

// spec: §7.3 (Resume driver; Retry and Resume); §4.7.1 (role and gateway RPC
// contract); §5.2 (pool configuration and execution modes); §6.2 (pod state
// machine).
// diagnosis: a resume whose bind met the §5.2 reclaim hold or a §4.7.1
// slot-bind refusal after its entry write is reported as runtime_crash, so the
// row re-enters resume_pending and the driver retries on a fresh pod; it is
// never demoted to failed. A failure means a refusal that succeeds on a fresh
// attempt ended the session, or a refusal wrapped in a setup failure was
// treated as a deterministic setup exit.
func TestReportResumeFailureSlotRefusals_spec_7_3(t *testing.T) {
	hold := func() error { return status.Error(codes.Aborted, "slot_reclaim_in_progress") }
	cases := []struct {
		name string
		err  error
	}{
		{"bare reclaim hold", hold()},
		{"superseded refusal", supersededRefusal()},
		{"reclaim hold in a SlotBindError", resumeSlotBindErr(hold())},
		{"superseded refusal in a SlotBindError", resumeSlotBindErr(supersededRefusal())},
		{"started-session refusal in a SlotBindError", resumeSlotBindErr(startedRefusal())},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := memstore.New()
			s := New(store, Options{})
			row := seedResumingRow(t, store, "sess-resume-refusal")
			s.reportResumeFailure(row, row.CoordinationGeneration, tc.err)
			got, err := store.Get(context.Background(), "acme", row.ID)
			if err != nil {
				t.Fatalf("get session: %v", err)
			}
			if got.State != session.StateResumePending || got.RetryCount != 1 {
				t.Fatalf("row = %q/retry %d, want resume_pending/retry 1", got.State, got.RetryCount)
			}
		})
	}
}

// spec: §7.3 (Resume driver); §4.7.1 (role and gateway RPC contract).
// diagnosis: only a setup-command failure whose cause is FailedPrecondition is
// reported as setup_command_failed; a bare or wrapped FailedPrecondition that
// is not a setup failure is a runtime_crash that spends a retry. A failure
// means a resume error this change does not classify was made non-retryable.
func TestResumeFailureReasonIgnoresPlainFailedPrecondition_spec_7_3(t *testing.T) {
	for _, err := range []error{
		status.Error(codes.FailedPrecondition, "workspace root mismatch"),
		resumeSlotBindErr(status.Error(codes.FailedPrecondition, "workspace root mismatch")),
	} {
		if got := resumeFailureReason(err); got != string(session.FailureRuntimeCrash) {
			t.Errorf("resumeFailureReason(%v) = %q, want runtime_crash", err, got)
		}
	}
}

// spec: §5.2 (pool configuration and execution modes); §7.3 (retry and
// resume); §6.2 (pod state machine).
// diagnosis: a failure means a failed §7.3 re-attach on a concurrent pool does
// not reach the §5.2 slot-health accounting, so a leaked resume slot holds
// occupancy while the pod is never counted unhealthy, drained, or surfaced on
// the leaked-slot gauge; or an exclusive pool, which reserved nothing, is
// accounted.
func TestResumeFailureReachesTheSlotAccounting_spec_5_2(t *testing.T) {
	match := podsession.PoolMatch{Pool: "pool-x", MaxConcurrentSessions: 4}
	cases := []struct {
		name       string
		match      podsession.PoolMatch
		slotID     string
		leaked     bool
		wantFailed int
		wantLeaked int
	}{
		{"unacknowledged reclaim leaks the slot", match, "sess-1", true, 0, 1},
		{"cleanly reclaimed resume is a windowed failure", match, "sess-1", false, 1, 0},
		{"exclusive pool reserved nothing", podsession.PoolMatch{Pool: "pool-x", MaxConcurrentSessions: 1}, "", true, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			binder := &fakeSlotBinder{}
			health := slothealth.New()
			slots := slotstate.NewRegistry()
			var gauge []int
			sbe := &podsession.SlotBindError{
				Pod: "sbx-1", SlotID: tc.slotID, Stage: "resume",
				Err: status.Error(codes.Unavailable, "resume failed"), Leaked: tc.leaked,
			}
			accountResumeSlotFailure(context.Background(), binder, health, slots, nil,
				func(_, _ string, n int) { gauge = append(gauge, n) }, tc.match,
				fmt.Errorf("podsession: resume session on pod sbx-1: %w", sbe))
			if failed, leaked := health.Counts("sbx-1"); failed != tc.wantFailed || leaked != tc.wantLeaked {
				t.Errorf("Counts = (failed=%d, leaked=%d), want (%d, %d)", failed, leaked, tc.wantFailed, tc.wantLeaked)
			}
			if len(gauge) != tc.wantLeaked {
				t.Errorf("leak gauge publications = %v, want %d", gauge, tc.wantLeaked)
			}
			if len(binder.released) != 0 {
				t.Errorf("released = %+v, want none: Binder.Resume owns the release", binder.released)
			}
			if len(binder.drained) != 0 {
				t.Errorf("drained = %v, want none below threshold 2", binder.drained)
			}
		})
	}
}

// spec: §7.3 (Resume driver), §7.2 (Interactive Session Model)
// diagnosis: a resume attempt's failure report writes only over the resuming
// incarnation it belongs to. A row a later incarnation committed to running,
// a row a DELETE cancelled, and a resuming row at a later generation keep
// their state. A failure means a failed attempt overwrote a newer state, so a
// running session is parked or a cancelled session is revived.
func TestReportResumeFailureKeepsNewerState_spec_7_3(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state session.State
		bump  bool
	}{
		{"running", session.StateRunning, false},
		{"cancelled", session.StateCancelled, false},
		{"later resuming incarnation", session.StateResuming, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := memstore.New()
			s := New(store, Options{})
			row := seedResumingRow(t, store, "sess-newer")
			gen := row.CoordinationGeneration
			if _, err := store.Update(context.Background(), "acme", row.ID, func(r *sessionstore.Session) error {
				r.State = tc.state
				if tc.bump {
					r.CoordinationGeneration++
				}
				return nil
			}); err != nil {
				t.Fatalf("move row: %v", err)
			}
			before, _ := store.Get(context.Background(), "acme", row.ID)
			s.reportResumeFailure(row, gen, podclaim.ErrNoIdlePod)
			got, err := store.Get(context.Background(), "acme", row.ID)
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			if got.State != tc.state || !got.UpdatedAt.Equal(before.UpdatedAt) || got.RetryCount != before.RetryCount {
				t.Errorf("row = %q (updated %v, retry %d), want %q unchanged (updated %v, retry %d)",
					got.State, got.UpdatedAt, got.RetryCount, tc.state, before.UpdatedAt, before.RetryCount)
			}
		})
	}
}
