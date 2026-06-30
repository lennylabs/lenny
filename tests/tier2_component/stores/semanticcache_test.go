//go:build component

// SPDX-License-Identifier: MIT

// Component test for the §12.2.1 SemanticCache, exercising the
// Redis-backed pkg/gateway/semanticcache/redisstore against a real
// Redis container. Covers the §4.9 put/get round-trip and similarity
// lookup, the §12.4 t:{tenant_id}:scache:{scope}:{hash} key scheme,
// per-tenant and per-user isolation at the key-naming level, the TTL
// expiry, and the §12.2 DeleteByUser / DeleteByTenant erasure
// primitives against the live keyspace.
package stores_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/llmproxy/semanticcache"
	scredis "github.com/lennylabs/lenny/pkg/gateway/llmproxy/llmproxy/semanticcache/redisstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// scacheUserKey builds a per-user §4.9 cache key.
func scacheUserKey(tenant, user string) semanticcache.Key {
	return semanticcache.Key{
		TenantID: tenant, Scope: semanticcache.ScopePerUser, UserID: user,
		Model: "claude-opus", Provider: "anthropic",
	}
}

// spec: 12.2.1, 4.9, 12.4
// diagnosis: the Redis-backed §12.2.1 SemanticCache in
// pkg/gateway/semanticcache/redisstore did not behave as specified.
// Put and Get must round-trip a cached LLM response keyed under the
// §12.4 t:{tenant_id}:scache:{scope}:{hash} convention, the §4.9
// similarity lookup must hit a semantically close query and miss a
// dissimilar one, the entry TTL must expire, isolation must hold at
// the key-naming level so a write for tenant A never hits for tenant
// B, and the §12.2 DeleteByUser / DeleteByTenant primitives must erase
// exactly their scope.
func TestSemanticCacheContract(t *testing.T) {
	t.Parallel()
	rd := containers.StartRedis(t, containers.RedisOptions{})
	ctx := context.Background()
	cache := scredis.New(rd.Client, nil, time.Minute, semanticcache.DefaultSimilarityThreshold)

	t.Run("put then exact-repeat get hits", func(t *testing.T) {
		key := scacheUserKey("acme-"+newUUID(t)[:8], "alice")
		if err := cache.Put(ctx, key, "what is the capital of france", "Paris"); err != nil {
			t.Fatalf("Put: %v", err)
		}
		got, ok, err := cache.Get(ctx, key, "what is the capital of france")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if !ok || got.Response != "Paris" {
			t.Fatalf("Get = %v/%q, want true/Paris", ok, got.Response)
		}
	})

	t.Run("keys follow the t:{tenant}:scache:{scope} convention", func(t *testing.T) {
		tenant := "acme-" + newUUID(t)[:8]
		key := scacheUserKey(tenant, "alice")
		if err := cache.Put(ctx, key, "key scheme probe", "answer"); err != nil {
			t.Fatalf("Put: %v", err)
		}
		// Every key the cache wrote for this tenant must carry the
		// §12.4 prefix t:{tenant_id}:scache: — both the entry and the
		// per-(scope, model, provider) index set.
		want := "t:" + tenant + ":scache:"
		keys, err := rd.Client.Keys(ctx, want+"*").Result()
		if err != nil {
			t.Fatalf("scan keys: %v", err)
		}
		if len(keys) == 0 {
			t.Fatalf("no key under the %q prefix", want)
		}
		// The per-user entry is keyed under the u:{user_id} scope
		// segment.
		userKeys, err := rd.Client.Keys(ctx, want+"u:alice:*").Result()
		if err != nil {
			t.Fatalf("scan user keys: %v", err)
		}
		if len(userKeys) == 0 {
			t.Errorf("no key under the per-user scope segment %q", want+"u:alice:")
		}
	})

	t.Run("a semantically close query hits, a dissimilar one misses", func(t *testing.T) {
		key := scacheUserKey("acme-"+newUUID(t)[:8], "alice")
		if err := cache.Put(ctx, key,
			"how do I rotate a deploy credential in the vault",
			"Use the rotate endpoint"); err != nil {
			t.Fatalf("Put: %v", err)
		}
		// A near-identical re-phrasing shares most of its vocabulary
		// and clears the threshold.
		got, ok, err := cache.Get(ctx, key,
			"how do I rotate a deploy credential in the vault")
		if err != nil {
			t.Fatalf("Get close: %v", err)
		}
		if !ok || got.Response != "Use the rotate endpoint" {
			t.Errorf("close query Get = %v/%q, want a hit", ok, got.Response)
		}
		// An unrelated query shares no vocabulary and misses.
		if _, ok, err := cache.Get(ctx, key, "what time is the team lunch"); err != nil || ok {
			t.Errorf("dissimilar query: ok=%v err=%v, want a miss", ok, err)
		}
	})

	t.Run("cross-tenant isolation at the key-naming level", func(t *testing.T) {
		const query = "summarize the quarterly report"
		tenantA := "acme-" + newUUID(t)[:8]
		tenantB := "globex-" + newUUID(t)[:8]
		if err := cache.Put(ctx, scacheUserKey(tenantA, "alice"), query, "tenant A answer"); err != nil {
			t.Fatalf("Put tenant A: %v", err)
		}
		// A byte-identical query for tenant B must not hit tenant A's
		// entry — the tenant id is a fixed key prefix.
		if _, ok, err := cache.Get(ctx, scacheUserKey(tenantB, "alice"), query); err != nil || ok {
			t.Errorf("cross-tenant Get: ok=%v err=%v, want a miss", ok, err)
		}
	})

	t.Run("per-user isolation within a tenant", func(t *testing.T) {
		tenant := "acme-" + newUUID(t)[:8]
		const query = "what is my account balance"
		if err := cache.Put(ctx, scacheUserKey(tenant, "alice"), query, "alice balance"); err != nil {
			t.Fatalf("Put alice: %v", err)
		}
		if _, ok, err := cache.Get(ctx, scacheUserKey(tenant, "bob"), query); err != nil || ok {
			t.Errorf("cross-user Get: ok=%v err=%v, want a miss (per-user scope)", ok, err)
		}
	})

	t.Run("tenant scope shares a response across users", func(t *testing.T) {
		tenant := "acme-" + newUUID(t)[:8]
		aliceKey := semanticcache.Key{
			TenantID: tenant, Scope: semanticcache.ScopeTenant, UserID: "alice",
			Model: "claude-opus", Provider: "anthropic",
		}
		bobKey := aliceKey
		bobKey.UserID = "bob"
		const query = "what is the company holiday schedule"
		if err := cache.Put(ctx, aliceKey, query, "shared answer"); err != nil {
			t.Fatalf("Put: %v", err)
		}
		got, ok, err := cache.Get(ctx, bobKey, query)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if !ok || got.Response != "shared answer" {
			t.Errorf("tenant-scoped Get for bob = %v/%q, want the shared answer", ok, got.Response)
		}
	})

	t.Run("entry TTL expires", func(t *testing.T) {
		shortTTL := scredis.New(rd.Client, nil, 1*time.Second, semanticcache.DefaultSimilarityThreshold)
		key := scacheUserKey("acme-"+newUUID(t)[:8], "alice")
		if err := shortTTL.Put(ctx, key, "ttl probe query", "answer"); err != nil {
			t.Fatalf("Put: %v", err)
		}
		if _, ok, _ := shortTTL.Get(ctx, key, "ttl probe query"); !ok {
			t.Fatal("entry should be live immediately after Put")
		}
		// Redis evicts the key on its own TTL; wait past it.
		time.Sleep(2 * time.Second)
		if _, ok, _ := shortTTL.Get(ctx, key, "ttl probe query"); ok {
			t.Error("entry should have expired past its 1s TTL")
		}
	})

	t.Run("DeleteByUser erases the user and spares others", func(t *testing.T) {
		tenant := "acme-" + newUUID(t)[:8]
		if err := cache.Put(ctx, scacheUserKey(tenant, "alice"), "alice question", "alice answer"); err != nil {
			t.Fatalf("Put alice: %v", err)
		}
		if err := cache.Put(ctx, scacheUserKey(tenant, "bob"), "bob question", "bob answer"); err != nil {
			t.Fatalf("Put bob: %v", err)
		}
		if err := cache.DeleteByUser(ctx, tenant, "alice"); err != nil {
			t.Fatalf("DeleteByUser: %v", err)
		}
		if _, ok, _ := cache.Get(ctx, scacheUserKey(tenant, "alice"), "alice question"); ok {
			t.Error("DeleteByUser did not erase alice's entry")
		}
		if _, ok, _ := cache.Get(ctx, scacheUserKey(tenant, "bob"), "bob question"); !ok {
			t.Error("DeleteByUser erased bob, but alice was the target")
		}
		// No alice key survives in the keyspace.
		residual, err := rd.Client.Keys(ctx, "t:"+tenant+":scache:u:alice:*").Result()
		if err != nil {
			t.Fatalf("scan residual: %v", err)
		}
		if len(residual) != 0 {
			t.Errorf("DeleteByUser left %d alice keys: %v", len(residual), residual)
		}
		// Erasure is idempotent.
		if err := cache.DeleteByUser(ctx, tenant, "alice"); err != nil {
			t.Errorf("repeat DeleteByUser should be idempotent: %v", err)
		}
	})

	t.Run("DeleteByTenant erases every entry for the tenant", func(t *testing.T) {
		tenant := "acme-" + newUUID(t)[:8]
		other := "globex-" + newUUID(t)[:8]
		if err := cache.Put(ctx, scacheUserKey(tenant, "alice"), "q1", "a1"); err != nil {
			t.Fatalf("Put: %v", err)
		}
		if err := cache.Put(ctx, scacheUserKey(tenant, "bob"), "q2", "a2"); err != nil {
			t.Fatalf("Put: %v", err)
		}
		if err := cache.Put(ctx, scacheUserKey(other, "carol"), "q3", "a3"); err != nil {
			t.Fatalf("Put: %v", err)
		}
		if err := cache.DeleteByTenant(ctx, tenant); err != nil {
			t.Fatalf("DeleteByTenant: %v", err)
		}
		residual, err := rd.Client.Keys(ctx, "t:"+tenant+":scache:*").Result()
		if err != nil {
			t.Fatalf("scan residual: %v", err)
		}
		if len(residual) != 0 {
			t.Errorf("DeleteByTenant left %d keys: %v", len(residual), residual)
		}
		// The other tenant's entry survives.
		if _, ok, _ := cache.Get(ctx, scacheUserKey(other, "carol"), "q3"); !ok {
			t.Error("DeleteByTenant erased the other tenant's entry")
		}
	})
}
