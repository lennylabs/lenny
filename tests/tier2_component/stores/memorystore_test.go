//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §9.4 MemoryStore, exercising the
// Postgres-backed pkg/gateway/memorystore/pgstore against a real
// container with the production migrations applied. Covers the
// write/query round-trip including the jsonb metadata, the
// case-insensitive content match, newest-first ordering, the empty-
// scope sentinel errors, tenant and user isolation, the §9.4 per-user
// capacity eviction, and the §12.8 DeleteByUser / DeleteByTenant
// erasure primitives.
package stores_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/memorystore"
	memorypg "github.com/lennylabs/lenny/pkg/gateway/memorystore/pgstore"
)

// memoryScope returns a tenant+user MemoryScope, the §9.4 owning scope
// every memory operation carries.
func memoryScope(tenant, user string) memorystore.MemoryScope {
	return memorystore.MemoryScope{TenantID: tenant, UserID: user}
}

// memoryContentsOf flattens a memory slice to its content strings for
// readable assertion failures.
func memoryContentsOf(ms []memorystore.Memory) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.Content
	}
	return out
}

// spec: 9.4
// diagnosis: the Postgres-backed MemoryStore in
// pkg/gateway/memorystore/pgstore did not behave as specified. Write
// and Query must round-trip a memory including its jsonb metadata, the
// content match must be case-insensitive and newest-first, the
// empty-scope sentinels must hold, memory must not leak across the
// tenant or user boundary, Write must evict each user's oldest
// memories beyond the §9.4 per-user capacity limit, and the §12.8
// DeleteByUser / DeleteByTenant primitives must erase exactly their
// scope and stay idempotent.
func TestMemoryStoreContract(t *testing.T) {
	t.Parallel()
	_, pg := startStore(t)
	store := memorypg.New(pg.Pool)
	ctx := context.Background()

	t.Run("write and query round-trip including jsonb metadata", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		scope := memorystore.MemoryScope{
			TenantID: tenant, UserID: "alice", AgentType: "claude", SessionID: "sess_1",
		}
		md := map[string]any{"source": "chat", "weight": float64(3)}
		if err := store.Write(ctx, scope, []memorystore.Memory{
			{Content: "the deploy key lives in vault", Metadata: md},
		}); err != nil {
			t.Fatalf("Write: %v", err)
		}
		got, err := store.Query(ctx, scope, "DEPLOY", 0) // case-insensitive
		if err != nil {
			t.Fatalf("Query: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("Query returned %d, want 1", len(got))
		}
		m := got[0]
		if m.ID == "" {
			t.Error("Write did not assign an id")
		}
		if m.TenantID != tenant || m.UserID != "alice" ||
			m.AgentType != "claude" || m.SessionID != "sess_1" {
			t.Errorf("scope not stamped: %+v", m)
		}
		if m.CreatedAt.IsZero() {
			t.Error("Write did not stamp CreatedAt")
		}
		if !reflect.DeepEqual(m.Metadata, md) {
			t.Errorf("Metadata lost in jsonb round-trip: got %+v want %+v", m.Metadata, md)
		}
		// A memory written without metadata reads back with a nil map.
		if err := store.Write(ctx, memoryScope(tenant, "bob"),
			[]memorystore.Memory{{Content: "no metadata here"}}); err != nil {
			t.Fatalf("Write bob: %v", err)
		}
		bare, _ := store.List(ctx, memoryScope(tenant, "bob"), memorystore.MemoryFilter{})
		if len(bare) != 1 || bare[0].Metadata != nil {
			t.Errorf("memory written without metadata read back %+v, want nil Metadata", bare)
		}
	})

	t.Run("write rejects an empty scope", func(t *testing.T) {
		if err := store.Write(ctx, memorystore.MemoryScope{UserID: "alice"}, nil); !errors.Is(err, memorystore.ErrEmptyTenant) {
			t.Errorf("Write empty tenant: got %v, want ErrEmptyTenant", err)
		}
		if err := store.Write(ctx, memorystore.MemoryScope{TenantID: "acme"}, nil); !errors.Is(err, memorystore.ErrEmptyUser) {
			t.Errorf("Write empty user: got %v, want ErrEmptyUser", err)
		}
	})

	t.Run("query orders newest-first and applies the limit", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		scope := memoryScope(tenant, "alice")
		base := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour)
		if err := store.Write(ctx, scope, []memorystore.Memory{
			{Content: "oldest", CreatedAt: base},
			{Content: "middle", CreatedAt: base.Add(time.Minute)},
			{Content: "newest", CreatedAt: base.Add(2 * time.Minute)},
		}); err != nil {
			t.Fatalf("Write: %v", err)
		}
		got, err := store.Query(ctx, scope, "", 2)
		if err != nil {
			t.Fatalf("Query: %v", err)
		}
		if len(got) != 2 || got[0].Content != "newest" || got[1].Content != "middle" {
			t.Errorf("Query = %v, want [newest middle] (newest-first, limit 2)", memoryContentsOf(got))
		}
	})

	t.Run("query scope narrows by agent type and session", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		at := time.Now().UTC().Truncate(time.Microsecond)
		if err := store.Write(ctx,
			memorystore.MemoryScope{TenantID: tenant, UserID: "alice", AgentType: "claude", SessionID: "s1"},
			[]memorystore.Memory{{Content: "from claude s1", CreatedAt: at}}); err != nil {
			t.Fatalf("Write claude: %v", err)
		}
		if err := store.Write(ctx,
			memorystore.MemoryScope{TenantID: tenant, UserID: "alice", AgentType: "gpt", SessionID: "s2"},
			[]memorystore.Memory{{Content: "from gpt s2", CreatedAt: at}}); err != nil {
			t.Fatalf("Write gpt: %v", err)
		}
		narrowed, _ := store.Query(ctx,
			memorystore.MemoryScope{TenantID: tenant, UserID: "alice", AgentType: "claude"}, "", 0)
		if len(narrowed) != 1 || narrowed[0].Content != "from claude s1" {
			t.Errorf("agent-scoped query = %v, want [from claude s1]", memoryContentsOf(narrowed))
		}
		all, _ := store.Query(ctx, memoryScope(tenant, "alice"), "", 0)
		if len(all) != 2 {
			t.Errorf("tenant+user query returned %d, want 2", len(all))
		}
	})

	t.Run("memory does not leak across the tenant boundary", func(t *testing.T) {
		owner := freshTenant(t, ctx, pg)
		intruder := freshTenant(t, ctx, pg)
		if err := store.Write(ctx, memoryScope(owner, "alice"),
			[]memorystore.Memory{{Content: "acme secret"}}); err != nil {
			t.Fatalf("Write: %v", err)
		}
		got, err := store.Query(ctx, memoryScope(intruder, "alice"), "secret", 0)
		if err != nil {
			t.Fatalf("Query: %v", err)
		}
		if len(got) != 0 {
			t.Error("a memory leaked across the tenant boundary")
		}
	})

	t.Run("memory does not leak across the user boundary within a tenant", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		if err := store.Write(ctx, memoryScope(tenant, "alice"),
			[]memorystore.Memory{{Content: "alice's memory"}}); err != nil {
			t.Fatalf("Write: %v", err)
		}
		got, err := store.List(ctx, memoryScope(tenant, "bob"), memorystore.MemoryFilter{})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(got) != 0 {
			t.Error("a memory leaked across the user boundary within a tenant")
		}
		// A Delete by another user must not remove the owner's memory.
		owned, _ := store.List(ctx, memoryScope(tenant, "alice"), memorystore.MemoryFilter{})
		if err := store.Delete(ctx, memoryScope(tenant, "bob"), []string{owned[0].ID}); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		after, _ := store.List(ctx, memoryScope(tenant, "alice"), memorystore.MemoryFilter{})
		if len(after) != 1 {
			t.Error("a memory was deleted by a caller outside its user scope")
		}
	})

	t.Run("write evicts each user's oldest memories beyond capacity", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		// A capacity-3 store proves oldest-first eviction without
		// inserting the §9.4 default of 10000 rows.
		capped := memorypg.NewWithMaxPerUser(pg.Pool, 3)
		alice := memoryScope(tenant, "alice")
		bob := memoryScope(tenant, "bob")
		base := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour)
		for i := 0; i < 5; i++ {
			at := base.Add(time.Duration(i) * time.Minute)
			if err := capped.Write(ctx, alice,
				[]memorystore.Memory{{Content: string(rune('a' + i)), CreatedAt: at}}); err != nil {
				t.Fatalf("Write alice %d: %v", i, err)
			}
			if err := capped.Write(ctx, bob,
				[]memorystore.Memory{{Content: "b" + string(rune('0'+i)), CreatedAt: at}}); err != nil {
				t.Fatalf("Write bob %d: %v", i, err)
			}
		}
		got, _ := capped.List(ctx, alice, memorystore.MemoryFilter{})
		if len(got) != 3 {
			t.Fatalf("after 5 writes with cap 3: %d memories, want 3", len(got))
		}
		for _, m := range got {
			if m.Content == "a" || m.Content == "b" {
				t.Errorf("evicted memory %q survived; want oldest-first eviction", m.Content)
			}
		}
		// Eviction is per-user: bob is independently capped at 3.
		bobGot, _ := capped.List(ctx, bob, memorystore.MemoryFilter{})
		if len(bobGot) != 3 {
			t.Errorf("bob has %d memories, want 3 (eviction is per-user)", len(bobGot))
		}
	})

	t.Run("DeleteByUser erases one user and is idempotent", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		if err := store.Write(ctx, memoryScope(tenant, "alice"),
			[]memorystore.Memory{{Content: "a1"}, {Content: "a2"}}); err != nil {
			t.Fatalf("Write alice: %v", err)
		}
		if err := store.Write(ctx, memoryScope(tenant, "bob"),
			[]memorystore.Memory{{Content: "b1"}}); err != nil {
			t.Fatalf("Write bob: %v", err)
		}
		if err := store.DeleteByUser(ctx, tenant, "alice"); err != nil {
			t.Fatalf("DeleteByUser: %v", err)
		}
		if got, _ := store.List(ctx, memoryScope(tenant, "alice"), memorystore.MemoryFilter{}); len(got) != 0 {
			t.Errorf("alice's memories survived DeleteByUser: %d", len(got))
		}
		if got, _ := store.List(ctx, memoryScope(tenant, "bob"), memorystore.MemoryFilter{}); len(got) != 1 {
			t.Error("bob's memory was erased by a DeleteByUser scoped to alice")
		}
		// Idempotent: a repeat call after successful deletion returns nil.
		if err := store.DeleteByUser(ctx, tenant, "alice"); err != nil {
			t.Errorf("repeat DeleteByUser: %v", err)
		}
		if err := store.DeleteByUser(ctx, "", "alice"); !errors.Is(err, memorystore.ErrEmptyTenant) {
			t.Errorf("DeleteByUser empty tenant: got %v, want ErrEmptyTenant", err)
		}
		if err := store.DeleteByUser(ctx, tenant, ""); !errors.Is(err, memorystore.ErrEmptyUser) {
			t.Errorf("DeleteByUser empty user: got %v, want ErrEmptyUser", err)
		}
	})

	t.Run("DeleteByTenant erases one tenant and is idempotent", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		other := freshTenant(t, ctx, pg)
		if err := store.Write(ctx, memoryScope(tenant, "alice"),
			[]memorystore.Memory{{Content: "a"}}); err != nil {
			t.Fatalf("Write alice: %v", err)
		}
		if err := store.Write(ctx, memoryScope(tenant, "bob"),
			[]memorystore.Memory{{Content: "b"}}); err != nil {
			t.Fatalf("Write bob: %v", err)
		}
		if err := store.Write(ctx, memoryScope(other, "carol"),
			[]memorystore.Memory{{Content: "c"}}); err != nil {
			t.Fatalf("Write carol: %v", err)
		}
		if err := store.DeleteByTenant(ctx, tenant); err != nil {
			t.Fatalf("DeleteByTenant: %v", err)
		}
		if got, _ := store.List(ctx, memoryScope(tenant, "alice"), memorystore.MemoryFilter{}); len(got) != 0 {
			t.Error("alice's memory survived DeleteByTenant")
		}
		if got, _ := store.List(ctx, memoryScope(tenant, "bob"), memorystore.MemoryFilter{}); len(got) != 0 {
			t.Error("bob's memory survived DeleteByTenant")
		}
		if got, _ := store.List(ctx, memoryScope(other, "carol"), memorystore.MemoryFilter{}); len(got) != 1 {
			t.Error("another tenant's memory was erased by DeleteByTenant")
		}
		// Idempotent: a repeat call after successful deletion returns nil.
		if err := store.DeleteByTenant(ctx, tenant); err != nil {
			t.Errorf("repeat DeleteByTenant: %v", err)
		}
		if err := store.DeleteByTenant(ctx, ""); !errors.Is(err, memorystore.ErrEmptyTenant) {
			t.Errorf("DeleteByTenant empty: got %v, want ErrEmptyTenant", err)
		}
	})
}
