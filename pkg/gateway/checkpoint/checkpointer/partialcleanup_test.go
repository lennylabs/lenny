// SPDX-License-Identifier: MIT

package checkpointer_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/blobstore/artifactcatalog"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/checkpointer"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
)

// fakeChunkCatalog is an in-memory ChunkCatalogReleaser. ListBySession
// returns the seeded rows for a session; SoftDeleteRow models the §12.5
// rule 4 exactly-once decrement by releasing a live row's bytes once and
// no-oping on a replay.
type fakeChunkCatalog struct {
	mu      sync.Mutex
	rows    []artifactcatalog.Record
	deleted map[string]bool
	freed   int64
	listErr error
	softErr error
}

func newFakeChunkCatalog(rows ...artifactcatalog.Record) *fakeChunkCatalog {
	return &fakeChunkCatalog{rows: rows, deleted: map[string]bool{}}
}

func (f *fakeChunkCatalog) ListBySession(_ context.Context, tenantID, sessionID string) ([]artifactcatalog.Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	var out []artifactcatalog.Record
	for _, r := range f.rows {
		if r.TenantID == tenantID && r.SessionID == sessionID {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeChunkCatalog) SoftDeleteRow(_ context.Context, uri string, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.softErr != nil {
		return f.softErr
	}
	if f.deleted[uri] {
		// Idempotent: a second release of the same row issues no decrement.
		return nil
	}
	for _, r := range f.rows {
		if r.URI == uri && r.State == artifactcatalog.StateLive {
			f.freed += r.SizeBytes
			break
		}
	}
	f.deleted[uri] = true
	return nil
}

func (f *fakeChunkCatalog) softDeleted() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.deleted))
	for uri := range f.deleted {
		out = append(out, uri)
	}
	return out
}

// fakeObjectDeleter records every HardDeleteObject key.
type fakeObjectDeleter struct {
	mu      sync.Mutex
	deleted []string
	err     error
}

func (d *fakeObjectDeleter) HardDeleteObject(u blobstore.URI) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.err != nil {
		return d.err
	}
	d.deleted = append(d.deleted, u.PartID)
	return nil
}

// chunkRow builds a live checkpoint artifact_store row for one chunk.
func chunkRow(tenantID, sessionID, checkpointID string, idx int, size int64) artifactcatalog.Record {
	u := blobstore.URI{
		TenantID:   tenantID,
		ObjectType: blobstore.ObjectTypeCheckpoint,
		SessionID:  sessionID,
		PartID:     fmt.Sprintf("%s/chunk-%05d.tar", checkpointID, idx),
		TTL:        30 * time.Second,
	}
	return artifactcatalog.Record{
		URI:          u.String(),
		TenantID:     tenantID,
		SessionID:    sessionID,
		PartID:       u.PartID,
		SizeBytes:    size,
		State:        artifactcatalog.StateLive,
		ArtifactType: artifactcatalog.ArtifactTypeCheckpoint,
	}
}

// seedCompleteManifest seeds a finalised complete (partial = false) manifest
// row for checkpointID and returns it.
func seedCompleteManifest(t *testing.T, store partialmanifeststore.Store, tenant, checkpointID, session string) partialmanifeststore.Record {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	if err := store.Put(ctx, partialmanifeststore.Record{
		TenantID:             tenant,
		CheckpointID:         checkpointID,
		SessionID:            session,
		ChunkObjectKeyPrefix: fmt.Sprintf("/%s/checkpoints/%s/%s/", tenant, session, checkpointID),
		ChunkSizeBytes:       16 << 20,
		ChunkEncoding:        partialmanifeststore.ChunkEncodingTar,
		CheckpointStartedAt:  now,
		CheckpointTimeoutAt:  now.Add(90 * time.Second),
	}); err != nil {
		t.Fatalf("Put manifest: %v", err)
	}
	// Confirm a chunk so Finalise does not soft-delete an empty manifest.
	if err := store.ConfirmChunk(ctx, tenant, checkpointID, 0, 100); err != nil {
		t.Fatalf("ConfirmChunk: %v", err)
	}
	if err := store.Finalise(ctx, tenant, checkpointID, false, partialmanifeststore.ReasonComplete); err != nil {
		t.Fatalf("Finalise: %v", err)
	}
	rec, err := store.Get(ctx, tenant, checkpointID)
	if err != nil {
		t.Fatalf("Get manifest: %v", err)
	}
	return rec
}

