// SPDX-License-Identifier: MIT

package checkpointer

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
)

// PartialChunkDeleter is the §4.4 line 236 MinIO surface the cleanup
// path uses to remove the chunk objects stored under a partial
// manifest's `chunk_object_key_prefix`. The implementation walks
// the prefix (`ListObjectsV2` semantics) and `DeleteObject`s each
// key; MinIO's delete-on-absent semantics make repeated deletions on
// the same key independently idempotent so a retry never errors on a
// stale chunk.
//
// The signature is interface-narrow so unit tests can stub it without
// pulling in a S3 client. The production wiring lives in
// pkg/blobstore/{s3,miniostore}.
type PartialChunkDeleter interface {
	// DeleteByPrefix removes every object under the supplied prefix.
	// Returns the count physically deleted and the first error
	// encountered; missing keys are not an error.
	DeleteByPrefix(ctx context.Context, prefix string) (count int, err error)
}

// PartialCleanupOutcome enumerates the §4.4 line 236 cleanup
// outcomes; the value is the label of the
// `lenny_partial_manifest_cleanup_total` counter.
type PartialCleanupOutcome string

const (
	// PartialCleanupSuccess marks a successful cleanup: every chunk
	// under the prefix was removed and the manifest row was
	// soft-deleted by the primary resume path.
	PartialCleanupSuccess PartialCleanupOutcome = "success"
	// PartialCleanupFailedDeleted marks a failed cleanup: a MinIO
	// `DeleteObject` call returned a non-retriable error or the
	// retry budget was exhausted. The §12.5 backstop sweep will
	// pick the row up on the next cycle.
	PartialCleanupFailedDeleted PartialCleanupOutcome = "failed_deleted"
	// PartialCleanupGCCollected marks a cleanup the §12.5 backstop
	// performed after the primary resume path was skipped (e.g.,
	// the session never resumed before its resume window expired).
	PartialCleanupGCCollected PartialCleanupOutcome = "gc_collected"
)

// PartialCleanupMetrics is the gateway-metrics surface the cleanup
// path emits the §4.4 line 236 counter on. The gateway's
// gatewaymetrics.Metrics satisfies it.
type PartialCleanupMetrics interface {
	IncPartialManifestCleanup(outcome string)
}

// CleanupPartialManifest performs the §4.4 line 236 cleanup of a
// single partial-manifest row: (1) delete every chunk under the
// row's `chunk_object_key_prefix` via per-key `DeleteObject`
// calls; (2) soft-delete the Postgres row under the
// `deleted_at IS NULL` predicate; (3) emit the
// `lenny_partial_manifest_cleanup_total` counter labeled by outcome.
//
// The cleanup is best-effort: a MinIO failure leaves the row active
// so the §12.5 backstop sweep can retry on the next cycle. The
// caller passes the source of the cleanup (a resume-path call versus
// a GC-backstop call) via the `gcCollected` flag so the metric
// label distinguishes the two cases.
//
// spec: §4.4 line 236 — "Regardless of reassembly outcome, the
// gateway MUST delete every chunk object listed under the manifest's
// chunk_object_key_prefix via per-key DeleteObject calls, then
// soft-delete the Postgres row".
func CleanupPartialManifest(
	ctx context.Context,
	store partialmanifeststore.Store,
	deleter PartialChunkDeleter,
	record partialmanifeststore.Record,
	metrics PartialCleanupMetrics,
	gcCollected bool,
) error {
	if store == nil {
		return errors.New("checkpointer: partial-manifest store is required")
	}
	if record.TenantID == "" || record.CheckpointID == "" {
		return errors.New("checkpointer: tenant id and checkpoint id are required")
	}
	if record.ChunkObjectKeyPrefix == "" {
		return errors.New("checkpointer: chunk_object_key_prefix is required")
	}
	if deleter != nil {
		if _, err := deleter.DeleteByPrefix(ctx, record.ChunkObjectKeyPrefix); err != nil {
			emitOutcome(metrics, PartialCleanupFailedDeleted)
			return fmt.Errorf("checkpointer: delete chunks under %s: %w",
				record.ChunkObjectKeyPrefix, err)
		}
	}
	if err := store.SoftDelete(ctx, record.TenantID, record.CheckpointID); err != nil {
		emitOutcome(metrics, PartialCleanupFailedDeleted)
		return fmt.Errorf("checkpointer: soft-delete partial manifest row: %w", err)
	}
	if gcCollected {
		emitOutcome(metrics, PartialCleanupGCCollected)
	} else {
		emitOutcome(metrics, PartialCleanupSuccess)
	}
	return nil
}

// ChunkCatalogReleaser is the §12.5 cataloging-decorator seam the chunk
// release runs a reclaimed checkpoint's chunks through: it soft-deletes each
// discovered chunk's artifact_store row under the deleted_at IS NULL guard,
// which issues the §12.5 rule 4 Redis decrement exactly once. A chunk with no
// catalog row (an uncounted straggler an outstanding grant landed under the
// prefix) soft-deletes as a no-op and issues no decrement. cataloging.Store
// satisfies it via SoftDeleteRow.
type ChunkCatalogReleaser interface {
	SoftDeleteRow(ctx context.Context, uri string, retention time.Duration) error
}

