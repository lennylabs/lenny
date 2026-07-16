//go:build component

// SPDX-License-Identifier: MIT

// Component test for the §4.4 partial-checkpoint manifest resume
// cleanup driven across BOTH real backends at once: a real MinIO
// container (chunk objects under chunk_object_key_prefix) and a
// real Postgres container (the manifest row). The tier1
// partialcleanup_test.go covers CleanupPartialManifest against a
// stubbed chunk deleter, and partialmanifeststore_test.go covers the
// Postgres soft-delete idempotency in isolation; neither drives a
// per-key DeleteObject against a real object store. This test closes
// that gap: it verifies that the resume cleanup deletes every chunk
// object via real DeleteObject calls, then soft-deletes the row, and
// that a second (GC-backstop) cleanup is idempotent across both
// stores — MinIO delete-on-absent returns without error on the now
// empty prefix and the Postgres soft-delete observes rows_affected==0
// and leaves the tombstone stable.
package stores_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"

	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/checkpointer"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
	partialmanifestpg "github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore/pgstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// minioPrefixDeleter is a real MinIO-backed checkpointer.PartialChunkDeleter.
// It walks the prefix with ListObjectsV2 semantics and issues a per-key
// RemoveObject (DeleteObject) for each object, exactly as §4.4 line 236
// requires ("delete every chunk object listed under the manifest's
// chunk_object_key_prefix via per-key DeleteObject calls"). MinIO's
// delete-on-absent semantics make repeated deletion on a vanished key a
// no-op, which is the independent idempotency the spec relies on.
type minioPrefixDeleter struct {
	client *minio.Client
	bucket string
}

func (d *minioPrefixDeleter) DeleteByPrefix(ctx context.Context, prefix string) (int, error) {
	deleted := 0
	for obj := range d.client.ListObjects(ctx, d.bucket, minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: true,
	}) {
		if obj.Err != nil {
			return deleted, obj.Err
		}
		if err := d.client.RemoveObject(ctx, d.bucket, obj.Key, minio.RemoveObjectOptions{}); err != nil {
			return deleted, err
		}
		deleted++
	}
	return deleted, nil
}

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

// spec: §4.4 line 236 — "On resume — whether the partial reconstruction
// succeeds or fails — the gateway MUST delete every chunk object listed
// under the manifest's chunk_object_key_prefix via per-key DeleteObject
// calls, then soft-delete the Postgres row ... a stale-leader retry, a
// crash-resumed resume path, or the §12.5 backstop sweep that races the
// primary cleanup all observe rows_affected == 0 on the second writer and
// correctly skip any side effects ... MinIO chunk deletion remains a
// hard-delete batch — MinIO's delete-on-absent semantics make it
// independently idempotent on repeated keys."
// diagnosis: a failure means the resume cleanup does not remove chunk
// objects from the real object store or does not soft-delete the manifest
// row, so partial-checkpoint chunks would be permanently orphaned, or the
// cross-store cleanup is not idempotent and a racing second writer would
// re-run side effects or error.
func TestPartialManifestResumeCleanupAcrossRealBackends(t *testing.T) {
	t.Parallel()
	_, pg := startStore(t)
	mc := containers.StartMinIO(t, containers.MinIOOptions{})
	store := partialmanifestpg.New(pg.Pool, nil)
	ctx := context.Background()

	tenant := freshTenant(t, ctx, pg)
	sessID := newUUID(t)
	checkpointID := newUUID(t)
	// Object keys carry no leading slash so they match how the store
	// lays blobs out under `{tenant}/...`.
	prefix := tenant + "/checkpoints/" + sessID + "/" + checkpointID + "/"

	// Upload two committed chunk objects (chunk-{n}.tar) to real MinIO,
	// mirroring the §10.1 single-part PutObject chunk-commit model.
	chunkKeys := []string{prefix + "chunk-0.tar", prefix + "chunk-1.tar"}
	for _, key := range chunkKeys {
		body := []byte("chunk:" + key)
		if _, err := mc.Client.PutObject(ctx, mc.Bucket, key,
			bytes.NewReader(body), int64(len(body)), minio.PutObjectOptions{}); err != nil {
			t.Fatalf("PutObject %q: %v", key, err)
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

	deleter := &minioPrefixDeleter{client: mc.Client, bucket: mc.Bucket}
	metrics := &recordingCleanupMetrics{}

	// Primary resume-path cleanup: deletes chunks in MinIO, then
	// soft-deletes the Postgres row.
	if err := checkpointer.CleanupPartialManifest(ctx, store, deleter, record, metrics, false); err != nil {
		t.Fatalf("primary CleanupPartialManifest: %v", err)
	}
	if got := countObjectsUnderPrefix(t, mc, prefix); got != 0 {
		t.Errorf("after cleanup: %d chunk objects remain under prefix, want 0", got)
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

	// Second cleanup modeling the §12.5 GC backstop racing the resume
	// path. MinIO delete-on-absent makes the now-empty prefix a no-op,
	// and the Postgres soft-delete hits rows_affected==0 under the
	// `deleted_at IS NULL` guard, so no error is raised, no chunk is
	// re-deleted, and the tombstone is unchanged.
	if err := checkpointer.CleanupPartialManifest(ctx, store, deleter, record, metrics, true); err != nil {
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