// stubChunkDeleter captures every DeleteByPrefix call.
type stubChunkDeleter struct {
	mu       sync.Mutex
	prefixes []string
	err      error
}

func (d *stubChunkDeleter) DeleteByPrefix(_ context.Context, prefix string) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.prefixes = append(d.prefixes, prefix)
	if d.err != nil {
		return 0, d.err
	}
	return 1, nil
}

// stubMetrics captures every IncPartialManifestCleanup call.
type stubMetrics struct {
	mu       sync.Mutex
	outcomes []string
}

func (s *stubMetrics) IncPartialManifestCleanup(outcome string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.outcomes = append(s.outcomes, outcome)
}

// seedManifest puts a single active manifest row and returns it.
func seedManifest(t *testing.T, store partialmanifeststore.Store, tenant, checkpointID, session, prefix string) partialmanifeststore.Record {
	t.Helper()
	now := time.Now().UTC()
	r := partialmanifeststore.Record{
		TenantID:             tenant,
		CheckpointID:         checkpointID,
		SessionID:            session,
		ChunkObjectKeyPrefix: prefix,
		ChunkSizeBytes:       16 << 20,
		ChunkEncoding:        partialmanifeststore.ChunkEncodingTar,
		CheckpointStartedAt:  now,
		CheckpointTimeoutAt:  now.Add(90 * time.Second),
	}
	if err := store.Put(context.Background(), r); err != nil {
		t.Fatalf("Put: %v", err)
	}
	return r
}

// spec: §12.5 GC rule 4 / rule 5 — the three-step release soft-deletes
// every chunk's artifact_store row (issuing the exactly-once decrement),
// hard-deletes each chunk object per key, and soft-deletes the finalised
// manifest row. Only the target checkpoint's chunks are released; a sibling
// checkpoint's rows and a non-checkpoint artifact are left untouched.
func TestReleaseCheckpointChunksReleasesRowsObjectsAndManifest(t *testing.T) {
	store := partialmanifeststore.NewMemoryStore(nil)
	rec := seedCompleteManifest(t, store, "acme", "cp-old", "s1")
	// Keep a second checkpoint's manifest so its chunks must survive.
	seedCompleteManifest(t, store, "acme", "cp-keep", "s1")

	catalog := newFakeChunkCatalog(
		chunkRow("acme", "s1", "cp-old", 0, 100),
		chunkRow("acme", "s1", "cp-old", 1, 250),
		chunkRow("acme", "s1", "cp-keep", 0, 999),
		// A non-checkpoint artifact under the same session (e.g. a
		// workspace snapshot) must not be released by the checkpoint sweep.
		artifactcatalog.Record{URI: "lenny-blob://acme/workspace/s1/snap?ttl=3600&enc=aes256gcm",
			TenantID: "acme", SessionID: "s1", PartID: "snap", SizeBytes: 7,
			State: artifactcatalog.StateLive, ArtifactType: artifactcatalog.ArtifactTypeWorkspace},
	)
	deleter := &fakeObjectDeleter{}

	if err := checkpointer.ReleaseCheckpointChunks(context.Background(), checkpointer.CheckpointChunkRelease{
		Manifests:          store,
		Catalog:            catalog,
		Objects:            deleter,
		TombstoneRetention: time.Hour,
	}, rec); err != nil {
		t.Fatalf("ReleaseCheckpointChunks: %v", err)
	}

	// (1) Exactly cp-old's two chunk rows are soft-deleted, releasing 350
	// bytes; cp-keep and the workspace row are untouched.
	if got := catalog.freed; got != 350 {
		t.Errorf("released bytes = %d, want 350 (only cp-old's chunks)", got)
	}
	if got := len(catalog.softDeleted()); got != 2 {
		t.Errorf("soft-deleted rows = %d (%v), want 2", got, catalog.softDeleted())
	}
	// (2) Both cp-old objects are hard-deleted per key; nothing else is.
	if len(deleter.deleted) != 2 {
		t.Fatalf("deleted objects = %v, want the two cp-old chunks", deleter.deleted)
	}
	for _, part := range deleter.deleted {
		if part != "cp-old/chunk-00000.tar" && part != "cp-old/chunk-00001.tar" {
			t.Errorf("unexpected object deleted: %q", part)
		}
	}
	// (3) The finalised manifest row is soft-deleted; cp-keep survives.
	old, _ := store.Get(context.Background(), "acme", "cp-old")
	if old.DeletedAt.IsZero() {
		t.Error("release did not soft-delete the finalised manifest row")
	}
	keep, _ := store.Get(context.Background(), "acme", "cp-keep")
	if !keep.DeletedAt.IsZero() {
		t.Error("release soft-deleted an unrelated checkpoint's manifest row")
	}
}

