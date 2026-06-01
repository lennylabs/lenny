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
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/minio/minio-go/v7/pkg/encrypt"
	"github.com/minio/minio-go/v7/pkg/tags"

	"github.com/lennylabs/lenny/pkg/blobstore"
)

// putRetryBackoffs is the §12.5 ll. 282 exponential-backoff schedule
// the MinIO Store applies on transient transport-class failures: 1s,
// 5s, 30s — three retry attempts after the initial try. Total budget
// (initial + 3 retries) caps the effective Put latency at 36s under
// repeated transient failures, matching the §4.4 line 262 retry-
// exhausted contract that increments
// lenny_checkpoint_storage_failure_total{reason="retry_exhausted"}.
//
// spec: §12.5 line 282.
var putRetryBackoffs = []time.Duration{1 * time.Second, 5 * time.Second, 30 * time.Second}

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
	// onArtifactUploadError, when set, fires on every Put that fails
	// after retries are exhausted. The gateway wires it to
	// lenny_artifact_upload_error_total and the §12.5 ll. 282 retry-
	// exhausted reason of lenny_checkpoint_storage_failure_total
	// without coupling the blob store to the metrics registry.
	//
	// errorType carries one of `transport`, `auth`, `quota`, `other`
	// so the metric's `error_type` label is bounded.
	//
	// spec: §12.5 ll. 282 + §16.5 ArtifactUploadError alert.
	onArtifactUploadError func(tenantID, errorType string)
	// retryBackoffs override putRetryBackoffs for tests; production
	// uses the package default.
	retryBackoffs []time.Duration
	// retrySleep overrides time.Sleep for tests; production uses the
	// real sleep.
	retrySleep func(time.Duration)
	legalHolds sync.Map // keyed on objectKey; protects §12.8 holds from DeleteBySession
	// catalog, when wired, reads the §12.5 artifact_store row at
	// DeleteBySession time so a blob whose row carries legal_hold=true
	// survives the per-session sweep. The decorator wiring in
	// pkg/blobstore/cataloging holds the catalog handle; the store
	// here uses the seam for the durable §12.8 hold path that
	// previously lived only in the in-memory legalHolds sync.Map.
	//
	// spec: §12.8 line 735.
	catalog legalHoldCatalog
}

