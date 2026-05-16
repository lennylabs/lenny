// SPDX-License-Identifier: MIT

package adapter_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/adapter"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

type fakeRuntime struct {
	started   []string
	startErr  error
	envelopes [][]byte
	writeErr  error
	closed    []string
}

func (f *fakeRuntime) Start(_ context.Context, sessionID string) error {
	if f.startErr != nil {
		return f.startErr
	}
	f.started = append(f.started, sessionID)
	return nil
}

func (f *fakeRuntime) WriteEnvelope(_ string, envelope []byte) error {
	if f.writeErr != nil {
		return f.writeErr
	}
	f.envelopes = append(f.envelopes, envelope)
	return nil
}

func (f *fakeRuntime) Close(_ context.Context, sessionID string) error {
	f.closed = append(f.closed, sessionID)
	return nil
}

// sessionServer builds an adapter Server wired to a fresh workspace
// directory and a fake runtime.
func sessionServer(t *testing.T) (*adapter.Server, *fakeRuntime, string) {
	t.Helper()
	root := t.TempDir()
	rt := &fakeRuntime{}
	s := adapter.New("test")
	s.WorkspaceRoot = root
	s.Runtime = rt
	return s, rt, root
}

func startReq(sessionID string, sources []*adapterv1.WorkspaceSource, setup []*adapterv1.SetupCommand) *adapterv1.StartSessionRequest {
	return &adapterv1.StartSessionRequest{
		SessionId: &adapterv1.SessionId{Value: sessionID},
		Runtime:   "echo",
		WorkspacePlan: &adapterv1.WorkspacePlan{
			SchemaVersion: 1,
			Sources:       sources,
			SetupCommands: setup,
		},
	}
}

func TestStartSessionMaterializesAndStartsRuntime(t *testing.T) {
	s, rt, root := sessionServer(t)
	_, err := s.StartSession(context.Background(), startReq("sess-1",
		[]*adapterv1.WorkspaceSource{
			{Type: "inlineFile", Path: "CLAUDE.md", Content: "notes", Mode: "644"},
		}, nil))
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "CLAUDE.md")); statErr != nil {
		t.Errorf("StartSession did not materialize the workspace: %v", statErr)
	}
	if len(rt.started) != 1 || rt.started[0] != "sess-1" {
		t.Errorf("runtime started = %v, want [sess-1]", rt.started)
	}
}

func TestStartSessionRunsSetupCommands(t *testing.T) {
	s, _, root := sessionServer(t)
	_, err := s.StartSession(context.Background(), startReq("sess-1", nil,
		[]*adapterv1.SetupCommand{{Cmd: "touch setup.done", TimeoutSeconds: 30}}))
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "setup.done")); statErr != nil {
		t.Errorf("StartSession did not run the setup command: %v", statErr)
	}
}

func TestStartSessionRejectsSecondSession(t *testing.T) {
	s, _, _ := sessionServer(t)
	if _, err := s.StartSession(context.Background(), startReq("sess-1", nil, nil)); err != nil {
		t.Fatalf("first StartSession: %v", err)
	}
	_, err := s.StartSession(context.Background(), startReq("sess-2", nil, nil))
	if status.Code(err) != codes.Unavailable {
		t.Errorf("second StartSession code = %v, want Unavailable", status.Code(err))
	}
}

func TestStartSessionRejectsEmptySessionID(t *testing.T) {
	s, _, _ := sessionServer(t)
	_, err := s.StartSession(context.Background(), startReq("", nil, nil))
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", status.Code(err))
	}
}

func TestStartSessionRequiresConfiguration(t *testing.T) {
	s := adapter.New("test") // no WorkspaceRoot, no Runtime
	_, err := s.StartSession(context.Background(), startReq("sess-1", nil, nil))
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("code = %v, want FailedPrecondition", status.Code(err))
	}
}

func TestStartSessionReleasesPodOnBadWorkspacePlan(t *testing.T) {
	s, _, _ := sessionServer(t)
	// A path-traversal source fails materialization.
	_, err := s.StartSession(context.Background(), startReq("sess-bad",
		[]*adapterv1.WorkspaceSource{
			{Type: "inlineFile", Path: "../escape", Content: "x", Mode: "644"},
		}, nil))
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument", status.Code(err))
	}
	// The pod must be idle again so a retry can proceed.
	if _, retryErr := s.StartSession(context.Background(), startReq("sess-good", nil, nil)); retryErr != nil {
		t.Errorf("pod was not released after a failed StartSession: %v", retryErr)
	}
}

