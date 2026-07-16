// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"io"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/adapter/workspace"
	"github.com/lennylabs/lenny/pkg/checkpoint"
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

// CheckpointMetrics is the §4.4 metrics surface the adapter checkpoint
// path emits on. The gateway-side adapter wires this against the
// gatewaymetrics.Metrics; tests substitute fakes through the same
// interface. A nil implementation makes every metric call a no-op.
//
// spec: §4.4 lines 248, 262 — `lenny_checkpoint_orphaned_objects_total`
// on abort-cleanup failure; `lenny_checkpoint_storage_failure_total`
// on MinIO upload failure for non-eviction triggers.
type CheckpointMetrics interface {
	// IncCheckpointOrphanedObjects bumps
	// `lenny_checkpoint_orphaned_objects_total` per §4.4 line 248
	// when an abort-cleanup DeleteObject call failed. Pool and trigger
	// label values are advisory; pass empty strings when unknown.
	IncCheckpointOrphanedObjects(pool, trigger string)
	// IncCheckpointStorageFailure bumps
	// `lenny_checkpoint_storage_failure_total` per §4.4 line 262 when
	// a non-eviction checkpoint upload fails after all retries (the
	// adapter discards the failed checkpoint and resumes the agent).
	// Pool, level, and trigger label values are advisory; pass empty
	// strings when unknown.
	// spec: §4.4 line 262.
	IncCheckpointStorageFailure(pool, level, trigger string)
}

// runAbortCleanup performs the §4.4 line 248 checkpoint abort cleanup:
// every failed checkpoint MUST delete any partially-uploaded MinIO
// objects for the in-flight attempt. The CheckpointAborter
// implementation walks the partial-manifest store for the session and
// issues per-key DeleteObject calls (or AbortMultipartUpload on
// in-flight multipart uploads); a DeleteObject failure bumps the
// `lenny_checkpoint_orphaned_objects_total` counter. The cleanup runs
// best-effort: an aborter failure never re-raises out of the checkpoint
// path (the underlying checkpoint error is already returned to the
// caller).
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
		// §4.4 / §7.3 step (f): bundle the workspace and the /sessions
		// session-file tmpfs so a resume can restore both.
		n, err := workspace.ArchiveTree(s.checkpointRoots(), pw)
		_ = pw.CloseWithError(err)
		archived <- archiveResult{n: n, err: err}
	}()
	id, saveErr := s.Checkpoints.SaveCheckpoint(ctx, sessionID, pr)
	// Closing the read end unblocks the archive goroutine if the sink
	// stopped reading before EOF (an error or a deadline).
	_ = pr.Close()
	res := <-archived
	if res.err != nil {
		// §4.4 line 262 — archive failure is treated as a non-eviction
		// checkpoint upload failure for telemetry purposes: the agent
		// resumes, the failed checkpoint is discarded, the next
		// scheduled checkpoint retries normally. Eviction triggers
		// bypass this counter (their fallback writer emits
		// lenny_checkpoint_eviction_fallback_total instead).
		s.recordStorageFailure()
		return "", 0, status.Errorf(codes.Internal, "archive workspace: %v", res.err)
	}
	if saveErr != nil {
		// spec: §4.4 line 262 — non-eviction MinIO upload failure.
		s.recordStorageFailure()
		return "", 0, status.Errorf(codes.Internal, "store checkpoint: %v", saveErr)
	}
	return id, res.n, nil
}

// recordStorageFailure emits the §4.4 line 262
// `lenny_checkpoint_storage_failure_total` counter. Eviction
// triggers skip this counter — they bump
// `lenny_checkpoint_eviction_fallback_total` instead through the
// fallback writer — so the emission is gated on the trigger label
// being non-eviction.
// spec: §4.4 line 262.
func (s *Server) recordStorageFailure() {
	if s.CheckpointMetrics == nil {
		return
	}
	trigger := s.CheckpointTriggerLabel
	// Eviction-trigger failures are accounted by the
	// evictionfallback writer's IncCheckpointEvictionFallback so
	// every fallback attempt counts once.
	if trigger == string(checkpoint.TriggerEviction) {
		return
	}
	s.CheckpointMetrics.IncCheckpointStorageFailure(s.CheckpointPoolLabel,
		s.CheckpointLevelLabel, trigger)
}
