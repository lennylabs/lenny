// SPDX-License-Identifier: MIT

package adapter_test

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/tests/testinfra/ackruntime"
)

// spec: §4.7.1 (Role and Gateway RPC Contract), §6.1 (What a Pre-Warmed Pod
// Looks Like), §7.3 (Credential leases on the replacement pod)
//
// A restore on a replacement pod assigns the session's leases with
// AssignCredentials and then sends Resume under the same bind-attempt token.
// The adapter admits the Resume into the entry the assignment created, so
// the session's credential file holds the lease when the runtime starts; it
// refuses a Resume under another token as superseded, and an
// AssignCredentials after the Resume as already started.
func TestResumeIsAdmittedIntoTheAssignmentsEntry_spec_4_7_1(t *testing.T) {
	assignReq := func(token string) *adapterv1.AssignCredentialsRequest {
		return &adapterv1.AssignCredentialsRequest{
			BindAttempt: token,
			SessionId:   &adapterv1.SessionId{Value: "alice"},
			Leases: map[string]*adapterv1.CredentialLease{
				"anthropic": credLease("l1", "anthropic", `{"deliveryMode":"direct"}`),
			},
		}
	}
	resumeReq := func(token string) *adapterv1.ResumeRequest {
		return &adapterv1.ResumeRequest{
			SessionId:    &adapterv1.SessionId{Value: "alice"},
			CheckpointId: "ckpt-1",
			BindAttempt:  token,
		}
	}

	t.Run("same token", func(t *testing.T) {
		s := credServer(t)
		s.Runtime = ackruntime.New(t)
		ctx := context.Background()
		if _, err := s.AssignCredentials(ctx, assignReq("attempt-1")); err != nil {
			t.Fatalf("AssignCredentials: %v", err)
		}
		if _, err := s.Resume(ctx, resumeReq("attempt-1")); err != nil {
			t.Fatalf("Resume under the assignment's token: %v", err)
		}
		if _, ok := credProviders(t, sessionCredsDir(s, "alice"))["anthropic"]; !ok {
			t.Error("the credential file lost the lease across the Resume")
		}
		_, err := s.AssignCredentials(ctx, assignReq("attempt-1"))
		if status.Code(err) != codes.FailedPrecondition ||
			adapterErrorCodeOf(err) != adapterv1.Error_ERROR_CODE_SLOT_BIND_ALREADY_STARTED {
			t.Errorf("AssignCredentials after Resume = %v, want SLOT_BIND_ALREADY_STARTED", err)
		}
	})

	t.Run("another token", func(t *testing.T) {
		s := credServer(t)
		s.Runtime = ackruntime.New(t)
		ctx := context.Background()
		if _, err := s.AssignCredentials(ctx, assignReq("attempt-1")); err != nil {
			t.Fatalf("AssignCredentials: %v", err)
		}
		_, err := s.Resume(ctx, resumeReq("attempt-2"))
		if status.Code(err) != codes.Aborted ||
			adapterErrorCodeOf(err) != adapterv1.Error_ERROR_CODE_SLOT_BIND_ATTEMPT_SUPERSEDED {
			t.Errorf("Resume under another token = %v, want SLOT_BIND_ATTEMPT_SUPERSEDED", err)
		}
	})
}

// adapterErrorCodeOf decodes the typed adapter error code a refusal carries.
func adapterErrorCodeOf(err error) adapterv1.Error_ErrorCode {
	st, ok := status.FromError(err)
	if !ok {
		return 0
	}
	for _, d := range st.Details() {
		if e, ok := d.(*adapterv1.Error); ok {
			return e.GetCode()
		}
	}
	return 0
}
