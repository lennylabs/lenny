// SPDX-License-Identifier: MIT

// Package gcs implements the §4.5 / §12.5 blobstore.Store over GCS.
//
// The adapter writes blobs into a configured bucket under a stable
// key derived from the §4.5 lenny-blob URI:
// "{tenant}/{session}/{part}". Bucket-level CMEK / Cloud KMS keys
// provide at-rest encryption; per-object CMEK via the
// Config.KMSKeyResolver hook overrides the bucket default when a
// tenant carries a per-tenant key.
//
// Tombstoning uses GCS object metadata: SoftDelete writes a
// "lenny-deleted-at" metadata key (RFC 3339 UTC). Get / Stat return
// ErrNotFound for tombstoned blobs. HardPrune lists the bucket and
// deletes tombstoned objects past retention.
package gcs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"

	"github.com/lennylabs/lenny/pkg/blobstore"
)

// objectClient narrows the GCS client surface this Store uses. Tests
// inject a fake implementing this interface.
type objectClient interface {
	NewWriter(ctx context.Context, key string, contentType, kmsKey string) writeCloser
	NewReader(ctx context.Context, key string) (readCloser, error)
	Attrs(ctx context.Context, key string) (*storage.ObjectAttrs, error)
	Update(ctx context.Context, key string, metadata map[string]string) error
	Delete(ctx context.Context, key string) error
	List(ctx context.Context) ([]string, error)
}

type writeCloser interface {
	io.WriteCloser
}

type readCloser interface {
	io.ReadCloser
	Attrs() *storage.ReaderObjectAttrs
}

// KMSKeyResolver returns the per-tenant Cloud KMS resource name for an
// object.
//
// Return semantics mirror the §4.5 MinIO backend so every artifact
// store fails closed identically for a T4 tenant (§12.5 line 303):
//   - (kmsKey, requireKey=false, nil) — use kmsKey when non-empty,
//     otherwise fall through to the bucket default (the T3 path).
//   - (kmsKey, requireKey=true, nil) — T4 tenant. The Put MUST wrap
//     under kmsKey; an empty kmsKey fails closed.
//   - ("", true, err) — T4 tenant whose key is unreachable at the
//     resolver level. The Put fails closed with
//     blobstore.ErrClassificationControlViolation.
//
// spec: §12.5 line 303; §12.9 line 1046.
type KMSKeyResolver func(u blobstore.URI) (kmsKey string, requireKey bool, err error)

// Config configures a Store.
type Config struct {
	// Bucket is the GCS bucket the Store writes into. Required.
	Bucket string

	// ClientOptions are passed verbatim to storage.NewClient.
	ClientOptions []option.ClientOption

	// KMSKeyResolver, when set, picks the per-object KMS key.
	KMSKeyResolver KMSKeyResolver
}

// Store implements blobstore.Store + blobstore.Tombstoner over GCS.
type Store struct {
	client                objectClient
	bucket                string
	resolver              KMSKeyResolver
	onArtifactUploadError func(tenantID, errorType string)
	onKMSUnavailable      func(tenantID string)
}

// SetOnArtifactUploadError registers the §12.5 / §16.5 callback the
// gateway uses to drive lenny_artifact_upload_error_total. errorType
// is the bounded §16.5 label value (`transport`, `auth`, `quota`,
// `other`).
//
// spec: §16.5 ArtifactUploadError.
func (s *Store) SetOnArtifactUploadError(fn func(tenantID, errorType string)) {
	s.onArtifactUploadError = fn
}

// SetOnKMSUnavailable registers the §12.5 line 303 fail-closed write
// callback the gateway uses to drive
// lenny_checkpoint_storage_failure_total{reason="kms_unavailable"}
// whenever a Put rejects a T4 tenant whose per-tenant KMS key is
// unavailable.
//
// spec: §12.5 line 303.
func (s *Store) SetOnKMSUnavailable(fn func(tenantID string)) {
	s.onKMSUnavailable = fn
}

// resolveKMSKey resolves the per-tenant Cloud KMS resource name for u,
// failing closed for a T4 tenant whose key is unavailable. An empty
// returned key leaves the bucket default in force (the T3 path).
//
// spec: §12.5 line 303; §12.9 line 1046.
func (s *Store) resolveKMSKey(u blobstore.URI) (string, error) {
	if s.resolver == nil {
		return "", nil
	}
	kmsKey, requireKey, err := s.resolver(u)
	switch {
	case err != nil && requireKey:
		s.fireKMSUnavailable(u.TenantID)
		return "", fmt.Errorf("%w: tenant=%s: %v", blobstore.ErrClassificationControlViolation, u.TenantID, err)
	case err != nil:
		return "", fmt.Errorf("blobstore/gcs: KMS key resolver for tenant %s: %w", u.TenantID, err)
	case requireKey && kmsKey == "":
		s.fireKMSUnavailable(u.TenantID)
		return "", fmt.Errorf("%w: tenant=%s: resolver returned empty key", blobstore.ErrClassificationControlViolation, u.TenantID)
	}
	return kmsKey, nil
}

