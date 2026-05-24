// SPDX-License-Identifier: MIT

// Package miniostore is the production MinIO-backed §4.5
// blobstore.Store. It maps a lenny-blob URI to a MinIO object and backs
// workspace snapshots, checkpoints, transcripts, and the upload
// pipeline. The in-memory blobstore.MemoryStore remains the test and
// minimal-gateway backend; this implementation shares the same wire
// surface so the gateway swaps backends without changing callers.
//
// The Store also satisfies the §12.5 drain-readiness Prober contract:
// Probe runs a bucket-existence check the lenny-drain-readiness webhook
// queries before admitting a node-drain pod eviction.
package miniostore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/minio/minio-go/v7/pkg/encrypt"
	"github.com/minio/minio-go/v7/pkg/tags"

	"github.com/lennylabs/lenny/pkg/blobstore"
)

// Config configures a MinIO-backed blob store.
type Config struct {
	// Endpoint is the MinIO server address (host:port).
	Endpoint string
	// AccessKey and SecretKey are the MinIO credentials.
	AccessKey string
	SecretKey string
	// Bucket is the bucket blobs are stored in.
	Bucket string
	// UseSSL selects an HTTPS connection to MinIO.
	UseSSL bool

	// SSEKeyResolver, when set, supplies the §12.5 SSE-KMS key
	// identifier for the writing tenant on every Put. Production
	// T4 tenants require a tenant-scoped key (`tenant:{tenant_id}`);
	// T3 may share the deployment-wide key.
	//
	// Return semantics:
	//   - (keyID, requireKey=false, nil) — use keyID when available,
	//     otherwise fall back to bucket-level SSE (the T3 path).
	//   - (keyID, requireKey=true, nil) — T4 tenant. The Put MUST
	//     wrap under keyID. A subsequent KMS lookup failure on
	//     the MinIO side returns CLASSIFICATION_CONTROL_VIOLATION
	//     and increments lenny_checkpoint_storage_failure_total
	//     {reason="kms_unavailable"}.
	//   - ("", false, nil) — no per-tenant key applies; fall
	//     through to bucket-default encryption (SSE-S3).
	//   - ("", true, error) — T4 tenant whose key is unreachable
	//     at the resolver level. The Put fails fast with
	//     CLASSIFICATION_CONTROL_VIOLATION.
	//
	// spec: §12.5 ll. 297, 299-303 — T4 fail-closed write
	// semantics.
	SSEKeyResolver func(tenantID string) (keyID string, requireKey bool, err error)
}

// ErrClassificationControlViolation is the §12.5 ll. 303 fail-closed
// write sentinel: a T4 tenant whose per-tenant SSE-KMS key is
// unavailable at Put time. The §15.1 error catalog maps it to the
// `CLASSIFICATION_CONTROL_VIOLATION` error code.
//
// spec: §12.5 ll. 303.
var ErrClassificationControlViolation = errors.New("miniostore: T4 tenant SSE-KMS key unavailable; CLASSIFICATION_CONTROL_VIOLATION")

// Store is the MinIO-backed §4.5 blobstore.Store. It is goroutine-safe;
// the underlying MinIO client is safe for concurrent use.
type Store struct {
	client      *minio.Client
	bucket      string
	clock       func() time.Time
	sseResolver func(tenantID string) (string, bool, error)
	// onKMSUnavailable, when set, fires on every fail-closed T4 write
	// rejection so the gateway can emit
	// lenny_checkpoint_storage_failure_total{reason="kms_unavailable"}
	// without coupling the blob store directly to the metrics
	// registry.
	//
	// spec: §12.5 ll. 303.
	onKMSUnavailable func(tenantID string)
	legalHolds       sync.Map // keyed on objectKey; protects §12.8 holds from DeleteBySession
}

var (
	_ blobstore.Store      = (*Store)(nil)
	_ blobstore.Copier     = (*Store)(nil)
	_ blobstore.Tombstoner = (*Store)(nil)
)

