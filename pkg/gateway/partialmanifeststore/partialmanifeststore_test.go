// SPDX-License-Identifier: MIT

package partialmanifeststore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/partialmanifeststore"
)

// spec: §4.4 line 234 (the partial manifest is the recovery-aid row
// the gateway writes when an eviction checkpoint exceeds the preStop
// tiered cap). The minimal Put + Get round-trip must persist every
// spec-mandated field.
func TestPutAndGet(t *testing.T) {
	clock := time.Date(2026, 5, 22, 13, 0, 0, 0, time.UTC)
	store := partialmanifeststore.NewMemoryStore(func() time.Time { return clock })

	r := partialmanifeststore.Record{
		TenantID:               "acme",
		SessionID:              "sess_42",
		Generation:             7,
		PartialObjectKeyPrefix: "/acme/checkpoints/sess_42/partial/ck-7/",
		ChunkEncoding:          partialmanifeststore.ChunkEncodingTarGz,
	}
	if err := store.Put(context.Background(), r); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := store.Get(context.Background(), "acme", "sess_42", 7)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.PartialObjectKeyPrefix != r.PartialObjectKeyPrefix {
		t.Errorf("PartialObjectKeyPrefix = %q, want %q", got.PartialObjectKeyPrefix, r.PartialObjectKeyPrefix)
	}
	if got.ChunkEncoding != partialmanifeststore.ChunkEncodingTarGz {
		t.Errorf("ChunkEncoding = %q, want tar.gz", got.ChunkEncoding)
	}
	if !got.CreatedAt.Equal(clock) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, clock)
	}
	if !got.DeletedAt.IsZero() {
		t.Errorf("DeletedAt = %v, want zero on an active row", got.DeletedAt)
	}
}

// spec: §4.4 line 236 — partial-manifest cleanup soft-deletes the row
// rather than hard-deleting. The §12.5 backstop sweep races the
// primary cleanup path; the second writer must observe the row
// already soft-deleted and skip side effects.
func TestSoftDeleteIsIdempotent(t *testing.T) {
	clock := time.Date(2026, 5, 22, 13, 0, 0, 0, time.UTC)
	store := partialmanifeststore.NewMemoryStore(func() time.Time { return clock })

	_ = store.Put(context.Background(), partialmanifeststore.Record{
		TenantID: "acme", SessionID: "s1", Generation: 1,
		PartialObjectKeyPrefix: "/acme/checkpoints/s1/partial/ck-1/",
		ChunkEncoding:          partialmanifeststore.ChunkEncodingTar,
	})

	clock = clock.Add(time.Minute)
	if err := store.SoftDelete(context.Background(), "acme", "s1", 1); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}
	got, _ := store.Get(context.Background(), "acme", "s1", 1)
	first := got.DeletedAt
	if first.IsZero() {
		t.Fatal("SoftDelete did not stamp DeletedAt")
	}

	// Replay: second writer observes the row already soft-deleted
	// and does not bump the timestamp.
	clock = clock.Add(time.Minute)
	if err := store.SoftDelete(context.Background(), "acme", "s1", 1); err != nil {
		t.Fatalf("Replay SoftDelete: %v", err)
	}
	got, _ = store.Get(context.Background(), "acme", "s1", 1)
	if !got.DeletedAt.Equal(first) {
		t.Errorf("DeletedAt = %v, want stable %v across replays", got.DeletedAt, first)
	}

	// SoftDelete on a missing row is also idempotent.
	if err := store.SoftDelete(context.Background(), "acme", "missing", 1); err != nil {
		t.Errorf("SoftDelete on missing: %v", err)
	}
}

// spec: §4.4 lines 234 / 236 — the resume path selects the highest
// active generation for (tenant, session) so a late-committed
// older-generation row cannot win against a fenced newer-generation
// writer.
func TestLatestActiveReturnsHighestGeneration(t *testing.T) {
	store := partialmanifeststore.NewMemoryStore(nil)
	for _, gen := range []int64{1, 2, 5, 3} {
		_ = store.Put(context.Background(), partialmanifeststore.Record{
			TenantID: "acme", SessionID: "s1", Generation: gen,
			PartialObjectKeyPrefix: "/acme/checkpoints/s1/partial/ck/",
			ChunkEncoding:          partialmanifeststore.ChunkEncodingTar,
		})
	}
	got, err := store.LatestActive(context.Background(), "acme", "s1")
	if err != nil {
		t.Fatalf("LatestActive: %v", err)
	}
	if got.Generation != 5 {
		t.Errorf("LatestActive.Generation = %d, want 5", got.Generation)
	}
}

