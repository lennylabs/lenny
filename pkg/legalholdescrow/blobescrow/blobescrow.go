// SPDX-License-Identifier: MIT

// Package blobescrow holds the blob-store adapters of the §12.8 legal-hold
// escrow: the escrow store built over the configured escrow bucket, the
// SourceReader that reads held originals from the artifact store, and the
// EscrowWriter and EscrowDeleter that write and remove escrow copies in the
// escrow store.
//
// The escrow store is separate from the artifact store. Phase 4's tenant
// prefix delete on the artifact store deletes the held originals, and the
// escrow copies, which sit in the escrow bucket, survive it. Each escrow
// object is keyed by the original tenant ID, which gives the §12.8
// legal-hold-escrow/{original_tenant_id}/... layout inside that bucket.
//
// spec: §12.8 (Compliance Interfaces: --acknowledge-hold-override admin action (tenant scope))
package blobescrow

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/blobstore/providerflags"
	"github.com/lennylabs/lenny/pkg/legalholdescrow"
)

// ObjectType is the blob object type every escrow object is stored under.
const ObjectType blobstore.ObjectType = "legal_hold_escrow"

// RetainUntilRelease is the TTL every escrow object URI carries. An escrow
// object has no expiry: §12.8 retains it until its hold is cleared, when
// the escrow-GC release deletes it. The blob stores apply a URI's TTL on
// read and report an object past it as absent, so the TTL is set beyond
// any retention horizon rather than left at zero, which would make every
// escrow object read as expired the moment it is written. It is not an
// operator setting, because nothing expires an escrow object.
const RetainUntilRelease = 100 * 365 * 24 * time.Hour

// ErrNoTombstoner is returned by Deleter when the escrow store cannot
// delete a single object, so the escrow-GC release cannot run.
var ErrNoTombstoner = errors.New("legalholdescrow/blobescrow: escrow store does not support escrow object deletion")

// ObjectURI is the blob URI of the escrow object at the §12.8 escrow
// object key. TenantID is the original tenant, and TTL is
// RetainUntilRelease. The escrow object key
// carries slashes and the original blob URI, so it is base64url-encoded
// into the part id to stay one valid object segment on every backend.
func ObjectURI(tenantID, key string) blobstore.URI {
	return blobstore.URI{
		TenantID:   tenantID,
		ObjectType: ObjectType,
		SessionID:  "escrow",
		PartID:     base64.RawURLEncoding.EncodeToString([]byte(key)),
		TTL:        RetainUntilRelease,
		Encoding:   blobstore.Encoding,
	}
}

// StoreOptions derives the escrow store options from the artifact
// store's: the same provider, credentials, and endpoint, with the escrow
// bucket in place of the artifact bucket and no SSE-KMS resolver. The
// payload is already sealed under the region-scoped escrow KEK, and the
// tenant resolver refuses a write for a tenant that has no row and marks
// a T4 tenant's write as needing its tenant key. A non-empty endpoint
// overrides the MinIO endpoint, the one provider whose options carry an
// endpoint. The filesystem provider takes a directory beside the artifact
// root, so the artifact store's tenant prefix delete never reaches the
// escrow copies.
func StoreOptions(artifact providerflags.Options, bucket, endpoint string) providerflags.Options {
	opts := artifact
	opts.Bucket = bucket
	opts.MinIOBucket = bucket
	opts.SSEKeyResolver = nil
	if endpoint != "" {
		opts.MinIOEndpoint = endpoint
	}
	if opts.FilesystemRoot != "" {
		opts.FilesystemRoot = filepath.Join(filepath.Clean(opts.FilesystemRoot)+".legal-hold-escrow", bucket)
	}
	return opts
}

// NewStore builds the one escrow store over the configured escrow bucket.
// With no bucket it builds no store and returns nil, and the escrow
// migration then fails closed as an unresolvable region.
func NewStore(ctx context.Context, artifact providerflags.Options, bucket, endpoint string) (blobstore.Store, error) {
	if bucket == "" {
		return nil, nil
	}
	store, err := providerflags.Resolve(ctx, StoreOptions(artifact, bucket, endpoint))
	if err != nil {
		return nil, fmt.Errorf("legal-hold escrow store for bucket %q: %w", bucket, err)
	}
	return store, nil
}

// Source reads a held artifact's plaintext from the tenant-keyed artifact
// store, which decrypts under the tenant KEK that is still live at Phase
// 3.5. It satisfies legalholdescrow.SourceReader.
type Source struct {
	// Blobs is the artifact store.
	Blobs blobstore.Store
}

// Read implements legalholdescrow.SourceReader.
func (s Source) Read(_ context.Context, a legalholdescrow.HeldArtifact) ([]byte, error) {
	u, err := blobstore.ParseURI(a.BlobURI)
	if err != nil {
		return nil, err
	}
	_, rc, err := s.Blobs.Get(u)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// Writer writes sealed payloads into the escrow store under the original
// tenant ID. In production the bucket is a MinIO or S3 endpoint with
// COMPLIANCE-mode object lock (retain-until-hold-release). It satisfies
// legalholdescrow.EscrowWriter.
type Writer struct {
	// Blobs is the escrow store.
	Blobs blobstore.Store
	// TenantID is the original tenant the escrow objects are keyed by.
	TenantID string
}

// Write implements legalholdescrow.EscrowWriter.
func (w Writer) Write(_ context.Context, key string, sealed []byte) error {
	if _, err := w.Blobs.Put(ObjectURI(w.TenantID, key), "application/octet-stream", bytes.NewReader(sealed)); err != nil {
		// An idempotent re-entry overwrites the same key; a backend that
		// rejects an overwrite (the §4.5 immutability guarantee) is treated
		// as already escrowed.
		if errors.Is(err, blobstore.ErrConflict) {
			return nil
		}
		return err
	}
	return nil
}

// Deleter removes a released escrow object from the escrow store, at the
// URI the Writer wrote it to. Clearing the hold lifts the
// retain-until-hold-release object lock, so the object becomes deletable.
// It satisfies legalholdescrow.EscrowDeleter.
type Deleter struct {
	// Blobs is the escrow store.
	Blobs blobstore.Store
}

// Delete implements legalholdescrow.EscrowDeleter. The single-object
// delete lives on the §4.5 Tombstoner sub-interface, so a store that
// cannot hard-delete cannot release escrow.
func (d Deleter) Delete(_ context.Context, tenantID, _ string, key string) error {
	tomb, ok := d.Blobs.(blobstore.Tombstoner)
	if !ok {
		return ErrNoTombstoner
	}
	if err := tomb.HardDeleteObject(ObjectURI(tenantID, key)); err != nil {
		// An already-absent object is a no-op: a re-cleared hold must not
		// fail the release.
		if errors.Is(err, blobstore.ErrNotFound) {
			return nil
		}
		return err
	}
	return nil
}

var (
	_ legalholdescrow.SourceReader  = Source{}
	_ legalholdescrow.EscrowWriter  = Writer{}
	_ legalholdescrow.EscrowDeleter = Deleter{}
)