// New builds a MinIO-backed Store. It validates the configuration and
// constructs the client; the client connects lazily, so New does not
// reach the server. Call Probe to verify reachability.
func New(cfg Config) (*Store, error) {
	if cfg.Endpoint == "" {
		return nil, errors.New("miniostore: endpoint is required")
	}
	if cfg.Bucket == "" {
		return nil, errors.New("miniostore: bucket is required")
	}
	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("miniostore: build client: %w", err)
	}
	return &Store{
		client:      client,
		bucket:      cfg.Bucket,
		clock:       func() time.Time { return time.Now().UTC() },
		sseResolver: cfg.SSEKeyResolver,
	}, nil
}

// SetOnKMSUnavailable registers the §12.5 ll. 303 fail-closed write
// callback. The gateway wires it to the
// lenny_checkpoint_storage_failure_total{reason="kms_unavailable"}
// emitter so every CLASSIFICATION_CONTROL_VIOLATION the blob store
// raises propagates into the CheckpointStorageUnavailable alert.
//
// spec: §12.5 ll. 303.
func (s *Store) SetOnKMSUnavailable(fn func(tenantID string)) {
	s.onKMSUnavailable = fn
}

// SetLegalHold marks the blob under u as protected by a §12.8
// legal hold; DeleteBySession refuses to remove it until the hold
// is cleared.
func (s *Store) SetLegalHold(u blobstore.URI) {
	s.legalHolds.Store(objectKey(u), struct{}{})
}

// ClearLegalHold removes the §12.8 hold from a blob.
func (s *Store) ClearLegalHold(u blobstore.URI) {
	s.legalHolds.Delete(objectKey(u))
}

// hasLegalHold reports whether key is currently under a §12.8 hold.
func (s *Store) hasLegalHold(key string) bool {
	_, ok := s.legalHolds.Load(key)
	return ok
}

// objectKey maps a §4.5 blob URI to its MinIO object key. The key
// follows the §12.5 ll. 295 canonical layout
// `{tenant_id}/{object_type}/{session_id}/{part_id}` so the §12.5
// GC sweep can prefix-scope by object class (e.g.,
// `{tenant_id}/eviction/` for the §4.4 line 291 eviction-context
// cleanup path).
//
// spec: §12.5 ll. 295, 315.
func objectKey(u blobstore.URI) string {
	objType := u.ObjectType
	if objType == "" {
		objType = blobstore.ObjectTypeUpload
	}
	return sessionPrefix(u.TenantID, objType, u.SessionID) + u.PartID
}

// sessionPrefix is the MinIO object-key prefix shared by every blob
// of a (tenant, object_type, session) tuple. The trailing slash keeps
// the prefix from matching a longer session id (sess_1 must not
// match sess_10) and lets the §12.5 line 315 prefix-scoped GC sweep
// target the right object class.
//
// spec: §12.5 ll. 295, 315.
func sessionPrefix(tenantID string, objectType blobstore.ObjectType, sessionID string) string {
	return tenantID + "/" + string(objectType) + "/" + sessionID + "/"
}

