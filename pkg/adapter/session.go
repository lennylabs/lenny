// SPDX-License-Identifier: MIT

package adapter

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/adapter/workspace"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// RuntimeProcess manages the pod's runtime process. The §4.7 adapter
// starts it at session start and closes it at session teardown.
type RuntimeProcess interface {
	// Start spawns the runtime process for the session.
	Start(ctx context.Context, sessionID string) error
	// Close tears the runtime process down.
	Close(ctx context.Context, sessionID string) error
}

// StartSession assigns a session to the pod (§4.7, §6.1). It rejects
// the call with Unavailable when the pod already holds a session,
// materializes the workspace from the request's WorkspacePlan, runs
// the plan's setup commands, and starts the runtime process. A
// session-mode pod is one-session-only: the pod is terminated and
// replaced after the session ends rather than reused.
//
// On any failure after the session is tentatively claimed, the pod is
// returned to the idle state so a retry can land on a fresh pod.
func (s *Server) StartSession(ctx context.Context, req *adapterv1.StartSessionRequest) (*adapterv1.StartSessionResponse, error) {
	sessionID := req.GetSessionId().GetValue()
	if sessionID == "" {
		return nil, status.Error(codes.InvalidArgument, "StartSession requires a session id")
	}
	if s.WorkspaceRoot == "" || s.Runtime == nil {
		return nil, status.Error(codes.FailedPrecondition,
			"adapter is not configured with a workspace root and runtime")
	}

	if err := s.claimSession(sessionID); err != nil {
		return nil, err
	}

	plan := req.GetWorkspacePlan()
	if err := workspace.Materialize(s.WorkspaceRoot, plan.GetSources()); err != nil {
		s.releaseSession()
		return nil, status.Errorf(codes.InvalidArgument, "materialize workspace: %v", err)
	}
	if err := workspace.RunSetup(ctx, s.WorkspaceRoot, plan.GetSetupCommands()); err != nil {
		s.releaseSession()
		return nil, status.Errorf(codes.FailedPrecondition, "run setup commands: %v", err)
	}
	if err := s.Runtime.Start(ctx, sessionID); err != nil {
		s.releaseSession()
		return nil, status.Errorf(codes.Internal, "start runtime: %v", err)
	}
	return &adapterv1.StartSessionResponse{}, nil
}

// claimSession marks the pod as holding sessionID, returning a gRPC
// Unavailable error when the pod is not idle. The §4.7 StartSession
// contract specifies Unavailable for a pod that is not idle.
func (s *Server) claimSession(sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessionID != "" {
		return status.Errorf(codes.Unavailable,
			"pod is not idle: session %s is already assigned", s.sessionID)
	}
	s.sessionID = sessionID
	return nil
}

// releaseSession returns the pod to the idle state.
func (s *Server) releaseSession() {
	s.mu.Lock()
	s.sessionID = ""
	s.mu.Unlock()
}
