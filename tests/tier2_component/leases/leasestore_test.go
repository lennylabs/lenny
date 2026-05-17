//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §12.2 LeaseStore, exercising the Redis-backed
// pkg/gateway/leasestore against a real Redis container. Covers
// acquire / renew / release / get, the holder-exclusivity invariant,
// idempotent re-acquire, the wrong-holder guards, and TTL expiry.
package leases_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/leasestore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

const tenant = "acme"

func sessionID(t *testing.T) string {
	t.Helper()
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return "sess-" + hex.EncodeToString(b[:])
}

// spec: 12.2
// diagnosis: the Redis-backed LeaseStore in pkg/gateway/leasestore did
// not behave as specified. Acquire must be exclusive but idempotent
// for the same holder, Renew and Release must guard against the wrong
// holder, a lease must expire after its TTL, and a non-positive TTL
// must be rejected.
func TestLeaseStoreContract(t *testing.T) {
	t.Parallel()
	rd := containers.StartRedis(t, containers.RedisOptions{})
	store := leasestore.New(rd.Client)
	ctx := context.Background()

	t.Run("acquire and get", func(t *testing.T) {
		sid := sessionID(t)
		lease, err := store.Acquire(ctx, tenant, sid, "replica-1", 10*time.Second)
		if err != nil {
			t.Fatalf("Acquire: %v", err)
		}
		if lease.Holder != "replica-1" {
			t.Errorf("Holder = %q, want replica-1", lease.Holder)
		}
		if !lease.ExpiresAt.After(time.Now()) {
			t.Errorf("ExpiresAt %v is not in the future", lease.ExpiresAt)
		}
		got, err := store.Get(ctx, tenant, sid)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.Holder != "replica-1" {
			t.Errorf("Get Holder = %q, want replica-1", got.Holder)
		}
	})

	t.Run("acquire is exclusive but idempotent for the same holder", func(t *testing.T) {
		sid := sessionID(t)
		if _, err := store.Acquire(ctx, tenant, sid, "replica-1", 10*time.Second); err != nil {
			t.Fatalf("Acquire: %v", err)
		}
		// A different replica cannot take the held lease.
		if _, err := store.Acquire(ctx, tenant, sid, "replica-2", 10*time.Second); !errors.Is(err, leasestore.ErrHeld) {
			t.Errorf("Acquire by another holder: got %v, want ErrHeld", err)
		}
		// The same replica re-acquiring succeeds (TTL refresh).
		if _, err := store.Acquire(ctx, tenant, sid, "replica-1", 10*time.Second); err != nil {
			t.Errorf("re-Acquire by the same holder: %v", err)
		}
	})

	t.Run("renew extends only for the holder", func(t *testing.T) {
		sid := sessionID(t)
		if _, err := store.Acquire(ctx, tenant, sid, "replica-1", 10*time.Second); err != nil {
			t.Fatalf("Acquire: %v", err)
		}
		if _, err := store.Renew(ctx, tenant, sid, "replica-1", 20*time.Second); err != nil {
			t.Errorf("Renew by the holder: %v", err)
		}
		if _, err := store.Renew(ctx, tenant, sid, "replica-2", 20*time.Second); !errors.Is(err, leasestore.ErrNotHeld) {
			t.Errorf("Renew by another holder: got %v, want ErrNotHeld", err)
		}
	})

	t.Run("release frees the lease only for the holder", func(t *testing.T) {
		sid := sessionID(t)
		if _, err := store.Acquire(ctx, tenant, sid, "replica-1", 10*time.Second); err != nil {
			t.Fatalf("Acquire: %v", err)
		}
		// A wrong-holder release is a no-op: the lease stays with replica-1.
		if err := store.Release(ctx, tenant, sid, "replica-2"); err != nil {
			t.Fatalf("Release by another holder: %v", err)
		}
		if got, err := store.Get(ctx, tenant, sid); err != nil || got.Holder != "replica-1" {
			t.Fatalf("after wrong-holder Release: got %+v err=%v, want replica-1 held", got, err)
		}
		// The holder's release frees the lease for another replica.
		if err := store.Release(ctx, tenant, sid, "replica-1"); err != nil {
			t.Fatalf("Release by the holder: %v", err)
		}
		if _, err := store.Get(ctx, tenant, sid); !errors.Is(err, leasestore.ErrNotFound) {
			t.Errorf("Get after Release: got %v, want ErrNotFound", err)
		}
		if _, err := store.Acquire(ctx, tenant, sid, "replica-2", 10*time.Second); err != nil {
			t.Errorf("Acquire after Release should succeed: %v", err)
		}
	})

	t.Run("get missing returns ErrNotFound", func(t *testing.T) {
		if _, err := store.Get(ctx, tenant, sessionID(t)); !errors.Is(err, leasestore.ErrNotFound) {
			t.Errorf("Get missing: got %v, want ErrNotFound", err)
		}
	})

	t.Run("lease expires after its TTL", func(t *testing.T) {
		sid := sessionID(t)
		if _, err := store.Acquire(ctx, tenant, sid, "replica-1", 150*time.Millisecond); err != nil {
			t.Fatalf("Acquire: %v", err)
		}
		time.Sleep(350 * time.Millisecond)
		if _, err := store.Get(ctx, tenant, sid); !errors.Is(err, leasestore.ErrNotFound) {
			t.Errorf("Get after TTL expiry: got %v, want ErrNotFound", err)
		}
		// The expired lease is freely acquirable by another replica.
		if _, err := store.Acquire(ctx, tenant, sid, "replica-2", 10*time.Second); err != nil {
			t.Errorf("Acquire after expiry: %v", err)
		}
	})

	t.Run("non-positive ttl is rejected", func(t *testing.T) {
		if _, err := store.Acquire(ctx, tenant, sessionID(t), "replica-1", 0); err == nil {
			t.Error("Acquire with a zero TTL should be rejected")
		}
	})
}