// fireKMSUnavailable invokes the gateway-registered hook on a
// fail-closed T4 write rejection. It is a no-op when no hook is wired.
//
// spec: §12.5 line 303.
func (s *Store) fireKMSUnavailable(tenantID string) {
	if s.onKMSUnavailable != nil {
		s.onKMSUnavailable(tenantID)
	}
}

// classifyGCSPutError maps a GCS write error onto the §16.5 error_type
// label. Transport-class failures emit `minio_unreachable` so the
// §16.5 MinIOUnavailable alert fires uniformly across self-managed
// MinIO and managed-cloud backends.
//
// spec: §16.5 ArtifactUploadError; §12.5 line 282.
func classifyGCSPutError(err error) string {
	s := err.Error()
	switch {
	case errors.Is(err, storage.ErrObjectNotExist):
		return "other"
	}
	switch {
	case containsAny(s, "PermissionDenied", "Unauthenticated", "401", "403"):
		return "auth"
	case containsAny(s, "QuotaExceeded", "ResourceExhausted", "Insufficient"):
		return "quota_exceeded"
	case containsAny(s, "Timeout", "DeadlineExceeded", "Unavailable", "connection", "500", "502", "503", "504"):
		return "minio_unreachable"
	}
	return "other"
}

func containsAny(s string, needles ...string) bool {
	for _, n := range needles {
		if len(n) > 0 && strings.Contains(s, n) {
			return true
		}
	}
	return false
}

var (
	_ blobstore.Store      = (*Store)(nil)
	_ blobstore.Tombstoner = (*Store)(nil)
)

// New returns a Store. Returns an error when cfg.Bucket is empty or
// the GCS client cannot be created.
func New(ctx context.Context, cfg Config) (*Store, error) {
	if cfg.Bucket == "" {
		return nil, errors.New("blobstore/gcs: cfg.Bucket is required")
	}
	cli, err := storage.NewClient(ctx, cfg.ClientOptions...)
	if err != nil {
		return nil, fmt.Errorf("blobstore/gcs: storage.NewClient: %w", err)
	}
	return &Store{
		client:   newClientAdapter(cli, cfg.Bucket),
		bucket:   cfg.Bucket,
		resolver: cfg.KMSKeyResolver,
	}, nil
}

// newWithClient is the test-injection constructor.
func newWithClient(c objectClient, bucket string, resolver KMSKeyResolver) *Store {
	return &Store{client: c, bucket: bucket, resolver: resolver}
}

// objectKey maps a blobstore.URI to the GCS object key. The key
// follows the §12.5 ll. 295 canonical layout
// `{tenant_id}/{object_type}/{session_id}/{part_id}` so the §12.5
// GC sweep can prefix-scope by object class.
//
// spec: §12.5 ll. 295, 315.
func objectKey(u blobstore.URI) string {
	objType := u.ObjectType
	if objType == "" {
		objType = blobstore.ObjectTypeUpload
	}
	return fmt.Sprintf("%s/%s/%s/%s", u.TenantID, objType, u.SessionID, u.PartID)
}

const tombstoneMeta = "lenny-deleted-at"

// Put implements blobstore.Store.
func (s *Store) Put(u blobstore.URI, mimeType string, data io.Reader) (string, error) {
	ctx := context.Background()
	key := objectKey(u)
	if attrs, err := s.client.Attrs(ctx, key); err == nil {
		if _, tombstoned := attrs.Metadata[tombstoneMeta]; !tombstoned {
			return "", blobstore.ErrConflict
		}
	} else if !errors.Is(err, storage.ErrObjectNotExist) {
		return "", fmt.Errorf("blobstore/gcs: Attrs: %w", err)
	}
	kmsKey, err := s.resolveKMSKey(u)
	if err != nil {
		return "", err
	}
	w := s.client.NewWriter(ctx, key, mimeType, kmsKey)
	if _, err := io.Copy(w, data); err != nil {
		_ = w.Close()
		if s.onArtifactUploadError != nil {
			s.onArtifactUploadError(u.TenantID, classifyGCSPutError(err))
		}
		return "", fmt.Errorf("blobstore/gcs: write body: %w", err)
	}
	if err := w.Close(); err != nil {
		if s.onArtifactUploadError != nil {
			s.onArtifactUploadError(u.TenantID, classifyGCSPutError(err))
		}
		return "", fmt.Errorf("blobstore/gcs: writer close: %w", err)
	}
	return u.String(), nil
}