func TestSendMessageForwardsEnvelopeToRuntime(t *testing.T) {
	s, rt, _ := sessionServer(t)
	if _, err := s.StartSession(context.Background(), startReq("sess-1", nil, nil)); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	envelope := []byte(`{"type":"message","input":[{"type":"text","inline":"hi"}]}`)
	_, err := s.SendMessage(context.Background(), &adapterv1.SendMessageRequest{
		SessionId:    &adapterv1.SessionId{Value: "sess-1"},
		EnvelopeJson: envelope,
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if len(rt.envelopes) != 1 || string(rt.envelopes[0]) != string(envelope) {
		t.Errorf("runtime received envelopes %v, want one matching the request", rt.envelopes)
	}
}

func TestSendMessageRejectsUnknownSession(t *testing.T) {
	s, _, _ := sessionServer(t)
	if _, err := s.StartSession(context.Background(), startReq("sess-1", nil, nil)); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	_, err := s.SendMessage(context.Background(), &adapterv1.SendMessageRequest{
		SessionId:    &adapterv1.SessionId{Value: "sess-other"},
		EnvelopeJson: []byte(`{}`),
	})
	if status.Code(err) != codes.NotFound {
		t.Errorf("code = %v, want NotFound for a session not assigned to the pod", status.Code(err))
	}
}

func TestSendMessageRejectsWhenNoSessionAssigned(t *testing.T) {
	s, _, _ := sessionServer(t)
	_, err := s.SendMessage(context.Background(), &adapterv1.SendMessageRequest{
		SessionId:    &adapterv1.SessionId{Value: "sess-1"},
		EnvelopeJson: []byte(`{}`),
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("code = %v, want FailedPrecondition when the pod is idle", status.Code(err))
	}
}

func TestSendMessageRejectsEmptyEnvelope(t *testing.T) {
	s, _, _ := sessionServer(t)
	if _, err := s.StartSession(context.Background(), startReq("sess-1", nil, nil)); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	_, err := s.SendMessage(context.Background(), &adapterv1.SendMessageRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-1"},
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("code = %v, want InvalidArgument for an empty envelope", status.Code(err))
	}
}

func TestShutdownClosesRuntimeAndReleasesPod(t *testing.T) {
	s, rt, _ := sessionServer(t)
	if _, err := s.StartSession(context.Background(), startReq("sess-1", nil, nil)); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	resp, err := s.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-1"},
	})
	if err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if !resp.ExitedCleanly {
		t.Error("Shutdown reported an unclean exit for a healthy runtime")
	}
	if len(rt.closed) != 1 || rt.closed[0] != "sess-1" {
		t.Errorf("runtime closed = %v, want [sess-1]", rt.closed)
	}
	// The pod must be idle again so a replacement session can be assigned.
	if _, retryErr := s.StartSession(context.Background(), startReq("sess-2", nil, nil)); retryErr != nil {
		t.Errorf("pod was not released after Shutdown: %v", retryErr)
	}
}

func TestShutdownRejectsUnknownSession(t *testing.T) {
	s, _, _ := sessionServer(t)
	if _, err := s.StartSession(context.Background(), startReq("sess-1", nil, nil)); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	_, err := s.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-other"},
	})
	if status.Code(err) != codes.NotFound {
		t.Errorf("code = %v, want NotFound", status.Code(err))
	}
}

func TestStartSessionReleasesPodOnRuntimeFailure(t *testing.T) {
	root := t.TempDir()
	s := adapter.New("test")
	s.WorkspaceRoot = root
	s.Runtime = &fakeRuntime{startErr: errors.New("runtime crashed")}

	_, err := s.StartSession(context.Background(), startReq("sess-1", nil, nil))
	if status.Code(err) != codes.Internal {
		t.Fatalf("code = %v, want Internal", status.Code(err))
	}
	// A healthy runtime must be able to take a fresh session afterward.
	s.Runtime = &fakeRuntime{}
	if _, retryErr := s.StartSession(context.Background(), startReq("sess-2", nil, nil)); retryErr != nil {
		t.Errorf("pod was not released after a runtime-start failure: %v", retryErr)
	}
}
