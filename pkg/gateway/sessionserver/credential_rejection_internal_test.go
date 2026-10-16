// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"fmt"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialstore"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/usercreds"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// assignErr wraps cause as a restore's credential assignment failure, as
// Binder.Resume returns it under a *SlotBindError at stage resume.
func assignErr(cause error) error {
	cae := &podsession.CredentialAssignmentError{Provider: "anthropic", Pool: "claude-prod", Err: cause}
	return fmt.Errorf("podsession: resume session on pod sbx-1: %w",
		&podsession.SlotBindError{Pod: "sbx-1", SlotID: "", Stage: "resume", Err: cae})
}

// TestCredentialRejectionEndsInAwaitingClientAction_spec_7_3 asserts that a
// credential assignment rejected after the entry write for a cause that
// recurs on every attempt ends the row in awaiting_client_action with
// credential_rejected and RetryCount unchanged, and that a transient cause
// (pool exhausted, Token Service unavailable, gRPC Unavailable, or a
// deadline) ends it in resume_pending with RetryCount + 1 and runtime_crash.
//
// spec: §7.3 (Resume driver; Credential leases on the replacement pod), §6.2
// (Pod State Machine)
func TestCredentialRejectionEndsInAwaitingClientAction_spec_7_3(t *testing.T) {
	for _, tc := range []struct {
		name      string
		cause     error
		rejection bool
	}{
		{"pool not found", credassign.ErrPoolNotFound, true},
		{"user credential not found", credentialstore.ErrNotFound, true},
		{"proxy unavailable", usercreds.ErrProxyUnavailable, true},
		{"invalid argument", status.Error(codes.InvalidArgument, "bad provider"), true},
		{"permission denied", status.Error(codes.PermissionDenied, "denied"), true},
		{"failed precondition", status.Error(codes.FailedPrecondition, "revoked"), true},
		{"unauthenticated", status.Error(codes.Unauthenticated, "no identity"), true},
		{"pool exhausted", credential.ErrPoolExhausted, false},
		{"token service unavailable", credassign.ErrTokenServiceUnavailable, false},
		{"grpc unavailable", status.Error(codes.Unavailable, "down"), false},
		{"deadline", context.DeadlineExceeded, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := memstore.New()
			srv := New(store, Options{})
			defer srv.Close()
			row := seedResumingRow(t, store, "s")
			srv.reportResumeFailure(row, row.CoordinationGeneration, assignErr(tc.cause))
			got, _ := store.Get(context.Background(), "acme", "s")
			if tc.rejection {
				if got.State != session.StateAwaitingClientAction || got.RetryCount != 0 ||
					got.FailureReason != string(session.FailureCredentialRejected) {
					t.Errorf("row = %q retry %d reason %q, want awaiting_client_action retry 0 credential_rejected",
						got.State, got.RetryCount, got.FailureReason)
				}
				return
			}
			if got.State != session.StateResumePending || got.RetryCount != 1 ||
				got.FailureReason != string(session.FailureRuntimeCrash) {
				t.Errorf("row = %q retry %d reason %q, want resume_pending retry 1 runtime_crash",
					got.State, got.RetryCount, got.FailureReason)
			}
		})
	}
	t.Run("a rejection outside a credential assignment is a crash", func(t *testing.T) {
		if got := resumeFailureReason(status.Error(codes.PermissionDenied, "adapter refused")); got != string(session.FailureRuntimeCrash) {
			t.Errorf("resumeFailureReason = %q, want runtime_crash", got)
		}
	})
}
