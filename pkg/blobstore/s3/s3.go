// SPDX-License-Identifier: MIT

// Package s3 implements the §4.5 / §12.5 blobstore.Store over AWS S3.
//
// The adapter writes blobs into a configured bucket under a stable
// key derived from the §4.5 lenny-blob URI:
// "{tenant}/{session}/{part}". The bucket-level encryption default
// (AES256 or SSE-KMS, configured per-bucket out of band) provides
// at-rest encryption; per-object SSE-KMS via the Config.SSEKeyResolver
// hook overrides the default when a tenant carries a per-tenant KMS
// key.
//
// The Store satisfies blobstore.Tombstoner with object-version
// metadata: SoftDelete records a "lenny-deleted-at" object tag (S3
// doesn't have a soft-delete primitive that survives Get/Stat); a
// blob carrying the tag returns ErrNotFound from Get and Stat.
// HardPrune lists the tombstoned objects and DeleteObject's them.
package s3

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/lennylabs/lenny/pkg/blobstore"
)

// objectClient narrows the S3 client surface this Store uses.
type objectClient interface {
	PutObject(ctx context.Context, in *s3.PutObjectInput, opts ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	GetObject(ctx context.Context, in *s3.GetObjectInput, opts ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	HeadObject(ctx context.Context, in *s3.HeadObjectInput, opts ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
	PutObjectTagging(ctx context.Context, in *s3.PutObjectTaggingInput, opts ...func(*s3.Options)) (*s3.PutObjectTaggingOutput, error)
	GetObjectTagging(ctx context.Context, in *s3.GetObjectTaggingInput, opts ...func(*s3.Options)) (*s3.GetObjectTaggingOutput, error)
	DeleteObject(ctx context.Context, in *s3.DeleteObjectInput, opts ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
	ListObjectsV2(ctx context.Context, in *s3.ListObjectsV2Input, opts ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
	CopyObject(ctx context.Context, in *s3.CopyObjectInput, opts ...func(*s3.Options)) (*s3.CopyObjectOutput, error)
}

// SSEKeyResolver returns the per-tenant SSE-KMS key id for an object.
// An empty return value selects the bucket default (SSE-S3 or the
// bucket-wide SSE-KMS key).
type SSEKeyResolver func(u blobstore.URI) string

// Config configures a Store.
type Config struct {
	// AWSConfig is the resolved AWS SDK config.
	AWSConfig awssdk.Config

	// Bucket is the S3 bucket the Store writes into. Required.
	Bucket string

	// SSEKeyResolver, when set, picks the per-object SSE-KMS key id.
	// A nil resolver lets the bucket default apply.
	SSEKeyResolver SSEKeyResolver
}

// Store implements blobstore.Store + blobstore.Tombstoner over S3.
type Store struct {
	client                objectClient
	bucket                string
	resolver              SSEKeyResolver
	onArtifactUploadError func(tenantID, errorType string)
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

// classifyS3PutError maps an AWS S3 error onto the §16.5 error_type
// label. The transport branch returns `minio_unreachable` so the
// §16.5 MinIOUnavailable alert (which keys on that label value)
// fires on S3-backed deployments under the same outage semantics
// as the self-managed MinIO backend.
//
// spec: §16.5 ArtifactUploadError; §12.5 line 282.
func classifyS3PutError(err error) string {
	s := err.Error()
	switch {
	case strings.Contains(s, "AccessDenied"),
		strings.Contains(s, "InvalidAccessKeyId"),
		strings.Contains(s, "SignatureDoesNotMatch"),
		strings.Contains(s, "ExpiredToken"),
		strings.Contains(s, "403"):
		return "auth"
	case strings.Contains(s, "QuotaExceeded"),
		strings.Contains(s, "EntityTooLarge"),
		strings.Contains(s, "StorageQuotaExceeded"),
		strings.Contains(s, "BucketFull"):
		return "quota_exceeded"
	case strings.Contains(s, "Timeout"),
		strings.Contains(s, "connection"),
		strings.Contains(s, "no such host"),
		strings.Contains(s, "500"),
		strings.Contains(s, "502"),
		strings.Contains(s, "503"),
		strings.Contains(s, "504"):
		return "minio_unreachable"
	}
	return "other"
}

var (
	_ blobstore.Store      = (*Store)(nil)
	_ blobstore.Tombstoner = (*Store)(nil)
	_ blobstore.Copier     = (*Store)(nil)
)

// New returns a Store. Returns an error when cfg.Bucket is empty or
// the AWS config has no credentials resolved.
func New(cfg Config) (*Store, error) {
	if cfg.Bucket == "" {
		return nil, errors.New("blobstore/s3: cfg.Bucket is required")
	}
	if _, err := cfg.AWSConfig.Credentials.Retrieve(context.Background()); err != nil {
		return nil, fmt.Errorf("blobstore/s3: resolve AWS credentials: %w", err)
	}
	return &Store{
		client:   s3.NewFromConfig(cfg.AWSConfig),
		bucket:   cfg.Bucket,
		resolver: cfg.SSEKeyResolver,
	}, nil
}

// newWithClient is the test-injection constructor.
func newWithClient(c objectClient, bucket string, resolver SSEKeyResolver) *Store {
	return &Store{client: c, bucket: bucket, resolver: resolver}
}

// objectKey maps a blobstore.URI to the S3 object key. The key
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

// tombstoneTag is the object-tag the Store sets on SoftDelete. The
// value is the RFC 3339 UTC timestamp the object was deleted at.
const tombstoneTag = "lenny-deleted-at"

// Put implements blobstore.Store.
func (s *Store) Put(u blobstore.URI, mimeType string, data io.Reader) (string, error) {
	ctx := context.Background()
	key := objectKey(u)
	// §4.5 immutability: refuse to overwrite an existing live blob.
	if exists, err := s.headLive(ctx, key); err != nil {
		return "", err
	} else if exists {
		return "", blobstore.ErrConflict
	}
	body, err := io.ReadAll(data)
	if err != nil {
		return "", fmt.Errorf("blobstore/s3: read body: %w", err)
	}
	in := &s3.PutObjectInput{
		Bucket:      awssdk.String(s.bucket),
		Key:         awssdk.String(key),
		Body:        bytes.NewReader(body),
		ContentType: awssdk.String(mimeType),
	}
	if s.resolver != nil {
		if kmsID := s.resolver(u); kmsID != "" {
			in.ServerSideEncryption = types.ServerSideEncryptionAwsKms
			in.SSEKMSKeyId = awssdk.String(kmsID)
		}
	}
	if _, err := s.client.PutObject(ctx, in); err != nil {
		if s.onArtifactUploadError != nil {
			s.onArtifactUploadError(u.TenantID, classifyS3PutError(err))
		}
		return "", fmt.Errorf("blobstore/s3: PutObject: %w", err)
	}
	return u.String(), nil
}

// Copy implements blobstore.Copier. It uses S3's native CopyObject
// to duplicate src to dst without round-tripping the bytes through
// the gateway.
//
// spec: §4.5 ll. 311 — derive copies parent bytes so the derived
// session's workspace survives parent GC.
func (s *Store) Copy(src, dst blobstore.URI) error {
	if src.TenantID != dst.TenantID {
		return blobstore.ErrCrossTenant
	}
	ctx := context.Background()
	srcKey := objectKey(src)
	dstKey := objectKey(dst)
	// §4.5 write-once: refuse to overwrite a live dst.
	if exists, err := s.headLive(ctx, dstKey); err != nil {
		return err
	} else if exists {
		return blobstore.ErrConflict
	}
	// Source must exist.
	if _, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: awssdk.String(s.bucket),
		Key:    awssdk.String(srcKey),
	}); err != nil {
		if isNoSuchKey(err) {
			return blobstore.ErrNotFound
		}
		return fmt.Errorf("blobstore/s3: HeadObject src: %w", err)
	}
	in := &s3.CopyObjectInput{
		Bucket:     awssdk.String(s.bucket),
		Key:        awssdk.String(dstKey),
		CopySource: awssdk.String(s.bucket + "/" + srcKey),
	}
	if s.resolver != nil {
		if kmsID := s.resolver(dst); kmsID != "" {
			in.ServerSideEncryption = types.ServerSideEncryptionAwsKms
			in.SSEKMSKeyId = awssdk.String(kmsID)
		}
	}
	if _, err := s.client.CopyObject(ctx, in); err != nil {
		return fmt.Errorf("blobstore/s3: CopyObject: %w", err)
	}
	return nil
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
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: awssdk.String(s.bucket),
		Key:    awssdk.String(key),
	})
	if err != nil {
		if isNoSuchKey(err) {
			return blobstore.BlobInfo{}, nil, blobstore.ErrNotFound
		}
		return blobstore.BlobInfo{}, nil, fmt.Errorf("blobstore/s3: GetObject: %w", err)
	}
	info := blobstore.BlobInfo{
		URI:      u,
		MimeType: awssdk.ToString(out.ContentType),
		Size:     awssdk.ToInt64(out.ContentLength),
	}
	if out.LastModified != nil {
		info.StoredAt = out.LastModified.UTC()
	}
	return info, out.Body, nil
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
	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: awssdk.String(s.bucket),
		Key:    awssdk.String(key),
	})
	if err != nil {
		if isNoSuchKey(err) {
			return blobstore.BlobInfo{}, blobstore.ErrNotFound
		}
		return blobstore.BlobInfo{}, fmt.Errorf("blobstore/s3: HeadObject: %w", err)
	}
	info := blobstore.BlobInfo{
		URI:      u,
		MimeType: awssdk.ToString(out.ContentType),
		Size:     awssdk.ToInt64(out.ContentLength),
	}
	if out.LastModified != nil {
		info.StoredAt = out.LastModified.UTC()
	}
	return info, nil
}

