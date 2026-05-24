// SPDX-License-Identifier: MIT

package checkpointer

import (
	"context"
	"errors"
	"fmt"

	"github.com/lennylabs/lenny/pkg/gateway/partialmanifeststore"
)

// PartialChunkDeleter is the §4.4 line 236 MinIO surface the cleanup
// path uses to remove the chunk objects stored under a partial
// manifest's `partial_object_key_prefix`. The implementation walks
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
// row's `partial_object_key_prefix` via per-key `DeleteObject`
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
// partial_object_key_prefix via per-key DeleteObject calls, then
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
	if record.TenantID == "" || record.SessionID == "" {
		return errors.New("checkpointer: tenant and session ids are required")
	}
	if record.PartialObjectKeyPrefix == "" {
		return errors.New("checkpointer: partial_object_key_prefix is required")
	}
	if deleter != nil {
		if _, err := deleter.DeleteByPrefix(ctx, record.PartialObjectKeyPrefix); err != nil {
			emitOutcome(metrics, PartialCleanupFailedDeleted)
			return fmt.Errorf("checkpointer: delete chunks under %s: %w",
				record.PartialObjectKeyPrefix, err)
		}
	}
	if err := store.SoftDelete(ctx, record.TenantID, record.SessionID, record.Generation); err != nil {
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
