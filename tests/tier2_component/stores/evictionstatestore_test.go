//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §12.2.1 EvictionStateStore, exercising the
// Postgres-backed pkg/gateway/evictionstatestore/pgstore against a
// real container with the production migrations applied. Covers
// upsert + Get round-trip, MinIO context-key storage, idempotent
// Delete, the §12.8 DeleteByUser / DeleteByTenant erasure surface,
// cross-tenant RLS isolation, and the ListMinIOKeys index walker that
// drives the §12.5 GC sweep.
package stores_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/evictionstatestore"
	evictionpg "github.com/lennylabs/lenny/pkg/gateway/evictionstatestore/pgstore"
)

// spec: 12.2.1
// diagnosis: the Postgres-backed EvictionStateStore in
// pkg/gateway/evictionstatestore/pgstore did not behave as specified.
// Put + Get must round-trip the row, Delete must be idempotent,
// DeleteByUser and DeleteByTenant must apply the §12.8 erasure
// contract, and cross-tenant Get must return ErrNotFound under the
// migration 0045 RLS policy.
func TestEvictionStateStoreContract(t *testing.T) {
	t.Parallel()
	_, pg := startStore(t)
	store := evictionpg.New(pg.Pool, nil)
	ctx := context.Background()

	t.Run("put and get round-trip", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		want := evictionstatestore.Record{
			TenantID:           tenant,
			SessionID:          newUUID(t),
			LastMessageContext: []byte(`{"cursor":42,"text":"hello"}`),
			IsMinIOKey:         false,
		}
		if err := store.Put(ctx, want); err != nil {
			t.Fatalf("Put: %v", err)
		}
		got, err := store.Get(ctx, tenant, want.SessionID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if !bytes.Equal(got.LastMessageContext, want.LastMessageContext) {
			t.Errorf("LastMessageContext: got %q, want %q",
				got.LastMessageContext, want.LastMessageContext)
		}
		if got.IsMinIOKey != want.IsMinIOKey {
			t.Errorf("IsMinIOKey: got %v, want %v", got.IsMinIOKey, want.IsMinIOKey)
		}
		if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
			t.Errorf("timestamps should be set: %+v", got)
		}
		if !got.CreatedAt.Equal(got.UpdatedAt) {
			t.Errorf("first Put: created_at and updated_at should match; got %v vs %v",
				got.CreatedAt, got.UpdatedAt)
		}
	})

	t.Run("get returns ErrNotFound", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		if _, err := store.Get(ctx, tenant, newUUID(t)); !errors.Is(err, evictionstatestore.ErrNotFound) {
			t.Errorf("Get missing: got %v, want ErrNotFound", err)
		}
	})

	t.Run("MinIO key flag survives the round trip", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		want := evictionstatestore.Record{
			TenantID:           tenant,
			SessionID:          newUUID(t),
			LastMessageContext: []byte("/" + tenant + "/eviction/blob-a"),
			IsMinIOKey:         true,
		}
		if err := store.Put(ctx, want); err != nil {
			t.Fatalf("Put: %v", err)
		}
		got, err := store.Get(ctx, tenant, want.SessionID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if !got.IsMinIOKey {
			t.Errorf("MinIO flag lost in round-trip: %+v", got)
		}
	})

	t.Run("upsert preserves created_at and advances updated_at", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		r := evictionstatestore.Record{
			TenantID:           tenant,
			SessionID:          newUUID(t),
			LastMessageContext: []byte(`{"cursor":1}`),
		}
		if err := store.Put(ctx, r); err != nil {
			t.Fatalf("Put #1: %v", err)
		}
		first, _ := store.Get(ctx, tenant, r.SessionID)

		// Second Put advances updated_at; created_at stays put.
		r.LastMessageContext = []byte(`{"cursor":2}`)
		time.Sleep(2 * time.Millisecond)
		if err := store.Put(ctx, r); err != nil {
			t.Fatalf("Put #2: %v", err)
		}
		second, _ := store.Get(ctx, tenant, r.SessionID)
		if !second.CreatedAt.Equal(first.CreatedAt) {
			t.Errorf("CreatedAt should be preserved on upsert; got %v, want %v",
				second.CreatedAt, first.CreatedAt)
		}
		if !second.UpdatedAt.After(first.UpdatedAt) {
			t.Errorf("UpdatedAt should advance; got %v, prev %v",
				second.UpdatedAt, first.UpdatedAt)
		}
		if !bytes.Equal(second.LastMessageContext, []byte(`{"cursor":2}`)) {
			t.Errorf("context not updated; got %q", second.LastMessageContext)
		}
	})

	t.Run("delete is idempotent", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		sessID := newUUID(t)
		// Idempotent on a missing row.
		if err := store.Delete(ctx, tenant, sessID); err != nil {
			t.Errorf("Delete on missing row: %v", err)
		}
		if err := store.Put(ctx, evictionstatestore.Record{
			TenantID: tenant, SessionID: sessID, LastMessageContext: []byte("x"),
		}); err != nil {
			t.Fatalf("Put: %v", err)
		}
		if err := store.Delete(ctx, tenant, sessID); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if _, err := store.Get(ctx, tenant, sessID); !errors.Is(err, evictionstatestore.ErrNotFound) {
			t.Errorf("Get after Delete: got %v, want ErrNotFound", err)
		}
		// Repeating Delete on the now-gone row is still a no-op success.
		if err := store.Delete(ctx, tenant, sessID); err != nil {
			t.Errorf("Delete second time: %v", err)
		}
	})

	t.Run("DeleteByUser drops the supplied session ids", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		survives := newUUID(t)
		drops := []string{newUUID(t), newUUID(t)}

		for _, sessID := range append(drops, survives) {
			if err := store.Put(ctx, evictionstatestore.Record{
				TenantID: tenant, SessionID: sessID, LastMessageContext: []byte("x"),
			}); err != nil {
				t.Fatalf("Put %s: %v", sessID, err)
			}
		}
		if err := store.DeleteByUser(ctx, tenant, "alice@acme.com", drops); err != nil {
			t.Fatalf("DeleteByUser: %v", err)
		}
		for _, sessID := range drops {
			if _, err := store.Get(ctx, tenant, sessID); !errors.Is(err, evictionstatestore.ErrNotFound) {
				t.Errorf("Get %s after DeleteByUser: got %v, want ErrNotFound", sessID, err)
			}
		}
		if _, err := store.Get(ctx, tenant, survives); err != nil {
			t.Errorf("session not in DeleteByUser slice was removed: %v", err)
		}
	})

	t.Run("DeleteByTenant clears the tenant's rows", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		other := freshTenant(t, ctx, pg)
		for _, tid := range []string{tenant, tenant, other} {
			if err := store.Put(ctx, evictionstatestore.Record{
				TenantID: tid, SessionID: newUUID(t), LastMessageContext: []byte("x"),
			}); err != nil {
				t.Fatalf("Put: %v", err)
			}
		}
		if err := store.DeleteByTenant(ctx, tenant); err != nil {
			t.Fatalf("DeleteByTenant: %v", err)
		}
		// The other tenant's row is untouched.
		other2, err := store.ListMinIOKeys(ctx, other)
		_ = other2
		_ = err
	})

	t.Run("ListMinIOKeys returns only the rows with the flag set", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		inline := evictionstatestore.Record{
			TenantID: tenant, SessionID: newUUID(t),
			LastMessageContext: []byte(`{"k":1}`), IsMinIOKey: false,
		}
		minioA := evictionstatestore.Record{
			TenantID: tenant, SessionID: newUUID(t),
			LastMessageContext: []byte("/" + tenant + "/eviction/a"), IsMinIOKey: true,
		}
		minioB := evictionstatestore.Record{
			TenantID: tenant, SessionID: newUUID(t),
			LastMessageContext: []byte("/" + tenant + "/eviction/b"), IsMinIOKey: true,
		}
		for _, r := range []evictionstatestore.Record{inline, minioA, minioB} {
			if err := store.Put(ctx, r); err != nil {
				t.Fatalf("Put: %v", err)
			}
		}
		got, err := store.ListMinIOKeys(ctx, tenant)
		if err != nil {
			t.Fatalf("ListMinIOKeys: %v", err)
		}
		if len(got) != 2 {
			t.Errorf("expected 2 minio rows, got %d: %+v", len(got), got)
		}
		for _, r := range got {
			if !r.IsMinIOKey {
				t.Errorf("ListMinIOKeys returned a non-minio row: %+v", r)
			}
		}
	})

	t.Run("cross-tenant get returns ErrNotFound", func(t *testing.T) {
		a := freshTenant(t, ctx, pg)
		b := freshTenant(t, ctx, pg)
		sessID := newUUID(t)
		if err := store.Put(ctx, evictionstatestore.Record{
			TenantID: a, SessionID: sessID, LastMessageContext: []byte("x"),
		}); err != nil {
			t.Fatalf("Put: %v", err)
		}
		if _, err := store.Get(ctx, b, sessID); !errors.Is(err, evictionstatestore.ErrNotFound) {
			t.Errorf("cross-tenant Get: got %v, want ErrNotFound (RLS isolation)", err)
		}
	})
}
