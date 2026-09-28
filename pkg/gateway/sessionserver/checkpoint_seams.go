// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"

	"github.com/lennylabs/lenny/pkg/checkpoint"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/resumechunks"
)

// PartialManifestCleaner executes the §4.4 partial-manifest
// cleanup after the resume path completes, regardless of whether the
// reassembly succeeded or failed. An implementation walks the latest
// active partial manifest for (tenant, session), deletes the chunks
// under its `chunk_object_key_prefix`, and soft-deletes the row.
// Best-effort: a failure leaves the row active for the §12.5
// backstop sweep to clean up on the next cycle.
type PartialManifestCleaner interface {
	// CleanupAfterResume runs the cleanup for the session's latest
	// active partial manifest. A no-op (returns nil) when no active
	// manifest exists.
	CleanupAfterResume(ctx context.Context, tenantID, sessionID string) error
}

// EvictionStateLookup reports whether the (tenant, session) carries
// the §4.4 minimal-state record written during the eviction-fallback
// path. The §7.2 resume path uses it to derive the
// `resumeMode: "conversation_only"` value carried on the
// `session.resumed` event when the workspace was lost during
// eviction.
//
// spec: §4.4 — "the client receives a session.resumed event
// with resumeMode: \"conversation_only\" and workspaceLost: true".
type EvictionStateLookup interface {
	// HasEvictionState returns true when the session has a
	// minimal-state record (workspace was lost during eviction).
	// Returns false when no record exists; an error reading the store
	// returns the error so the resume path can degrade gracefully
	// (the gateway falls back to ResumeFull rather than block on a
	// transient lookup failure).
	HasEvictionState(ctx context.Context, tenantID, sessionID string) (bool, error)
}

// PartialManifestLookup reports whether the (tenant, session) carries
// an active §4.4 / §10.1 partial-checkpoint manifest. The §7.2 resume
// path uses it to derive the `resumeMode: "partial_workspace"` value
// carried on the `session.resumed` event when the resume reassembled
// the workspace from partial chunks rather than from a full
// checkpoint.
//
// spec: §10.1 partial-manifest path — `session.resumed` carries
// `resumeMode: "partial_workspace"` when the manifest selected by
// MAX(coordination_generation) yielded a reassembled workspace.
type PartialManifestLookup interface {
	// HasActivePartialManifest returns true when an active partial
	// manifest exists for (tenant, session). Returns false when none
	// exists; a store error returns the error so the resume path can
	// degrade gracefully (falls back to ResumeFull).
	HasActivePartialManifest(ctx context.Context, tenantID, sessionID string) (bool, error)
}

// ResumeChunkResolver resolves the §10.1.7 reassembly chunk set for
// a checkpoint the resume path restores. It lists the committed chunk
// objects under the manifest row's chunk_object_key_prefix, verifies
// contiguity of the prefix [0, chunk_count), and mints one presigned
// single-key GET capability per index. The gateway is the sole authority
// that resolves, lists, validates, and signs the keys; the pod fetches the
// capabilities and concatenates the bodies. resumechunks.Resolver
// implements it.
//
// spec: §10.1.
type ResumeChunkResolver interface {
	// Resolve returns one presigned GET capability per chunk of the named
	// checkpoint in ascending index order together with the §16.1
	// recovered signal, or resumechunks.ErrReassemblyContiguity when the
	// committed objects do not form a contiguous [0, chunk_count) prefix.
	Resolve(ctx context.Context, tenantID, sessionID, checkpointID string) (resumechunks.ResolveResult, error)
}

// CheckpointRecoveryMetrics is the narrow slice of §16.1 checkpoint
// telemetry the resume path emits when it reassembles an above-threshold
// partial checkpoint. *gatewaymetrics.Metrics satisfies it, so the resume's
// recovered = true emission lands on the same lenny_checkpoint_partial_total
// series the upload driver's recovered = false abort arms write, without a
// sessionserver → concrete-metrics import. trigger is a checkpoint.Trigger,
// so the resume path can only stamp a value inside the closed §4.4 enum.
//
// spec: §16.1.
type CheckpointRecoveryMetrics interface {
	IncCheckpointPartial(pool string, recovered bool, manifestReason string, trigger checkpoint.Trigger)
}

// CheckpointManifestReader reads §10.1 checkpoint_manifest rows for the
// resume and workspace-download paths: Get resolves one checkpoint by id
// (for its chunk_count / chunk_encoding), LatestActiveAny resolves the
// active row at MAX(coordination_generation) regardless of partial (the
// §10.1.7 resume-reassembly selector), and LatestFull resolves the
// last successful full checkpoint the resume path falls back to when
// reassembly of the selected manifest fails its contiguity or recovery-
// threshold check. The Postgres and in-memory checkpoint_manifest stores
// implement it.
type CheckpointManifestReader interface {
	Get(ctx context.Context, tenantID, checkpointID string) (partialmanifeststore.Record, error)
	LatestActiveAny(ctx context.Context, tenantID, sessionID string) (partialmanifeststore.Record, error)
	LatestFull(ctx context.Context, tenantID, sessionID string) (partialmanifeststore.Record, error)
}

// CheckpointManifestWriter writes a §10.1 checkpoint_manifest row for the
// derived session after the §7.1 derive path copies the parent's chunks
// into the derived prefix, so the derived session owns a resumable /
// downloadable checkpoint independent of the parent's GC. The store models
// a manifest as an intent row (Put, always partial) advanced by
// ConfirmChunk and stamped terminal by Finalise, so recording a complete
// derived checkpoint is Put → ConfirmChunk → Finalise(partial=false). The
// Postgres and in-memory checkpoint_manifest stores implement it.
type CheckpointManifestWriter interface {
	Put(ctx context.Context, r partialmanifeststore.Record) error
	ConfirmChunk(ctx context.Context, tenantID, checkpointID string, n int, workspaceBytesUploaded int64) error
	Finalise(ctx context.Context, tenantID, checkpointID string, partial bool, manifestReason string) error
}
