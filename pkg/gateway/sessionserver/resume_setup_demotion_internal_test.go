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
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/slothealth"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/sandbox/slotstate"
)

// seedResumingRow inserts a row in the §7.2 internal `resuming` transient
// that the resume failure-handling block reconciles, mirroring the state
// handleResume writes before calling resumeOnPod.
func seedResumingRow(t *testing.T, store *memstore.Store, id string) {
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
}

// spec: §7.3 (awaiting_client_action holding state for a retryable resume
// failure), §15.1 (SETUP_COMMAND_FAILED), §6.2 (transient setup failure
// retried on a fresh pod), §7.2.
// diagnosis: on a /resume failure the gateway must demote the row to terminal
// `failed` exactly when the cause is the deterministic codes.FailedPrecondition
// setup-command exit (the non-retryable 422 SETUP_COMMAND_FAILED), and hold the
// row in resumable `awaiting_client_action` for every other setup-time cause
// (the retryable RESUME_FAILED envelope). A failure here means a recoverable
// session was abandoned in terminal `failed` under a retryable envelope (the
// explicit resume retry, valid only from awaiting_client_action, would be
// rejected against the terminal row), or a deterministic failure was left
// resumable so the client retries a setup script that fails identically.
func TestHoldOrFailOnResumeErrorSetupCommand_spec_7_3(t *testing.T) {
	cases := []struct {
		name      string
		cause     error
		wantState session.State
	}{
		{
			"deterministic FailedPrecondition demotes to failed",
			status.Error(codes.FailedPrecondition, "run setup commands: exit 3"),
			session.StateFailed,
		},
		{
			"transient Unavailable stays resumable (crashed pod)",
			status.Error(codes.Unavailable, "pod unreachable"),
			session.StateAwaitingClientAction,
		},
		{
			"transient DeadlineExceeded stays resumable",
			status.Error(codes.DeadlineExceeded, "setup timed out"),
			session.StateAwaitingClientAction,
		},
		{
			"transient Internal stays resumable",
			status.Error(codes.Internal, "adapter internal error"),
			session.StateAwaitingClientAction,
		},
		{
			"non-status cause is Unknown and stays resumable",
			errors.New("wrapped non-status transport boom"),
			session.StateAwaitingClientAction,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := memstore.New()
			s := New(store, Options{})
			id := "sess-resume-demote"
			seedResumingRow(t, store, id)

			// The binder wraps every RunSetup/RunSetupSlot error, deterministic
			// or transient, as *SetupCommandFailure; the resume path passes the
			// wrapped error to holdOrFailOnResumeError just as handleResume does.
			err := fmt.Errorf("podsession: resume: %w",
				&podsession.SetupCommandFailure{Pod: "sbx-1", Cause: tc.cause})
			s.holdOrFailOnResumeError(context.Background(), "acme", id, err)

			row, gerr := store.Get(context.Background(), "acme", id)
			if gerr != nil {
				t.Fatalf("get session: %v", gerr)
			}
			if row.State != tc.wantState {
				t.Errorf("state = %q, want %q", row.State, tc.wantState)
			}

			// spec: §7.2 / §15.1 — a row held in awaiting_client_action remains
			// a valid resume precondition (the retry can succeed), while a
			// demoted-to-failed row is terminal and rejects a further resume.
			resumable := session.Validate(session.PreconditionRequest{
				Endpoint:     session.EndpointResume,
				CurrentState: row.State,
			}) == nil
			wantResumable := tc.wantState == session.StateAwaitingClientAction
			if resumable != wantResumable {
				t.Errorf("resume precondition from %q = %v, want %v (a retryable failure must stay resumable)",
					row.State, resumable, wantResumable)
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

// spec: §7.3 (retry and resume); §4.7.1 (role and gateway RPC contract);
// §5.2 (pool configuration and execution modes); §6.2 (pod state machine).
// diagnosis: a failure means a resume that met the §5.2 reclaim hold or a
// §4.7.1 slot-bind refusal demoted the row to terminal `failed` while the wire
// answered the retryable RESUME_FAILED, so the client's explicit resume retry
// is rejected against a terminal row. The setup-command case fails when the
// refusal arms sit after the setupFail case, which demotes any
// FailedPrecondition cause first.
func TestHoldOrFailOnResumeErrorSlotRefusals_spec_7_3(t *testing.T) {
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
		{
			"started-session refusal as a setup-command cause",
			fmt.Errorf("podsession: resume: %w",
				&podsession.SetupCommandFailure{Pod: "sbx-1", Cause: startedRefusal()}),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := memstore.New()
			s := New(store, Options{})
			id := "sess-resume-refusal"
			seedResumingRow(t, store, id)
			s.holdOrFailOnResumeError(context.Background(), "acme", id, tc.err)
			row, err := store.Get(context.Background(), "acme", id)
			if err != nil {
				t.Fatalf("get session: %v", err)
			}
			if row.State != session.StateAwaitingClientAction {
				t.Fatalf("state = %q, want %q", row.State, session.StateAwaitingClientAction)
			}
			if verr := session.Validate(session.PreconditionRequest{
				Endpoint:     session.EndpointResume,
				CurrentState: row.State,
			}); verr != nil {
				t.Errorf("resume precondition from %q rejected: %v", row.State, verr)
			}
		})
	}
}

// spec: §7.3 (retry and resume); §4.7.1 (role and gateway RPC contract).
// diagnosis: a failure means the resume classifier holds a row on any
// FailedPrecondition rather than on the §4.7.1 started-session sentinel,
// which reclassifies causes this change does not own.
func TestIsTransientPodClaimErrorIgnoresPlainFailedPrecondition_spec_7_3(t *testing.T) {
	if isTransientPodClaimError(status.Error(codes.FailedPrecondition, "workspace root mismatch")) {
		t.Error("a bare FailedPrecondition that is not the started-session refusal classified transient")
	}
	if isTransientPodClaimError(resumeSlotBindErr(status.Error(codes.FailedPrecondition, "workspace root mismatch"))) {
		t.Error("a wrapped FailedPrecondition that is not the started-session refusal classified transient")
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
