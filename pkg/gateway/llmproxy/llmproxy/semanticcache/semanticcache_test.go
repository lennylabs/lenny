// SPDX-License-Identifier: MIT

package semanticcache_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/llmproxy/semanticcache"
)

// spec: §12.2 SemanticCache — the Redis-backed LLM query/response
// cache. These unit tests exercise the in-memory implementation; the
// component test exercises the Redis backend.

func newCache() *semanticcache.InMemory {
	return semanticcache.NewInMemory(nil, time.Minute, semanticcache.DefaultSimilarityThreshold, nil)
}

func userKey(tenant, user string) semanticcache.Key {
	return semanticcache.Key{
		TenantID: tenant, Scope: semanticcache.ScopePerUser, UserID: user,
		Model: "claude-opus", Provider: "anthropic",
	}
}

func TestPutThenGetExactRepeatHits(t *testing.T) {
	c := newCache()
	ctx := context.Background()
	key := userKey("acme", "alice")
	if err := c.Put(ctx, key, "what is the capital of france", "Paris"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, ok, err := c.Get(ctx, key, "what is the capital of france")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("exact-repeat query should hit the cache")
	}
	if got.Response != "Paris" {
		t.Errorf("cached response = %q, want Paris", got.Response)
	}
	if got.Similarity < 0.999 {
		t.Errorf("exact-repeat similarity = %v, want ~1.0", got.Similarity)
	}
}

func TestGetMissOnUnseenQuery(t *testing.T) {
	c := newCache()
	ctx := context.Background()
	_, ok, err := c.Get(ctx, userKey("acme", "alice"), "never asked this")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if ok {
		t.Error("a query with no cached entry should miss")
	}
}

