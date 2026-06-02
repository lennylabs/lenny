// SPDX-License-Identifier: MIT

// Package cataloging decorates a blobstore.Store with the §12.5
// artifact_store catalog write that mirrors every bucket object as a
// Postgres row. The catalog row is the §12.5 ll. 309 / 311-321 / 331-339
// surface the GC sweep, the soft-delete state machine, the hard-prune
// pass, and the §11.2 per-tenant size accounting all read against.
//
// The decorator interposes between the gateway-side caller and the
// underlying MinIO / S3 / in-memory backend. A successful Put writes
// the object to the bucket and then inserts the matching catalog row
// before returning to the caller; a Put that fails before the bucket
// write also fails before the catalog insert, so the catalog never
// drifts ahead of object storage. A catalog insert that fails AFTER a
// successful bucket write surfaces as a wrapped error to the caller —
// the spec's "single writer per resource" §12.5 invariant prefers
// caller-observable rollback over a silently orphaned object. The
// decorator's Stat / Get / Copy methods pass through to the underlying
// store unchanged; the catalog is read-side only through
// artifactcatalog.Store directly.
//
// spec: §4.5 ll. 309-321; §12.5 ll. 309, 311-321, 331-339, 341.
package cataloging

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/blobstore/artifactcatalog"
)

// QuotaReleaser releases freed storage bytes back to the §11 line 37
// per-tenant storage-quota counter when an artifact is soft-deleted.
// The decorator decrements the counter by the deleted object's size
// AFTER the catalog row's soft-delete (the spec's `deleted_at`) has
// committed, which is the spec's ordering requirement against the
// double-decrement hazard. gateway/storagequota.Counter satisfies it
// structurally; the seam keeps the blobstore layer free of a gateway
// import.
//
// spec: §11 line 37 — GC-triggered decrement, ordering requirement.
type QuotaReleaser interface {
	Adjust(ctx context.Context, tenantID string, delta int64) error
}

// Store wraps an underlying blobstore.Store and writes a catalog row
// after every successful Put. Construct via New.
//
// Store satisfies the blobstore.Store interface and forwards
// blobstore.Copier and blobstore.Tombstoner when the inner store
// implements them. The tombstone path also moves the catalog row to
// `soft_deleted` so the §12.5 GC's hard-prune pass observes the same
// state the bucket reflects.
type Store struct {
	inner     blobstore.Store
	catalog   artifactcatalog.Store
	now       func() time.Time
	logOnFail func(uri string, err error)
	quota     QuotaReleaser
}

// Options configures a decorator.
type Options struct {
	// Now returns the current UTC time used by the catalog insert's
	// CreatedAt and by SoftDelete's tombstone deadline. A nil value
	// defaults to time.Now().UTC.
	Now func() time.Time
	// LogOnCatalogFailure is invoked when a catalog insert fails after
	// a successful bucket write. The decorator still returns the
	// underlying error to the caller; the hook is an observability seam
	// so a deployment can record the orphan condition without coupling
	// the decorator to a metrics registry.
	LogOnCatalogFailure func(uri string, err error)
	// QuotaReleaser, when set, receives the §11 line 37 storage-quota
	// decrement when an artifact is soft-deleted: the decorator releases
	// the deleted object's bytes back to the per-tenant counter after the
	// catalog soft-delete commits. Nil leaves the counter untouched
	// (dev-mode and tests without a quota counter).
	QuotaReleaser QuotaReleaser
}

// New returns a Store that wraps inner. Both inner and catalog must be
// non-nil; either zero value is a programming error and panics.
func New(inner blobstore.Store, catalog artifactcatalog.Store, opts Options) *Store {
	if inner == nil {
		panic("blobstore/cataloging: inner store is nil")
	}
	if catalog == nil {
		panic("blobstore/cataloging: catalog is nil")
	}
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Store{
		inner:     inner,
		catalog:   catalog,
		now:       now,
		logOnFail: opts.LogOnCatalogFailure,
		quota:     opts.QuotaReleaser,
	}
}

// SetQuotaReleaser wires the §11 line 37 storage-quota counter after
// construction. The gateway builds the decorator before the Redis-backed
// storagequota.Counter is resolved, so it installs the releaser through
// this setter once the counter is ready. Passing nil disables the
// decrement.
//
// spec: §11 line 37.
func (s *Store) SetQuotaReleaser(q QuotaReleaser) { s.quota = q }