// ChunkObjectStore discovers a reclaimed checkpoint's chunk objects by walking
// its chunk_object_key_prefix (ListObjectsV2 semantics) and hard-deletes each
// one by key after its artifact_store row is soft-deleted. A missing key is a
// no-op on delete (delete-on-absent, S3-compatible semantics), so a re-run
// over an already-reclaimed checkpoint never errors. miniostore.Store
// satisfies it via ListByPrefix and HardDeleteObject.
type ChunkObjectStore interface {
	ListByPrefix(ctx context.Context, u blobstore.URI) ([]blobstore.BlobInfo, error)
	HardDeleteObject(u blobstore.URI) error
}

// CheckpointChunkRelease bundles the seams the §12.5 three-step chunk
// release runs against.
type CheckpointChunkRelease struct {
	// Manifests soft-deletes the finalised manifest row in the release's
	// final step. Required.
	Manifests partialmanifeststore.Store
	// Catalog soft-deletes each chunk's artifact_store row and issues the
	// §12.5 rule 4 Redis decrement. Required.
	Catalog ChunkCatalogReleaser
	// Objects discovers the checkpoint's chunk objects under its
	// chunk_object_key_prefix and hard-deletes each one per key. Required.
	Objects ChunkObjectStore
	// TombstoneRetention is the §12.5 line 341 gc.tombstoneRetentionSeconds
	// window stamped as each soft-deleted chunk row's hard-prune deadline.
	TombstoneRetention time.Duration
	// OnOrphanedObject, when set, is called once for the chunk object whose
	// HardDeleteObject failed, before the release returns the error. A
	// completed (partial = false) checkpoint has no §12.5 backstop sweep to
	// retry the delete — that sweep's selector carries partial = true (§12.5
	// GC rule 6) and never re-selects a finalised checkpoint — so a failed
	// object delete on the rotation path orphans the object with no reclaimer.
	// The callback lets the caller surface that leak on the
	// lenny_checkpoint_orphaned_objects_total counter rather than leaving it
	// silent. Nil skips the emission.
	// spec: §4.4 line 248 (orphaned-object counter); §12.5 GC rule 6.
	OnOrphanedObject func()
}