// SoftDelete implements blobstore.Tombstoner.
func (s *Store) SoftDelete(u blobstore.URI) error {
	ctx := context.Background()
	key := objectKey(u)
	// Idempotent: a missing object is a no-op.
	if _, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: awssdk.String(s.bucket),
		Key:    awssdk.String(key),
	}); err != nil {
		if isNoSuchKey(err) {
			return nil
		}
		return fmt.Errorf("blobstore/s3: HeadObject before SoftDelete: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := s.client.PutObjectTagging(ctx, &s3.PutObjectTaggingInput{
		Bucket: awssdk.String(s.bucket),
		Key:    awssdk.String(key),
		Tagging: &types.Tagging{TagSet: []types.Tag{
			{Key: awssdk.String(tombstoneTag), Value: awssdk.String(now)},
		}},
	}); err != nil {
		return fmt.Errorf("blobstore/s3: PutObjectTagging: %w", err)
	}
	return nil
}

// StatIncludingTombstones implements blobstore.Tombstoner. It returns
// the blob's metadata and §12.5 lifecycle state: Active when the
// object is live, SoftDeleted when it carries the
// lenny-deleted-at tombstone tag, NotFound when the object is absent.
// Unlike Stat (which collapses a tombstoned object to ErrNotFound),
// this surface lets the GC sweep and observability code distinguish
// the two terminal states.
//
// spec: §12.5 ll. 333-339.
func (s *Store) StatIncludingTombstones(u blobstore.URI) (blobstore.BlobInfo, blobstore.BlobState, error) {
	ctx := context.Background()
	key := objectKey(u)
	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: awssdk.String(s.bucket),
		Key:    awssdk.String(key),
	})
	if err != nil {
		if isNoSuchKey(err) {
			return blobstore.BlobInfo{}, blobstore.BlobStateNotFound, blobstore.ErrNotFound
		}
		return blobstore.BlobInfo{}, blobstore.BlobStateNotFound, fmt.Errorf("blobstore/s3: HeadObject: %w", err)
	}
	info := blobstore.BlobInfo{
		URI:      u,
		MimeType: awssdk.ToString(out.ContentType),
		Size:     awssdk.ToInt64(out.ContentLength),
	}
	if out.LastModified != nil {
		info.StoredAt = out.LastModified.UTC()
	}
	tombstoned, terr := s.isTombstoned(ctx, key)
	if terr != nil {
		return blobstore.BlobInfo{}, blobstore.BlobStateNotFound, terr
	}
	if tombstoned {
		return info, blobstore.BlobStateSoftDeleted, nil
	}
	return info, blobstore.BlobStateActive, nil
}