// releaseBytes issues the §11 line 37 storage-quota decrement for n
// bytes freed by a soft-delete. It is called only after the catalog
// soft-delete has committed (the spec's `deleted_at` ordering
// requirement) and is a no-op when no releaser is wired or no bytes
// were freed. A decrement failure is reported through logOnFail rather
// than failing the delete: the bytes are durably released in Postgres
// and the next §11 line 37 rehydration reconciles the counter.
//
// spec: §11 line 37 — Redis decrement only after the Postgres row is
// committed with deleted_at set.
func (s *Store) releaseBytes(ctx context.Context, tenantID string, n int64) {
	if s.quota == nil || n <= 0 || tenantID == "" {
		return
	}
	if err := s.quota.Adjust(ctx, tenantID, -n); err != nil && s.logOnFail != nil {
		s.logOnFail(tenantID, fmt.Errorf("storage-quota decrement of %d bytes: %w", n, err))
	}
}

// Put delegates to the inner store and, on success, inserts the §12.5
// catalog row. A catalog insert failure is wrapped and returned so a
// caller observes the inconsistency; production wires
// Options.LogOnCatalogFailure to also record it.
//
// spec: §12.5 ll. 309 — every artifact_store row is inserted alongside
// the bucket object.
func (s *Store) Put(u blobstore.URI, mimeType string, data io.Reader) (string, error) {
	// Buffer the reader so we know the final size for the catalog row
	// AFTER the bucket write reports success. This keeps the size
	// accounting accurate even when the caller streams an unknown-size
	// reader.
	body, err := io.ReadAll(data)
	if err != nil {
		return "", err
	}
	size := int64(len(body))
	ref, err := s.inner.Put(u, mimeType, byteReader(body))
	if err != nil {
		return "", err
	}
	rec := artifactcatalog.Record{
		URI:          ref,
		TenantID:     u.TenantID,
		SessionID:    u.SessionID,
		PartID:       u.PartID,
		MimeType:     mimeType,
		SizeBytes:    size,
		State:        artifactcatalog.StateLive,
		ArtifactType: ArtifactTypeFor(u.ObjectType),
		CreatedAt:    s.now(),
	}
	if err := s.catalog.Insert(context.Background(), rec); err != nil {
		if s.logOnFail != nil {
			s.logOnFail(ref, err)
		}
		return ref, fmt.Errorf("cataloging: insert catalog row for %s: %w", ref, err)
	}
	return ref, nil
}

// Get forwards to the inner store.
func (s *Store) Get(u blobstore.URI) (blobstore.BlobInfo, io.ReadCloser, error) {
	return s.inner.Get(u)
}

// Stat forwards to the inner store.
func (s *Store) Stat(u blobstore.URI) (blobstore.BlobInfo, error) {
	return s.inner.Stat(u)
}

// Copy forwards to the inner store when it implements Copier; on
// success, inserts a catalog row for the destination. Returns
// blobstore.ErrCrossTenant if src and dst differ, mirroring the
// underlying Copier contract.
//
// spec: §4.5 ll. 311 — derive copy of the parent workspace snapshot
// produces a new artifact_store row scoped to the derived session.
func (s *Store) Copy(src, dst blobstore.URI) error {
	cop, ok := s.inner.(blobstore.Copier)
	if !ok {
		return errors.New("cataloging: inner store does not implement Copier")
	}
	if err := cop.Copy(src, dst); err != nil {
		return err
	}
	// Best-effort size resolution: a Copy doesn't carry the destination
	// bytes through this layer, so we Stat the destination to capture
	// the size for the catalog row. A Stat failure does not roll the
	// copy back — the artifact exists; the catalog row is best-effort
	// (the GC reconciler reconstructs missing rows on the next sweep).
	info, statErr := s.inner.Stat(dst)
	rec := artifactcatalog.Record{
		URI:          dst.String(),
		TenantID:     dst.TenantID,
		SessionID:    dst.SessionID,
		PartID:       dst.PartID,
		State:        artifactcatalog.StateLive,
		ArtifactType: ArtifactTypeFor(dst.ObjectType),
		CreatedAt:    s.now(),
	}
	if statErr == nil {
		rec.MimeType = info.MimeType
		rec.SizeBytes = info.Size
	}
	if err := s.catalog.Insert(context.Background(), rec); err != nil {
		if s.logOnFail != nil {
			s.logOnFail(dst.String(), err)
		}
		return fmt.Errorf("cataloging: insert catalog row for copy %s: %w", dst.String(), err)
	}
	return nil
}