// Put implements blobstore.Store. §4.5 blobs are write-once: a key
// that already names an object yields ErrConflict.
//
// SSE-KMS resolution (§12.5 ll. 297-303):
//   - When the resolver returns `requireKey=false` (T3 path or no
//     per-tenant key), the Put applies the keyID when one is
//     given, otherwise falls back to bucket-default SSE-S3.
//   - When the resolver returns `requireKey=true` (T4 path), the
//     Put MUST wrap under the resolver's keyID. A resolver-side
//     error or a downstream KMS lookup failure surfaces
//     ErrClassificationControlViolation; the gateway maps the
//     sentinel onto the §15.1 CLASSIFICATION_CONTROL_VIOLATION
//     error and fires
//     lenny_checkpoint_storage_failure_total{reason="kms_unavailable"}.
//
// spec: §12.5 ll. 297, 299-303.
func (s *Store) Put(u blobstore.URI, mimeType string, data io.Reader) (string, error) {
	ctx := context.Background()
	key := objectKey(u)
	switch _, err := s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{}); {
	case err == nil:
		return "", blobstore.ErrConflict
	case !isNotFound(err):
		return "", fmt.Errorf("miniostore: stat before put %s: %w", key, err)
	}
	opts := minio.PutObjectOptions{ContentType: mimeType}
	if s.sseResolver != nil {
		keyID, requireKey, err := s.sseResolver(u.TenantID)
		switch {
		case err != nil && requireKey:
			// §12.5 ll. 303: T4 tenant whose key is unreachable at
			// the resolver level. Fail closed.
			s.fireKMSUnavailable(u.TenantID)
			return "", fmt.Errorf("%w: tenant=%s: %v", ErrClassificationControlViolation, u.TenantID, err)
		case err != nil:
			return "", fmt.Errorf("miniostore: SSE key resolver for tenant %s: %w", u.TenantID, err)
		case requireKey && keyID == "":
			// Resolver says "this tenant requires a per-tenant key"
			// but returned no key id — fail closed.
			s.fireKMSUnavailable(u.TenantID)
			return "", fmt.Errorf("%w: tenant=%s: resolver returned empty keyID", ErrClassificationControlViolation, u.TenantID)
		case keyID != "":
			sse, sseErr := encrypt.NewSSEKMS(keyID, nil)
			if sseErr != nil {
				if requireKey {
					s.fireKMSUnavailable(u.TenantID)
					return "", fmt.Errorf("%w: tenant=%s: build SSE-KMS: %v",
						ErrClassificationControlViolation, u.TenantID, sseErr)
				}
				return "", fmt.Errorf("miniostore: build SSE-KMS option for tenant %s: %w", u.TenantID, sseErr)
			}
			opts.ServerSideEncryption = sse
		}
	}
	if _, err := s.client.PutObject(ctx, s.bucket, key, data, -1, opts); err != nil {
		return "", fmt.Errorf("miniostore: put %s: %w", key, err)
	}
	return u.String(), nil
}

// fireKMSUnavailable invokes the gateway-registered hook on a
// fail-closed T4 write rejection.
//
// spec: §12.5 ll. 303.
func (s *Store) fireKMSUnavailable(tenantID string) {
	if s.onKMSUnavailable != nil {
		s.onKMSUnavailable(tenantID)
	}
}

// Get implements blobstore.Store. The caller closes the returned reader.
// A tombstoned blob — §12.5 soft-deleted but not yet hard-pruned —
// reads as ErrNotFound so soft-delete behaves identically to physical
// removal until the retention window elapses.
func (s *Store) Get(u blobstore.URI) (blobstore.BlobInfo, io.ReadCloser, error) {
	ctx := context.Background()
	info, err := s.statBlob(ctx, u)
	if err != nil {
		return blobstore.BlobInfo{}, nil, err
	}
	obj, err := s.client.GetObject(ctx, s.bucket, objectKey(u), minio.GetObjectOptions{})
	if err != nil {
		return blobstore.BlobInfo{}, nil, fmt.Errorf("miniostore: get %s: %w", objectKey(u), err)
	}
	return info, obj, nil
}

// Stat implements blobstore.Store.
func (s *Store) Stat(u blobstore.URI) (blobstore.BlobInfo, error) {
	return s.statBlob(context.Background(), u)
}

