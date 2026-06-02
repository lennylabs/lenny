// SPDX-License-Identifier: MIT

package playground

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// spec: §27.3.1 line 98 — "TestPlaygroundSessionRevocationCrossReplica
// MUST assert that a logout on replica A invalidates a subsequent
// request carrying the same cookie or bearer on replica B, both before
// and after the pub/sub message is delivered (the authoritative Redis
// check covers the pre-delivery case; the LRU negative cache covers the
// post-delivery case)." F-27.3.10.
func TestPlaygroundSessionRevocationCrossReplica_spec_27_3_1_98(t *testing.T) {
	mr := miniredis.RunT(t)
	clientA := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	clientB := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = clientA.Close(); _ = clientB.Close() })

	const tenant = "acme"
	ctx := context.Background()

	// Replica A is the revoking replica.
	replicaA := NewRedisSessionStore(clientA)

	// --- Pre-delivery case: authoritative Redis GET ---
	// replicaBCold runs no subscription, so its negative cache is never
	// warmed and IsBearerRevoked always consults Redis authoritatively.
	replicaBCold := NewRedisSessionStore(clientB)
	const jtiCold = "jti-cold"
	if err := replicaA.PutSession(ctx, tenant, "sess-cold",
		SessionRecord{UserID: "alice", TenantID: tenant, BearerJTIs: []string{jtiCold}}, time.Hour); err != nil {
		t.Fatalf("PutSession: %v", err)
	}
	// Before the logout the bearer is honored on replica B.
	if revoked, err := replicaBCold.IsBearerRevoked(ctx, tenant, jtiCold); err != nil || revoked {
		t.Fatalf("pre-logout replicaB IsBearerRevoked = %v, %v; want false, nil", revoked, err)
	}
	// Logout on replica A commits the revocation marker to Redis.
	if err := replicaA.RevokeSession(ctx, tenant, "sess-cold", []string{jtiCold}, time.Hour); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}
	// Replica B observes the revocation immediately via the authoritative
	// Redis check, before any pub/sub delivery warms a cache.
	if revoked, err := replicaBCold.IsBearerRevoked(ctx, tenant, jtiCold); err != nil || !revoked {
		t.Fatalf("pre-delivery replicaB IsBearerRevoked = %v, %v; want true, nil", revoked, err)
	}

	// --- Post-delivery case: pub/sub-warmed negative cache ---
	subCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	replicaBWarm := NewRedisSessionStore(clientB)
	go replicaBWarm.SubscribeAllRevocations(subCtx)

	const jtiWarm = "jti-warm"
	// Re-publish on each poll until replica B's cache converges; miniredis
	// drops a message published before PSUBSCRIBE registers.
	waitForCondition(t, 3*time.Second, func() bool {
		if err := replicaA.RevokeSession(ctx, tenant, "sess-warm", []string{jtiWarm}, time.Hour); err != nil {
			t.Fatalf("RevokeSession(warm): %v", err)
		}
		replicaBWarm.cacheMu.RLock()
		_, ok := replicaBWarm.cache[revokedKey(tenant, jtiWarm)]
		replicaBWarm.cacheMu.RUnlock()
		return ok
	})
	if revoked, err := replicaBWarm.IsBearerRevoked(ctx, tenant, jtiWarm); err != nil || !revoked {
		t.Fatalf("post-delivery replicaB IsBearerRevoked = %v, %v; want true, nil", revoked, err)
	}
}

// spec: §27.3.1 line 98 / §12.4 — the Redis-backed store extends the
// tenant-key-isolation guarantee to the pg:sess:* and pg:revoked:* key
// prefixes: a record or revocation marker written for tenant A must not
// be visible to a request scoped to tenant B reusing the same
// (lexically-equal) session id or jti. F-27.3.10.
func TestRedisSessionStoreTenantKeyIsolation_spec_27_3_1_98(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	store := NewRedisSessionStore(client)
	ctx := context.Background()

	// pg:sess:* — a record for tenant acme is not readable by tenant globex.
	if err := store.PutSession(ctx, "acme", "sess-shared",
		SessionRecord{UserID: "alice", TenantID: "acme", Origin: PlaygroundOrigin}, time.Hour); err != nil {
		t.Fatalf("PutSession: %v", err)
	}
	if _, err := store.GetSession(ctx, "globex", "sess-shared"); err == nil {
		t.Fatal("tenant globex read tenant acme's pg:sess:* record")
	}
	got, err := store.GetSession(ctx, "acme", "sess-shared")
	if err != nil || got.UserID != "alice" {
		t.Fatalf("GetSession(acme) = %+v, %v; want UserID alice", got, err)
	}

	// pg:revoked:* — a marker for tenant acme's jti must not reject a
	// tenant globex request reusing the same jti value.
	if err := store.MarkBearerRevoked(ctx, "acme", "jti-shared", time.Hour); err != nil {
		t.Fatalf("MarkBearerRevoked: %v", err)
	}
	if revoked, err := store.IsBearerRevoked(ctx, "acme", "jti-shared"); err != nil || !revoked {
		t.Fatalf("acme jti-shared revoked = %v, %v; want true, nil", revoked, err)
	}
	if revoked, err := store.IsBearerRevoked(ctx, "globex", "jti-shared"); err != nil || revoked {
		t.Fatalf("globex jti-shared revoked = %v, %v; want false, nil", revoked, err)
	}
}

// TestRedisTenantForSessionIndexRoundTrip exercises the §27.3.1 fan-in
// index on the Redis-backed store: PutSession writes pg:sess-tenant:{id}
// so the tenant is recoverable from the opaque cookie id alone, and
// RevokeSession deletes it. F-27.3.8.
func TestRedisTenantForSessionIndexRoundTrip_spec_27_3_1_81(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	store := NewRedisSessionStore(client)
	ctx := context.Background()

	rec := SessionRecord{UserID: "alice", TenantID: "acme", Origin: PlaygroundOrigin, BearerJTIs: []string{"jti-1"}}
	if err := store.PutSession(ctx, "acme", "opaque-id", rec, time.Hour); err != nil {
		t.Fatalf("PutSession: %v", err)
	}
	// The index key is the platform-scoped fan-in entry, not tenant-prefixed.
	if got, err := client.Get(ctx, "pg:sess-tenant:opaque-id").Result(); err != nil || got != "acme" {
		t.Fatalf("pg:sess-tenant:opaque-id = %q, %v; want acme, nil", got, err)
	}
	tenant, ok, err := store.TenantForSession(ctx, "opaque-id")
	if err != nil || !ok || tenant != "acme" {
		t.Fatalf("TenantForSession = (%q, %v, %v); want (acme, true, nil)", tenant, ok, err)
	}
	if _, ok, _ := store.TenantForSession(ctx, "never-issued"); ok {
		t.Fatal("TenantForSession resolved an id that was never issued")
	}
	if err := store.RevokeSession(ctx, "acme", "opaque-id", rec.BearerJTIs, time.Hour); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}
	if _, ok, _ := store.TenantForSession(ctx, "opaque-id"); ok {
		t.Fatal("TenantForSession resolved a revoked id; index entry survived RevokeSession")
	}
}
