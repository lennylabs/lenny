// SPDX-License-Identifier: MIT

package sessionlogstore

import (
	"context"

	"github.com/lennylabs/lenny/pkg/blobstore/artifactcatalog"
)

// CatalogBridge adapts a *artifactcatalog.PgStore to the
// ArtifactCatalog interface so the gateway can wire the production
// §12.5 catalog into the §4.4 line 226 session-log accounting path.
// The bridge is a thin translation layer: it builds the catalog
// Record with `artifact_type = session_log` and the URI / size
// passed in from the writer.
//
// spec: §4.4 line 226 — session-log artifact_store row.
type CatalogBridge struct {
	// Catalog is the §12.5 artifact_store catalog. A nil Catalog
	// makes RecordSessionLog a no-op so the gateway runs without
	// the Postgres-backed accounting in dev mode.
	Catalog artifactcatalog.Store
}

// RecordSessionLog inserts an `artifact_store` row for the uploaded
// session-log object with `artifact_type = session_log`. The URI
// doubles as the part_id (the session log is a single object, not
// a multi-part artifact); the session_id and tenant_id flow
// verbatim from the writer.
func (b *CatalogBridge) RecordSessionLog(ctx context.Context, tenantID, sessionID, uri string, sizeBytes int64) error {
	if b == nil || b.Catalog == nil {
		return nil
	}
	return b.Catalog.Insert(ctx, artifactcatalog.Record{
		URI:          uri,
		TenantID:     tenantID,
		SessionID:    sessionID,
		PartID:       "stderr",
		SizeBytes:    sizeBytes,
		State:        artifactcatalog.StateLive,
		ArtifactType: artifactcatalog.ArtifactTypeSessionLog,
	})
}