// HardPrune implements blobstore.Tombstoner. Lists every object in
// the bucket whose tombstoneTag is older than the retention window,
// then DeleteObjects each. Returns the count physically removed.
func (s *Store) HardPrune(now time.Time, retention time.Duration) int {
	ctx := context.Background()
	cutoff := now.Add(-retention).UTC()
	count := 0
	var token *string
	for {
		out, err := s.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            awssdk.String(s.bucket),
			ContinuationToken: token,
		})
		if err != nil {
			// HardPrune surfaces nothing — it's a background sweep. A
			// listing failure is logged in production; here it short-
			// circuits the sweep.
			return count
		}
		for _, obj := range out.Contents {
			tagOut, err := s.client.GetObjectTagging(ctx, &s3.GetObjectTaggingInput{
				Bucket: awssdk.String(s.bucket),
				Key:    obj.Key,
			})
			if err != nil {
				continue
			}
			deletedAt, ok := readTombstone(tagOut.TagSet)
			if !ok || deletedAt.After(cutoff) {
				continue
			}
			if _, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
				Bucket: awssdk.String(s.bucket),
				Key:    obj.Key,
			}); err == nil {
				count++
			}
		}
		if out.IsTruncated == nil || !*out.IsTruncated {
			break
		}
		token = out.NextContinuationToken
	}
	return count
}

