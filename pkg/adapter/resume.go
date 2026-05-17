// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"io"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/adapter/workspace"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// CheckpointSource loads a stored §4.4 workspace checkpoint archive
// from the artifact store. It is the read counterpart of CheckpointSink.
type CheckpointSource interface {
	// LoadCheckpoint opens the gzip-tar workspace archive for the
	// checkpoint. The caller closes the returned reader.
	LoadCheckpoint(ctx context.Context, checkpointID string) (io.ReadCloser, error)
}

// Resume implements the §4.7 / §7.1 Resume RPC: it restores a session's
// workspace from a checkpoint on a replacement pod. It claims the pod
// for the session, rebuilds the workspace from the checkpoint archive,
// and starts the runtime — the replacement-pod counterpart of
// StartSession. On any failure after the session is claimed the pod is
// returned to idle so a retry can land on a fresh pod.
func (s *Server) Resume(ctx context.Context, req *adapterv1.ResumeRequest) (*adapterv1.ResumeResponse, error) {
	sessionID := req.GetSessionId().GetValue()
	if sessionID == "" {
		return nil, status.Error(codes.InvalidArgument, "Resume requires a session id")
	}
	if req.GetCheckpointId() == "" {
		return nil, status.Error(codes.InvalidArgument, "Resume requires a checkpoint id")
	}
	if s.WorkspaceRoot == "" || s.Runtime == nil {
		return nil, status.Error(codes.FailedPrecondition,
			"adapter is not configured with a workspace root and runtime")
	}
	if s.Restorer == nil {
		return nil, status.Error(codes.Unimplemented,
			"adapter is not configured with a checkpoint source")
	}

	if err := s.claimSession(sessionID); err != nil {
		return nil, err
	}

	rc, err := s.Restorer.LoadCheckpoint(ctx, req.GetCheckpointId())
	if err != nil {
		s.releaseSession()
		return nil, status.Errorf(codes.Internal, "load checkpoint %s: %v", req.GetCheckpointId(), err)
	}
	restored, extractErr := workspace.Extract(s.WorkspaceRoot, rc)
	_ = rc.Close()
	if extractErr != nil {
		s.releaseSession()
		return nil, status.Errorf(codes.Internal, "restore workspace from checkpoint %s: %v",
			req.GetCheckpointId(), extractErr)
	}
	// §15.4: re-deliver the manifest so the restored runtime reads the
	// same §8.3 experimentContext and tracingContext as before the resume.
	if err := s.writeSessionManifest(sessionID, req.GetExperimentContext(), req.GetTracingContext()); err != nil {
		s.releaseSession()
		return nil, status.Errorf(codes.Internal, "write adapter manifest: %v", err)
	}
	if err := s.Runtime.Start(ctx, sessionID); err != nil {
		s.releaseSession()
		return nil, status.Errorf(codes.Internal, "start runtime: %v", err)
	}
	return &adapterv1.ResumeResponse{RestoredBytes: restored}, nil
}
