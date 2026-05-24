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
	if tomb, ok := s.inner.(blobstore.Tombstoner); ok {
		if err := tomb.SoftDelete(u); err != nil {
			return err
		}
	}
	deadline := s.now().Add(retention)
	if err := s.catalog.SoftDelete(context.Background(), u.String(), deadline); err != nil {
		if errors.Is(err, artifactcatalog.ErrNotFound) {
			// The row may legitimately be absent — a SoftDelete on a
			// session whose blobs predate the catalog wiring, or a
			// SoftDelete that races a GC pass. Either way the bucket
			// state is now consistent; treat as idempotent.
			return nil
		}
		return fmt.Errorf("cataloging: soft-delete catalog row for %s: %w", u.String(), err)
	}
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
	for _, r := range rows {
		// SoftDelete then Tombstone is the strict transition path; for
		// erasure we collapse both into a single Tombstone so the
		// HardPruneExpired pass removes the row on its next cycle. A
		// row already past `live` falls back to Tombstone directly.
		if r.State == artifactcatalog.StateLive {
			_ = s.catalog.SoftDelete(ctx, r.URI, s.now())
		}
		_ = s.catalog.Tombstone(ctx, r.URI)
	}
	return deleted, nil
}

// HardPrune drives the §12.5 ll. 341 hard-prune sweep: the catalog
// returns the rows whose tombstone deadline has elapsed, the
// decorator removes the matching bucket objects, and the catalog row
// is then deleted. Returns the count of rows pruned end-to-end.
//
// HardPrune is a background sweep — per-row failures are skipped
// (the next cycle picks them up); a catalog query failure short-
// circuits and returns the count completed so far plus the error.
//
// spec: §12.5 ll. 341 — tombstone hard-prune.
func (s *Store) HardPrune(ctx context.Context, now time.Time) (int, error) {
	tomb, _ := s.inner.(blobstore.Tombstoner)
	// HardPruneExpired removes the catalog row directly; for symmetry
	// we delete the bucket object first if the underlying store
	// implements Tombstoner. The S3 / MinIO HardPrune surfaces a count
	// of removed bucket objects independently of the catalog count
	// returned here, so a deployment that ran the bucket HardPrune
	// out-of-band is still consistent.
	if tomb != nil {
		// Best-effort tombstone-prune of bucket-side ghosts (objects
		// whose lenny-deleted-at tag is past the retention window).
		// The number returned by HardPrune is a side metric — the
		// authoritative count is the catalog rows actually deleted
		// below.
		_ = tomb.HardPrune(now, 0)
	}
	count, err := s.catalog.HardPruneExpired(ctx, now)
	if err != nil {
		return count, fmt.Errorf("cataloging: hard-prune catalog: %w", err)
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
		count++
	}
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
