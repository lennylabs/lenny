// SPDX-License-Identifier: MIT

package evictionfallback

import (
	"context"

	"github.com/lennylabs/lenny/pkg/blobstore/artifactcatalog"
)

// CatalogBridge adapts a *artifactcatalog.PgStore to the
// ArtifactCatalog interface so the gateway can wire the production
// §12.5 catalog into the §4.4 line 291 eviction-context accounting
// path. The bridge is a thin translation layer: it builds the catalog
// Record with `artifact_type = eviction_context` and the URI / size
// passed in from the writer.
//
// spec: §4.4 line 291 — "MinIO eviction-context-object write paired
// in the same Postgres transaction with `artifact_store` row insert
// (artifact_type = eviction_context)".
type CatalogBridge struct {
	// Catalog is the §12.5 artifact_store catalog. A nil Catalog
	// makes RecordEvictionContext a no-op so the gateway runs without
	// the Postgres-backed accounting in dev mode.
	Catalog artifactcatalog.Store
}

// RecordEvictionContext inserts an `artifact_store` row for the
// uploaded eviction-context object with `artifact_type =
// eviction_context`. The URI doubles as the part_id (the eviction
// context is a single object, not a multi-part artifact); the
// session_id and tenant_id flow verbatim from the writer.
func (b *CatalogBridge) RecordEvictionContext(ctx context.Context, tenantID, sessionID, uri string, sizeBytes int64) error {
	if b == nil || b.Catalog == nil {
		return nil
	}
	return b.Catalog.Insert(ctx, artifactcatalog.Record{
		URI:          uri,
		TenantID:     tenantID,
		SessionID:    sessionID,
		PartID:       "context",
		SizeBytes:    sizeBytes,
		State:        artifactcatalog.StateLive,
		ArtifactType: artifactcatalog.ArtifactTypeEvictionContext,
	})
}

// QuotaBridge adapts a storagequota.Counter to the StorageQuotaSink
// interface. The bridge maps the writer's Adjust call onto the
// per-tenant counter's Adjust method, which is the §11.2 / §4.4 line
// 291 surface the gateway uses for post-upload byte accounting.
//
// spec: §4.4 line 291 — "After both rows are durably committed, the
// gateway follows the standard post-upload increment and increments
// the tenant's Redis `storage_bytes_used` counter by the confirmed
// object size."
type QuotaBridge struct {
	// Counter is the §11.2 per-tenant byte counter. A nil Counter
	// makes Adjust a no-op so the gateway runs without the
	// Redis-backed accounting in dev mode.
	Counter quotaAdjuster
}

// quotaAdjuster is the narrow Adjust subset of storagequota.Counter
// the bridge needs. The bridge depends on this internal interface
// rather than on the full Counter so unit tests can stub it without a
// Redis client.
type quotaAdjuster interface {
	Adjust(ctx context.Context, tenantID string, delta int64) error
}

// Adjust shifts the tenant's storage-bytes counter by delta. A nil
// bridge or a nil counter makes the call a no-op so dev-mode
// deployments without a Redis-backed quota counter still produce a
// row.
func (b *QuotaBridge) Adjust(ctx context.Context, tenantID string, delta int64) error {
	if b == nil || b.Counter == nil {
		return nil
	}
	return b.Counter.Adjust(ctx, tenantID, delta)
}