// Get implements blobstore.Store.
func (s *Store) Get(u blobstore.URI) (blobstore.BlobInfo, io.ReadCloser, error) {
	ctx := context.Background()
	key := objectKey(u)
	if tombstoned, err := s.isTombstoned(ctx, key); err != nil {
		return blobstore.BlobInfo{}, nil, err
	} else if tombstoned {
		return blobstore.BlobInfo{}, nil, blobstore.ErrNotFound
	}
	r, err := s.client.NewReader(ctx, key)
	if err != nil {
		if errors.Is(err, storage.ErrObjectNotExist) {
			return blobstore.BlobInfo{}, nil, blobstore.ErrNotFound
		}
		return blobstore.BlobInfo{}, nil, fmt.Errorf("blobstore/gcs: NewReader: %w", err)
	}
	attrs := r.Attrs()
	info := blobstore.BlobInfo{
		URI:      u,
		MimeType: attrs.ContentType,
		Size:     attrs.Size,
		StoredAt: attrs.LastModified.UTC(),
	}
	return info, r, nil
}

// Stat implements blobstore.Store.
func (s *Store) Stat(u blobstore.URI) (blobstore.BlobInfo, error) {
	ctx := context.Background()
	key := objectKey(u)
	if tombstoned, err := s.isTombstoned(ctx, key); err != nil {
		return blobstore.BlobInfo{}, err
	} else if tombstoned {
		return blobstore.BlobInfo{}, blobstore.ErrNotFound
	}
	attrs, err := s.client.Attrs(ctx, key)
	if err != nil {
		if errors.Is(err, storage.ErrObjectNotExist) {
			return blobstore.BlobInfo{}, blobstore.ErrNotFound
		}
		return blobstore.BlobInfo{}, fmt.Errorf("blobstore/gcs: Attrs: %w", err)
	}
	return blobstore.BlobInfo{
		URI:      u,
		MimeType: attrs.ContentType,
		Size:     attrs.Size,
		StoredAt: attrs.Updated.UTC(),
	}, nil
}

