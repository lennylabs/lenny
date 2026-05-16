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
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

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
}

// Store is the MinIO-backed §4.5 blobstore.Store. It is goroutine-safe;
// the underlying MinIO client is safe for concurrent use.
type Store struct {
	client *minio.Client
	bucket string
	clock  func() time.Time
}

var _ blobstore.Store = (*Store)(nil)

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
		client: client,
		bucket: cfg.Bucket,
		clock:  func() time.Time { return time.Now().UTC() },
	}, nil
}

// objectKey maps a §4.5 blob URI to its MinIO object key. It mirrors
// the in-memory store's key so the two implementations address a blob
// identically.
func objectKey(u blobstore.URI) string {
	return sessionPrefix(u.TenantID, u.SessionID) + u.PartID
}

// sessionPrefix is the MinIO object-key prefix shared by every blob of
// a session. The trailing slash keeps the prefix from matching a
// longer session id (sess_1 must not match sess_10).
func sessionPrefix(tenantID, sessionID string) string {
	return tenantID + "/" + sessionID + "/"
}

// Put implements blobstore.Store. §4.5 blobs are write-once: a key that
// already names an object yields ErrConflict.
func (s *Store) Put(u blobstore.URI, mimeType string, data io.Reader) (string, error) {
	ctx := context.Background()
	key := objectKey(u)
	switch _, err := s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{}); {
	case err == nil:
		return "", blobstore.ErrConflict
	case !isNotFound(err):
		return "", fmt.Errorf("miniostore: stat before put %s: %w", key, err)
	}
	if _, err := s.client.PutObject(ctx, s.bucket, key, data, -1, minio.PutObjectOptions{
		ContentType: mimeType,
	}); err != nil {
		return "", fmt.Errorf("miniostore: put %s: %w", key, err)
	}
	return u.String(), nil
}

// Get implements blobstore.Store. The caller closes the returned reader.
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
func (s *Store) statBlob(ctx context.Context, u blobstore.URI) (blobstore.BlobInfo, error) {
	obj, err := s.client.StatObject(ctx, s.bucket, objectKey(u), minio.StatObjectOptions{})
	if err != nil {
		if isNotFound(err) {
			return blobstore.BlobInfo{}, blobstore.ErrNotFound
		}
		return blobstore.BlobInfo{}, fmt.Errorf("miniostore: stat %s: %w", objectKey(u), err)
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

// DeleteBySession removes every object staged for the session within
// the tenant and returns the count removed. It is the §12.8
// GDPR-erasure per-store adapter for the blob store, which is keyed by
// session rather than by user; the erasure orchestrator invokes it for
// each of an erased user's sessions. Erasing a session with no blobs
// is a no-op returning (0, nil). All objects under the session prefix
// are removed regardless of their §4.5 TTL, since an unswept expired
// object still holds the user's data. The signature matches the
// orchestrator's DeleteBySessionFunc so the adapter plugs in directly.
func (s *Store) DeleteBySession(ctx context.Context, tenantID, sessionID string) (int, error) {
	prefix := sessionPrefix(tenantID, sessionID)
	var objects []minio.ObjectInfo
	for obj := range s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: true,
	}) {
		if obj.Err != nil {
			return 0, fmt.Errorf("miniostore: list %s: %w", prefix, obj.Err)
		}
		objects = append(objects, obj)
	}
	if len(objects) == 0 {
		return 0, nil
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
