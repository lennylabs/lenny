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

// checkpointRoots returns the §4.4 checkpoint bundle: the session
// workspace under workspace.WorkspacePrefix, plus the §6.4 line 380
// /sessions session-file tmpfs under workspace.SessionsPrefix when the
// adapter is configured with a SessionsRoot. The sessions root is
// skipped (no entries) when unset or absent on disk, so a runtime that
// keeps no session file checkpoints workspace-only exactly as before.
//
// spec: §7.3 line 408 step (e) (replay workspace checkpoint) + line 409
// step (f) (restore session file to expected path) — both replayed from
// this one bundle on Resume.
func (s *Server) checkpointRoots() []workspace.NamedRoot {
	roots := []workspace.NamedRoot{
		{Prefix: workspace.WorkspacePrefix, Root: s.WorkspaceRoot},
	}
	if s.SessionsRoot != "" {
		roots = append(roots, workspace.NamedRoot{
			Prefix: workspace.SessionsPrefix, Root: s.SessionsRoot,
		})
	}
	return roots
}

// probeWorkspaceBytes measures the on-disk workspace byte total for the
// §4.4 line 254 pre-checkpoint size probe, summing every checkpoint root
// the resume path replays so the gateway reserves quota against the whole
// bundle rather than the workspace tree alone. It takes the precomputed
// roots the handler resolved for the slot so the probe measures the same
// subtree the archive captures.
func (s *Server) probeWorkspaceBytes(roots []workspace.NamedRoot) (int64, error) {
	var total int64
	for _, root := range roots {
		n, err := workspace.Size(root.Root)
		if err != nil {
			return 0, err
		}
		total += n
	}
	return total, nil
}

