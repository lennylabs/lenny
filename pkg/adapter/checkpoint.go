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

// CheckpointAborter is the §4.4 line 248 abort-cleanup surface the
// adapter invokes whenever a checkpoint fails between the initial
// quiescence request and the metadata commit. The implementation
// deletes any partially-uploaded MinIO objects for the session's
// in-flight checkpoint (`AbortMultipartUpload` for multipart uploads,
// `DeleteObject` for single-part). The cleanup is best-effort: a
// DeleteObject failure returns an error so the adapter can bump the
// `lenny_checkpoint_orphaned_objects_total` counter.
//
// spec: §4.4 line 248 — "When a checkpoint is aborted ... the adapter
// MUST delete any partially uploaded MinIO objects for that checkpoint
// attempt".
type CheckpointAborter interface {
	// AbortPartial removes any partially-uploaded objects for the
	// session's in-flight checkpoint attempt. Returns an error when
	// the underlying storage delete failed; the adapter bumps the
	// orphan counter in that case. A no-op (no partial objects to
	// clean) returns nil.
	AbortPartial(ctx context.Context, sessionID string) error
}

// CheckpointMetrics is the §4.4 metrics surface the adapter Checkpoint
// RPC emits on. The gateway-side adapter wires this against the
// gatewaymetrics.Metrics; tests substitute fakes through the same
// interface. A nil implementation makes every metric call a no-op.
//
// spec: §4.4 lines 248, 254 — `lenny_checkpoint_orphaned_objects_total`
// on abort-cleanup failure; `lenny_checkpoint_size_exceeded_total` on
// pre-checkpoint workspace-size probe exceed.
type CheckpointMetrics interface {
	// IncCheckpointOrphanedObjects bumps
	// `lenny_checkpoint_orphaned_objects_total` per §4.4 line 248
	// when an abort-cleanup DeleteObject call failed. Pool and trigger
	// label values are advisory; pass empty strings when unknown.
	IncCheckpointOrphanedObjects(pool, trigger string)
	// IncCheckpointSizeExceeded bumps
	// `lenny_checkpoint_size_exceeded_total` per §4.4 line 254 when
	// the pre-checkpoint workspace-size probe rejects the run. Pool
	// and level label values are advisory; pass empty strings when
	// unknown.
	IncCheckpointSizeExceeded(pool, level string)
}

// CheckpointEventEmitter is the §4.4 session-event surface the adapter
// publishes `checkpoint.skipped` events on. The gateway-side adapter
// wires it against the session event bus; tests substitute fakes
// through the same interface. A nil implementation makes every emit
// call a no-op.
//
// spec: §4.4 line 254 — on workspace_size_limit reject the session
// emits a `checkpoint.skipped` event with `reason:
// "workspace_size_limit"`.
type CheckpointEventEmitter interface {
	// EmitCheckpointSkipped publishes a `checkpoint.skipped` event on
	// the session's event stream with the supplied reason and detail
	// fields. Best-effort: emission failures are logged by the
	// implementation, not returned, so a stuck event bus never blocks
	// the checkpoint path.
	EmitCheckpointSkipped(ctx context.Context, sessionID, reason string, fields map[string]any)
}

// WorkspaceSizeFunc returns the on-disk byte count of the session's
// workspace root. The adapter calls this immediately before quiescing
// the runtime to evaluate the §4.4 pre-checkpoint workspace-size
// probe. Returning (0, nil) for an unconfigured workspace path skips
// the probe (the kubelet `emptyDir.sizeLimit` is the backstop). A nil
// WorkspaceSizeFunc on the Server skips the probe entirely.
//
// spec: §4.4 line 254 — "the adapter additionally performs a
// pre-checkpoint workspace size probe before initiating the
// quiescence handshake".
type WorkspaceSizeFunc func(workspaceRoot string) (int64, error)

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
//
// Before any quiescence, the adapter evaluates the §4.4 line 254
// pre-checkpoint workspace-size probe. If the workspace exceeds
// `WorkspaceSizeLimitBytes`, the checkpoint is aborted immediately
// (no quiescence), the `lenny_checkpoint_size_exceeded_total` counter
// is incremented, and the session emits a `checkpoint.skipped` event
// with `reason: "workspace_size_limit"`.
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

	// §4.4 line 254 — pre-checkpoint workspace-size probe. Runs before
	// quiescence so an oversized workspace never pauses the runtime.
	if err := s.workspaceSizePreCheck(ctx, sessionID); err != nil {
		return nil, err
	}

	// §4.7: a Full-level runtime checkpoints cooperatively over the
	// lifecycle channel — quiesce, snapshot, resume. Other runtimes are
	// archived live (best-effort consistency).
	if s.Lifecycle != nil && s.Lifecycle.Supports("checkpoint") {
		return s.checkpointViaLifecycle(ctx, req, sessionID)
	}
	id, size, err := s.archiveAndStore(ctx, sessionID)
	if err != nil {
		// §4.4 line 248 — abort cleanup on every failed checkpoint.
		s.runAbortCleanup(ctx, sessionID)
		return nil, err
	}
	return &adapterv1.CheckpointResponse{CheckpointId: id, SizeBytes: size}, nil
}

