// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"io"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/adapter/workspace"
	"github.com/lennylabs/lenny/pkg/checkpoint"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// CheckpointSink stores a session's workspace checkpoint archive in the
// §4.4 artifact store and returns the stored checkpoint's identifier.
type CheckpointSink interface {
	// SaveCheckpoint reads the gzip-tar workspace archive from r and
	// stores it as a checkpoint for the session, returning the
	// checkpoint's identifier. The implementation reads r to EOF on
	// success and may stop early on error.
	SaveCheckpoint(ctx context.Context, sessionID string, r io.Reader) (checkpointID string, err error)
}

// Checkpoint implements the §4.4 / §4.7 Checkpoint RPC: it snapshots the
// session's workspace and stores it as a checkpoint in the artifact
// store. This is the best-effort path — the workspace is archived live
// without quiescing the runtime, suitable for Basic and Standard-level
// runtimes; Full-level cooperative quiescence runs over the lifecycle
// channel.
//
// The archive is streamed to the sink through an in-process pipe so a
// large workspace is never buffered in memory. The compressed byte
// count Archive reports is returned as the checkpoint size.
func (s *Server) Checkpoint(ctx context.Context, req *adapterv1.CheckpointRequest) (*adapterv1.CheckpointResponse, error) {
	sessionID := req.GetSessionId().GetValue()
	if sessionID == "" {
		return nil, status.Error(codes.InvalidArgument, "Checkpoint requires a session id")
	}
	if err := s.checkSession(sessionID); err != nil {
		return nil, err
	}
	if s.WorkspaceRoot == "" {
		return nil, status.Error(codes.FailedPrecondition,
			"adapter is not configured with a workspace root")
	}
	if s.Checkpoints == nil {
		return nil, status.Error(codes.Unimplemented,
			"adapter is not configured with a checkpoint sink")
	}

	deadline := checkpoint.CheckpointTimeout
	if ms := req.GetDeadlineMs(); ms > 0 {
		deadline = time.Duration(ms) * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()

	type archiveResult struct {
		n   int64
		err error
	}
	archived := make(chan archiveResult, 1)
	pr, pw := io.Pipe()
	go func() {
		n, err := workspace.Archive(s.WorkspaceRoot, pw)
		_ = pw.CloseWithError(err)
		archived <- archiveResult{n: n, err: err}
	}()

	id, saveErr := s.Checkpoints.SaveCheckpoint(ctx, sessionID, pr)
	// Closing the read end unblocks the archive goroutine if the sink
	// stopped reading before EOF (an error or a deadline).
	_ = pr.Close()
	res := <-archived

	if res.err != nil {
		return nil, status.Errorf(codes.Internal, "archive workspace: %v", res.err)
	}
	if saveErr != nil {
		return nil, status.Errorf(codes.Internal, "store checkpoint: %v", saveErr)
	}
	return &adapterv1.CheckpointResponse{
		CheckpointId: id,
		SizeBytes:    res.n,
	}, nil
}