// spec: §12.5 GC rule 4 — the release is idempotent: a re-run over the same
// checkpoint issues no second decrement (the exactly-once guarantee).
func TestReleaseCheckpointChunksIsIdempotent(t *testing.T) {
	store := partialmanifeststore.NewMemoryStore(nil)
	rec := seedCompleteManifest(t, store, "acme", "cp-old", "s1")
	catalog := newFakeChunkCatalog(
		chunkRow("acme", "s1", "cp-old", 0, 100),
		chunkRow("acme", "s1", "cp-old", 1, 250),
	)
	deleter := &fakeObjectDeleter{}
	cfg := checkpointer.CheckpointChunkRelease{Manifests: store, Catalog: catalog, Objects: deleter, TombstoneRetention: time.Hour}

	for i := 0; i < 3; i++ {
		if err := checkpointer.ReleaseCheckpointChunks(context.Background(), cfg, rec); err != nil {
			t.Fatalf("release %d: %v", i, err)
		}
	}
	if catalog.freed != 350 {
		t.Errorf("released bytes after three runs = %d, want 350 (exactly once)", catalog.freed)
	}
}

// spec: §12.5 GC rule 5 — a partially-wired release (missing seam) is
// rejected so the caller can degrade to leaving the row for the backstop.
func TestReleaseCheckpointChunksRequiresEverySeam(t *testing.T) {
	store := partialmanifeststore.NewMemoryStore(nil)
	rec := seedCompleteManifest(t, store, "acme", "cp-old", "s1")
	if err := checkpointer.ReleaseCheckpointChunks(context.Background(),
		checkpointer.CheckpointChunkRelease{Catalog: newFakeChunkCatalog(), Objects: &fakeObjectDeleter{}}, rec); err == nil {
		t.Error("release accepted a nil manifest store")
	}
}

// spec: §4.4 line 236 — successful cleanup deletes every chunk under
// the prefix and soft-deletes the manifest row.
func TestCleanupPartialManifestSuccess(t *testing.T) {
	store := partialmanifeststore.NewMemoryStore(nil)
	r := seedManifest(t, store, "acme", "cp-1", "s1", "/acme/checkpoints/s1/cp-1/")
	deleter := &stubChunkDeleter{}
	metrics := &stubMetrics{}

	if err := checkpointer.CleanupPartialManifest(context.Background(), store, deleter, r, metrics, false); err != nil {
		t.Fatalf("CleanupPartialManifest: %v", err)
	}
	if len(deleter.prefixes) != 1 || deleter.prefixes[0] != r.ChunkObjectKeyPrefix {
		t.Errorf("chunk deletion prefixes = %v, want [%q]", deleter.prefixes, r.ChunkObjectKeyPrefix)
	}

	row, _ := store.Get(context.Background(), "acme", "cp-1")
	if row.DeletedAt.IsZero() {
		t.Error("cleanup did not soft-delete the manifest row")
	}
	if len(metrics.outcomes) != 1 || metrics.outcomes[0] != string(checkpointer.PartialCleanupSuccess) {
		t.Errorf("metric outcomes = %v, want [success]", metrics.outcomes)
	}
}

// spec: §4.4 line 236 — a MinIO failure leaves the row active so the
// §12.5 backstop sweep can retry on the next cycle.
func TestCleanupPartialManifestMinIOFailure(t *testing.T) {
	store := partialmanifeststore.NewMemoryStore(nil)
	r := seedManifest(t, store, "acme", "cp-1", "s1", "/acme/checkpoints/s1/cp-1/")
	deleter := &stubChunkDeleter{err: errors.New("minio down")}
	metrics := &stubMetrics{}

	if err := checkpointer.CleanupPartialManifest(context.Background(), store, deleter, r, metrics, false); err == nil {
		t.Fatal("expected an error when the chunk deleter fails")
	}
	row, _ := store.Get(context.Background(), "acme", "cp-1")
	if !row.DeletedAt.IsZero() {
		t.Error("cleanup soft-deleted the row despite the MinIO failure")
	}
	if len(metrics.outcomes) != 1 || metrics.outcomes[0] != string(checkpointer.PartialCleanupFailedDeleted) {
		t.Errorf("metric outcomes = %v, want [failed_deleted]", metrics.outcomes)
	}
}