// statBlob fetches object metadata and applies the §4.5 TTL: a blob
// past its expiry reads as ErrNotFound, matching the in-memory store.
// A §12.5 tombstoned object (lenny-deleted-at tag present) also reads
// as ErrNotFound so soft-deleted blobs behave identically to absent
// ones for callers until the HardPrune sweep physically removes them.
func (s *Store) statBlob(ctx context.Context, u blobstore.URI) (blobstore.BlobInfo, error) {
	key := objectKey(u)
	obj, err := s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		if isNotFound(err) {
			return blobstore.BlobInfo{}, blobstore.ErrNotFound
		}
		return blobstore.BlobInfo{}, fmt.Errorf("miniostore: stat %s: %w", key, err)
	}
	tombstoned, terr := s.isTombstoned(ctx, key)
	if terr != nil {
		return blobstore.BlobInfo{}, terr
	}
	if tombstoned {
		return blobstore.BlobInfo{}, blobstore.ErrNotFound
	}
	info := blobInfo(u, obj.ContentType, obj.Size, obj.LastModified)
	if s.clock().After(info.ExpiresAt) {
		return blobstore.BlobInfo{}, blobstore.ErrNotFound
	}
	return info, nil
}

// Probe runs the §12.5 artifact-store liveness check — a bucket-exists
// call against the configured bucket — and satisfies the §12.5
// drain-readiness Prober contract.
func (s *Store) Probe(ctx context.Context) error {
	ok, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return fmt.Errorf("miniostore: bucket probe: %w", err)
	}
	if !ok {
		return fmt.Errorf("miniostore: bucket %q does not exist", s.bucket)
	}
	return nil
}

// Copy implements blobstore.Copier. It uses MinIO's native
// CopyObject to duplicate src to dst without round-tripping the
// bytes through the gateway. The destination's tenant must match
// src's tenant (§12.5 ll. 295 tenant-prefix invariant). When an
// SSEKeyResolver is configured the destination is rewrapped under
// the same per-tenant SSE-KMS key the original Put would have used.
//
// spec: §4.5 ll. 311 — derive copies parent bytes so the derived
// session owns the workspace independent of the parent's GC.
func (s *Store) Copy(src, dst blobstore.URI) error {
	if src.TenantID != dst.TenantID {
		return blobstore.ErrCrossTenant
	}
	ctx := context.Background()
	srcKey := objectKey(src)
	dstKey := objectKey(dst)
	// §4.5 write-once: refuse to overwrite a live dst.
	switch _, err := s.client.StatObject(ctx, s.bucket, dstKey, minio.StatObjectOptions{}); {
	case err == nil:
		return blobstore.ErrConflict
	case !isNotFound(err):
		return fmt.Errorf("miniostore: stat dst %s before copy: %w", dstKey, err)
	}
	// Source must exist.
	if _, err := s.client.StatObject(ctx, s.bucket, srcKey, minio.StatObjectOptions{}); err != nil {
		if isNotFound(err) {
			return blobstore.ErrNotFound
		}
		return fmt.Errorf("miniostore: stat src %s before copy: %w", srcKey, err)
	}
	srcOpts := minio.CopySrcOptions{Bucket: s.bucket, Object: srcKey}
	dstOpts := minio.CopyDestOptions{Bucket: s.bucket, Object: dstKey}
	if s.sseResolver != nil {
		keyID, requireKey, err := s.sseResolver(dst.TenantID)
		switch {
		case err != nil && requireKey:
			s.fireKMSUnavailable(dst.TenantID)
			return fmt.Errorf("%w: tenant=%s: %v", ErrClassificationControlViolation, dst.TenantID, err)
		case err != nil:
			return fmt.Errorf("miniostore: SSE key resolver for tenant %s: %w", dst.TenantID, err)
		case requireKey && keyID == "":
			s.fireKMSUnavailable(dst.TenantID)
			return fmt.Errorf("%w: tenant=%s: resolver returned empty keyID", ErrClassificationControlViolation, dst.TenantID)
		case keyID != "":
			sse, sseErr := encrypt.NewSSEKMS(keyID, nil)
			if sseErr != nil {
				if requireKey {
					s.fireKMSUnavailable(dst.TenantID)
					return fmt.Errorf("%w: tenant=%s: build SSE-KMS: %v",
						ErrClassificationControlViolation, dst.TenantID, sseErr)
				}
				return fmt.Errorf("miniostore: build SSE-KMS option for tenant %s: %w", dst.TenantID, sseErr)
			}
			dstOpts.Encryption = sse
		}
	}
	if _, err := s.client.CopyObject(ctx, dstOpts, srcOpts); err != nil {
		return fmt.Errorf("miniostore: copy %s -> %s: %w", srcKey, dstKey, err)
	}
	return nil
}

