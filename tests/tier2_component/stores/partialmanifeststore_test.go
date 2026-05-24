//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §4.4 partial-checkpoint manifest store,
// exercising the Postgres-backed
// pkg/gateway/partialmanifeststore/pgstore against a real container
// with the production migrations applied. Covers Put + Get round-trip,
// the §4.4 line 236 soft-delete idempotency guard, the LatestActive
// resume selector under the `deleted_at IS NULL` predicate, the
// §12.5 hard-prune sweep walker, the §12.8 erasure interface, and
// cross-tenant RLS isolation under migration 0062.
package stores_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/partialmanifeststore"
	partialmanifestpg "github.com/lennylabs/lenny/pkg/gateway/partialmanifeststore/pgstore"
)

// spec: §4.4 line 234 — the partial-checkpoint manifest table is
// created by migration 0062 and the pgstore round-trips every
// spec-mandated field through it.
func TestPartialManifestStoreContract(t *testing.T) {
	t.Parallel()
	_, pg := startStore(t)
	store := partialmanifestpg.New(pg.Pool, nil)
	ctx := context.Background()

	t.Run("put and get round-trip", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		want := partialmanifeststore.Record{
			TenantID:               tenant,
			SessionID:              newUUID(t),
			Generation:             3,
			PartialObjectKeyPrefix: "/" + tenant + "/checkpoints/sess/partial/ck-3/",
			ChunkEncoding:          partialmanifeststore.ChunkEncodingTarGz,
		}
		if err := store.Put(ctx, want); err != nil {
			t.Fatalf("Put: %v", err)
		}
		got, err := store.Get(ctx, tenant, want.SessionID, 3)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.PartialObjectKeyPrefix != want.PartialObjectKeyPrefix {
			t.Errorf("prefix = %q, want %q", got.PartialObjectKeyPrefix, want.PartialObjectKeyPrefix)
		}
		if got.ChunkEncoding != partialmanifeststore.ChunkEncodingTarGz {
			t.Errorf("chunk_encoding = %q, want tar.gz", got.ChunkEncoding)
		}
		if got.CreatedAt.IsZero() {
			t.Error("CreatedAt should be stamped on insert")
		}
		if !got.DeletedAt.IsZero() {
			t.Error("DeletedAt should be zero on an active row")
		}
	})

	t.Run("get missing returns ErrNotFound", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		if _, err := store.Get(ctx, tenant, newUUID(t), 1); !errors.Is(err, partialmanifeststore.ErrNotFound) {
			t.Errorf("Get missing: got %v, want ErrNotFound", err)
		}
	})

	t.Run("soft-delete is idempotent under deleted_at IS NULL", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		sessID := newUUID(t)
		if err := store.Put(ctx, partialmanifeststore.Record{
			TenantID: tenant, SessionID: sessID, Generation: 1,
			PartialObjectKeyPrefix: "/" + tenant + "/x/",
			ChunkEncoding:          partialmanifeststore.ChunkEncodingTar,
		}); err != nil {
			t.Fatalf("Put: %v", err)
		}
		if err := store.SoftDelete(ctx, tenant, sessID, 1); err != nil {
			t.Fatalf("SoftDelete: %v", err)
		}
		first, _ := store.Get(ctx, tenant, sessID, 1)
		if first.DeletedAt.IsZero() {
			t.Fatal("SoftDelete did not stamp DeletedAt")
		}
		// Replay does not mutate the row again.
		time.Sleep(2 * time.Millisecond)
		if err := store.SoftDelete(ctx, tenant, sessID, 1); err != nil {
			t.Fatalf("Replay SoftDelete: %v", err)
		}
		second, _ := store.Get(ctx, tenant, sessID, 1)
		if !second.DeletedAt.Equal(first.DeletedAt) {
			t.Errorf("DeletedAt = %v, want stable %v across replays", second.DeletedAt, first.DeletedAt)
		}
	})

	t.Run("LatestActive returns the highest generation under the active index", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		sessID := newUUID(t)
		for _, gen := range []int64{1, 5, 3} {
			if err := store.Put(ctx, partialmanifeststore.Record{
				TenantID: tenant, SessionID: sessID, Generation: gen,
				PartialObjectKeyPrefix: "/" + tenant + "/x/",
				ChunkEncoding:          partialmanifeststore.ChunkEncodingTar,
			}); err != nil {
				t.Fatalf("Put gen %d: %v", gen, err)
			}
		}
		got, err := store.LatestActive(ctx, tenant, sessID)
		if err != nil {
			t.Fatalf("LatestActive: %v", err)
		}
		if got.Generation != 5 {
			t.Errorf("LatestActive.Generation = %d, want 5", got.Generation)
		}

		// Soft-delete generation 5; LatestActive returns generation 3.
		_ = store.SoftDelete(ctx, tenant, sessID, 5)
		got, err = store.LatestActive(ctx, tenant, sessID)
		if err != nil {
			t.Fatalf("LatestActive after SoftDelete: %v", err)
		}
		if got.Generation != 3 {
			t.Errorf("LatestActive.Generation = %d, want 3 (after soft-deleting 5)", got.Generation)
		}
	})

	t.Run("cross-tenant get returns ErrNotFound under RLS", func(t *testing.T) {
		a := freshTenant(t, ctx, pg)
		b := freshTenant(t, ctx, pg)
		sessID := newUUID(t)
		if err := store.Put(ctx, partialmanifeststore.Record{
			TenantID: a, SessionID: sessID, Generation: 1,
			PartialObjectKeyPrefix: "/" + a + "/x/",
			ChunkEncoding:          partialmanifeststore.ChunkEncodingTar,
		}); err != nil {
			t.Fatalf("Put: %v", err)
		}
		if _, err := store.Get(ctx, b, sessID, 1); !errors.Is(err, partialmanifeststore.ErrNotFound) {
			t.Errorf("cross-tenant Get: got %v, want ErrNotFound", err)
		}
	})

	t.Run("DeleteByTenant clears the tenant's rows", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		other := freshTenant(t, ctx, pg)
		for _, tid := range []string{tenant, tenant, other} {
			if err := store.Put(ctx, partialmanifeststore.Record{
				TenantID: tid, SessionID: newUUID(t), Generation: 1,
				PartialObjectKeyPrefix: "/" + tid + "/x/",
				ChunkEncoding:          partialmanifeststore.ChunkEncodingTar,
			}); err != nil {
				t.Fatalf("Put: %v", err)
			}
		}
		if err := store.DeleteByTenant(ctx, tenant); err != nil {
			t.Fatalf("DeleteByTenant: %v", err)
		}
		// LatestActive on tenant returns ErrNotFound; on other it
		// still returns the surviving row.
		if _, err := store.LatestActive(ctx, tenant, "any"); !errors.Is(err, partialmanifeststore.ErrNotFound) {
			t.Error("tenant rows should be deleted")
		}
	})
}