// SoftDelete forwards to the inner store (when it implements
// Tombstoner) and transitions the catalog row to `soft_deleted` with
// the §12.5 tombstone-retention deadline. The catalog SoftDelete must
// run after the bucket-side tag is in place so a crash between the
// two leaves a tombstoned bucket object with a live catalog row —
// the next GC sweep observes the mismatch and re-runs the catalog
// transition. SoftDelete is idempotent: a row already past `live`
// returns nil so a replay does not error.
//
// spec: §12.5 ll. 311-313, 331-339.
func (s *Store) SoftDelete(u blobstore.URI, retention time.Duration) error {
	ctx := context.Background()
	if tomb, ok := s.inner.(blobstore.Tombstoner); ok {
		if err := tomb.SoftDelete(u); err != nil {
			return err
		}
	}
	// Capture the freed byte size before the transition: the §11 line 37
	// decrement uses the artifact_store-recorded size as the source of
	// truth. A missing row leaves freed at zero so no decrement fires.
	var freed int64
	if rec, gerr := s.catalog.Get(ctx, u.String()); gerr == nil && rec.State == artifactcatalog.StateLive {
		freed = rec.SizeBytes
	}
	deadline := s.now().Add(retention)
	if err := s.catalog.SoftDelete(ctx, u.String(), deadline); err != nil {
		if errors.Is(err, artifactcatalog.ErrNotFound) {
			// The row may legitimately be absent — a SoftDelete on a
			// session whose blobs predate the catalog wiring, or a
			// SoftDelete that races a GC pass. Either way the bucket
			// state is now consistent; treat as idempotent. No bytes are
			// released because no live→soft_deleted transition occurred.
			return nil
		}
		return fmt.Errorf("cataloging: soft-delete catalog row for %s: %w", u.String(), err)
	}
	// The catalog row is committed with `deleted_at` set; only now is the
	// §11 line 37 Redis decrement safe (the spec's ordering requirement).
	s.releaseBytes(ctx, u.TenantID, freed)
	return nil
}

// DeleteBySession forwards the §12.8 erasure sweep to the inner store
// when it implements the per-session deleter contract, then drops the
// matching catalog rows so the §12.8 ledger no longer references the
// erased session. A nil count return (0, nil) means the session had
// no artifacts.
//
// spec: §12.8 — erasure orchestrator's per-store adapter.
func (s *Store) DeleteBySession(ctx context.Context, tenantID, sessionID string) (int, error) {
	type sessionDeleter interface {
		DeleteBySession(ctx context.Context, tenantID, sessionID string) (int, error)
	}
	inner, ok := s.inner.(sessionDeleter)
	if !ok {
		return 0, nil
	}
	deleted, err := inner.DeleteBySession(ctx, tenantID, sessionID)
	if err != nil {
		return deleted, err
	}
	// Drop the matching catalog rows so the §12.8 orchestrator sees a
	// consistent picture. Failures here are wrapped so the orchestrator
	// can record them; the bucket-side delete has already succeeded so
	// the next GC cycle reconciles the rows.
	rows, lerr := s.catalog.ListBySession(ctx, tenantID, sessionID)
	if lerr != nil {
		return deleted, fmt.Errorf("cataloging: list session for catalog reconciliation: %w", lerr)
	}
	var freed int64
	for _, r := range rows {
		// SoftDelete then Tombstone is the strict transition path; for
		// erasure we collapse both into a single Tombstone so the
		// HardPruneExpired pass removes the row on its next cycle. A
		// row already past `live` falls back to Tombstone directly.
		if r.State == artifactcatalog.StateLive {
			if serr := s.catalog.SoftDelete(ctx, r.URI, s.now()); serr == nil {
				// Only a live→soft_deleted transition this call performed
				// releases bytes; a row already past live had its bytes
				// released by the prior delete.
				freed += r.SizeBytes
			}
		}
		_ = s.catalog.Tombstone(ctx, r.URI)
	}
	// Issue the §11 line 37 decrement after the catalog soft-deletes are
	// committed (the spec's `deleted_at`-first ordering).
	s.releaseBytes(ctx, tenantID, freed)
	return deleted, nil
}