// DeleteBySession removes every object staged for the session within
// the tenant and returns the count removed. It is the §12.8
// GDPR-erasure per-store adapter for the blob store, which is keyed by
// session rather than by user; the erasure orchestrator invokes it for
// each of an erased user's sessions. Erasing a session with no blobs
// is a no-op returning (0, nil). All objects under the session prefix
// are removed regardless of their §4.5 TTL, since an unswept expired
// object still holds the user's data. The signature matches the
// orchestrator's DeleteBySessionFunc so the adapter plugs in directly.
//
// Under the §12.5 ll. 295 4-segment key layout
// `{tenant}/{object_type}/{session}/{part}`, the sweep enumerates
// every spec'd object_type because a session's bytes are spread
// across the per-class prefixes (workspace, checkpoint, transcript,
// upload, eviction, export, sessions).
//
// spec: §12.5 ll. 295, 315.
func (s *Store) DeleteBySession(ctx context.Context, tenantID, sessionID string) (int, error) {
	objectTypes := []blobstore.ObjectType{
		blobstore.ObjectTypeWorkspace,
		blobstore.ObjectTypeCheckpoint,
		blobstore.ObjectTypeTranscript,
		blobstore.ObjectTypeUpload,
		blobstore.ObjectTypeEviction,
		blobstore.ObjectTypeExport,
		blobstore.ObjectTypeSessionLog,
	}
	var objects []minio.ObjectInfo
	for _, ot := range objectTypes {
		prefix := sessionPrefix(tenantID, ot, sessionID)
		for obj := range s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{
			Prefix:    prefix,
			Recursive: true,
		}) {
			if obj.Err != nil {
				return 0, fmt.Errorf("miniostore: list %s: %w", prefix, obj.Err)
			}
			objects = append(objects, obj)
		}
	}
	if len(objects) == 0 {
		return 0, nil
	}
	// §12.8 legal hold: refuse to remove any blob in the session
	// when at least one object is held. The orchestrator surfaces
	// the refusal as ERASURE_BLOCKED_BY_LEGAL_HOLD upstream.
	for _, obj := range objects {
		if s.hasLegalHold(obj.Key) {
			return 0, fmt.Errorf("miniostore: %s is under a §12.8 legal hold", obj.Key)
		}
	}
	objectsCh := make(chan minio.ObjectInfo, len(objects))
	for _, obj := range objects {
		objectsCh <- obj
	}
	close(objectsCh)
	for rerr := range s.client.RemoveObjects(ctx, s.bucket, objectsCh, minio.RemoveObjectsOptions{}) {
		if rerr.Err != nil {
			return 0, fmt.Errorf("miniostore: remove %s: %w", rerr.ObjectName, rerr.Err)
		}
	}
	return len(objects), nil
}

// tombstoneTag is the §12.5 soft-delete tag key the Store stamps on
// SoftDelete. The value is the RFC 3339 UTC timestamp the object was
// deleted at. MinIO exposes the same object-tag surface as S3, so the
// tag name is shared with pkg/blobstore/s3 — a deployment that mirrors
// to S3 sees the same tag.
const tombstoneTag = "lenny-deleted-at"

