//go:build component

// SPDX-License-Identifier: MIT

// Component test for the §4.4 partial-checkpoint manifest resume
// cleanup driven across BOTH real backends at once: a real MinIO
// container (chunk objects under chunk_object_key_prefix) and a
// real Postgres container (the manifest row and the §12.5
// artifact_store catalog rows). The tier1 partialcleanup_test.go
// covers CleanupPartialManifest against in-memory fakes, and
// partialmanifeststore_test.go covers the Postgres soft-delete
// idempotency in isolation; neither drives the catalog-decorated
// release against a real object store. This test closes that gap: it
// verifies that the resume cleanup soft-deletes every confirmed
// chunk's artifact_store row through the cataloging decorator and
// deletes the object via a real HardDeleteObject, then soft-deletes
// the manifest row, and that a second (GC-backstop) cleanup is
// idempotent across all three stores.
package stores_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/blobstore/artifactcatalog"
	"github.com/lennylabs/lenny/pkg/blobstore/cataloging"
	"github.com/lennylabs/lenny/pkg/blobstore/miniostore"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/checkpointer"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
	partialmanifestpg "github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore/pgstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// recordingCleanupMetrics captures the lenny_partial_manifest_cleanup_total
// outcome labels emitted by CleanupPartialManifest.
type recordingCleanupMetrics struct {
	outcomes []string
}

func (m *recordingCleanupMetrics) IncPartialManifestCleanup(outcome string) {
	m.outcomes = append(m.outcomes, outcome)
}

// countObjectsUnderPrefix returns how many objects remain under the
// prefix in the real bucket.
func countObjectsUnderPrefix(t *testing.T, mc *containers.MinIO, prefix string) int {
	t.Helper()
	n := 0
	for obj := range mc.Client.ListObjects(context.Background(), mc.Bucket, minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: true,
	}) {
		if obj.Err != nil {
			t.Fatalf("list under %q: %v", prefix, obj.Err)
		}
		n++
	}
	return n
}

