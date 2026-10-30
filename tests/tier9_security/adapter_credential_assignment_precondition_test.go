// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 §6.1 credential-delivery fail-closed probe for the merged
// per-session RotateCredentials and RevokeCredentials handlers. Both
// handlers open with a registry lookup that tests whether the session
// holds a slot entry, and every live session holds one: the workspace
// preparation RPCs and the session claim register the entry with an
// empty lease set well before any credential is assigned. Registration
// is therefore not evidence that the gateway assigned this session
// credentials, and a handler keyed on it alone would write an upstream
// LLM credential into /run/lenny/slots/{sessionId}/credentials.json for
// a session that was never granted one. This probe drives the real
// adapter server end to end over the merged handlers and asserts the
// refusal and the absence of the file.
package tier9_security_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/adapter/credfile"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// stubRuntime is the pod-global runtime process the session claim
// starts. It does no work: this probe exercises the credential handlers
// rather than the runtime.
type stubRuntime struct{}

func (stubRuntime) Start(context.Context, string) error { return nil }
func (stubRuntime) WriteEnvelope(string, []byte) error  { return nil }

func (stubRuntime) Output(context.Context, string) (<-chan []byte, error) {
	ch := make(chan []byte)
	close(ch)
	return ch, nil
}

func (stubRuntime) Interrupt(context.Context, string, bool) error { return nil }
func (stubRuntime) Close(context.Context, string) error           { return nil }

// diagnosis: the §6.1 was-assigned precondition on the merged
// credential handlers did not fail closed. A RotateCredentials or a
// RevokeCredentials naming a session that started on a pool with no
// credential source was admitted and materialized that session's
// per-slot credential file, delivering upstream LLM credential material
// to a session the gateway never assigned any to.
// spec: 6.1 (per-slot credential delivery), 13.1 (credential-file read boundary)
func TestMergedCredentialHandlersRefuseASessionWithNoAssignment_spec_6_1(t *testing.T) {
	base := t.TempDir()
	s := adapter.New("tier9-cred-precondition")
	s.WorkspaceBase = filepath.Join(base, "workspace")
	s.SessionsRoot = filepath.Join(base, "sessions")
	s.ArtifactsRoot = filepath.Join(base, "artifacts")
	s.CredentialsDir = filepath.Join(base, "run", "lenny")
	s.Runtime = stubRuntime{}

	ctx := context.Background()
	const sessionID = "alice-session"
	if _, err := s.StartSession(ctx, &adapterv1.StartSessionRequest{
		SessionId: &adapterv1.SessionId{Value: sessionID},
		Runtime:   "echo",
	}); err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	credPath := filepath.Join(s.CredentialsDir, "slots", sessionID, credfile.FileName)

	_, rotErr := s.RotateCredentials(ctx, &adapterv1.RotateCredentialsRequest{
		SessionId: &adapterv1.SessionId{Value: sessionID},
		Leases: map[string]*adapterv1.CredentialLease{
			"anthropic_direct": {
				LeaseId:  "lease-rotated",
				Provider: "anthropic_direct",
				Payload:  []byte(`{"deliveryMode":"direct","materializedConfig":{"key":"sk-ant-ROTATED"}}`),
			},
		},
	})
	if status.Code(rotErr) != codes.FailedPrecondition {
		t.Errorf("§6.1: RotateCredentials for a never-assigned session returned %v, want FailedPrecondition",
			status.Code(rotErr))
	}
	if _, err := os.Stat(credPath); !os.IsNotExist(err) {
		t.Fatalf("§6.1 violation: the refused rotation materialized %s for a session the gateway never "+
			"assigned credentials to", credPath)
	}

	_, revErr := s.RevokeCredentials(ctx, &adapterv1.RevokeCredentialsRequest{
		SessionId: &adapterv1.SessionId{Value: sessionID},
		Providers: []string{"anthropic_direct"},
		Reason:    "lease expired",
	})
	if status.Code(revErr) != codes.FailedPrecondition {
		t.Errorf("§6.1: RevokeCredentials for a never-assigned session returned %v, want FailedPrecondition",
			status.Code(revErr))
	}
	if _, err := os.Stat(credPath); !os.IsNotExist(err) {
		t.Fatalf("§6.1 violation: the refused revocation materialized %s", credPath)
	}
}