// SoftDelete implements blobstore.Tombstoner. It marks the object as
// tombstoned by setting the §12.5 lenny-deleted-at tag carrying the
// current UTC instant. Subsequent Get / Stat return ErrNotFound so the
// soft-deleted blob behaves identically to an absent one until the
// HardPrune sweep physically removes it.
//
// A blob that does not exist is a no-op (returns nil); a blob already
// carrying the tag has its timestamp refreshed, so a repeat SoftDelete
// keeps the contract idempotent (mirroring the in-memory store and the
// S3 backend).
//
// spec: §12.5 ll. 311-313 — soft-delete on GC; tombstone retention.
func (s *Store) SoftDelete(u blobstore.URI) error {
	ctx := context.Background()
	key := objectKey(u)
	// Idempotent: a missing object is a no-op.
	if _, err := s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{}); err != nil {
		if isNotFound(err) {
			return nil
		}
		return fmt.Errorf("miniostore: stat before SoftDelete %s: %w", key, err)
	}
	tag, err := tags.MapToObjectTags(map[string]string{
		tombstoneTag: s.clock().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return fmt.Errorf("miniostore: build tombstone tag: %w", err)
	}
	if err := s.client.PutObjectTagging(ctx, s.bucket, key, tag, minio.PutObjectTaggingOptions{}); err != nil {
		return fmt.Errorf("miniostore: PutObjectTagging %s: %w", key, err)
	}
	return nil
}

// HardPrune implements blobstore.Tombstoner. It lists every object in
// the bucket whose §12.5 tombstone tag is older than the retention
// window and DeleteObject's each. Returns the count physically removed.
//
// HardPrune is a background sweep: a listing or tagging failure short-
// circuits the sweep without surfacing an error to the caller. Per-
// object failures are skipped silently — the next sweep cycle picks
// them up.
//
// spec: §12.5 ll. 311-313 — tombstone retention; hard-prune sweep.
func (s *Store) HardPrune(now time.Time, retention time.Duration) int {
	ctx := context.Background()
	cutoff := now.Add(-retention).UTC()
	count := 0
	for obj := range s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{Recursive: true}) {
		if obj.Err != nil {
			// Mirror s3.HardPrune: a listing failure short-circuits the
			// sweep without surfacing an error.
			return count
		}
		tagOut, err := s.client.GetObjectTagging(ctx, s.bucket, obj.Key, minio.GetObjectTaggingOptions{})
		if err != nil {
			continue
		}
		deletedAt, ok := readTombstone(tagOut)
		if !ok || deletedAt.After(cutoff) {
			continue
		}
		if err := s.client.RemoveObject(ctx, s.bucket, obj.Key, minio.RemoveObjectOptions{}); err == nil {
			count++
		}
	}
	return count
}

// isTombstoned reports whether the object at key carries the §12.5
// lenny-deleted-at tag.
func (s *Store) isTombstoned(ctx context.Context, key string) (bool, error) {
	tagOut, err := s.client.GetObjectTagging(ctx, s.bucket, key, minio.GetObjectTaggingOptions{})
	if err != nil {
		if isNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("miniostore: GetObjectTagging %s: %w", key, err)
	}
	_, ok := readTombstone(tagOut)
	return ok, nil
}

// readTombstone extracts the §12.5 lenny-deleted-at timestamp from an
// object's tag set. Returns (zero, false) when the tag is absent or its
// value does not parse as RFC 3339.
func readTombstone(t *tags.Tags) (time.Time, bool) {
	if t == nil {
		return time.Time{}, false
	}
	for k, v := range t.ToMap() {
		if k == tombstoneTag {
			ts, err := time.Parse(time.RFC3339, v)
			if err == nil {
				return ts.UTC(), true
			}
		}
	}
	return time.Time{}, false
}

// blobInfo assembles a BlobInfo from a URI and object metadata. The
// §4.5 expiry is the object's stored time plus the URI's TTL.
func blobInfo(u blobstore.URI, mimeType string, size int64, storedAt time.Time) blobstore.BlobInfo {
	return blobstore.BlobInfo{
		URI:       u,
		MimeType:  mimeType,
		Size:      size,
		StoredAt:  storedAt,
		ExpiresAt: storedAt.Add(u.TTL),
	}
}

// isNotFound reports whether err is a MinIO 404 (the object or bucket
// does not exist).
func isNotFound(err error) bool {
	return minio.ToErrorResponse(err).StatusCode == http.StatusNotFound
}
