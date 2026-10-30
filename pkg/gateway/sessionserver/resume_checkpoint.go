// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"log"

	"github.com/lennylabs/lenny/pkg/checkpoint"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/resumechunks"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// resolveResumeChunks resolves the §10.1.7 reassembly chunk set for
// the checkpoint the session is being restored from. It returns one
// presigned GET capability per chunk in ascending index order, which the
// gateway hands the adapter on ResumeRequest.Chunks. When reassembly of the
// selected manifest fails its contiguity check (a gap or an out-of-order
// index below chunk_count), it falls back to the last successful full
// checkpoint. A nil resolver (dev mode, or the gateway wired without a
// chunk store), an unresolvable manifest (a checkpoint that predates the
// chunked-object model), or a transient store outage leaves the chunk set
// empty so the resume still proceeds; the adapter then restores nothing
// beyond what the size pre-check permits.
//
// spec: §10.1.7 — reassembly on resume; fallback to the last full
// checkpoint on a contiguity failure.
func (s *Server) resolveResumeChunks(ctx context.Context, row sessionstore.Session) []adapterclient.ChunkGrant {
	if s.resumeChunkResolver == nil {
		return nil
	}
	// spec: §10.1.7 — reassembly is manifest-driven, so it is not gated
	// on the WorkspaceSnapshot.Ref being present. The ref is a validation input
	// that names the session's last completed checkpoint (written only on a
	// partial = false success), and the selector takes the active manifest row
	// at MAX(coordination_generation) regardless of partial. A partial = true
	// drain whose session never completed a full checkpoint has an empty ref,
	// yet LatestActiveAny still selects it and its NULL-baseline threshold is 0
	// (§10.1.7), so it must reassemble. Gating on a non-empty ref would
	// short-circuit that partial-only session to zero chunks even though
	// classifyResume reports partial_workspace for it.
	var ref string
	if row.WorkspaceSnapshot != nil {
		ref = row.WorkspaceSnapshot.Ref
	}
	checkpointID := s.selectResumeCheckpoint(ctx, row.TenantID, row.ID, ref)
	if checkpointID == "" {
		// No active manifest row and no ref: the manifest reader is not wired
		// (dev mode) or the checkpoint predates the chunked model. Restore
		// nothing rather than resolve an empty key.
		return nil
	}
	res, err := s.resumeChunkResolver.Resolve(ctx, row.TenantID, row.ID, checkpointID)
	if err == nil {
		s.emitCheckpointRecovered(row, res)
		return res.Grants
	}
	if errors.Is(err, resumechunks.ErrReassemblyContiguity) || errors.Is(err, resumechunks.ErrBelowRecoveryThreshold) {
		// spec: §10.1.7 — reassembly failed atomically before any
		// chunk body was fetched (a contiguity failure) or the generation-
		// selected partial checkpoint did not clear the recovery threshold;
		// fall back to the last successful full checkpoint so the resume
		// restores intact bytes rather than a corrupted or not-worth-it
		// splice.
		return s.resolveFullCheckpointFallback(ctx, row, checkpointID)
	}
	// A missing manifest row or a transient store outage: the resume itself
	// succeeded, so a lookup failure must not block the session from coming
	// back online. Restore nothing rather than fail the resume.
	log.Printf("sessionserver: resume %s could not resolve checkpoint %s chunks: %v", row.ID, checkpointID, err)
	return nil
}

