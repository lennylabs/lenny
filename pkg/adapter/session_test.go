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
	started  []string
	startErr error
}

func (f *fakeRuntime) Start(_ context.Context, sessionID string) error {
	if f.startErr != nil {
		return f.startErr
	}
	f.started = append(f.started, sessionID)
	return nil
}

func (f *fakeRuntime) Close(context.Context, string) error { return nil }

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
