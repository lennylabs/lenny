// SPDX-License-Identifier: MIT

package adapter

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/adapter/workspace"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// RunSetup executes the §14 WorkspacePlan setup commands against the
// materialized workspace. It is the third RPC of the §4.7 session
// assignment sequence (PrepareWorkspace, FinalizeWorkspace, RunSetup,
// StartSession): the workspace has already been materialized into
// WorkspaceRoot by the time RunSetup runs, so this RPC neither claims
// the session nor touches pod assignment state. The §5.1 setupPolicy in
// the request bounds the aggregate setup phase.
func (s *Server) RunSetup(ctx context.Context, req *adapterv1.RunSetupRequest) (*adapterv1.RunSetupResponse, error) {
	if req.GetSessionId().GetValue() == "" {
		return nil, status.Error(codes.InvalidArgument, "RunSetup requires a session id")
	}
	if s.WorkspaceRoot == "" {
		return nil, status.Error(codes.FailedPrecondition,
			"adapter is not configured with a workspace root")
	}
	if err := workspace.RunSetup(ctx, s.WorkspaceRoot, req.GetSetupCommands(),
		setupOptionsFromProto(req.GetSetupPolicy())); err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "run setup commands: %v", err)
	}
	return &adapterv1.RunSetupResponse{}, nil
}