func TestDissimilarQueryMisses(t *testing.T) {
	c := newCache()
	ctx := context.Background()
	key := userKey("acme", "alice")
	if err := c.Put(ctx, key, "how do I rotate a deploy credential", "Use the rotate endpoint"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	// A wholly unrelated query shares no vocabulary, so its cosine
	// similarity is below the 0.92 threshold and the lookup misses.
	_, ok, err := c.Get(ctx, key, "what time is the team lunch today")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if ok {
		t.Error("a semantically dissimilar query should miss the cache")
	}
}

// TestTenantIsolation is the §12.4 / §4.9 cross-tenant guarantee: a
// write for tenant A must never produce a hit for tenant B, even for a
// byte-identical query.
func TestTenantIsolation(t *testing.T) {
	c := newCache()
	ctx := context.Background()
	const query = "summarize the quarterly report"
	if err := c.Put(ctx, userKey("acme", "alice"), query, "acme summary"); err != nil {
		t.Fatalf("Put acme: %v", err)
	}
	_, ok, err := c.Get(ctx, userKey("globex", "alice"), query)
	if err != nil {
		t.Fatalf("Get globex: %v", err)
	}
	if ok {
		t.Error("an identical query from another tenant must not hit the cache")
	}
}

// TestUserIsolation is the §4.9 per-user default: a write for one user
// must not hit for another user in the same tenant.
func TestUserIsolation(t *testing.T) {
	c := newCache()
	ctx := context.Background()
	const query = "what is my account balance"
	if err := c.Put(ctx, userKey("acme", "alice"), query, "alice balance"); err != nil {
		t.Fatalf("Put alice: %v", err)
	}
	_, ok, err := c.Get(ctx, userKey("acme", "bob"), query)
	if err != nil {
		t.Fatalf("Get bob: %v", err)
	}
	if ok {
		t.Error("per-user scope must not share a cached response across users")
	}
}

// TestTenantScopeSharesAcrossUsers verifies the §4.9 cacheScope:
// tenant opt-in: with ScopeTenant the user id is omitted from the key,
// so two users in the same tenant share a cached response.
func TestTenantScopeSharesAcrossUsers(t *testing.T) {
	c := newCache()
	ctx := context.Background()
	aliceKey := semanticcache.Key{
		TenantID: "acme", Scope: semanticcache.ScopeTenant, UserID: "alice",
		Model: "claude-opus", Provider: "anthropic",
	}
	bobKey := aliceKey
	bobKey.UserID = "bob"
	const query = "what is the company holiday schedule"
	if err := c.Put(ctx, aliceKey, query, "shared answer"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, ok, err := c.Get(ctx, bobKey, query)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok || got.Response != "shared answer" {
		t.Errorf("tenant-scoped cache: bob hit = %v/%q, want true/\"shared answer\"", ok, got.Response)
	}
}

// TestModelAndProviderPartitionTheKeySpace verifies the §4.9 key space
// includes model and provider: a response cached for one model is not
// returned for another.
func TestModelAndProviderPartitionTheKeySpace(t *testing.T) {
	c := newCache()
	ctx := context.Background()
	const query = "translate hello to french"
	opus := userKey("acme", "alice")
	if err := c.Put(ctx, opus, query, "bonjour"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	sonnet := opus
	sonnet.Model = "claude-sonnet"
	if _, ok, err := c.Get(ctx, sonnet, query); err != nil || ok {
		t.Errorf("a different model must miss: ok=%v err=%v", ok, err)
	}
	otherProvider := opus
	otherProvider.Provider = "bedrock"
	if _, ok, err := c.Get(ctx, otherProvider, query); err != nil || ok {
		t.Errorf("a different provider must miss: ok=%v err=%v", ok, err)
	}
}

// TestDeleteByUserErasesUserEntries is the §12.2 / §4.9
// ValidateSemanticCacheErasure contract: DeleteByUser removes the
// user's entries, a later Get for that user misses, and another user's
// entries in the same tenant are untouched.
func TestDeleteByUserErasesUserEntries(t *testing.T) {
	c := newCache()
	ctx := context.Background()
	if err := c.Put(ctx, userKey("acme", "alice"), "alice question", "alice answer"); err != nil {
		t.Fatalf("Put alice: %v", err)
	}
	if err := c.Put(ctx, userKey("acme", "bob"), "bob question", "bob answer"); err != nil {
		t.Fatalf("Put bob: %v", err)
	}
	if err := c.DeleteByUser(ctx, "acme", "alice"); err != nil {
		t.Fatalf("DeleteByUser: %v", err)
	}
	if _, ok, _ := c.Get(ctx, userKey("acme", "alice"), "alice question"); ok {
		t.Error("DeleteByUser did not erase alice's cached entry")
	}
	if _, ok, _ := c.Get(ctx, userKey("acme", "bob"), "bob question"); !ok {
		t.Error("DeleteByUser erased bob's entry, but bob was not the erasure target")
	}
	// Erasure is idempotent: a repeat returns nil.
	if err := c.DeleteByUser(ctx, "acme", "alice"); err != nil {
		t.Errorf("repeat DeleteByUser should be idempotent: %v", err)
	}
}

func TestDeleteByTenantErasesEveryEntry(t *testing.T) {
	c := newCache()
	ctx := context.Background()
	if err := c.Put(ctx, userKey("acme", "alice"), "q1", "a1"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := c.Put(ctx, userKey("acme", "bob"), "q2", "a2"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	// A different tenant's entry must survive.
	if err := c.Put(ctx, userKey("globex", "carol"), "q3", "a3"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := c.DeleteByTenant(ctx, "acme"); err != nil {
		t.Fatalf("DeleteByTenant: %v", err)
	}
	if _, ok, _ := c.Get(ctx, userKey("acme", "alice"), "q1"); ok {
		t.Error("DeleteByTenant left an acme entry")
	}
	if _, ok, _ := c.Get(ctx, userKey("acme", "bob"), "q2"); ok {
		t.Error("DeleteByTenant left an acme entry")
	}
	if _, ok, _ := c.Get(ctx, userKey("globex", "carol"), "q3"); !ok {
		t.Error("DeleteByTenant erased globex, but acme was the target")
	}
}

func TestEmptyScopeIDsRejected(t *testing.T) {
	c := newCache()
	ctx := context.Background()
	if err := c.DeleteByUser(ctx, "", "alice"); !errors.Is(err, semanticcache.ErrEmptyTenant) {
		t.Errorf("DeleteByUser empty tenant: got %v, want ErrEmptyTenant", err)
	}
	if err := c.DeleteByUser(ctx, "acme", ""); !errors.Is(err, semanticcache.ErrEmptyUser) {
		t.Errorf("DeleteByUser empty user: got %v, want ErrEmptyUser", err)
	}
	if err := c.DeleteByTenant(ctx, ""); !errors.Is(err, semanticcache.ErrEmptyTenant) {
		t.Errorf("DeleteByTenant empty tenant: got %v, want ErrEmptyTenant", err)
	}
	// A per-user Get with no user id is rejected, not silently widened.
	if _, _, err := c.Get(ctx, semanticcache.Key{TenantID: "acme", Scope: semanticcache.ScopePerUser}, "q"); !errors.Is(err, semanticcache.ErrEmptyUser) {
		t.Errorf("Get per-user empty user: got %v, want ErrEmptyUser", err)
	}
	// A per-session Get with no session id is rejected.
	if _, _, err := c.Get(ctx, semanticcache.Key{TenantID: "acme", Scope: semanticcache.ScopePerSession}, "q"); !errors.Is(err, semanticcache.ErrEmptySession) {
		t.Errorf("Get per-session empty session: got %v, want ErrEmptySession", err)
	}
}

func TestTTLExpiry(t *testing.T) {
	now := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	c := semanticcache.NewInMemory(nil, 30*time.Second, semanticcache.DefaultSimilarityThreshold, clock)
	ctx := context.Background()
	key := userKey("acme", "alice")
	if err := c.Put(ctx, key, "ttl probe query", "answer"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, ok, _ := c.Get(ctx, key, "ttl probe query"); !ok {
		t.Fatal("entry should be live immediately after Put")
	}
	// Advance the clock past the TTL; the entry must self-evict.
	now = now.Add(31 * time.Second)
	if _, ok, _ := c.Get(ctx, key, "ttl probe query"); ok {
		t.Error("entry should have expired past its TTL")
	}
}

func TestPerSessionScopeIsolatesSessions(t *testing.T) {
	c := newCache()
	ctx := context.Background()
	s1 := semanticcache.Key{
		TenantID: "acme", Scope: semanticcache.ScopePerSession, SessionID: "sess_1",
		Model: "claude-opus", Provider: "anthropic",
	}
	s2 := s1
	s2.SessionID = "sess_2"
	const query = "session scoped question"
	if err := c.Put(ctx, s1, query, "session one answer"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, ok, _ := c.Get(ctx, s1, query); !ok {
		t.Error("same-session query should hit")
	}
	if _, ok, _ := c.Get(ctx, s2, query); ok {
		t.Error("per-session scope must not share a response across sessions")
	}
}

// TestExportedErasureContractHelper proves the §4.9 exported
// ValidateSemanticCacheErasure helper passes against the default
// in-memory implementation. Pluggable implementations call the same
// helper from their own test packages.
func TestExportedErasureContractHelper(t *testing.T) {
	semanticcache.ValidateSemanticCacheErasure(t, newCache())
}

func TestInvalidScopeRejected(t *testing.T) {
	c := newCache()
	ctx := context.Background()
	bad := semanticcache.Key{TenantID: "acme", Scope: semanticcache.CacheScope("global")}
	if err := c.Put(ctx, bad, "q", "a"); !errors.Is(err, semanticcache.ErrInvalidScope) {
		t.Errorf("Put with an invalid scope: got %v, want ErrInvalidScope", err)
	}
}