// workspaceSizePreCheck evaluates the §4.4 line 254 pre-checkpoint
// workspace-size probe. On a size_exceeded result it emits the
// `lenny_checkpoint_size_exceeded_total` counter, publishes the
// `checkpoint.skipped` event with `reason: "workspace_size_limit"`,
// and returns a FailedPrecondition gRPC error so the gateway sees the
// abort. Returns nil when the probe passes or is unconfigured.
//
// spec: §4.4 line 254 — "If the measured size exceeds
// workspaceSizeLimitBytes, the checkpoint is aborted immediately ...
// the lenny_checkpoint_size_exceeded_total counter is incremented ...
// the session emits a checkpoint.skipped event with reason:
// 'workspace_size_limit'".
func (s *Server) workspaceSizePreCheck(ctx context.Context, sessionID string) error {
	if s.WorkspaceSize == nil || s.WorkspaceSizeLimitBytes <= 0 {
		// Probe is unconfigured; the kubelet emptyDir.sizeLimit backstop
		// is the only enforcement (per §4.4 hard workspace size limit).
		return nil
	}
	size, err := s.WorkspaceSize(s.WorkspaceRoot)
	if err != nil {
		// A stat failure should not block a checkpoint — fall through
		// without invoking the size limit. The §4.4 emptyDir cap still
		// applies as the kubelet-enforced backstop.
		return nil
	}
	if probeErr := checkpoint.WorkspaceSizePreCheck(size, s.WorkspaceSizeLimitBytes); probeErr != nil {
		var sizeErr *checkpoint.WorkspaceSizeExceededError
		if errors.As(probeErr, &sizeErr) {
			s.recordSizeExceeded(ctx, sessionID, sizeErr)
			return status.Errorf(codes.FailedPrecondition,
				"checkpoint: %s", sizeErr.Error())
		}
		return status.Errorf(codes.FailedPrecondition,
			"checkpoint: workspace-size probe: %v", probeErr)
	}
	return nil
}

// recordSizeExceeded emits the §4.4 line 254 size-exceeded telemetry:
// the `lenny_checkpoint_size_exceeded_total` counter increment and the
// `checkpoint.skipped` session-event publish with `reason:
// "workspace_size_limit"`. Both surfaces are no-ops when unwired so a
// dev-mode adapter without metrics or events still functions.
func (s *Server) recordSizeExceeded(ctx context.Context, sessionID string, err *checkpoint.WorkspaceSizeExceededError) {
	if s.CheckpointMetrics != nil {
		s.CheckpointMetrics.IncCheckpointSizeExceeded(s.CheckpointPoolLabel, s.CheckpointLevelLabel)
	}
	if s.CheckpointEvents != nil {
		s.CheckpointEvents.EmitCheckpointSkipped(ctx, sessionID,
			"workspace_size_limit",
			map[string]any{
				"workspace_bytes": err.WorkspaceBytes,
				"limit_bytes":     err.LimitBytes,
				"pool":            s.CheckpointPoolLabel,
				"level":           s.CheckpointLevelLabel,
			})
	}
}

// runAbortCleanup performs the §4.4 line 248 checkpoint abort cleanup:
// every failed checkpoint MUST delete any partially-uploaded MinIO
// objects for the in-flight attempt. The CheckpointAborter
// implementation walks the partial-manifest store for the session and
// issues per-key DeleteObject calls (or AbortMultipartUpload on
// in-flight multipart uploads); a DeleteObject failure bumps the
// `lenny_checkpoint_orphaned_objects_total` counter. The cleanup runs
// best-effort: an aborter failure never re-raises out of the
// Checkpoint RPC (the underlying checkpoint error is already returned
// to the caller).
//
// spec: §4.4 line 248.
func (s *Server) runAbortCleanup(ctx context.Context, sessionID string) {
	if s.CheckpointAborter == nil {
		return
	}
	if err := s.CheckpointAborter.AbortPartial(ctx, sessionID); err != nil {
		if s.CheckpointMetrics != nil {
			s.CheckpointMetrics.IncCheckpointOrphanedObjects(s.CheckpointPoolLabel, s.CheckpointTriggerLabel)
		}
	}
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
		// §4.4 line 248 — a Go recover() wrapping the checkpoint
		// goroutine MUST either re-panic or mark /healthz unhealthy.
		// Re-panic preserves the existing crash semantics: the adapter
		// process exits and Kubernetes restarts the pod (the agent
		// process resumes from the last successful checkpoint per
		// §7.2). The PipeWriter is closed with an error so the read
		// side unblocks immediately rather than waiting for the close
		// implied by the goroutine exit.
		defer func() {
			if r := recover(); r != nil {
				_ = pw.CloseWithError(io.ErrClosedPipe)
				archived <- archiveResult{err: io.ErrClosedPipe}
				panic(r) // spec: §4.4 line 248 re-panic mandate.
			}
		}()
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
		// §4.4 line 248 — abort cleanup on every failed checkpoint
		// (Full-level lifecycle path included).
		s.runAbortCleanup(ctx, sessionID)
		return nil, archiveErr
	}
	return &adapterv1.CheckpointResponse{CheckpointId: id, SizeBytes: size}, nil
}