// Checkpoint serves the §4.4 / §10.1 gateway-driven Checkpoint stream. The
// gateway is the gRPC client: it opens the stream with a CheckpointStart
// carrying the gateway-minted checkpoint_id, the adapter reports the
// on-disk workspace size (CheckpointProbe) so the gateway reserves storage
// quota, and then the adapter archives the checkpoint bundle into a
// continuous tar (or tar.gz) byte stream, slices it into fixed-size
// chunks, and for each chunk declares it (ChunkReady), receives a
// single-chunk presigned PUT capability (CheckpointGrant), PUTs the chunk
// against object storage replaying every signed header verbatim, and
// confirms it (ChunkCommitted). The stream closes with a CheckpointSummary
// on success, a CheckpointFailed carrying the object store's HTTP status
// and error code when a chunk PUT is rejected, or a FailedPrecondition
// gRPC status when the §4.4 line 255 workspace-size probe rejects the
// attempt before any grant is minted.
//
// spec: §4.4 (checkpoint quiescence, hard workspace size limit, storage
// failure retry budget); §10.1 line 130 (the gateway mints checkpoint_id).
func (s *Server) Checkpoint(stream adapterv1.Adapter_CheckpointServer) error {
	ctx := stream.Context()
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	start := first.GetStart()
	if start == nil {
		return status.Error(codes.InvalidArgument,
			"Checkpoint stream must open with a CheckpointStart")
	}
	if s.WorkspaceRoot == "" {
		return status.Error(codes.FailedPrecondition,
			"adapter is not configured with a workspace root")
	}
	if s.CheckpointTransport == nil {
		return status.Error(codes.FailedPrecondition,
			"adapter is not configured with a checkpoint transport")
	}

	// spec: §5.2, §6.4 — resolve the checkpoint bundle for the target slot
	// once. An absent slot_id yields the pod-global bundle unchanged; a
	// named slot yields its slot subtree, and an unassigned slot is rejected
	// with FailedPrecondition before the op lock is taken or any grant is
	// minted.
	roots, err := s.checkpointRootsForSlot(start.GetSlotId().GetValue())
	if err != nil {
		return err
	}

	// spec: §4.7 — the Checkpoint RPC shares the pod-level op lock with
	// Interrupt so at most one runs at a time. The lock admits one pending
	// checkpoint per distinct slotId and promotes them in slot-ID order
	// (spec: §5.2), so a concurrent-session drain checkpoints every slot.
	// A barrier-window checkpoint runs through the same lock; the barrier's
	// quiescence has already drained dispatch, so the lock is uncontended
	// there by construction.
	release, err := s.ops.Begin(ctx, opCheckpoint, start.GetSlotId().GetValue())
	if err != nil {
		// A busy lock is a gateway-side abort of this attempt; the gateway
		// finalises the manifest row partial.
		return status.Errorf(codes.Aborted, "checkpoint op lock: %v", err)
	}
	defer release()

	// Link a quiesce-and-hold barrier, if one is waiting, to this stream's
	// checkpoint_id and signal it on stream termination so the barrier's
	// CheckpointBarrierAck echoes the gateway-minted id (§10.1 line 167).
	linked := s.barrier.link(start.GetCheckpointId())
	if linked {
		defer s.barrier.complete()
	}

	trigger := checkpoint.TriggerFromProto(start.GetTrigger())

	// spec: §4.4 line 254-255 — probe the on-disk workspace size and enforce
	// the hard limit before the quiescence handshake. An over-limit workspace
	// aborts with FailedPrecondition before any grant is minted and before
	// the runtime is quiesced, so a checkpoint that cannot be taken never
	// pauses the agent. The gateway maps the FailedPrecondition onto
	// lenny_checkpoint_size_exceeded_total and the checkpoint.skipped event.
	wsBytes, err := s.probeWorkspaceBytes(roots)
	if err != nil {
		return status.Errorf(codes.Internal, "probe workspace size: %v", err)
	}
	if serr := checkpoint.WorkspaceSizePreCheck(wsBytes, s.WorkspaceSizeLimitBytes); serr != nil {
		return status.Errorf(codes.FailedPrecondition, "%s", serr.Error())
	}
	if serr := stream.Send(&adapterv1.CheckpointResponse{
		Msg: &adapterv1.CheckpointResponse_Probe{
			Probe: &adapterv1.CheckpointProbe{WorkspaceBytes: wsBytes},
		},
	}); serr != nil {
		return serr
	}

	// spec: §4.4 line 241 — Full-level runtimes quiesce cooperatively over
	// the lifecycle channel: checkpoint_request → checkpoint_ready before the
	// first chunk is archived, checkpoint_complete after the stream ends. The
	// runtime stays quiesced for the whole chunked archive, and the completion
	// frame carries status ok only when Summary is reached; a terminal Failed
	// frame or a gateway Abort completes with status failed and the reason.
	completeStatus, completeReason := "ok", ""
	if s.Lifecycle != nil {
		if rerr := s.Lifecycle.RequestCheckpoint(ctx, start.GetCheckpointId(), int32(start.GetDeadlineMs())); rerr != nil {
			return status.Errorf(codes.Internal, "checkpoint quiesce handshake: %v", rerr)
		}
		defer func() {
			_ = s.Lifecycle.CompleteCheckpoint(start.GetCheckpointId(), completeStatus, completeReason)
		}()
	}

	failReason, serr := s.streamChunks(ctx, stream, start, trigger, roots)
	if serr != nil {
		completeStatus, completeReason = "failed", serr.Error()
		return serr
	}
	if failReason != "" {
		completeStatus, completeReason = "failed", failReason
	}
	return nil
}