// legalHoldCatalog narrows the artifactcatalog.Store surface the MinIO
// blob store reaches for at DeleteBySession time. The interface is
// declared here so the miniostore package does not import the
// catalog package directly, and so tests can swap a fake.
//
// spec: §12.8 line 735.
type legalHoldCatalog interface {
	IsLegalHeldAt(ctx context.Context, tenantID, sessionID string) (bool, error)
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
// Retry behaviour (§12.5 line 282): a transient transport-class
// PutObject failure is retried on the putRetryBackoffs schedule
// (1s, 5s, 30s — three retries after the initial attempt). Non-
// transient failures (auth, quota, malformed body, conflict) return
// immediately with the wrapped error and no retry. After the retry
// budget is exhausted the configured onArtifactUploadError callback
// fires with the §16.5 error_type classification so the gateway
// emits lenny_artifact_upload_error_total and
// lenny_checkpoint_storage_failure_total{reason="retry_exhausted"}.
//
// spec: §12.5 ll. 297, 299-303, 282.
func (s *Store) Put(u blobstore.URI, mimeType string, data io.Reader) (string, error) {
	ctx := context.Background()
	key := objectKey(u)
	// F-4.5.20: §4.5 write-once contract is enforced today with an
	// explicit StatObject precheck followed by PutObject. The
	// precheck doubles the network round-trip on every Put — the S3
	// contract supports a single-call alternative via the
	// `If-None-Match: *` request header on PutObject, which returns
	// 412 PreconditionFailed when the key already exists. Migrating
	// to the conditional-put form would halve the latency on the hot
	// path and remove the TOCTOU window between the precheck and the
	// PutObject call. The migration is deferred because:
	//   (a) minio-go does not yet expose If-None-Match on
	//       PutObjectOptions (tracked upstream),
	//   (b) the in-memory blobstore.MemoryStore that the gateway
	//       falls back to in dev mode has no conditional-put
	//       primitive, so the precheck is the simplest portable shape.
	// When the upstream gains the header, drop the StatObject branch
	// and trust PutObject's 412 to surface as ErrConflict; the
	// MemoryStore implementation can keep its current explicit check.
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
	// Buffer the body so retries can replay the bytes. PutObject with
	// a -1 size accepts any io.Reader; once we know the bytes we use
	// a *bytes.Reader to keep memory bounded and avoid the SDK's
	// internal multipart upload (which would also consume the reader).
	body, err := io.ReadAll(data)
	if err != nil {
		return "", fmt.Errorf("miniostore: read body for put %s: %w", key, err)
	}
	backoffs := s.retryBackoffs
	if backoffs == nil {
		backoffs = putRetryBackoffs
	}
	sleep := s.retrySleep
	if sleep == nil {
		sleep = time.Sleep
	}
	var lastErr error
	for attempt := 0; attempt <= len(backoffs); attempt++ {
		if _, err := s.client.PutObject(ctx, s.bucket, key, bytes.NewReader(body), int64(len(body)), opts); err == nil {
			return u.String(), nil
		} else {
			lastErr = err
			if !isTransientPutError(err) {
				et := classifyPutError(err)
				if s.onArtifactUploadError != nil {
					s.onArtifactUploadError(u.TenantID, et)
				}
				return "", fmt.Errorf("miniostore: put %s: %w", key, err)
			}
			if attempt == len(backoffs) {
				break
			}
			sleep(backoffs[attempt])
		}
	}
	if s.onArtifactUploadError != nil {
		s.onArtifactUploadError(u.TenantID, "minio_unreachable")
	}
	return "", fmt.Errorf("miniostore: put %s after %d retries: %w", key, len(backoffs), lastErr)
}

// SetOnArtifactUploadError registers the §12.5 / §16.5 callback the
// gateway uses to drive lenny_artifact_upload_error_total. The
// callback fires once per Put that fails after every retry attempt
// (or on a non-transient failure that skips the retry budget). The
// errorType argument is the §16.5 label value: `transport`, `auth`,
// `quota`, or `other`.
//
// spec: §12.5 line 282; §16.5 ArtifactUploadError.
func (s *Store) SetOnArtifactUploadError(fn func(tenantID, errorType string)) {
	s.onArtifactUploadError = fn
}

// SetCatalog wires the §12.8 durable legal-hold reader so the per-
// session sweep (DeleteBySession) refuses to remove blobs whose
// session row carries legal_hold=true.
//
// spec: §12.8 line 735.
func (s *Store) SetCatalog(c legalHoldCatalog) {
	s.catalog = c
}

// isTransientPutError classifies a MinIO PutObject error as
// transport-class (worth retrying) or terminal (an immediate failure
// like auth, quota, or a malformed request). Transport-class covers
// network DNS / dial / timeout / 5xx responses; terminal covers 4xx
// responses except 408 Request Timeout (which is also transient).
//
// spec: §12.5 line 282.
func isTransientPutError(err error) bool {
	if err == nil {
		return false
	}
	// net.Error covers DNS resolution, dial, and stream-level
	// timeouts. The minio-go SDK wraps such errors so errors.As is
	// reliable.
	var nerr net.Error
	if errors.As(err, &nerr) && nerr.Timeout() {
		return true
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	resp := minio.ToErrorResponse(err)
	switch resp.StatusCode {
	case 0:
		// No HTTP response — a transport failure (refused, reset,
		// dial timeout). Treat as transient.
		return true
	case http.StatusRequestTimeout, http.StatusTooManyRequests:
		return true
	case http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

// classifyPutError maps a non-transient PutObject error onto the
// §16.5 lenny_artifact_upload_error_total `error_type` label. The
// label values are bounded so the metric does not explode and match
// the §16.5 MinIOUnavailable alert's `error_type="minio_unreachable"`
// selector for the retry-exhausted transport case.
//
// spec: §16.5 ArtifactUploadError; §12.5 line 282.
func classifyPutError(err error) string {
	resp := minio.ToErrorResponse(err)
	switch resp.StatusCode {
	case http.StatusForbidden, http.StatusUnauthorized:
		return "auth"
	case http.StatusInsufficientStorage, http.StatusPaymentRequired:
		return "quota_exceeded"
	}
	switch resp.Code {
	case "AccessDenied", "InvalidAccessKeyId", "SignatureDoesNotMatch":
		return "auth"
	case "QuotaExceeded", "EntityTooLarge", "StorageQuotaExceeded":
		return "quota_exceeded"
	}
	return "other"
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
	// when the durable catalog row signals a hold or the legacy
	// in-process hash mirrors one. The catalog row is the durable
	// source of truth wired by the gateway (artifactcatalog.Store);
	// the in-process map is the v1 fallback for deployments without
	// a Postgres catalog (in-memory dev mode). Either signal aborts
	// the sweep so the §12.8 orchestrator surfaces
	// ERASURE_BLOCKED_BY_LEGAL_HOLD upstream.
	//
	// spec: §12.8 line 735.
	if s.catalog != nil {
		held, lerr := s.catalog.IsLegalHeldAt(ctx, tenantID, sessionID)
		if lerr != nil {
			return 0, fmt.Errorf("miniostore: legal-hold lookup for %s/%s: %w", tenantID, sessionID, lerr)
		}
		if held {
			return 0, fmt.Errorf("miniostore: session %s/%s is under a §12.8 legal hold (durable catalog)", tenantID, sessionID)
		}
	}
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

// DeleteByTenant implements blobstore.TenantPrefixDeleter: a single
// prefix-scoped bulk delete on `{tenant_id}/*` for the §12.8 Phase 4
// tenant-deletion orchestrator. It lists every object under the
// tenant prefix (across all object_type / session segments) and
// removes it in one RemoveObjects batch, returning the count removed.
// A tenant with no objects is a no-op returning (0, nil); an empty
// tenantID matches nothing so a mis-scoped call cannot wipe the
// bucket.
//
// §12.8 legal hold: the sweep aborts if any object under the prefix
// carries an in-process legal-hold marker, so a held tenant survives
// until the hold is lifted. Tenant deletion is additionally gated by
// the §12.8 legal-hold preflight at the orchestrator; this per-object
// check is the in-process defense for dev-mode deployments without a
// durable catalog.
//
// DeleteByUser implements the §12.1 Eraser primitive. Artifact erasure
// is session-scoped (§12.8 step 7), so this whole-user call is a no-op
// returning (0, nil).
func (s *Store) DeleteByUser(_ context.Context, _, _ string) (int, error) {
	return 0, nil
}

var _ blobstore.Eraser = (*Store)(nil)

// spec: §12.5 ll. 295; §12.8 line 735.
func (s *Store) DeleteByTenant(ctx context.Context, tenantID string) (int, error) {
	if tenantID == "" {
		return 0, nil
	}
	prefix := tenantID + "/"
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

// StatIncludingTombstones implements blobstore.Tombstoner. It returns
// the blob's metadata and its §12.5 lifecycle state (Active,
// SoftDeleted, NotFound). Unlike Stat (which collapses a soft-deleted
// blob to ErrNotFound) this surface lets the GC sweep and
// observability paths distinguish "tombstoned, awaiting hard-prune"
// from "never existed or hard-pruned".
//
// spec: §12.5 ll. 333-339.
func (s *Store) StatIncludingTombstones(u blobstore.URI) (blobstore.BlobInfo, blobstore.BlobState, error) {
	ctx := context.Background()
	key := objectKey(u)
	obj, err := s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		if isNotFound(err) {
			return blobstore.BlobInfo{}, blobstore.BlobStateNotFound, blobstore.ErrNotFound
		}
		return blobstore.BlobInfo{}, blobstore.BlobStateNotFound, fmt.Errorf("miniostore: stat %s: %w", key, err)
	}
	tombstoned, terr := s.isTombstoned(ctx, key)
	if terr != nil {
		return blobstore.BlobInfo{}, blobstore.BlobStateNotFound, terr
	}
	info := blobInfo(u, obj.ContentType, obj.Size, obj.LastModified)
	if tombstoned {
		return info, blobstore.BlobStateSoftDeleted, nil
	}
	if s.clock().After(info.ExpiresAt) {
		return blobstore.BlobInfo{}, blobstore.BlobStateNotFound, blobstore.ErrNotFound
	}
	return info, blobstore.BlobStateActive, nil
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