// SoftDelete implements blobstore.Tombstoner.
func (s *Store) SoftDelete(u blobstore.URI) error {
	ctx := context.Background()
	key := objectKey(u)
	if _, err := s.client.Attrs(ctx, key); err != nil {
		if errors.Is(err, storage.ErrObjectNotExist) {
			return nil
		}
		return fmt.Errorf("blobstore/gcs: Attrs: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if err := s.client.Update(ctx, key, map[string]string{tombstoneMeta: now}); err != nil {
		return fmt.Errorf("blobstore/gcs: Update metadata: %w", err)
	}
	return nil
}

// StatIncludingTombstones implements blobstore.Tombstoner. Returns
// the blob's metadata and its §12.5 lifecycle state: Active for a live
// object, SoftDeleted for one carrying the lenny-deleted-at metadata
// key, NotFound when the object is absent. Lets the GC sweep
// distinguish "soft-deleted, awaiting hard-prune" from "physically
// absent".
//
// spec: §12.5 ll. 333-339.
func (s *Store) StatIncludingTombstones(u blobstore.URI) (blobstore.BlobInfo, blobstore.BlobState, error) {
	ctx := context.Background()
	key := objectKey(u)
	attrs, err := s.client.Attrs(ctx, key)
	if err != nil {
		if errors.Is(err, storage.ErrObjectNotExist) {
			return blobstore.BlobInfo{}, blobstore.BlobStateNotFound, blobstore.ErrNotFound
		}
		return blobstore.BlobInfo{}, blobstore.BlobStateNotFound, fmt.Errorf("blobstore/gcs: Attrs: %w", err)
	}
	info := blobstore.BlobInfo{
		URI:      u,
		MimeType: attrs.ContentType,
		Size:     attrs.Size,
		StoredAt: attrs.Updated.UTC(),
	}
	if _, tombstoned := attrs.Metadata[tombstoneMeta]; tombstoned {
		return info, blobstore.BlobStateSoftDeleted, nil
	}
	return info, blobstore.BlobStateActive, nil
}

// HardPrune implements blobstore.Tombstoner.
func (s *Store) HardPrune(now time.Time, retention time.Duration) int {
	ctx := context.Background()
	cutoff := now.Add(-retention).UTC()
	keys, err := s.client.List(ctx)
	if err != nil {
		return 0
	}
	count := 0
	for _, key := range keys {
		attrs, err := s.client.Attrs(ctx, key)
		if err != nil {
			continue
		}
		raw, ok := attrs.Metadata[tombstoneMeta]
		if !ok {
			continue
		}
		deletedAt, err := time.Parse(time.RFC3339, raw)
		if err != nil || deletedAt.After(cutoff) {
			continue
		}
		if err := s.client.Delete(ctx, key); err == nil {
			count++
		}
	}
	return count
}

// DeleteByUser implements the §12.1 Eraser primitive. Artifact erasure
// is session-scoped (§12.8 step 7), so this whole-user call is a no-op
// returning (0, nil).
func (s *Store) DeleteByUser(_ context.Context, _, _ string) (int, error) {
	return 0, nil
}

var _ blobstore.Eraser = (*Store)(nil)

// DeleteByTenant implements blobstore.TenantPrefixDeleter: a
// prefix-scoped bulk delete on `{tenant_id}/*` for the §12.8 Phase 4
// tenant-deletion orchestrator. It lists the bucket, removes every
// object under the tenant prefix, and returns the count removed. A
// tenant with no objects is a no-op returning (0, nil); an empty
// tenantID matches nothing.
//
// spec: §12.5 ll. 295.
func (s *Store) DeleteByTenant(ctx context.Context, tenantID string) (int, error) {
	if tenantID == "" {
		return 0, nil
	}
	prefix := tenantID + "/"
	keys, err := s.client.List(ctx)
	if err != nil {
		return 0, fmt.Errorf("blobstore/gcs: list: %w", err)
	}
	count := 0
	for _, key := range keys {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		if err := s.client.Delete(ctx, key); err != nil {
			return count, fmt.Errorf("blobstore/gcs: delete %s: %w", key, err)
		}
		count++
	}
	return count, nil
}

func (s *Store) isTombstoned(ctx context.Context, key string) (bool, error) {
	attrs, err := s.client.Attrs(ctx, key)
	if err != nil {
		if errors.Is(err, storage.ErrObjectNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("blobstore/gcs: Attrs: %w", err)
	}
	_, ok := attrs.Metadata[tombstoneMeta]
	return ok, nil
}

// clientAdapter wraps a real *storage.Client to satisfy the
// internal objectClient interface.
type clientAdapter struct {
	cli    *storage.Client
	bucket string
}

func newClientAdapter(cli *storage.Client, bucket string) objectClient {
	return &clientAdapter{cli: cli, bucket: bucket}
}

func (a *clientAdapter) NewWriter(ctx context.Context, key, contentType, kmsKey string) writeCloser {
	w := a.cli.Bucket(a.bucket).Object(key).NewWriter(ctx)
	w.ContentType = contentType
	if kmsKey != "" {
		w.KMSKeyName = kmsKey
	}
	return w
}

type gcsReader struct {
	r *storage.Reader
}

func (g *gcsReader) Read(p []byte) (int, error) { return g.r.Read(p) }
func (g *gcsReader) Close() error               { return g.r.Close() }
func (g *gcsReader) Attrs() *storage.ReaderObjectAttrs {
	attrs := g.r.Attrs
	return &attrs
}

func (a *clientAdapter) NewReader(ctx context.Context, key string) (readCloser, error) {
	r, err := a.cli.Bucket(a.bucket).Object(key).NewReader(ctx)
	if err != nil {
		return nil, err
	}
	return &gcsReader{r: r}, nil
}

func (a *clientAdapter) Attrs(ctx context.Context, key string) (*storage.ObjectAttrs, error) {
	return a.cli.Bucket(a.bucket).Object(key).Attrs(ctx)
}

func (a *clientAdapter) Update(ctx context.Context, key string, metadata map[string]string) error {
	_, err := a.cli.Bucket(a.bucket).Object(key).Update(ctx, storage.ObjectAttrsToUpdate{
		Metadata: metadata,
	})
	return err
}

func (a *clientAdapter) Delete(ctx context.Context, key string) error {
	return a.cli.Bucket(a.bucket).Object(key).Delete(ctx)
}

func (a *clientAdapter) List(ctx context.Context) ([]string, error) {
	out := []string{}
	it := a.cli.Bucket(a.bucket).Objects(ctx, nil)
	for {
		attrs, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, err
		}
		out = append(out, attrs.Name)
	}
	return out, nil
}
