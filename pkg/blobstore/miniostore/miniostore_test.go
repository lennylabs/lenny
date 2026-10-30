// SPDX-License-Identifier: MIT

package miniostore

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/tags"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/drainreadiness"
)

// spec: §4.5 / §12.5 — the MinIO-backed blob store's pure mapping and
// metadata logic. The S3 round-trip is exercised by the component-tier
// contract test against a MinIO container.

// Store satisfies the §12.5 drain-readiness Prober.
var _ drainreadiness.Prober = (*Store)(nil)

func TestNewValidatesConfig(t *testing.T) {
	if _, err := New(Config{Bucket: "lenny-artifacts"}); err == nil {
		t.Error("New accepted an empty endpoint")
	}
	if _, err := New(Config{Endpoint: "minio:9000"}); err == nil {
		t.Error("New accepted an empty bucket")
	}
	s, err := New(Config{Endpoint: "minio:9000", Bucket: "lenny-artifacts"})
	if err != nil {
		t.Fatalf("New with a valid config: %v", err)
	}
	if s == nil {
		t.Fatal("New returned a nil store for a valid config")
	}
}

func TestObjectKeyMirrorsTheInMemoryStore(t *testing.T) {
	// spec: §12.5 ll. 295 — path format
	// `{tenant}/{object_type}/{session}/{part}`.
	u := blobstore.URI{
		TenantID:   "acme",
		ObjectType: blobstore.ObjectTypeUpload,
		SessionID:  "s_1",
		PartID:     "part_ab",
	}
	if got := objectKey(u); got != "acme/upload/s_1/part_ab" {
		t.Errorf("objectKey = %q, want acme/upload/s_1/part_ab", got)
	}
}

func TestObjectKeyHonoursObjectType(t *testing.T) {
	// spec: §12.5 ll. 295, 315 — the object_type segment lets the
	// §12.5 GC sweep prefix-scope by artifact class (eviction,
	// checkpoint, workspace, etc.).
	cases := []struct {
		ot   blobstore.ObjectType
		want string
	}{
		{blobstore.ObjectTypeWorkspace, "acme/workspace/s_1/p"},
		{blobstore.ObjectTypeCheckpoint, "acme/checkpoints/s_1/p"},
		{blobstore.ObjectTypeTranscript, "acme/transcript/s_1/p"},
		{blobstore.ObjectTypeUpload, "acme/upload/s_1/p"},
		{blobstore.ObjectTypeEviction, "acme/eviction/s_1/p"},
		{blobstore.ObjectTypeExport, "acme/export/s_1/p"},
		{blobstore.ObjectTypeSessionLog, "acme/sessions/s_1/p"},
	}
	for _, tc := range cases {
		got := objectKey(blobstore.URI{
			TenantID:   "acme",
			ObjectType: tc.ot,
			SessionID:  "s_1",
			PartID:     "p",
		})
		if got != tc.want {
			t.Errorf("objectKey(%q) = %q, want %q", tc.ot, got, tc.want)
		}
	}
}

func TestObjectKeyConcatenatesNestedCheckpointChunkPartID(t *testing.T) {
	// spec: §10.1 — the chunked checkpoint key nests
	// {checkpoint_id}/chunk-{n}.{enc} under the checkpoints prefix, and
	// objectKey concatenates the part id verbatim so the nested path is
	// preserved in the MinIO object key.
	u := blobstore.URI{
		TenantID:   "acme",
		ObjectType: blobstore.ObjectTypeCheckpoint,
		SessionID:  "sess_1",
		PartID:     "ck_9f2a/chunk-00000.tar",
	}
	if got, want := objectKey(u), "acme/checkpoints/sess_1/ck_9f2a/chunk-00000.tar"; got != want {
		t.Errorf("objectKey = %q, want %q", got, want)
	}
}