// spec: §4.4 line 236 — `deleted_at IS NULL` is the idempotency
// guard. Repeated cleanup invocations converge to a single
// state mutation; the second writer observes the row already
// soft-deleted.
func TestCleanupPartialManifestIsIdempotent(t *testing.T) {
	store := partialmanifeststore.NewMemoryStore(nil)
	r := seedManifest(t, store, "acme", "cp-1", "s1", "/acme/checkpoints/s1/cp-1/")
	metrics := &stubMetrics{}

	for i := 0; i < 3; i++ {
		if err := checkpointer.CleanupPartialManifest(context.Background(), store, &stubChunkDeleter{}, r, metrics, false); err != nil {
			t.Fatalf("cleanup %d: %v", i, err)
		}
	}
	// All three calls succeed; the metric records three success
	// outcomes because the cleanup path is at-least-once-safe by
	// design (idempotent on the second writer).
	for i, outcome := range metrics.outcomes {
		if outcome != string(checkpointer.PartialCleanupSuccess) {
			t.Errorf("invocation %d: outcome %q, want success", i, outcome)
		}
	}
}

// spec: §4.4 line 236 — the §12.5 backstop sweep emits
// `gc_collected` rather than `success` so operators can distinguish
// resume-path cleanups from GC-backstop cleanups.
func TestCleanupPartialManifestGCCollectedOutcome(t *testing.T) {
	store := partialmanifeststore.NewMemoryStore(nil)
	r := seedManifest(t, store, "acme", "cp-1", "s1", "/acme/checkpoints/s1/cp-1/")
	metrics := &stubMetrics{}

	if err := checkpointer.CleanupPartialManifest(context.Background(), store, &stubChunkDeleter{}, r, metrics, true); err != nil {
		t.Fatalf("CleanupPartialManifest: %v", err)
	}
	if len(metrics.outcomes) != 1 || metrics.outcomes[0] != string(checkpointer.PartialCleanupGCCollected) {
		t.Errorf("metric outcomes = %v, want [gc_collected]", metrics.outcomes)
	}
}

// spec: §4.4 line 236 — a nil deleter is honored as a "skip MinIO"
// path (tests / dev-mode); the row is still soft-deleted.
func TestCleanupPartialManifestNilDeleterSkipsMinIO(t *testing.T) {
	store := partialmanifeststore.NewMemoryStore(nil)
	r := seedManifest(t, store, "acme", "cp-1", "s1", "/acme/checkpoints/s1/cp-1/")
	metrics := &stubMetrics{}

	if err := checkpointer.CleanupPartialManifest(context.Background(), store, nil, r, metrics, false); err != nil {
		t.Fatalf("CleanupPartialManifest: %v", err)
	}
	row, _ := store.Get(context.Background(), "acme", "cp-1")
	if row.DeletedAt.IsZero() {
		t.Error("row was not soft-deleted when deleter is nil")
	}
}

// spec: §10.1 line 141 — input validation: an empty tenant id,
// checkpoint id, or chunk_object_key_prefix is a programming error the
// cleanup path rejects before touching either store.
func TestCleanupPartialManifestRejectsEmptyFields(t *testing.T) {
	store := partialmanifeststore.NewMemoryStore(nil)
	cases := []partialmanifeststore.Record{
		{CheckpointID: "cp-1", ChunkObjectKeyPrefix: "/x/"},
		{TenantID: "acme", ChunkObjectKeyPrefix: "/x/"},
		{TenantID: "acme", CheckpointID: "cp-1"},
	}
	for _, r := range cases {
		if err := checkpointer.CleanupPartialManifest(context.Background(), store, nil, r, nil, false); err == nil {
			t.Errorf("cleanup accepted record with empty fields: %+v", r)
		}
	}
}