// streamChunks archives the checkpoint bundle, slices it into fixed-size
// chunks, and runs the ChunkReady → CheckpointGrant → PUT → ChunkCommitted
// loop for each, closing with a CheckpointSummary. A rejected chunk PUT or a
// gateway Abort terminates the stream with a CheckpointFailed frame (not a
// gRPC error) so the gateway observes the object store's HTTP status and
// error code. It returns a non-empty failReason when the stream terminated
// on a Failed frame or an Abort (so the caller reports a Full-level runtime a
// failed checkpoint_complete) and an empty reason on the Summary success
// path. A non-nil error is a gRPC-level failure of the stream itself.
func (s *Server) streamChunks(ctx context.Context, stream adapterv1.Adapter_CheckpointServer, start *adapterv1.CheckpointStart, trigger checkpoint.Trigger, roots []workspace.NamedRoot) (string, error) {
	// Archive the bundle into a pipe. The goroutine keeps the recover() →
	// re-panic semantics §4.4 mandates: silently recovering the checkpoint
	// goroutine panic without signaling unhealthiness would leave the agent
	// frozen while liveness still passes, so the re-panic exits the process
	// and Kubernetes restarts the pod, resuming from the last checkpoint.
	pr, pw := io.Pipe()
	go func() {
		defer func() {
			if r := recover(); r != nil {
				_ = pw.CloseWithError(io.ErrClosedPipe)
				panic(r) // spec: §4.4 re-panic mandate.
			}
		}()
		_, aerr := workspace.ArchiveTree(roots, pw)
		_ = pw.CloseWithError(aerr)
	}()
	defer func() { _ = pr.Close() }()

	chunkSize := start.GetChunkSizeBytes()
	if chunkSize <= 0 {
		chunkSize = defaultCheckpointChunkSize
	}
	budget := checkpoint.RetryBudgetFor(trigger)
	spill := s.spillDir()

	var index uint32
	var total int64
	for {
		chunk, more, rerr := readChunk(pr, chunkSize, checkpointBufferMemoryBytes, spill)
		if rerr != nil {
			return "", status.Errorf(codes.Internal, "archive workspace: %v", rerr)
		}
		if !more {
			break
		}
		failReason, serr := s.uploadChunk(ctx, stream, index, chunk, budget)
		chunk.close()
		if serr != nil {
			return "", serr
		}
		if failReason != "" {
			// A CheckpointFailed frame or a gateway Abort ended the stream.
			return failReason, nil
		}
		total += chunk.len()
		index++
	}

	return "", stream.Send(&adapterv1.CheckpointResponse{
		Msg: &adapterv1.CheckpointResponse_Summary{
			Summary: &adapterv1.CheckpointSummary{ChunkCount: index, TotalBytes: total},
		},
	})
}

// uploadChunk declares one chunk, receives its grant (or a gateway Abort),
// PUTs it within the §4.4 retry budget, and confirms it. It returns a
// non-empty failReason when the stream terminated on this chunk (a
// CheckpointFailed frame the caller already sent, or a gateway Abort) so the
// loop stops without emitting a Summary and the Full-level runtime is told
// the checkpoint failed. A successful ChunkCommitted returns an empty reason
// and nil error; a non-nil error is a gRPC-level failure of the stream.
func (s *Server) uploadChunk(ctx context.Context, stream adapterv1.Adapter_CheckpointServer, index uint32, chunk *chunkBuffer, budget checkpoint.RetryBudget) (string, error) {
	if err := stream.Send(&adapterv1.CheckpointResponse{
		Msg: &adapterv1.CheckpointResponse_ChunkReady{
			ChunkReady: &adapterv1.ChunkReady{Index: index, Length: chunk.len()},
		},
	}); err != nil {
		return "", err
	}

	grant, aborted, err := s.awaitGrant(stream, index)
	if err != nil {
		return "", err
	}
	if aborted {
		// spec: §4.4 — the adapter does not retry a PUT the gateway aborts;
		// the attempt terminates as a failed checkpoint.
		return "checkpoint aborted by gateway", nil
	}

	deadline := time.Now().Add(budget.TotalBudget)
	delay := budget.Initial
	var lastStatus int
	var lastCode string
	for {
		httpStatus, code, retriable, perr := s.attemptPut(ctx, grant, chunk)
		if perr != nil {
			return "", status.Errorf(codes.Internal, "checkpoint PUT chunk %d: %v", index, perr)
		}
		if httpStatus >= 200 && httpStatus < 300 {
			return "", stream.Send(&adapterv1.CheckpointResponse{
				Msg: &adapterv1.CheckpointResponse_ChunkCommitted{
					ChunkCommitted: &adapterv1.ChunkCommitted{Index: index},
				},
			})
		}
		lastStatus, lastCode = httpStatus, code
		if !retriable {
			// spec: §4.4 — an object-store rejection terminates the stream
			// with the HTTP status and error code so the gateway can map a
			// kms:-coded rejection onto a classification-control violation.
			return s.failChunk(stream, index, "object store rejected chunk", httpStatus, code)
		}
		if !time.Now().Before(deadline) {
			// spec: §4.4 lines 261-264 — retry budget exhausted; report the
			// retry-exhausted failure so the gateway stamps
			// lenny_checkpoint_storage_failure_total{reason="retry_exhausted"}.
			return s.failChunk(stream, index, "retry_exhausted", lastStatus, lastCode)
		}
		if serr := sleepCtx(ctx, delay); serr != nil {
			return "", serr
		}
		delay *= 2
		if delay > budget.Cap {
			delay = budget.Cap
		}
		// spec: §4.4 — a retry that outlives the grant's expiry requests a
		// fresh grant for the same index on the open stream.
		if grantExpired(grant) {
			if serr := stream.Send(&adapterv1.CheckpointResponse{
				Msg: &adapterv1.CheckpointResponse_ChunkReady{
					ChunkReady: &adapterv1.ChunkReady{Index: index, Length: chunk.len()},
				},
			}); serr != nil {
				return "", serr
			}
			fresh, aborted, gerr := s.awaitGrant(stream, index)
			if gerr != nil {
				return "", gerr
			}
			if aborted {
				return "checkpoint aborted by gateway", nil
			}
			grant = fresh
		}
	}
}

