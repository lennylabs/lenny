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
	u := blobstore.URI{TenantID: "acme", SessionID: "s_1", PartID: "part_ab"}
	if got := objectKey(u); got != "acme/s_1/part_ab" {
		t.Errorf("objectKey = %q, want acme/s_1/part_ab", got)
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