// ReleaseCheckpointChunks runs the §12.5 three-step release for one
// finalised checkpoint a reclaimer dropped (the retention latest-2 rotation
// in this step's caller): for every chunk object under the checkpoint it
// (1) soft-deletes the chunk's artifact_store row through the cataloging
// decorator under the deleted_at IS NULL guard, which issues the §12.5
// rule 4 Redis decrement exactly once and is what returns the tenant's
// storage_bytes_used toward zero; (2) hard-deletes the chunk object per key;
// and finally (3) soft-deletes the finalised manifest row so the checkpoint
// drops out of every deleted_at IS NULL reclaimer's selector.
//
// The chunk set is discovered by walking the checkpoint's
// chunk_object_key_prefix resolved from the finalised manifest row
// (ListObjectsV2 semantics), the same discovery the abort, resume, and
// supersede release paths use. Enumerating only the confirmed artifact_store
// rows would miss an object an outstanding grant landed under the prefix
// without a catalog row, then soft-delete the manifest row and drop the
// prefix from every deleted_at IS NULL reclaimer's selector, orphaning that
// object; the prefix walk sees every stored object, and the guarded catalog
// soft-delete releases a confirmed chunk's bytes once and no-ops for an
// uncounted straggler.
//
// The release is idempotent: a re-run observes each catalog row already
// soft-deleted (no second decrement), each object already absent
// (delete-on-absent no-op), and the manifest row already soft-deleted.
//
// On a HardDeleteObject failure the release returns early with the wrapped
// error and leaves the finalised manifest row active (the manifest soft-delete
// is step 3, past the failure), and it invokes cfg.OnOrphanedObject for the
// object it could not delete. A completed (partial = false) checkpoint has no
// §12.5 backstop sweep to retry that delete — the backstop's selector carries
// partial = true (§12.5 GC rule 6) and never re-selects a finalised checkpoint,
// and the rotation catalog row is already dropped — so the object is orphaned
// with no reclaimer. The callback is what makes that leak observable on
// lenny_checkpoint_orphaned_objects_total rather than silent.
//
// spec: §12.5 GC rule 4 (Redis decrement gated on the Postgres soft-delete,
// exactly once), GC rule 5 (keep only the latest 2 checkpoints per active
// session), GC rule 6 (the backstop sweep selects partial = true rows only);
// §4.4 line 236 (soft-delete the row, then per-key DeleteObject); §4.4 line 248
// (orphaned-object counter).
func ReleaseCheckpointChunks(ctx context.Context, cfg CheckpointChunkRelease, record partialmanifeststore.Record) error {
	if cfg.Manifests == nil || cfg.Catalog == nil || cfg.Objects == nil {
		return errors.New("checkpointer: chunk release requires the manifest store, catalog, and object store")
	}
	if record.TenantID == "" || record.SessionID == "" || record.CheckpointID == "" {
		return errors.New("checkpointer: tenant, session, and checkpoint ids are required")
	}
	if record.ChunkObjectKeyPrefix == "" {
		return errors.New("checkpointer: chunk_object_key_prefix is required")
	}
	// Discover the checkpoint's chunk objects by walking its
	// chunk_object_key_prefix. The prefix is the object key of a checkpoint
	// URI whose PartID is `{checkpoint_id}/`, so the walk scopes to exactly
	// this checkpoint's objects and out of the session's other checkpoints.
	prefix := blobstore.URI{
		TenantID:   record.TenantID,
		ObjectType: blobstore.ObjectTypeCheckpoint,
		SessionID:  record.SessionID,
		PartID:     record.CheckpointID + "/",
	}
	objects, err := cfg.Objects.ListByPrefix(ctx, prefix)
	if err != nil {
		return fmt.Errorf("checkpointer: list chunk objects under %s: %w",
			record.ChunkObjectKeyPrefix, err)
	}
	for _, obj := range objects {
		uri := obj.URI.String()
		// (1) Soft-delete the chunk's artifact_store row through the
		// cataloging decorator: the §12.5 rule 4 Redis decrement fires here,
		// exactly once for the live→soft_deleted transition. A straggler with
		// no catalog row soft-deletes as a no-op and issues no decrement.
		if err := cfg.Catalog.SoftDeleteRow(ctx, uri, cfg.TombstoneRetention); err != nil {
			return fmt.Errorf("checkpointer: release chunk row %s: %w", uri, err)
		}
		// (2) Hard-delete the chunk object per key. On failure the object is
		// orphaned: its artifact_store row is already soft-deleted (step 1),
		// so the TTL sweep skips it, and no §12.5 backstop re-selects a
		// completed checkpoint. Surface it on the orphaned-object counter so
		// the leak is observable rather than silently attributed to a backstop
		// that never runs for a partial = false checkpoint.
		if err := cfg.Objects.HardDeleteObject(obj.URI); err != nil {
			if cfg.OnOrphanedObject != nil {
				cfg.OnOrphanedObject()
			}
			return fmt.Errorf("checkpointer: delete chunk object %s: %w", uri, err)
		}
	}
	// (3) Soft-delete the finalised manifest row under its own
	// deleted_at IS NULL guard.
	if err := cfg.Manifests.SoftDelete(ctx, record.TenantID, record.CheckpointID); err != nil {
		return fmt.Errorf("checkpointer: soft-delete manifest row %s/%s: %w",
			record.TenantID, record.CheckpointID, err)
	}
	return nil
}

func emitOutcome(metrics PartialCleanupMetrics, outcome PartialCleanupOutcome) {
	if metrics == nil {
		return
	}
	metrics.IncPartialManifestCleanup(string(outcome))
}

// PartialCleaner is a §4.4 line 236 cleanup adapter the gateway wires
// into the sessionserver's PartialManifestCleaner option. It reads
// the latest active partial manifest for (tenant, session) via the
// Store and invokes CleanupPartialManifest. A session with no active
// partial manifest is a no-op.
//
// PartialCleaner is the production-side wrapper around the
// CleanupPartialManifest function; the function is exported
// independently so the §12.5 backstop sweep can invoke it directly
// without re-reading the manifest.
type PartialCleaner struct {
	// Store is the partial-manifest store. Required.
	Store partialmanifeststore.Store
	// Deleter is the MinIO surface that removes chunk objects.
	// Optional — when nil the cleaner skips MinIO deletion and only
	// soft-deletes the row, matching dev-mode and test deployments.
	Deleter PartialChunkDeleter
	// Metrics, when set, receives the cleanup-outcome counter
	// increments. Nil is permitted; the cleanup still runs.
	Metrics PartialCleanupMetrics
}

// CleanupAfterResume satisfies the sessionserver.PartialManifestCleaner
// surface. It looks up the latest active partial manifest for the
// supplied (tenant, session) and runs the cleanup pipeline. A session
// with no active manifest is a no-op (returns nil).
func (c *PartialCleaner) CleanupAfterResume(ctx context.Context, tenantID, sessionID string) error {
	if c == nil || c.Store == nil {
		return nil
	}
	record, err := c.Store.LatestActive(ctx, tenantID, sessionID)
	if err != nil {
		if errors.Is(err, partialmanifeststore.ErrNotFound) {
			// No active partial manifest — cleanup is a no-op.
			return nil
		}
		return fmt.Errorf("checkpointer: read latest active partial manifest: %w", err)
	}
	return CleanupPartialManifest(ctx, c.Store, c.Deleter, record, c.Metrics, false)
}