// DeleteByUser implements the §12.1 Eraser primitive. Artifact erasure
// is session-scoped (§12.8 step 7), so this whole-user call is a no-op
// returning (0, nil).
func (s *Store) DeleteByUser(_ context.Context, _, _ string) (int, error) {
	return 0, nil
}

var _ blobstore.Eraser = (*Store)(nil)

// DeleteByTenant forwards the §12.8 Phase 4 prefix-scoped bulk delete
// to the inner store, then drops the tenant's catalog rows so the
// §12.5 ledger no longer references the erased tenant. The bucket
// delete runs first: when the inner store aborts on a §12.8 legal
// hold the error propagates before any catalog row is removed, so a
// held tenant survives intact. An inner store without the
// TenantPrefixDeleter contract is a no-op returning (0, nil).
//
// spec: §12.5 ll. 295; §12.8 Phase 4.
func (s *Store) DeleteByTenant(ctx context.Context, tenantID string) (int, error) {
	inner, ok := s.inner.(blobstore.TenantPrefixDeleter)
	if !ok {
		return 0, nil
	}
	deleted, err := inner.DeleteByTenant(ctx, tenantID)
	if err != nil {
		return deleted, err
	}
	if _, cerr := s.catalog.DeleteByTenant(ctx, tenantID); cerr != nil {
		return deleted, fmt.Errorf("cataloging: delete tenant catalog rows for %s: %w", tenantID, cerr)
	}
	return deleted, nil
}

// HardPrune drives the §12.5 line 320/341 hard-prune sweep, catalog-
// first: it queries Postgres for the artifacts past their tombstone
// deadline (ListPrunable), deletes exactly those bucket objects via
// the inner store's targeted HardDeleteObject, then drops the catalog
// rows whose object delete succeeded (HardPruneURIs). Returns the
// count of rows pruned end-to-end.
//
// This replaces the prior per-cycle bucket-wide ListObjects + per-
// object GetObjectTagging scan: the cost now scales with the Postgres-
// supplied set rather than with the total object count in the bucket,
// which dominated GC duration at large fleet sizes (F-12.5.23). The
// object is deleted before its row so a crash or per-object delete
// failure between the two leaves the row for the next cycle rather
// than orphaning a bucket object.
//
// HardPrune is a background sweep — per-object failures are skipped
// (the next cycle picks them up); a catalog query failure short-
// circuits and returns the error.
//
// spec: §12.5 line 320 (catalog-driven), line 341 (delete object then
// row).
func (s *Store) HardPrune(ctx context.Context, now time.Time) (int, error) {
	uris, err := s.catalog.ListPrunable(ctx, now)
	if err != nil {
		return 0, fmt.Errorf("cataloging: list prunable: %w", err)
	}
	if len(uris) == 0 {
		return 0, nil
	}
	tomb, hasTomb := s.inner.(blobstore.Tombstoner)
	deleted := make([]string, 0, len(uris))
	for _, raw := range uris {
		if hasTomb {
			u, perr := blobstore.ParseURI(raw)
			if perr != nil {
				// A malformed uri cannot be mapped to a bucket object;
				// leave the row so an operator can inspect it.
				continue
			}
			if derr := tomb.HardDeleteObject(u); derr != nil {
				// Best-effort: leave the row for the next cycle.
				continue
			}
		}
		deleted = append(deleted, raw)
	}
	count, err := s.catalog.HardPruneURIs(ctx, deleted)
	if err != nil {
		return count, fmt.Errorf("cataloging: hard-prune catalog rows: %w", err)
	}
	return count, nil
}

// ListBySession exposes the underlying catalog query so callers can
// enumerate a session's artifacts without bypassing the decorator.
func (s *Store) ListBySession(ctx context.Context, tenantID, sessionID string) ([]artifactcatalog.Record, error) {
	return s.catalog.ListBySession(ctx, tenantID, sessionID)
}