// TestPresignPutFailsClosedWhenT4KeyUnavailable pins the §12.5 / §13.2
// mint-time fail-closed contract: PresignPut for a T4 tenant whose
// per-tenant SSE-KMS key is unreachable returns
// CLASSIFICATION_CONTROL_VIOLATION and fires the KMS-unavailable hook
// before signing anything, so the gateway never hands a pod a
// capability that would persist restricted data under a weaker key. The
// resolver fails before any network call, so the assertion is pure.
//
// spec: §12.5 ll. 303 (T4 fail-closed); §13.2 (capability model).
func TestPresignPutFailsClosedWhenT4KeyUnavailable(t *testing.T) {
	s, err := New(Config{
		Endpoint: "minio:9000",
		Bucket:   "lenny-artifacts",
		SSEKeyResolver: func(string) (string, bool, error) {
			return "", true, errors.New("kms backend unreachable")
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var fired string
	s.SetOnKMSUnavailable(func(tenantID string) { fired = tenantID })
	u := blobstore.URI{
		TenantID:   "acme",
		ObjectType: blobstore.ObjectTypeCheckpoint,
		SessionID:  "sess_1",
		PartID:     "ck_9f2a/chunk-00000.tar",
	}
	if _, err := s.PresignPut(u, 4096, 30*time.Second); !errors.Is(err, blobstore.ErrClassificationControlViolation) {
		t.Fatalf("PresignPut err = %v, want CLASSIFICATION_CONTROL_VIOLATION", err)
	}
	if fired != "acme" {
		t.Errorf("KMS-unavailable hook fired for %q, want acme", fired)
	}
}

func TestSessionPrefixMirrorsObjectKey(t *testing.T) {
	u := blobstore.URI{
		TenantID:   "acme",
		ObjectType: blobstore.ObjectTypeWorkspace,
		SessionID:  "s_1",
		PartID:     "part_ab",
	}
	prefix := sessionPrefix(u.TenantID, u.ObjectType, u.SessionID)
	if prefix != "acme/workspace/s_1/" {
		t.Errorf("sessionPrefix = %q, want acme/workspace/s_1/", prefix)
	}
	key := objectKey(u)
	if len(key) <= len(prefix) || key[:len(prefix)] != prefix {
		t.Errorf("objectKey %q is not under sessionPrefix %q", key, prefix)
	}
}

func TestSessionPrefixTrailingSlashAvoidsCollision(t *testing.T) {
	// The §12.8 erasure of s_1 must not list objects belonging to s_10.
	short := sessionPrefix("acme", blobstore.ObjectTypeUpload, "s_1")
	siblingKey := objectKey(blobstore.URI{
		TenantID:   "acme",
		ObjectType: blobstore.ObjectTypeUpload,
		SessionID:  "s_10",
		PartID:     "p",
	})
	if len(siblingKey) >= len(short) && siblingKey[:len(short)] == short {
		t.Errorf("prefix %q matches sibling session key %q — erasure would over-delete", short, siblingKey)
	}
}

func TestBlobInfoComputesExpiryFromTTL(t *testing.T) {
	stored := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	u := blobstore.URI{TenantID: "acme", SessionID: "s_1", PartID: "p", TTL: 2 * time.Hour}
	info := blobInfo(u, "application/gzip", 4096, stored)

	if info.MimeType != "application/gzip" || info.Size != 4096 {
		t.Errorf("blobInfo = %+v, want the object's mime type and size", info)
	}
	if !info.StoredAt.Equal(stored) {
		t.Errorf("StoredAt = %v, want the object's stored time", info.StoredAt)
	}
	if !info.ExpiresAt.Equal(stored.Add(2 * time.Hour)) {
		t.Errorf("ExpiresAt = %v, want storedAt + TTL", info.ExpiresAt)
	}
}

func TestIsNotFound(t *testing.T) {
	if !isNotFound(minio.ErrorResponse{StatusCode: http.StatusNotFound}) {
		t.Error("a MinIO 404 was not recognised as not-found")
	}
	if isNotFound(minio.ErrorResponse{StatusCode: http.StatusForbidden}) {
		t.Error("a MinIO 403 was misread as not-found")
	}
	if isNotFound(nil) {
		t.Error("a nil error was misread as not-found")
	}
}

// spec: §12.5 ll. 311-313 — the MinIO Store satisfies the §12.5
// soft-delete + hard-prune Tombstoner contract. The interface
// assertion at construction time means a deployment that mirrors the
// S3 path against MinIO exercises the same tombstone flow rather
// than the unwired no-op fallback.
func TestStoreImplementsTombstoner(t *testing.T) {
	var _ blobstore.Tombstoner = (*Store)(nil)
}

// spec: §12.5 ll. 311-313 — readTombstone parses the
// lenny-deleted-at object tag the SoftDelete sweep stamps. A
// well-formed RFC 3339 value comes back as the deletion instant;
// missing, malformed, or extraneous tags do not.
func TestReadTombstone(t *testing.T) {
	cases := []struct {
		name    string
		tagMap  map[string]string
		wantOK  bool
		wantUTC string
	}{
		{
			name:    "well-formed tag returns the parsed instant",
			tagMap:  map[string]string{tombstoneTag: "2026-05-23T12:00:00Z"},
			wantOK:  true,
			wantUTC: "2026-05-23T12:00:00Z",
		},
		{
			name:   "missing tag is not a tombstone",
			tagMap: map[string]string{"other": "v"},
			wantOK: false,
		},
		{
			name:   "malformed tag value reads as no tombstone",
			tagMap: map[string]string{tombstoneTag: "not-a-timestamp"},
			wantOK: false,
		},
		{
			name:   "empty tag set is not a tombstone",
			tagMap: map[string]string{},
			wantOK: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tagSet, err := tags.MapToObjectTags(tc.tagMap)
			if err != nil {
				t.Fatalf("MapToObjectTags: %v", err)
			}
			got, ok := readTombstone(tagSet)
			if ok != tc.wantOK {
				t.Errorf("readTombstone ok = %v, want %v", ok, tc.wantOK)
			}
			if !tc.wantOK {
				return
			}
			wantTime, err := time.Parse(time.RFC3339, tc.wantUTC)
			if err != nil {
				t.Fatalf("parse expected: %v", err)
			}
			if !got.Equal(wantTime) {
				t.Errorf("readTombstone time = %v, want %v", got, wantTime)
			}
		})
	}
}

// spec: §12.5 ll. 311-313 — readTombstone handles a nil tag set
// (the empty XML body MinIO can return) without panicking, matching
// the "no tombstone" branch.
func TestReadTombstoneNilSafe(t *testing.T) {
	if _, ok := readTombstone(nil); ok {
		t.Error("readTombstone(nil) reported a tombstone")
	}
}

// TestStoreImplementsTenantPrefixDeleter pins the §12.5 ll. 295
// prefix-scoped bulk-delete contract at compile time. The behavioral
// path (ListObjects + RemoveObjects under the tenant prefix) needs a
// live MinIO and is exercised by the higher-tier integration suite.
//
// spec: §12.5 ll. 295.
func TestStoreImplementsTenantPrefixDeleter(t *testing.T) {
	var _ blobstore.TenantPrefixDeleter = (*Store)(nil)
}
