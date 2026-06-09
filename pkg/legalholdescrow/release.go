// SPDX-License-Identifier: MIT

package legalholdescrow

import (
	"context"
	"fmt"
	"time"
)

// EscrowDeleter removes a released escrow object from the region-scoped
// legal-hold escrow bucket. Until the hold is cleared the object is
// immutable under the retain-until-hold-release object lock; clearing the
// hold lifts the lock and makes the object eligible for deletion.
//
// spec: §12.8 line 884.
type EscrowDeleter interface {
	// Delete removes tenantID's escrow object at key in region. A delete of
	// an already-absent object is not an error (idempotent re-clear). The
	// tenant id is the tombstone-target tenant the object was escrowed
	// under; the deleter rebuilds the escrow bucket key from it.
	Delete(ctx context.Context, tenantID, region, key string) error
}

// Released is the §16.7 line 694 legal_hold.escrow_released event written
// once per escrow object the GC deletes after its hold is cleared.
type Released struct {
	TenantID        string
	ResourceType    string
	ResourceID      string
	EscrowObjectKey string
	EscrowRegion    string
	ClearedAt       time.Time
	ClearedBy       string
}

// ReleaseLedger records the legal_hold.escrow_released audit event on the
// platform tenant (so it survives the tenant tombstone, CMP-058 routed via
// the target tenant id).
type ReleaseLedger interface {
	EscrowReleased(ctx context.Context, ev Released) error
}

// Releaser implements the §12.8 line 884 escrow-GC release flow: when a
// legal hold is cleared via POST /v1/admin/legal-hold (hold: false) — which
// the API accepts on a tombstoned tenant for this purpose — it deletes the
// escrow objects the cleared hold protected and emits a
// legal_hold.escrow_released audit event per object.
type Releaser struct {
	// Records resolves and marks the escrow records.
	Records RecordStore
	// Deleter removes the escrow object from the bucket.
	Deleter EscrowDeleter
	// Ledger emits the legal_hold.escrow_released event.
	Ledger ReleaseLedger
	// Clock supplies the cleared_at instant. Nil defaults to time.Now UTC.
	Clock func() time.Time
}

func (r *Releaser) now() time.Time {
	if r.Clock != nil {
		return r.Clock()
	}
	return time.Now().UTC()
}

// ReleaseForSession releases every escrow object owned by a held session
// when that session's hold is cleared. Returns the number of objects
// released.
func (r *Releaser) ReleaseForSession(ctx context.Context, tenantID, sessionID, clearedBy string) (int, error) {
	recs, err := r.Records.ActiveForSession(ctx, tenantID, sessionID)
	if err != nil {
		return 0, fmt.Errorf("legalholdescrow: list escrow for session %q: %w", sessionID, err)
	}
	return r.releaseAll(ctx, recs, clearedBy)
}

// ReleaseForArtifact releases the escrow object for an artifact when its
// own hold is cleared. Returns the number of objects released.
func (r *Releaser) ReleaseForArtifact(ctx context.Context, tenantID, artifactURI, clearedBy string) (int, error) {
	recs, err := r.Records.ActiveForArtifact(ctx, tenantID, artifactURI)
	if err != nil {
		return 0, fmt.Errorf("legalholdescrow: list escrow for artifact %q: %w", artifactURI, err)
	}
	return r.releaseAll(ctx, recs, clearedBy)
}

// releaseAll deletes each escrow object, marks the record released, and
// emits the audit event. It is fail-fast: a delete or emit error aborts
// with the objects released so far already marked, so a retry resumes
// without re-deleting. The object delete precedes the record mark and the
// audit emit, so the event is only written for an object that is gone.
func (r *Releaser) releaseAll(ctx context.Context, recs []Record, clearedBy string) (int, error) {
	released := 0
	at := r.now()
	for _, rec := range recs {
		if err := r.Deleter.Delete(ctx, rec.TenantID, rec.EscrowRegion, rec.EscrowObjectKey); err != nil {
			return released, fmt.Errorf("legalholdescrow: delete escrow object %q: %w", rec.EscrowObjectKey, err)
		}
		if err := r.Records.MarkReleased(ctx, rec.TenantID, rec.EscrowObjectKey, clearedBy, at); err != nil {
			return released, fmt.Errorf("legalholdescrow: mark escrow released %q: %w", rec.EscrowObjectKey, err)
		}
		if err := r.Ledger.EscrowReleased(ctx, Released{
			TenantID:        rec.TenantID,
			ResourceType:    rec.ResourceType,
			ResourceID:      rec.ResourceID,
			EscrowObjectKey: rec.EscrowObjectKey,
			EscrowRegion:    rec.EscrowRegion,
			ClearedAt:       at,
			ClearedBy:       clearedBy,
		}); err != nil {
			return released, fmt.Errorf("legalholdescrow: emit escrow_released %q: %w", rec.EscrowObjectKey, err)
		}
		released++
	}
	return released, nil
}
