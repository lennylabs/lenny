// SPDX-License-Identifier: MIT

package miniostore

import (
	"net/http"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/drainreadiness"
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
		{blobstore.ObjectTypeCheckpoint, "acme/checkpoint/s_1/p"},
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