// spec: §4.4 line 236 — `deleted_at IS NULL` is the load-bearing
// idempotency guard; LatestActive must skip soft-deleted rows so a
// resume after cleanup does not re-attempt a stale manifest.
func TestLatestActiveSkipsSoftDeleted(t *testing.T) {
	store := partialmanifeststore.NewMemoryStore(nil)
	_ = store.Put(context.Background(), partialmanifeststore.Record{
		TenantID: "acme", SessionID: "s1", Generation: 5,
		PartialObjectKeyPrefix: "/acme/checkpoints/s1/partial/ck-5/",
		ChunkEncoding:          partialmanifeststore.ChunkEncodingTar,
	})
	_ = store.Put(context.Background(), partialmanifeststore.Record{
		TenantID: "acme", SessionID: "s1", Generation: 3,
		PartialObjectKeyPrefix: "/acme/checkpoints/s1/partial/ck-3/",
		ChunkEncoding:          partialmanifeststore.ChunkEncodingTar,
	})
	_ = store.SoftDelete(context.Background(), "acme", "s1", 5)

	got, _ := store.LatestActive(context.Background(), "acme", "s1")
	if got.Generation != 3 {
		t.Errorf("LatestActive skipped soft-deleted row, got generation %d, want 3", got.Generation)
	}

	// Soft-delete the last active row — LatestActive returns
	// ErrNotFound.
	_ = store.SoftDelete(context.Background(), "acme", "s1", 3)
	if _, err := store.LatestActive(context.Background(), "acme", "s1"); !errors.Is(err, partialmanifeststore.ErrNotFound) {
		t.Errorf("LatestActive with all rows soft-deleted: got %v, want ErrNotFound", err)
	}
}

// spec: §4.4 line 234 — a row with an empty `partial_object_key_prefix`
// is meaningless (the cleanup path cannot locate the chunks); reject
// at the store boundary.
func TestPutRejectsEmptyPrefix(t *testing.T) {
	store := partialmanifeststore.NewMemoryStore(nil)
	err := store.Put(context.Background(), partialmanifeststore.Record{
		TenantID: "acme", SessionID: "s1",
		ChunkEncoding: partialmanifeststore.ChunkEncodingTar,
	})
	if err == nil {
		t.Error("Put accepted empty partial_object_key_prefix")
	}
}

// spec: §10.1 — chunk_encoding is the closed enum {tar, tar.gz}. An
// invalid value cannot be silently silently coerced.
func TestPutRejectsInvalidChunkEncoding(t *testing.T) {
	store := partialmanifeststore.NewMemoryStore(nil)
	err := store.Put(context.Background(), partialmanifeststore.Record{
		TenantID: "acme", SessionID: "s1",
		PartialObjectKeyPrefix: "/acme/x/",
		ChunkEncoding:          "zip",
	})
	if err == nil {
		t.Error("Put accepted invalid chunk_encoding")
	}
}

