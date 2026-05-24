// SPDX-License-Identifier: MIT

package semanticcache

import (
	"context"
	"testing"
)

// ValidateSemanticCacheErasure is the §4.9 / §12.2.1 erasure contract
// validation helper. Pluggable SemanticCache implementations import it
// and must pass it: it is exported (not a local _test.go helper) so a
// third-party implementation in another module can call it.
//
// spec: §4.9 lines 1554-1556 — the helper verifies that (a)
// DeleteByUser removes every cached entry for the specified user, (b) a
// subsequent Get for the deleted user returns no hit, and (c) entries
// for another user in the same tenant are unaffected. A pluggable
// implementation that silently no-ops DeleteByUser fails (b); one that
// over-deletes fails (c).
func ValidateSemanticCacheErasure(t *testing.T, cache SemanticCache) {
	t.Helper()
	ctx := context.Background()

	aliceKey := Key{
		TenantID: "acme", Scope: ScopePerUser, UserID: "alice",
		Model: "claude-opus", Provider: "anthropic",
	}
	bobKey := aliceKey
	bobKey.UserID = "bob"

	const aliceQuery = "alice contract question"
	const bobQuery = "bob contract question"
	if err := cache.Put(ctx, aliceKey, aliceQuery, "alice answer"); err != nil {
		t.Fatalf("ValidateSemanticCacheErasure: Put alice: %v", err)
	}
	if err := cache.Put(ctx, bobKey, bobQuery, "bob answer"); err != nil {
		t.Fatalf("ValidateSemanticCacheErasure: Put bob: %v", err)
	}

	// Precondition: alice's entry is present before erasure, so a later
	// miss is attributable to the erasure rather than a never-written
	// entry.
	if _, ok, err := cache.Get(ctx, aliceKey, aliceQuery); err != nil || !ok {
		t.Fatalf("ValidateSemanticCacheErasure: alice entry must hit before erasure: ok=%v err=%v", ok, err)
	}

	if err := cache.DeleteByUser(ctx, "acme", "alice"); err != nil {
		t.Fatalf("ValidateSemanticCacheErasure: DeleteByUser alice: %v", err)
	}

	// (a)+(b): alice's entry is gone and a later Get misses.
	if _, ok, err := cache.Get(ctx, aliceKey, aliceQuery); err != nil {
		t.Fatalf("ValidateSemanticCacheErasure: Get alice after erasure: %v", err)
	} else if ok {
		t.Error("ValidateSemanticCacheErasure: DeleteByUser did not erase the user's cached entry")
	}

	// (c): another user in the same tenant is untouched.
	if _, ok, err := cache.Get(ctx, bobKey, bobQuery); err != nil {
		t.Fatalf("ValidateSemanticCacheErasure: Get bob after erasure: %v", err)
	} else if !ok {
		t.Error("ValidateSemanticCacheErasure: DeleteByUser erased a non-target user's entry")
	}

	// Erasure is idempotent: a repeat for the same user returns nil.
	if err := cache.DeleteByUser(ctx, "acme", "alice"); err != nil {
		t.Errorf("ValidateSemanticCacheErasure: repeat DeleteByUser must be idempotent: %v", err)
	}
}