// SoftDeleteSession soft-deletes every live catalog row for the given
// tenant + session, mirroring the §7.1 retention GC's per-session
// granularity. Each catalog row receives the tombstone deadline at
// `now + retention`; the bucket-side SoftDelete fires per row when the
// inner store implements Tombstoner. Returns the count of catalog rows
// transitioned. A row already past `live` is silently skipped (the
// underlying catalog returns ErrNotFound for the transition, which is
// the idempotent contract).
//
// spec: §12.5 ll. 311-313 — soft-delete on the §7.1 retention boundary.
func (s *Store) SoftDeleteSession(ctx context.Context, tenantID, sessionID string, retention time.Duration) (int, error) {
	rows, err := s.catalog.ListBySession(ctx, tenantID, sessionID)
	if err != nil {
		return 0, fmt.Errorf("cataloging: list session %s/%s: %w", tenantID, sessionID, err)
	}
	count := 0
	var freed int64
	deadline := s.now().Add(retention)
	tomb, _ := s.inner.(blobstore.Tombstoner)
	for _, r := range rows {
		if r.State != artifactcatalog.StateLive {
			continue
		}
		if tomb != nil {
			u, perr := blobstore.ParseURI(r.URI)
			if perr == nil {
				if derr := tomb.SoftDelete(u); derr != nil {
					if s.logOnFail != nil {
						s.logOnFail(r.URI, derr)
					}
					continue
				}
			}
		}
		if derr := s.catalog.SoftDelete(ctx, r.URI, deadline); derr != nil {
			if errors.Is(derr, artifactcatalog.ErrNotFound) {
				continue
			}
			if s.logOnFail != nil {
				s.logOnFail(r.URI, derr)
			}
			continue
		}
		freed += r.SizeBytes
		count++
	}
	// The §11 line 37 decrement runs once the catalog rows are committed
	// with `deleted_at` set (the spec's ordering requirement): the GC
	// sweep that soft-deletes a terminal session's artifacts releases
	// their bytes back to the per-tenant storage-quota counter so a
	// tenant near its cap can upload again.
	s.releaseBytes(ctx, tenantID, freed)
	return count, nil
}

// Inner returns the underlying store. Tests use it to assert the
// decorator forwards without surprise.
func (s *Store) Inner() blobstore.Store { return s.inner }

// Catalog returns the catalog handle. Tests use it for read-side
// assertions.
func (s *Store) Catalog() artifactcatalog.Store { return s.catalog }

// ArtifactTypeFor maps a §4.5 blobstore.ObjectType to the §12.5
// artifactcatalog.ArtifactType the catalog row stamps. The mapping is
// total: every supported ObjectType resolves to a defined catalog
// artifact type. An empty or unknown ObjectType falls through to the
// catalog's default (ArtifactTypeWorkspace) because the catalog's own
// Insert applies the same default — keep the two surfaces in sync.
//
// spec: §4.4 / §12.5 artifact-kind tag.
func ArtifactTypeFor(ot blobstore.ObjectType) artifactcatalog.ArtifactType {
	switch ot {
	case blobstore.ObjectTypeWorkspace, blobstore.ObjectTypeUpload, blobstore.ObjectTypeTranscript:
		return artifactcatalog.ArtifactTypeWorkspace
	case blobstore.ObjectTypeCheckpoint:
		return artifactcatalog.ArtifactTypeCheckpoint
	case blobstore.ObjectTypeEviction:
		return artifactcatalog.ArtifactTypeEvictionContext
	case blobstore.ObjectTypeExport:
		return artifactcatalog.ArtifactTypeExport
	case blobstore.ObjectTypeSessionLog:
		return artifactcatalog.ArtifactTypeSessionLog
	default:
		return artifactcatalog.ArtifactTypeWorkspace
	}
}

// byteReader is the io.Reader wrapper used to replay buffered Put
// bytes through the inner store. A fresh reader per Put invocation
// avoids an io.Reader's one-shot nature surprising the decorator
// caller.
func byteReader(b []byte) io.Reader { return &repeatReader{buf: b} }

type repeatReader struct {
	buf []byte
	off int
}

func (r *repeatReader) Read(p []byte) (int, error) {
	if r.off >= len(r.buf) {
		return 0, io.EOF
	}
	n := copy(p, r.buf[r.off:])
	r.off += n
	return n, nil
}