// DeleteByTenant implements blobstore.TenantPrefixDeleter: a
// prefix-scoped bulk delete on `{tenant_id}/*` for the §12.8 Phase 4
// tenant-deletion orchestrator. It pages the bucket under the tenant
// prefix and removes every object, returning the count removed. A
// tenant with no objects is a no-op returning (0, nil); an empty
// tenantID matches nothing.
//
// spec: §12.5 ll. 295.
func (s *Store) DeleteByTenant(ctx context.Context, tenantID string) (int, error) {
	if tenantID == "" {
		return 0, nil
	}
	prefix := tenantID + "/"
	count := 0
	var token *string
	for {
		out, err := s.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            awssdk.String(s.bucket),
			Prefix:            awssdk.String(prefix),
			ContinuationToken: token,
		})
		if err != nil {
			return count, fmt.Errorf("blobstore/s3: list %s: %w", prefix, err)
		}
		for _, obj := range out.Contents {
			if _, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
				Bucket: awssdk.String(s.bucket),
				Key:    obj.Key,
			}); err != nil {
				return count, fmt.Errorf("blobstore/s3: delete %s: %w", awssdk.ToString(obj.Key), err)
			}
			count++
		}
		if out.IsTruncated == nil || !*out.IsTruncated {
			break
		}
		token = out.NextContinuationToken
	}
	return count, nil
}

// headLive reports whether a live (non-tombstoned) object exists for
// key. A tombstoned object reports false so a Put on a soft-deleted
// path is admitted.
func (s *Store) headLive(ctx context.Context, key string) (bool, error) {
	_, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: awssdk.String(s.bucket),
		Key:    awssdk.String(key),
	})
	if err != nil {
		if isNoSuchKey(err) {
			return false, nil
		}
		return false, fmt.Errorf("blobstore/s3: HeadObject: %w", err)
	}
	tombstoned, err := s.isTombstoned(ctx, key)
	if err != nil {
		return false, err
	}
	return !tombstoned, nil
}

func (s *Store) isTombstoned(ctx context.Context, key string) (bool, error) {
	out, err := s.client.GetObjectTagging(ctx, &s3.GetObjectTaggingInput{
		Bucket: awssdk.String(s.bucket),
		Key:    awssdk.String(key),
	})
	if err != nil {
		if isNoSuchKey(err) {
			return false, nil
		}
		return false, fmt.Errorf("blobstore/s3: GetObjectTagging: %w", err)
	}
	_, ok := readTombstone(out.TagSet)
	return ok, nil
}

func readTombstone(tags []types.Tag) (time.Time, bool) {
	for _, t := range tags {
		if awssdk.ToString(t.Key) == tombstoneTag {
			ts, err := time.Parse(time.RFC3339, awssdk.ToString(t.Value))
			if err == nil {
				return ts.UTC(), true
			}
		}
	}
	return time.Time{}, false
}

func isNoSuchKey(err error) bool {
	var nsk *types.NoSuchKey
	if errors.As(err, &nsk) {
		return true
	}
	// HeadObject does not surface NoSuchKey; it returns a generic
	// http.ResponseError with StatusCode 404. Detect both forms.
	var rerr interface {
		Error() string
		HTTPStatusCode() int
	}
	if errors.As(err, &rerr) && rerr.HTTPStatusCode() == 404 {
		return true
	}
	// fallback: the smithy-go transport sometimes wraps the 404 as a
	// generic error; match on the text.
	return strings.Contains(err.Error(), "404") ||
		strings.Contains(err.Error(), "NotFound") ||
		strings.Contains(err.Error(), "NoSuchKey")
}

// ContentLength provides an integer accessor used by tests.
func contentLength(n int) string {
	return strconv.Itoa(n)
}
