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
	"github.com/lennylabs/lenny/pkg/gateway/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
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
// retried on a fresh pod), §7.2 line 195 (resuming internal transient).
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
