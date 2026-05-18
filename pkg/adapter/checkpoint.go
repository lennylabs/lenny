// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"errors"
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

	// §4.7: Checkpoint and Interrupt are serialized per session.
	release, err := s.ops.Begin(ctx, opCheckpoint)
	if err != nil {
		if errors.Is(err, errOpCoalesced) || errors.Is(err, errOpBusy) {
			return nil, status.Error(codes.Aborted,
				"checkpoint coalesced into an in-flight operation; retry")
		}
		return nil, status.FromContextError(err).Err()
	}
	defer release()

	// §4.7: a Full-level runtime checkpoints cooperatively over the
	// lifecycle channel — quiesce, snapshot, resume. Other runtimes are
	// archived live (best-effort consistency).
	if s.Lifecycle != nil && s.Lifecycle.Supports("checkpoint") {
		return s.checkpointViaLifecycle(ctx, req, sessionID)
	}
	id, size, err := s.archiveAndStore(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return &adapterv1.CheckpointResponse{CheckpointId: id, SizeBytes: size}, nil
}

// archiveAndStore archives the session workspace and streams it to the
// checkpoint sink through an in-process pipe, so a large workspace is
// never buffered in memory. It returns the stored checkpoint id and the
// compressed archive size.
func (s *Server) archiveAndStore(ctx context.Context, sessionID string) (string, int64, error) {
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
		return "", 0, status.Errorf(codes.Internal, "archive workspace: %v", res.err)
	}
	if saveErr != nil {
		return "", 0, status.Errorf(codes.Internal, "store checkpoint: %v", saveErr)
	}
	return id, res.n, nil
}

// checkpointViaLifecycle runs the §4.7 cooperative checkpoint: it asks
// the runtime to quiesce, captures the workspace snapshot once the
// runtime reports checkpoint_ready, and resumes the runtime with the
// snapshot outcome via checkpoint_complete.
func (s *Server) checkpointViaLifecycle(ctx context.Context, req *adapterv1.CheckpointRequest, sessionID string) (*adapterv1.CheckpointResponse, error) {
	corrID := newLifecycleID()
	if err := s.Lifecycle.RequestCheckpoint(ctx, corrID, req.GetDeadlineMs()); err != nil {
		return nil, status.Errorf(codes.Internal, "checkpoint quiesce: %v", err)
	}
	id, size, archiveErr := s.archiveAndStore(ctx, sessionID)
	// Resume the runtime whatever the snapshot outcome.
	cpStatus, reason := "ok", ""
	if archiveErr != nil {
		cpStatus, reason = "failed", archiveErr.Error()
	}
	_ = s.Lifecycle.CompleteCheckpoint(corrID, cpStatus, reason)
	if archiveErr != nil {
		return nil, archiveErr
	}
	return &adapterv1.CheckpointResponse{CheckpointId: id, SizeBytes: size}, nil
}
