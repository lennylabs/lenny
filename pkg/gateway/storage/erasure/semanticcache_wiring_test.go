// SPDX-License-Identifier: MIT

package erasure_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/semanticcache"
	"github.com/lennylabs/lenny/pkg/gateway/storage/erasure"
)

// TestSemanticCacheWiredAsStep2EraserPurgesUser exercises the §12.8
// step-2 wiring the gateway installs in cmd/lenny-gateway: the §4.9
// SemanticCache is registered as a user-scoped "semantic_cache" eraser
// so a DeleteByUser job purges the user's cached LLM query/response
// pairs while leaving another user's entries in the same tenant intact.
//
// spec: §12.8 step 2 line 794 (SemanticCache — delete cached
// query/response pairs scoped to the user). F-12.2.16.
func TestSemanticCacheWiredAsStep2EraserPurgesUser(t *testing.T) {
	ctx := context.Background()
	clock := func() time.Time { return time.Unix(1_700_000_000, 0).UTC() }
	cache := semanticcache.NewInMemory(nil, 0, 0, clock)

	aliceKey := semanticcache.Key{
		TenantID: "acme", Scope: semanticcache.ScopePerUser, UserID: "alice",
		Model: "claude-opus", Provider: "anthropic",
	}
	bobKey := aliceKey
	bobKey.UserID = "bob"
	if err := cache.Put(ctx, aliceKey, "alice q", "alice a"); err != nil {
		t.Fatalf("put alice: %v", err)
	}
	if err := cache.Put(ctx, bobKey, "bob q", "bob a"); err != nil {
		t.Fatalf("put bob: %v", err)
	}

	// Mirror the cmd/lenny-gateway adapter: SemanticCache.DeleteByUser
	// returns only an error, so the orchestrator adapter reports a 0 count.
	orch := erasure.New(erasure.Config{UserScoped: []erasure.Eraser{
		{Name: "semantic_cache", DeleteByUser: func(ctx context.Context, tenantID, userID string) (int, error) {
			return 0, cache.DeleteByUser(ctx, tenantID, userID)
		}},
		// SessionStore is the FK parent; including it confirms the wiring
		// passes ValidateOrder alongside the real store.
		{Name: "sessions", DeleteByUser: func(context.Context, string, string) (int, error) { return 1, nil }},
	}})

	if err := erasure.ValidateOrder(erasure.Config{UserScoped: []erasure.Eraser{
		{Name: "semantic_cache"}, {Name: "sessions"},
	}}); err != nil {
		t.Fatalf("ValidateOrder rejected the semantic_cache wiring: %v", err)
	}

	if _, err := orch.DeleteByUser(ctx, "acme", "alice"); err != nil {
		t.Fatalf("DeleteByUser: %v", err)
	}

	if _, ok, err := cache.Get(ctx, aliceKey, "alice q"); err != nil {
		t.Fatalf("get alice after erasure: %v", err)
	} else if ok {
		t.Error("step-2 erasure did not purge alice's cached entry")
	}
	if _, ok, err := cache.Get(ctx, bobKey, "bob q"); err != nil {
		t.Fatalf("get bob after erasure: %v", err)
	} else if !ok {
		t.Error("step-2 erasure over-deleted: bob's entry in the same tenant is gone")
	}
}
