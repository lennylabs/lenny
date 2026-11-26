//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §11.5 idempotency-key store, exercising the
// Postgres-backed pkg/idempotency/idempotencymw/pgstore against a
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

	"github.com/lennylabs/lenny/pkg/idempotency"
	idempgstore "github.com/lennylabs/lenny/pkg/idempotency/idempotencymw/pgstore"
)

// spec: 11.5
// diagnosis: the Postgres-backed idempotency-key store in
// pkg/idempotency/idempotencymw/pgstore did not behave as
// specified. Put and Get must round-trip a record, Put must replace on
// key conflict, keys must be isolated per tenant, a record past the
// 24-hour TTL must read as absent, DeleteExpired must reclaim only
// expired rows, and an over-long key must be rejected.
func TestIdempotencyStoreContract(t *testing.T) {
	t.Parallel()
	_, pg := startStore(t)
	store := idempgstore.New(pg.Pool)
	ctx := context.Background()

	t.Run("put and get round-trip", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		rec := idempotency.Record{
			Key:      idempotency.Key{TenantID: tenant, Subject: "alice", Value: "create-session-1"},
			BodyHash: idempotency.HashBody([]byte(`{"runtime":"echo"}`)),
			Response: idempotency.Response{
				StatusCode: 201,
				Headers: map[string][]string{
					"Location":   {"/v1/sessions/abc"},
					"Set-Cookie": {"sid=abc; Path=/", "trace=x; Path=/"},
				},
				Body: []byte(`{"sessionId":"abc"}`),
			},
			StoredAt: time.Now().UTC(),
		}
		if err := store.Put(ctx, rec); err != nil {
			t.Fatalf("Put: %v", err)
		}
		got, found, err := store.Get(ctx, aliceRecord(tenant, "create-session-1"))
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
		// spec: §11.5 — replay reproduces "the cached response
		// (same HTTP status and body)"; preserving multi-value headers
		// keeps Set-Cookie round-trips faithful instead of dropping the
		// later values. Closes F-11.5.9.
		gotLoc := got.Response.Headers["Location"]
		if len(gotLoc) != 1 || gotLoc[0] != "/v1/sessions/abc" {
			t.Errorf("Headers[Location]: got %v", gotLoc)
		}
		gotCookies := got.Response.Headers["Set-Cookie"]
		if len(gotCookies) != 2 || gotCookies[0] != "sid=abc; Path=/" || gotCookies[1] != "trace=x; Path=/" {
			t.Errorf("Headers[Set-Cookie]: got %v, want both values preserved", gotCookies)
		}
		if string(got.Response.Body) != `{"sessionId":"abc"}` {
			t.Errorf("Body: got %q", got.Response.Body)
		}
	})

	t.Run("get reports an unknown key as absent", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		_, found, err := store.Get(ctx, aliceRecord(tenant, "never-stored"))
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if found {
			t.Error("Get: an unstored key must report found=false")
		}
	})

	t.Run("put replaces the record on key conflict", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		key := idempotency.Key{TenantID: tenant, Subject: "alice", Value: "resume-1"}
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
		got, found, err := store.Get(ctx, aliceRecord(tenant, "resume-1"))
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
			Key:      idempotency.Key{TenantID: tenantA, Subject: "alice", Value: "shared-key"},
			BodyHash: "hash", Response: idempotency.Response{StatusCode: 200},
			StoredAt: time.Now().UTC(),
		}
		if err := store.Put(ctx, rec); err != nil {
			t.Fatalf("Put: %v", err)
		}
		if _, found, err := store.Get(ctx, aliceRecord(tenantB, "shared-key")); err != nil || found {
			t.Errorf("tenant B must not see tenant A's key: found=%v err=%v", found, err)
		}
		if _, found, err := store.Get(ctx, aliceRecord(tenantA, "shared-key")); err != nil || !found {
			t.Errorf("tenant A must see its own key: found=%v err=%v", found, err)
		}
	})

	t.Run("a record past the TTL reads as absent", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		rec := idempotency.Record{
			Key:      idempotency.Key{TenantID: tenant, Subject: "alice", Value: "stale-key"},
			BodyHash: "hash", Response: idempotency.Response{StatusCode: 200},
			StoredAt: time.Now().Add(-idempotency.TTL - time.Hour),
		}
		if err := store.Put(ctx, rec); err != nil {
			t.Fatalf("Put: %v", err)
		}
		if _, found, err := store.Get(ctx, aliceRecord(tenant, "stale-key")); err != nil || found {
			t.Errorf("an expired record must read as absent: found=%v err=%v", found, err)
		}
	})

	t.Run("DeleteExpired reclaims only rows past the TTL", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		stale := idempotency.Record{
			Key:      idempotency.Key{TenantID: tenant, Subject: "alice", Value: "gc-stale"},
			BodyHash: "hash", Response: idempotency.Response{StatusCode: 200},
			StoredAt: time.Now().Add(-idempotency.TTL - time.Hour),
		}
		fresh := idempotency.Record{
			Key:      idempotency.Key{TenantID: tenant, Subject: "alice", Value: "gc-fresh"},
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
		if _, found, err := store.Get(ctx, aliceRecord(tenant, "gc-fresh")); err != nil || !found {
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
			Key:      idempotency.Key{TenantID: tenant, Subject: "alice", Value: string(long)},
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

// aliceRecord is the key alice, bound to no session, files value under in
// tenant.
func aliceRecord(tenant, value string) idempotency.Key {
	return idempotency.Key{TenantID: tenant, Subject: "alice", Value: value}
}

// spec: §11.5 (Idempotency)
// diagnosis: the Postgres idempotency store matched a record across
// subjects or across bound sessions, or refused a claim under a scope that
// holds no record. Every query of pkg/idempotency/idempotencymw/pgstore
// must name tenant_id, subject, session_id, and idempotency_key, and the
// ON CONFLICT target must be the four-column primary key of migration 0205.
func TestThePostgresIdempotencyStoreKeysRecordsByTenantSubjectSessionAndKey(t *testing.T) {
	t.Parallel()
	_, pg := startStore(t)
	store := idempgstore.New(pg.Pool)
	ctx := context.Background()
	tenant := freshTenant(t, ctx, pg)
	now := time.Now().UTC()

	alice := aliceRecord(tenant, "k1")
	bob := idempotency.Key{TenantID: tenant, Subject: "bob", Value: "k1"}
	aliceS1 := idempotency.Key{TenantID: tenant, Subject: "alice", SessionID: "s1", Value: "k1"}
	aliceS2 := idempotency.Key{TenantID: tenant, Subject: "alice", SessionID: "s2", Value: "k1"}

	for i, key := range []idempotency.Key{alice, bob, aliceS1, aliceS2} {
		if _, claimed, err := store.Claim(ctx, key, "h", now); err != nil || !claimed {
			t.Fatalf("Claim(%+v): claimed=%v err=%v, want each scope to claim its own slot", key, claimed, err)
		}
		rec := idempotency.Record{Key: key, BodyHash: "h", Response: idempotency.Response{StatusCode: 201 + i}, StoredAt: now}
		if err := store.Put(ctx, rec); err != nil {
			t.Fatalf("Put(%+v): %v", key, err)
		}
	}
	for i, key := range []idempotency.Key{alice, bob, aliceS1, aliceS2} {
		got, found, err := store.Get(ctx, key)
		if err != nil || !found {
			t.Fatalf("Get(%+v): found=%v err=%v", key, found, err)
		}
		if got.Response.StatusCode != 201+i || got.Key != key {
			t.Errorf("Get(%+v) = status %d key %+v, want status %d under the same key", key, got.Response.StatusCode, got.Key, 201+i)
		}
		existing, claimed, err := store.Claim(ctx, key, "h", now)
		if err != nil || claimed || existing.Response.StatusCode != 201+i {
			t.Errorf("re-Claim(%+v) = claimed %v status %d err %v, want its own stored record", key, claimed, existing.Response.StatusCode, err)
		}
	}

	if err := store.Release(ctx, aliceS1); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, found, _ := store.Get(ctx, aliceS1); found {
		t.Error("Release left the released record in place")
	}
	for _, key := range []idempotency.Key{alice, bob, aliceS2} {
		if _, found, _ := store.Get(ctx, key); !found {
			t.Errorf("Release of alice's s1 record removed %+v", key)
		}
	}

	if _, _, err := store.Claim(ctx, idempotency.Key{TenantID: tenant, Value: "k2"}, "h", now); !errors.Is(err, idempotency.ErrSubjectRequired) {
		t.Errorf("Claim with no subject: err = %v, want ErrSubjectRequired", err)
	}
}

// spec: §12.8 (Compliance Interfaces)
// diagnosis: the Postgres idempotency store's DeleteByUser left a record
// of the erased subject under some session_id, deleted a record of another
// subject or another tenant, or DeleteByTenant crossed tenants. User
// erasure and tenant deletion Phase 4 call these through the gateway's
// idempotency_records erasers; a surviving row is personal data the
// erasure receipt claims is gone.
func TestThePostgresIdempotencyStoreDeletesByUserAndByTenant(t *testing.T) {
	t.Parallel()
	_, pg := startStore(t)
	store := idempgstore.New(pg.Pool)
	ctx := context.Background()
	acme := freshTenant(t, ctx, pg)
	globex := freshTenant(t, ctx, pg)
	now := time.Now().UTC()

	aliceKeys := []idempotency.Key{
		aliceRecord(acme, "k1"),
		{TenantID: acme, Subject: "alice", SessionID: "s1", Value: "k1"},
		{TenantID: acme, Subject: "alice", SessionID: "s2", Value: "child-1"},
	}
	bob := idempotency.Key{TenantID: acme, Subject: "bob", Value: "k1"}
	globexAlice := aliceRecord(globex, "k1")
	for _, k := range append(append([]idempotency.Key{}, aliceKeys...), bob, globexAlice) {
		if err := store.Put(ctx, idempotency.Record{Key: k, BodyHash: "h", Response: idempotency.Response{StatusCode: 201}, StoredAt: now}); err != nil {
			t.Fatalf("Put(%+v): %v", k, err)
		}
	}

	n, err := store.DeleteByUser(ctx, acme, "alice")
	if err != nil || n != len(aliceKeys) {
		t.Fatalf("DeleteByUser = %d, %v; want %d, nil", n, err, len(aliceKeys))
	}
	for _, k := range aliceKeys {
		if _, found, _ := store.Get(ctx, k); found {
			t.Errorf("%+v survived DeleteByUser", k)
		}
	}
	for _, k := range []idempotency.Key{bob, globexAlice} {
		if _, found, _ := store.Get(ctx, k); !found {
			t.Errorf("DeleteByUser removed %+v", k)
		}
	}
	if n, err := store.DeleteByUser(ctx, acme, "alice"); err != nil || n != 0 {
		t.Errorf("second DeleteByUser = %d, %v; want 0, nil", n, err)
	}
	if _, err := store.DeleteByUser(ctx, acme, ""); err == nil {
		t.Error("DeleteByUser with no subject succeeded, want an error")
	}

	if n, err := store.DeleteByTenant(ctx, acme); err != nil || n != 1 {
		t.Errorf("DeleteByTenant(acme) = %d, %v; want 1, nil", n, err)
	}
	if _, found, _ := store.Get(ctx, globexAlice); !found {
		t.Error("DeleteByTenant(acme) removed globex's record")
	}
	if _, err := store.DeleteByTenant(ctx, ""); err == nil {
		t.Error("DeleteByTenant with no tenant succeeded, want an error")
	}
}
