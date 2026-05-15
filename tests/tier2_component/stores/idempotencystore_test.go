//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §11.5 idempotency-key store, exercising the
// Postgres-backed pkg/gateway/middleware/idempotency/pgstore against a
// real container with the production migrations applied. Covers the
// put/get round-trip, replace-on-conflict, cross-tenant isolation, the
// 24-hour TTL read filter, the DeleteExpired garbage-collection sweep,
// and key-format validation.
package stores_test

import (
	"context"
	"errors"
	"testing"
	"time"

	idempgstore "github.com/lennylabs/lenny/pkg/gateway/middleware/idempotency/pgstore"
	"github.com/lennylabs/lenny/pkg/idempotency"
)

func TestIdempotencyStoreContract(t *testing.T) {
	t.Parallel()
	_, pg := startStore(t)
	store := idempgstore.New(pg.Pool)
	ctx := context.Background()

	t.Run("put and get round-trip", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		rec := idempotency.Record{
			Key:      idempotency.Key{TenantID: tenant, Value: "create-session-1"},
			BodyHash: idempotency.HashBody([]byte(`{"runtime":"echo"}`)),
			Response: idempotency.Response{
				StatusCode: 201,
				Headers:    map[string]string{"Location": "/v1/sessions/abc"},
				Body:       []byte(`{"sessionId":"abc"}`),
			},
			StoredAt: time.Now().UTC(),
		}
		if err := store.Put(ctx, rec); err != nil {
			t.Fatalf("Put: %v", err)
		}
		got, found, err := store.Get(ctx, tenant, "create-session-1")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if !found {
			t.Fatal("Get: record should be present after Put")
		}
		if got.BodyHash != rec.BodyHash {
			t.Errorf("BodyHash: got %q, want %q", got.BodyHash, rec.BodyHash)
		}
		if got.Response.StatusCode != 201 {
			t.Errorf("StatusCode: got %d, want 201", got.Response.StatusCode)
		}
		if got.Response.Headers["Location"] != "/v1/sessions/abc" {
			t.Errorf("Headers: got %v", got.Response.Headers)
		}
		if string(got.Response.Body) != `{"sessionId":"abc"}` {
			t.Errorf("Body: got %q", got.Response.Body)
		}
	})

	t.Run("get reports an unknown key as absent", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		_, found, err := store.Get(ctx, tenant, "never-stored")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if found {
			t.Error("Get: an unstored key must report found=false")
		}
	})

	t.Run("put replaces the record on key conflict", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		key := idempotency.Key{TenantID: tenant, Value: "resume-1"}
		first := idempotency.Record{
			Key: key, BodyHash: "hash-a",
			Response: idempotency.Response{StatusCode: 200, Body: []byte("first")},
			StoredAt: time.Now().UTC(),
		}
		if err := store.Put(ctx, first); err != nil {
			t.Fatalf("Put first: %v", err)
		}
		second := idempotency.Record{
			Key: key, BodyHash: "hash-b",
			Response: idempotency.Response{StatusCode: 202, Body: []byte("second")},
			StoredAt: time.Now().UTC(),
		}
		if err := store.Put(ctx, second); err != nil {
			t.Fatalf("Put second: %v", err)
		}
		got, found, err := store.Get(ctx, tenant, "resume-1")
		if err != nil || !found {
			t.Fatalf("Get: found=%v err=%v", found, err)
		}
		if got.BodyHash != "hash-b" || got.Response.StatusCode != 202 {
			t.Errorf("conflict put should replace: got %+v", got)
		}
	})

	t.Run("keys are isolated per tenant", func(t *testing.T) {
		tenantA := freshTenant(t, ctx, pg)
		tenantB := freshTenant(t, ctx, pg)
		rec := idempotency.Record{
			Key:      idempotency.Key{TenantID: tenantA, Value: "shared-key"},
			BodyHash: "hash", Response: idempotency.Response{StatusCode: 200},
			StoredAt: time.Now().UTC(),
		}
		if err := store.Put(ctx, rec); err != nil {
			t.Fatalf("Put: %v", err)
		}
		if _, found, err := store.Get(ctx, tenantB, "shared-key"); err != nil || found {
			t.Errorf("tenant B must not see tenant A's key: found=%v err=%v", found, err)
		}
		if _, found, err := store.Get(ctx, tenantA, "shared-key"); err != nil || !found {
			t.Errorf("tenant A must see its own key: found=%v err=%v", found, err)
		}
	})

	t.Run("a record past the TTL reads as absent", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		rec := idempotency.Record{
			Key:      idempotency.Key{TenantID: tenant, Value: "stale-key"},
			BodyHash: "hash", Response: idempotency.Response{StatusCode: 200},
			StoredAt: time.Now().Add(-idempotency.TTL - time.Hour),
		}
		if err := store.Put(ctx, rec); err != nil {
			t.Fatalf("Put: %v", err)
		}
		if _, found, err := store.Get(ctx, tenant, "stale-key"); err != nil || found {
			t.Errorf("an expired record must read as absent: found=%v err=%v", found, err)
		}
	})

	t.Run("DeleteExpired reclaims only rows past the TTL", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		stale := idempotency.Record{
			Key:      idempotency.Key{TenantID: tenant, Value: "gc-stale"},
			BodyHash: "hash", Response: idempotency.Response{StatusCode: 200},
			StoredAt: time.Now().Add(-idempotency.TTL - time.Hour),
		}
		fresh := idempotency.Record{
			Key:      idempotency.Key{TenantID: tenant, Value: "gc-fresh"},
			BodyHash: "hash", Response: idempotency.Response{StatusCode: 200},
			StoredAt: time.Now().UTC(),
		}
		if err := store.Put(ctx, stale); err != nil {
			t.Fatalf("Put stale: %v", err)
		}
		if err := store.Put(ctx, fresh); err != nil {
			t.Fatalf("Put fresh: %v", err)
		}
		cutoff := time.Now().Add(-idempotency.TTL)
		n, err := store.DeleteExpired(ctx, tenant, cutoff)
		if err != nil {
			t.Fatalf("DeleteExpired: %v", err)
		}
		if n != 1 {
			t.Errorf("DeleteExpired reclaimed %d rows, want 1", n)
		}
		if _, found, err := store.Get(ctx, tenant, "gc-fresh"); err != nil || !found {
			t.Errorf("the fresh record must survive the sweep: found=%v err=%v", found, err)
		}
		again, err := store.DeleteExpired(ctx, tenant, cutoff)
		if err != nil {
			t.Fatalf("DeleteExpired (second pass): %v", err)
		}
		if again != 0 {
			t.Errorf("a second sweep reclaimed %d rows, want 0", again)
		}
	})

	t.Run("put rejects an over-long key", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		long := make([]byte, 129)
		for i := range long {
			long[i] = 'k'
		}
		rec := idempotency.Record{
			Key:      idempotency.Key{TenantID: tenant, Value: string(long)},
			BodyHash: "hash", Response: idempotency.Response{StatusCode: 200},
			StoredAt: time.Now().UTC(),
		}
		err := store.Put(ctx, rec)
		var tooLong *idempotency.KeyTooLongError
		if !errors.As(err, &tooLong) {
			t.Errorf("Put should reject an over-long key with *KeyTooLongError, got %v", err)
		}
	})
}