// spec: §10.1 — chunk_encoding defaults to `tar` when the caller
// leaves it empty (the §4.4 fallback writer always sets a value, but
// tests and dev-mode writers should not silently fail).
func TestPutDefaultsChunkEncodingToTar(t *testing.T) {
	store := partialmanifeststore.NewMemoryStore(nil)
	if err := store.Put(context.Background(), partialmanifeststore.Record{
		TenantID: "acme", SessionID: "s1", Generation: 1,
		PartialObjectKeyPrefix: "/acme/x/",
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, _ := store.Get(context.Background(), "acme", "s1", 1)
	if got.ChunkEncoding != partialmanifeststore.ChunkEncodingTar {
		t.Errorf("default ChunkEncoding = %q, want tar", got.ChunkEncoding)
	}
}

// spec: §4.4 line 234 — empty tenant or session id is a programming
// error; reject at the store boundary so a malformed row cannot
// land in storage.
func TestPutRejectsEmptyIDs(t *testing.T) {
	store := partialmanifeststore.NewMemoryStore(nil)
	cases := []partialmanifeststore.Record{
		{SessionID: "s1", PartialObjectKeyPrefix: "/x/"},
		{TenantID: "acme", PartialObjectKeyPrefix: "/x/"},
		{PartialObjectKeyPrefix: "/x/"},
	}
	for _, r := range cases {
		if err := store.Put(context.Background(), r); err == nil {
			t.Errorf("Put accepted record with empty id: %+v", r)
		}
	}
}

// spec: §4.4 line 236 — re-Put on an already soft-deleted row is
// rejected because a partial manifest, once cleaned up, is terminal.
func TestPutRejectsSoftDeletedRow(t *testing.T) {
	store := partialmanifeststore.NewMemoryStore(nil)
	r := partialmanifeststore.Record{
		TenantID: "acme", SessionID: "s1", Generation: 1,
		PartialObjectKeyPrefix: "/acme/x/",
		ChunkEncoding:          partialmanifeststore.ChunkEncodingTar,
	}
	_ = store.Put(context.Background(), r)
	_ = store.SoftDelete(context.Background(), "acme", "s1", 1)
	if err := store.Put(context.Background(), r); err == nil {
		t.Error("Put on a soft-deleted row was not rejected")
	}
}

// spec: §12.5 backstop sweep — ListSoftDeletedBefore returns every
// soft-deleted row whose DeletedAt is older than the cutoff; the
// sweep walks the result to hard-prune.
func TestListSoftDeletedBeforeWalksTombstones(t *testing.T) {
	clock := time.Date(2026, 5, 22, 13, 0, 0, 0, time.UTC)
	store := partialmanifeststore.NewMemoryStore(func() time.Time { return clock })

	for _, gen := range []int64{1, 2, 3} {
		_ = store.Put(context.Background(), partialmanifeststore.Record{
			TenantID: "acme", SessionID: "s1", Generation: gen,
			PartialObjectKeyPrefix: "/acme/x/",
			ChunkEncoding:          partialmanifeststore.ChunkEncodingTar,
		})
	}
	// Soft-delete two rows at distinct timestamps.
	_ = store.SoftDelete(context.Background(), "acme", "s1", 1)
	clock = clock.Add(time.Hour)
	_ = store.SoftDelete(context.Background(), "acme", "s1", 2)

	cutoff := clock.Add(-30 * time.Minute) // between the two deletes
	got, err := store.ListSoftDeletedBefore(context.Background(), cutoff)
	if err != nil {
		t.Fatalf("ListSoftDeletedBefore: %v", err)
	}
	// Only the first soft-delete (at clock - 1h) is older than the cutoff.
	if len(got) != 1 || got[0].Generation != 1 {
		t.Errorf("ListSoftDeletedBefore = %+v, want generation 1 only", got)
	}
}

// spec: §12.8 — DeleteByUser removes every row tied to the supplied
// session ids.
func TestDeleteByUserScopes(t *testing.T) {
	store := partialmanifeststore.NewMemoryStore(nil)
	for _, sessID := range []string{"a", "b", "c"} {
		_ = store.Put(context.Background(), partialmanifeststore.Record{
			TenantID: "acme", SessionID: sessID, Generation: 1,
			PartialObjectKeyPrefix: "/acme/x/",
			ChunkEncoding:          partialmanifeststore.ChunkEncodingTar,
		})
	}
	if err := store.DeleteByUser(context.Background(), "acme", "alice", []string{"a", "c"}); err != nil {
		t.Fatalf("DeleteByUser: %v", err)
	}
	if _, err := store.Get(context.Background(), "acme", "a", 1); !errors.Is(err, partialmanifeststore.ErrNotFound) {
		t.Error("session 'a' should be removed")
	}
	if _, err := store.Get(context.Background(), "acme", "b", 1); err != nil {
		t.Errorf("session 'b' should survive: %v", err)
	}
	if _, err := store.Get(context.Background(), "acme", "c", 1); !errors.Is(err, partialmanifeststore.ErrNotFound) {
		t.Error("session 'c' should be removed")
	}
}

// spec: §12.8 — DeleteByTenant removes every row scoped to the tenant.
func TestDeleteByTenantSweepsAll(t *testing.T) {
	store := partialmanifeststore.NewMemoryStore(nil)
	for _, tid := range []string{"acme", "acme", "globex"} {
		_ = store.Put(context.Background(), partialmanifeststore.Record{
			TenantID: tid, SessionID: "s-" + tid, Generation: 1,
			PartialObjectKeyPrefix: "/" + tid + "/x/",
			ChunkEncoding:          partialmanifeststore.ChunkEncodingTar,
		})
	}
	if err := store.DeleteByTenant(context.Background(), "acme"); err != nil {
		t.Fatalf("DeleteByTenant: %v", err)
	}
	if _, err := store.Get(context.Background(), "acme", "s-acme", 1); !errors.Is(err, partialmanifeststore.ErrNotFound) {
		t.Error("acme rows should be removed")
	}
	if _, err := store.Get(context.Background(), "globex", "s-globex", 1); err != nil {
		t.Errorf("globex row should survive: %v", err)
	}
}

// spec: §12.5 — HardDelete removes the row entirely (called by the
// hard-prune sweep on rows past the tombstone retention window).
func TestHardDeleteRemovesRow(t *testing.T) {
	store := partialmanifeststore.NewMemoryStore(nil)
	_ = store.Put(context.Background(), partialmanifeststore.Record{
		TenantID: "acme", SessionID: "s1", Generation: 1,
		PartialObjectKeyPrefix: "/acme/x/",
		ChunkEncoding:          partialmanifeststore.ChunkEncodingTar,
	})
	if err := store.HardDelete(context.Background(), "acme", "s1", 1); err != nil {
		t.Fatalf("HardDelete: %v", err)
	}
	if _, err := store.Get(context.Background(), "acme", "s1", 1); !errors.Is(err, partialmanifeststore.ErrNotFound) {
		t.Error("HardDelete did not remove the row")
	}
}

// spec: §10.1 chunk_encoding closed enum.
func TestChunkEncodingValidValues(t *testing.T) {
	if !partialmanifeststore.ChunkEncodingTar.IsValid() {
		t.Error("ChunkEncodingTar should be valid")
	}
	if !partialmanifeststore.ChunkEncodingTarGz.IsValid() {
		t.Error("ChunkEncodingTarGz should be valid")
	}
	if partialmanifeststore.ChunkEncoding("zip").IsValid() {
		t.Error("zip should not be a valid chunk encoding")
	}
}
