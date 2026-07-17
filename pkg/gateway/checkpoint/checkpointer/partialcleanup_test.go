// SPDX-License-Identifier: MIT

package checkpointer_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/blobstore/artifactcatalog"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/checkpointer"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
)

// fakeChunkCatalog is an in-memory ChunkCatalogReleaser. SoftDeleteRow models
// the §12.5 rule 4 exactly-once decrement by releasing a live row's bytes once
// and no-oping on a replay; a URI with no seeded row (an uncounted straggler)
// releases nothing.
type fakeChunkCatalog struct {
	mu      sync.Mutex
	rows    []artifactcatalog.Record
	deleted map[string]bool
	freed   int64
	softErr error
}

func newFakeChunkCatalog(rows ...artifactcatalog.Record) *fakeChunkCatalog {
	return &fakeChunkCatalog{rows: rows, deleted: map[string]bool{}}
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

// fakeObjectStore is an in-memory ChunkObjectStore. ListByPrefix returns the
// seeded objects whose key falls under the walked prefix (the checkpoint's
// chunk_object_key_prefix); HardDeleteObject records the key. It models the
// prefix walk the release uses to discover a checkpoint's chunk objects,
// including a straggler an outstanding grant landed under the prefix with no
// catalog row.
type fakeObjectStore struct {
	mu        sync.Mutex
	objects   []blobstore.URI
	deleted   []string
	listErr   error
	deleteErr error
}

func newFakeObjectStore(objects ...blobstore.URI) *fakeObjectStore {
	return &fakeObjectStore{objects: objects}
}

func (f *fakeObjectStore) ListByPrefix(_ context.Context, u blobstore.URI) ([]blobstore.BlobInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	var out []blobstore.BlobInfo
	for _, o := range f.objects {
		if o.TenantID == u.TenantID && o.SessionID == u.SessionID &&
			strings.HasPrefix(o.PartID, u.PartID) {
			out = append(out, blobstore.BlobInfo{URI: o})
		}
	}
	return out, nil
}

func (f *fakeObjectStore) HardDeleteObject(u blobstore.URI) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deleted = append(f.deleted, u.PartID)
	return nil
}

// chunkURI builds the object URI for one checkpoint chunk, matching the key
// the upload path writes and the catalog row records.
func chunkURI(tenantID, sessionID, checkpointID string, idx int) blobstore.URI {
	return blobstore.URI{
		TenantID:   tenantID,
		ObjectType: blobstore.ObjectTypeCheckpoint,
		SessionID:  sessionID,
		PartID:     fmt.Sprintf("%s/chunk-%05d.tar", checkpointID, idx),
		TTL:        30 * time.Second,
	}
}

// chunkRow builds a live checkpoint artifact_store row for one chunk, keyed on
// the same URI chunkURI produces.
func chunkRow(tenantID, sessionID, checkpointID string, idx int, size int64) artifactcatalog.Record {
	u := chunkURI(tenantID, sessionID, checkpointID, idx)
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
// manifest row. The chunk set is discovered by walking the target
// checkpoint's chunk_object_key_prefix, so a sibling checkpoint's objects
// (under a different prefix) are left untouched.
func TestReleaseCheckpointChunksReleasesRowsObjectsAndManifest(t *testing.T) {
	store := partialmanifeststore.NewMemoryStore(nil)
	rec := seedCompleteManifest(t, store, "acme", "cp-old", "s1")
	// Keep a second checkpoint's manifest so its chunks must survive.
	seedCompleteManifest(t, store, "acme", "cp-keep", "s1")

	catalog := newFakeChunkCatalog(
		chunkRow("acme", "s1", "cp-old", 0, 100),
		chunkRow("acme", "s1", "cp-old", 1, 250),
		chunkRow("acme", "s1", "cp-keep", 0, 999),
	)
	// Both checkpoints' objects live in the bucket; only cp-old's prefix is
	// walked, so cp-keep's object must survive.
	objects := newFakeObjectStore(
		chunkURI("acme", "s1", "cp-old", 0),
		chunkURI("acme", "s1", "cp-old", 1),
		chunkURI("acme", "s1", "cp-keep", 0),
	)

	if err := checkpointer.ReleaseCheckpointChunks(context.Background(), checkpointer.CheckpointChunkRelease{
		Manifests:          store,
		Catalog:            catalog,
		Objects:            objects,
		TombstoneRetention: time.Hour,
	}, rec); err != nil {
		t.Fatalf("ReleaseCheckpointChunks: %v", err)
	}

	// (1) Exactly cp-old's two chunk rows are soft-deleted, releasing 350
	// bytes; cp-keep's row is untouched.
	if got := catalog.freed; got != 350 {
		t.Errorf("released bytes = %d, want 350 (only cp-old's chunks)", got)
	}
	if got := len(catalog.softDeleted()); got != 2 {
		t.Errorf("soft-deleted rows = %d (%v), want 2", got, catalog.softDeleted())
	}
	// (2) Both cp-old objects are hard-deleted per key; cp-keep's is not.
	if len(objects.deleted) != 2 {
		t.Fatalf("deleted objects = %v, want the two cp-old chunks", objects.deleted)
	}
	for _, part := range objects.deleted {
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

// spec: §12.5 GC rule 5 — the release discovers a checkpoint's objects by
// walking its chunk_object_key_prefix, so an object landed under the prefix
// with no artifact_store row (a grant that outlived the finalising deadline)
// is still deleted. A confirmed-catalog-set enumeration would miss the
// straggler and orphan it once the manifest row is soft-deleted and the prefix
// drops out of every reclaimer's selector.
func TestReleaseCheckpointChunksDeletesUncataloguedStraggler(t *testing.T) {
	store := partialmanifeststore.NewMemoryStore(nil)
	rec := seedCompleteManifest(t, store, "acme", "cp-old", "s1")
	// Only chunk 0 carries a catalog row; chunk 1 is an uncounted straggler an
	// outstanding grant landed under the prefix.
	catalog := newFakeChunkCatalog(chunkRow("acme", "s1", "cp-old", 0, 100))
	objects := newFakeObjectStore(
		chunkURI("acme", "s1", "cp-old", 0),
		chunkURI("acme", "s1", "cp-old", 1),
	)

	if err := checkpointer.ReleaseCheckpointChunks(context.Background(), checkpointer.CheckpointChunkRelease{
		Manifests:          store,
		Catalog:            catalog,
		Objects:            objects,
		TombstoneRetention: time.Hour,
	}, rec); err != nil {
		t.Fatalf("ReleaseCheckpointChunks: %v", err)
	}

	// Both objects are deleted, including the straggler the confirmed-catalog
	// set would have missed.
	if len(objects.deleted) != 2 {
		t.Fatalf("deleted objects = %v, want chunk-0 and the straggler chunk-1", objects.deleted)
	}
	// Only chunk 0's bytes are released; the straggler has no row, so its
	// guarded soft-delete issues no decrement.
	if catalog.freed != 100 {
		t.Errorf("released bytes = %d, want 100 (only the catalogued chunk)", catalog.freed)
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
	objects := newFakeObjectStore(
		chunkURI("acme", "s1", "cp-old", 0),
		chunkURI("acme", "s1", "cp-old", 1),
	)
	cfg := checkpointer.CheckpointChunkRelease{Manifests: store, Catalog: catalog, Objects: objects, TombstoneRetention: time.Hour}

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
		checkpointer.CheckpointChunkRelease{Catalog: newFakeChunkCatalog(), Objects: newFakeObjectStore()}, rec); err == nil {
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
