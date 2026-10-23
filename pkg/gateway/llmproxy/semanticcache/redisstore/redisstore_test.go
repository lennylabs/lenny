// SPDX-License-Identifier: MIT

package redisstore_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/semanticcache"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/semanticcache/redisstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/rediskeys"
	"github.com/lennylabs/lenny/pkg/redisconn/clustertest"
	"github.com/lennylabs/lenny/pkg/redisconn/slotguard"
)

func userKey(tenant, user string) semanticcache.Key {
	return semanticcache.Key{
		TenantID: tenant, Scope: semanticcache.ScopePerUser, UserID: user,
		Model: "claude-opus", Provider: "anthropic",
	}
}

// newStore returns a Store over a miniredis client with the cross-slot
// hook installed.
func newStore(t *testing.T) *redisstore.Store {
	t.Helper()
	mr := miniredis.RunT(t)
	cl := slotguard.Install(redis.NewClient(&redis.Options{Addr: mr.Addr()}))
	t.Cleanup(func() { _ = cl.Close() })
	return redisstore.New(cl, nil, 0, 0)
}

// spec: §12.4 Redis HA and Failure Modes (the `t:{tenant_id}:` prefix
// hashes a tenant's keys to one slot); §4.9 (Semantic Caching). Put writes
// the entry and its index set in one MULTI block and Get reads the
// candidate entries with one MGET; both stay in the tenant's slot, so the
// cross-slot hook admits them and the round trip returns the entry.
func TestPutGetStayInTenantSlot_spec_12_4(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	k := userKey("acme", "alice")
	for i := 0; i < 3; i++ {
		if err := s.Put(ctx, k, fmt.Sprintf("question %d", i), fmt.Sprintf("answer %d", i)); err != nil {
			t.Fatalf("Put %d: %v", i, err)
		}
	}
	got, ok, err := s.Get(ctx, k, "question 1")
	if err != nil || !ok || got.Response != "answer 1" {
		t.Fatalf("Get = %+v, %v, %v; want answer 1", got, ok, err)
	}
	if err := s.DeleteByUser(ctx, "acme", "alice"); err != nil {
		t.Fatalf("DeleteByUser: %v", err)
	}
	if _, ok, _ := s.Get(ctx, k, "question 1"); ok {
		t.Fatal("entry survived DeleteByUser")
	}
}

// spec: §12.4 Redis HA and Failure Modes (Redis Cluster at Tier 3); §12.8
// (erasure scope). On a multi-master cluster DeleteByUser and
// DeleteByTenant remove every entry and index set of the target (a bare
// SCAN on the cluster client reaches one master only), and the other
// tenant's cache survives.
func TestErasureCoversClusterMasters_spec_12_8(t *testing.T) {
	cl := clustertest.New(t, 3)
	s := redisstore.New(cl.Client, nil, 0, 0)
	ctx := context.Background()
	for _, tenant := range []string{"acme", "globex"} {
		for _, user := range []string{"alice", "bob", "carol"} {
			for i := 0; i < 5; i++ {
				if err := s.Put(ctx, userKey(tenant, user), fmt.Sprintf("q%d", i), "a"); err != nil {
					t.Fatalf("Put: %v", err)
				}
			}
		}
	}
	if !cl.ServerFor(rediskeys.TenantPrefix("acme")).Exists(mustEntryKey(t, userKey("acme", "alice"), "q0")) {
		t.Fatal("acme entry is not on the tenant's master")
	}
	if err := s.DeleteByUser(ctx, "globex", "bob"); err != nil {
		t.Fatalf("DeleteByUser: %v", err)
	}
	if err := s.DeleteByTenant(ctx, "acme"); err != nil {
		t.Fatalf("DeleteByTenant: %v", err)
	}
	globex := 0
	for _, k := range cl.Keys() {
		if strings.HasPrefix(k, semanticcache.TenantKeyPrefix("acme")) {
			t.Errorf("acme key %q survived DeleteByTenant", k)
		}
		if strings.HasPrefix(k, semanticcache.UserKeyPrefix("globex", "bob")) {
			t.Errorf("globex/bob key %q survived DeleteByUser", k)
		}
		if strings.HasPrefix(k, semanticcache.TenantKeyPrefix("globex")) {
			globex++
		}
	}
	if globex == 0 {
		t.Fatal("DeleteByTenant(acme) removed globex's cache")
	}
	if _, ok, err := s.Get(ctx, userKey("globex", "carol"), "q2"); err != nil || !ok {
		t.Fatalf("globex/carol Get after erasures = %v, %v; want a hit", ok, err)
	}
}

func mustEntryKey(t *testing.T, k semanticcache.Key, query string) string {
	t.Helper()
	ek, err := k.EntryKey(k.QueryHash(query))
	if err != nil {
		t.Fatal(err)
	}
	return ek
}