// spec: §4.4 line 236 / §12.5 GC rule 4 — "On resume — whether the partial
// reconstruction succeeds or fails — the gateway MUST delete every chunk object
// listed under the manifest's chunk_object_key_prefix via per-key DeleteObject
// calls, then soft-delete the Postgres row"; the catalog soft-delete is the
// sole mechanism that releases a confirmed chunk's bytes.
// diagnosis: a failure means the resume cleanup does not release confirmed
// chunks' artifact_store rows (leaving their bytes charged to the tenant
// forever) or does not remove the chunk objects, or the cross-store cleanup is
// not idempotent. Against the pre-fix code, which deleted objects with a plain
// prefix delete around the catalog, the confirmed chunk rows stay live and this
// test's catalog-soft-delete assertion fails.
func TestPartialManifestResumeCleanupReleasesChunksAcrossRealBackends(t *testing.T) {
	t.Parallel()
	_, pg := startStore(t)
	mc := containers.StartMinIO(t, containers.MinIOOptions{})
	store := partialmanifestpg.New(pg.Pool, nil)
	objStore, err := miniostore.New(miniostore.Config{
		Endpoint:  mc.Endpoint,
		AccessKey: mc.AccessKey,
		SecretKey: mc.SecretKey,
		Bucket:    mc.Bucket,
	})
	if err != nil {
		t.Fatalf("miniostore.New: %v", err)
	}
	catalog := artifactcatalog.New(pg.Pool, func() time.Time { return time.Now().UTC() })
	release := cataloging.New(objStore, catalog, cataloging.Options{})
	ctx := context.Background()

	tenant := freshTenant(t, ctx, pg)
	sessID := newUUID(t)
	checkpointID := newUUID(t)
	// Object keys carry no leading slash so they match how the store
	// lays blobs out under `{tenant}/checkpoints/...`.
	prefix := tenant + "/checkpoints/" + sessID + "/" + checkpointID + "/"

	// Upload two committed chunk objects (chunk-{n}.tar) to real MinIO and
	// insert their live artifact_store catalog rows, mirroring the §10.1
	// single-part PutObject chunk-commit plus §12.5 cataloging.
	chunkURIs := make([]blobstore.URI, 2)
	for i := 0; i < 2; i++ {
		u := blobstore.URI{
			TenantID:   tenant,
			ObjectType: blobstore.ObjectTypeCheckpoint,
			SessionID:  sessID,
			PartID:     checkpointID + "/chunk-" + string(rune('0'+i)) + ".tar",
		}
		chunkURIs[i] = u
		key := prefix + "chunk-" + string(rune('0'+i)) + ".tar"
		body := []byte("chunk:" + key)
		if _, err := mc.Client.PutObject(ctx, mc.Bucket, key,
			bytes.NewReader(body), int64(len(body)), minio.PutObjectOptions{}); err != nil {
			t.Fatalf("PutObject %q: %v", key, err)
		}
		if err := catalog.Insert(ctx, artifactcatalog.Record{
			URI:          u.String(),
			TenantID:     tenant,
			SessionID:    sessID,
			PartID:       u.PartID,
			SizeBytes:    int64(len(body)),
			ArtifactType: artifactcatalog.ArtifactTypeCheckpoint,
		}); err != nil {
			t.Fatalf("Insert catalog row %q: %v", u.String(), err)
		}
	}
	if got := countObjectsUnderPrefix(t, mc, prefix); got != 2 {
		t.Fatalf("precondition: %d objects under prefix, want 2", got)
	}

	// Seed the matching active manifest row in real Postgres.
	now := time.Now().UTC()
	record := partialmanifeststore.Record{
		TenantID:             tenant,
		CheckpointID:         checkpointID,
		SessionID:            sessID,
		ChunkObjectKeyPrefix: prefix,
		ChunkSizeBytes:       16 << 20,
		ChunkEncoding:        partialmanifeststore.ChunkEncodingTar,
		CheckpointStartedAt:  now,
		CheckpointTimeoutAt:  now.Add(90 * time.Second),
	}
	if err := store.Put(ctx, record); err != nil {
		t.Fatalf("Put manifest: %v", err)
	}

	metrics := &recordingCleanupMetrics{}

	// Primary resume-path cleanup: releases each confirmed chunk's catalog
	// row, deletes the object in MinIO, then soft-deletes the Postgres row.
	if err := checkpointer.CleanupPartialManifest(ctx, store, release, objStore, time.Hour, record, metrics, false); err != nil {
		t.Fatalf("primary CleanupPartialManifest: %v", err)
	}
	if got := countObjectsUnderPrefix(t, mc, prefix); got != 0 {
		t.Errorf("after cleanup: %d chunk objects remain under prefix, want 0", got)
	}
	for _, u := range chunkURIs {
		row, err := catalog.Get(ctx, u.String())
		if err != nil {
			t.Fatalf("Get catalog row %q after cleanup: %v", u.String(), err)
		}
		if row.State == artifactcatalog.StateLive {
			t.Errorf("catalog row %q still live after cleanup; its bytes stay charged to the tenant", u.String())
		}
	}
	afterFirst, err := store.Get(ctx, tenant, checkpointID)
	if err != nil {
		t.Fatalf("Get after primary cleanup: %v", err)
	}
	if afterFirst.DeletedAt.IsZero() {
		t.Error("primary cleanup did not soft-delete the manifest row")
	}
	if len(metrics.outcomes) != 1 || metrics.outcomes[0] != string(checkpointer.PartialCleanupSuccess) {
		t.Errorf("metric outcomes after primary cleanup = %v, want [success]", metrics.outcomes)
	}

	// Second cleanup modeling the §12.5 GC backstop racing the resume path.
	// MinIO delete-on-absent makes the now-empty prefix a no-op, each catalog
	// row is already soft-deleted (no second decrement), and the Postgres
	// soft-delete hits rows_affected==0 under the `deleted_at IS NULL` guard,
	// so no error is raised and the tombstone is unchanged.
	if err := checkpointer.CleanupPartialManifest(ctx, store, release, objStore, time.Hour, record, metrics, true); err != nil {
		t.Fatalf("GC-backstop CleanupPartialManifest: %v", err)
	}
	afterSecond, err := store.Get(ctx, tenant, checkpointID)
	if err != nil {
		t.Fatalf("Get after GC-backstop cleanup: %v", err)
	}
	if !afterSecond.DeletedAt.Equal(afterFirst.DeletedAt) {
		t.Errorf("second cleanup mutated the tombstone: got %v, want stable %v",
			afterSecond.DeletedAt, afterFirst.DeletedAt)
	}
	if len(metrics.outcomes) != 2 || metrics.outcomes[1] != string(checkpointer.PartialCleanupGCCollected) {
		t.Errorf("metric outcomes after GC-backstop cleanup = %v, want [success gc_collected]", metrics.outcomes)
	}
}