// selectResumeCheckpoint resolves the checkpoint_id the resume reassembles
// from. It selects the active manifest row for the session at
// MAX(coordination_generation) regardless of partial (§10.1.7), so a
// newer partial = true drain row at or above the completed checkpoint's
// generation is preferred over the older completed checkpoint. The
// WorkspaceSnapshot.Ref, written only on a successful checkpoint, is the
// fallback identifier when no active row is selectable: the manifest reader
// is not wired (dev mode), a store lookup fails, or the completed checkpoint
// was rotated out of the active set. Resolving by ref alone must never be the
// selection mechanism, or the partialRecoveryThresholdFraction gate never
// engages. The ref is normalized through checkpointIDFromSnapshotRef so the
// four-segment object-path form (/{tenant}/checkpoints/{session}/{checkpoint_id})
// resolves to the checkpoint_id the manifest row is keyed by, matching the
// workspace-download and derive paths.
//
// spec: §10.1.7 — MAX(coordination_generation) select regardless of
// partial; the ref is a validation input in the four-segment form.
func (s *Server) selectResumeCheckpoint(ctx context.Context, tenantID, sessionID, ref string) string {
	if s.checkpointManifests == nil {
		return checkpointIDFromSnapshotRef(ref)
	}
	active, err := s.checkpointManifests.LatestActiveAny(ctx, tenantID, sessionID)
	if err != nil || active.CheckpointID == "" {
		return checkpointIDFromSnapshotRef(ref)
	}
	return active.CheckpointID
}

// resolveFullCheckpointFallback resolves the last successful full checkpoint's
// chunk set for a resume whose generation-selected manifest failed its
// contiguity or recovery-threshold check. It returns nil when the manifest
// reader is not wired, no surviving full checkpoint exists, the full
// checkpoint is the row that already failed (selected), or the fallback does
// not resolve.
//
// spec: §10.1.7 — fallback to the last successful full checkpoint.
func (s *Server) resolveFullCheckpointFallback(ctx context.Context, row sessionstore.Session, selected string) []adapterclient.ChunkGrant {
	if s.checkpointManifests == nil {
		return nil
	}
	full, ferr := s.checkpointManifests.LatestFull(ctx, row.TenantID, row.ID)
	if ferr != nil || full.CheckpointID == "" || full.CheckpointID == selected {
		return nil
	}
	// spec: §16.1 — the full-checkpoint fallback restores a complete
	// (partial = false) checkpoint, which is not a partial recovery, so it
	// emits nothing on the recovered arm. The resolver reports Recovered =
	// false for a full checkpoint regardless, but the fallback deliberately
	// does not consult it.
	fallback, ferr := s.resumeChunkResolver.Resolve(ctx, row.TenantID, row.ID, full.CheckpointID)
	if ferr != nil {
		log.Printf("sessionserver: resume %s fell back to full checkpoint %s but it did not resolve: %v",
			row.ID, full.CheckpointID, ferr)
		return nil
	}
	return fallback.Grants
}

// emitCheckpointRecovered stamps the §16.1
// lenny_checkpoint_partial_total{recovered=true} counter once when a resume
// reassembled an above-threshold partial checkpoint. A complete
// (partial = false) restore, a below-threshold or non-contiguous partial
// (which never reaches a successful ResolveResult), and a resume that
// resolved no chunks all leave res.Recovered false and emit nothing.
//
// The recovered=true / (recovered=true + recovered=false) ratio is the
// counter-level partial-recovery signal §16.1 keeps; it is correct
// in aggregate regardless of which trigger label the recovered=true arm
// carries. The trigger label is a required member of the closed
// checkpoint.Trigger enum, and the §10.1 checkpoint_manifest column set does
// not persist the trigger the aborting attempt was started with, so a resume
// running on a replacement pod long after the originating attempt is gone
// cannot read that origin back. finaliseAbort retains every
// manifest_reason = "timeout" row for the resume path regardless of trigger
// (a periodic or pre_scale_down deadline fire is retained the same as an
// eviction drain), so a reassembled partial can originate from any trigger.
// eviction is stamped as the enum-valid representative because the preStop
// drain is the recovery mechanism this counter primarily serves (§10.1); the per-trigger breakdown of the recovered=true arm therefore
// attributes every recovery to eviction and is join-exact against the
// recovered=false write-path arm only for eviction-originated partials. The
// aggregate recovery signal is unaffected.
//
// spec: §16.1; §10.1; §10.1.
func (s *Server) emitCheckpointRecovered(row sessionstore.Session, res resumechunks.ResolveResult) {
	if s.checkpointRecoveryMetrics == nil || !res.Recovered {
		return
	}
	s.checkpointRecoveryMetrics.IncCheckpointPartial(row.PoolRef, true, res.ManifestReason, checkpoint.TriggerEviction)
}