// awaitGrant reads the gateway's response to a ChunkReady: the presigned
// PUT capability for index, or a CheckpointAbort that stops the attempt.
func (s *Server) awaitGrant(stream adapterv1.Adapter_CheckpointServer, index uint32) (*adapterv1.CheckpointGrant, bool, error) {
	msg, err := stream.Recv()
	if err != nil {
		return nil, false, err
	}
	if msg.GetAbort() != nil {
		return nil, true, nil
	}
	grant := msg.GetGrant()
	if grant == nil {
		return nil, false, status.Errorf(codes.InvalidArgument,
			"expected a CheckpointGrant or CheckpointAbort for chunk %d", index)
	}
	if grant.GetIndex() != index {
		return nil, false, status.Errorf(codes.InvalidArgument,
			"grant index %d does not match declared chunk %d", grant.GetIndex(), index)
	}
	return grant, false, nil
}

// attemptPut issues one PUT of chunk against grant. retriable reports
// whether a non-2xx result (a 5xx or a transport error) should be retried
// within the budget; a 4xx rejection is terminal.
func (s *Server) attemptPut(ctx context.Context, grant *adapterv1.CheckpointGrant, chunk *chunkBuffer) (httpStatus int, errorCode string, retriable bool, err error) {
	body, oerr := chunk.reopen()
	if oerr != nil {
		return 0, "", false, oerr
	}
	defer func() { _ = body.Close() }()
	httpStatus, errorCode, err = s.CheckpointTransport.PutChunk(
		ctx, grant.GetUrl(), grant.GetHeaders(), grant.GetContentLength(), body,
	)
	if err != nil {
		// Transport-level failure (unreachable object store): retriable.
		return 0, "", true, nil
	}
	if httpStatus >= 500 {
		return httpStatus, errorCode, true, nil
	}
	return httpStatus, errorCode, false, nil
}

// failChunk terminates the stream with a CheckpointFailed frame and returns
// the reason so the caller reports the Full-level runtime a failed
// checkpoint_complete. It returns a non-nil error only when the Failed frame
// itself could not be sent.
func (s *Server) failChunk(stream adapterv1.Adapter_CheckpointServer, index uint32, reason string, httpStatus int, code string) (string, error) {
	if err := s.sendFailed(stream, index, reason, httpStatus, code); err != nil {
		return "", err
	}
	return reason, nil
}

// sendFailed terminates the stream with a CheckpointFailed frame.
func (s *Server) sendFailed(stream adapterv1.Adapter_CheckpointServer, index uint32, reason string, httpStatus int, code string) error {
	return stream.Send(&adapterv1.CheckpointResponse{
		Msg: &adapterv1.CheckpointResponse_Failed{
			Failed: &adapterv1.CheckpointFailed{
				Reason:     reason,
				Index:      index,
				HttpStatus: int32(httpStatus),
				ErrorCode:  code,
			},
		},
	})
}

// grantExpired reports whether grant's capability has expired. A grant
// with no expiry is treated as valid.
func grantExpired(grant *adapterv1.CheckpointGrant) bool {
	ts := grant.GetExpiresAt()
	if ts == nil {
		return false
	}
	return time.Now().After(ts.AsTime())
}

// sleepCtx sleeps for d unless ctx is cancelled first.
func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
