// SPDX-License-Identifier: MIT

package memorystore_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/session/memorystore"
)

// spec: §9.4 MemoryStore — tenant-scoped agent memory.

var t0 = time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)

func acmeAlice() memorystore.MemoryScope {
	return memorystore.MemoryScope{TenantID: "acme", UserID: "alice"}
}

func mem(content string, at time.Time) memorystore.Memory {
	return memorystore.Memory{Content: content, CreatedAt: at}
}

func TestWriteStampsScopeAndID(t *testing.T) {
	s := memorystore.NewInMemory(0, nil)
	scope := memorystore.MemoryScope{TenantID: "acme", UserID: "alice", AgentType: "claude", SessionID: "sess_1"}
	if err := s.Write(context.Background(), scope, []memorystore.Memory{{Content: "remember this"}}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := s.List(context.Background(), scope, memorystore.MemoryFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("List returned %d, want 1", len(got))
	}
	m := got[0]
	if m.ID == "" || !strings.HasPrefix(m.ID, "mem_") {
		t.Errorf("Write did not assign a mem_ id: %q", m.ID)
	}
	if m.TenantID != "acme" || m.UserID != "alice" || m.AgentType != "claude" || m.SessionID != "sess_1" {
		t.Errorf("scope not stamped: %+v", m)
	}
	if m.CreatedAt.IsZero() {
		t.Error("Write did not stamp CreatedAt")
	}
}

func TestWriteRejectsEmptyScope(t *testing.T) {
	s := memorystore.NewInMemory(0, nil)
	if err := s.Write(context.Background(), memorystore.MemoryScope{UserID: "alice"}, nil); !errors.Is(err, memorystore.ErrEmptyTenant) {
		t.Errorf("Write with empty tenant: got %v, want ErrEmptyTenant", err)
	}
	if err := s.Write(context.Background(), memorystore.MemoryScope{TenantID: "acme"}, nil); !errors.Is(err, memorystore.ErrEmptyUser) {
		t.Errorf("Write with empty user: got %v, want ErrEmptyUser", err)
	}
}

func TestQueryMatchesContent(t *testing.T) {
	s := memorystore.NewInMemory(0, nil)
	ctx := context.Background()
	_ = s.Write(ctx, acmeAlice(), []memorystore.Memory{
		mem("the deploy key is in vault", t0),
		mem("lunch was good", t0.Add(time.Minute)),
	})
	got, err := s.Query(ctx, acmeAlice(), "DEPLOY", 0) // case-insensitive
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != 1 || !strings.Contains(got[0].Content, "deploy key") {
		t.Errorf("Query = %v, want the one deploy-key memory", contents(got))
	}
}

func TestQueryNewestFirstAndLimit(t *testing.T) {
	s := memorystore.NewInMemory(0, nil)
	ctx := context.Background()
	_ = s.Write(ctx, acmeAlice(), []memorystore.Memory{
		mem("oldest", t0),
		mem("middle", t0.Add(time.Minute)),
		mem("newest", t0.Add(2*time.Minute)),
	})
	got, _ := s.Query(ctx, acmeAlice(), "", 2)
	if len(got) != 2 || got[0].Content != "newest" || got[1].Content != "middle" {
		t.Errorf("Query = %v, want [newest middle] (newest-first, limit 2)", contents(got))
	}
}

func TestQueryTenantIsolation(t *testing.T) {
	s := memorystore.NewInMemory(0, nil)
	ctx := context.Background()
	_ = s.Write(ctx, acmeAlice(), []memorystore.Memory{mem("acme secret", t0)})
	got, err := s.Query(ctx, memorystore.MemoryScope{TenantID: "globex", UserID: "alice"}, "secret", 0)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != 0 {
		t.Error("a memory leaked across the tenant boundary")
	}
}

func TestQueryUserIsolation(t *testing.T) {
	s := memorystore.NewInMemory(0, nil)
	ctx := context.Background()
	_ = s.Write(ctx, acmeAlice(), []memorystore.Memory{mem("alice's memory", t0)})
	got, _ := s.Query(ctx, memorystore.MemoryScope{TenantID: "acme", UserID: "bob"}, "", 0)
	if len(got) != 0 {
		t.Error("a memory leaked across the user boundary within a tenant")
	}
}

func TestQueryScopeNarrowsByAgentAndSession(t *testing.T) {
	s := memorystore.NewInMemory(0, nil)
	ctx := context.Background()
	_ = s.Write(ctx, memorystore.MemoryScope{TenantID: "acme", UserID: "alice", AgentType: "claude", SessionID: "s1"},
		[]memorystore.Memory{mem("from claude s1", t0)})
	_ = s.Write(ctx, memorystore.MemoryScope{TenantID: "acme", UserID: "alice", AgentType: "gpt", SessionID: "s2"},
		[]memorystore.Memory{mem("from gpt s2", t0)})

	// Scoping to agentType=claude returns only the claude memory.
	got, _ := s.Query(ctx, memorystore.MemoryScope{TenantID: "acme", UserID: "alice", AgentType: "claude"}, "", 0)
	if len(got) != 1 || got[0].Content != "from claude s1" {
		t.Errorf("agent-scoped query = %v, want [from claude s1]", contents(got))
	}
	// An unscoped query (tenant+user only) sees both.
	all, _ := s.Query(ctx, acmeAlice(), "", 0)
	if len(all) != 2 {
		t.Errorf("unscoped query returned %d, want 2", len(all))
	}
}

func TestDeleteWithinScope(t *testing.T) {
	s := memorystore.NewInMemory(0, nil)
	ctx := context.Background()
	_ = s.Write(ctx, acmeAlice(), []memorystore.Memory{mem("keep", t0), mem("drop", t0)})
	listed, _ := s.List(ctx, acmeAlice(), memorystore.MemoryFilter{})
	var dropID string
	for _, m := range listed {
		if m.Content == "drop" {
			dropID = m.ID
		}
	}
	if err := s.Delete(ctx, acmeAlice(), []string{dropID}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	after, _ := s.List(ctx, acmeAlice(), memorystore.MemoryFilter{})
	if len(after) != 1 || after[0].Content != "keep" {
		t.Errorf("after Delete = %v, want [keep]", contents(after))
	}
}

func TestDeleteIgnoresCrossScopeID(t *testing.T) {
	s := memorystore.NewInMemory(0, nil)
	ctx := context.Background()
	_ = s.Write(ctx, acmeAlice(), []memorystore.Memory{mem("alice's", t0)})
	listed, _ := s.List(ctx, acmeAlice(), memorystore.MemoryFilter{})
	aliceID := listed[0].ID
	// bob tries to delete alice's memory by id — it must not be removed.
	if err := s.Delete(ctx, memorystore.MemoryScope{TenantID: "acme", UserID: "bob"}, []string{aliceID}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	after, _ := s.List(ctx, acmeAlice(), memorystore.MemoryFilter{})
	if len(after) != 1 {
		t.Error("a memory was deleted by a caller outside its scope")
	}
}

func TestDeleteByUser(t *testing.T) {
	s := memorystore.NewInMemory(0, nil)
	ctx := context.Background()
	_ = s.Write(ctx, acmeAlice(), []memorystore.Memory{mem("a1", t0), mem("a2", t0)})
	_ = s.Write(ctx, memorystore.MemoryScope{TenantID: "acme", UserID: "bob"}, []memorystore.Memory{mem("b1", t0)})

	if err := s.DeleteByUser(ctx, "acme", "alice"); err != nil {
		t.Fatalf("DeleteByUser: %v", err)
	}
	if got, _ := s.List(ctx, acmeAlice(), memorystore.MemoryFilter{}); len(got) != 0 {
		t.Errorf("alice's memories survived DeleteByUser: %d", len(got))
	}
	if got, _ := s.List(ctx, memorystore.MemoryScope{TenantID: "acme", UserID: "bob"}, memorystore.MemoryFilter{}); len(got) != 1 {
		t.Error("bob's memory was erased by a DeleteByUser scoped to alice")
	}
	// Idempotent: a repeat call after successful deletion returns nil.
	if err := s.DeleteByUser(ctx, "acme", "alice"); err != nil {
		t.Errorf("repeat DeleteByUser: %v", err)
	}
}

func TestDeleteByUserRejectsEmptyIDs(t *testing.T) {
	s := memorystore.NewInMemory(0, nil)
	if err := s.DeleteByUser(context.Background(), "", "alice"); !errors.Is(err, memorystore.ErrEmptyTenant) {
		t.Errorf("DeleteByUser empty tenant: got %v, want ErrEmptyTenant", err)
	}
	if err := s.DeleteByUser(context.Background(), "acme", ""); !errors.Is(err, memorystore.ErrEmptyUser) {
		t.Errorf("DeleteByUser empty user: got %v, want ErrEmptyUser", err)
	}
}

func TestDeleteByTenant(t *testing.T) {
	s := memorystore.NewInMemory(0, nil)
	ctx := context.Background()
	_ = s.Write(ctx, acmeAlice(), []memorystore.Memory{mem("a", t0)})
	_ = s.Write(ctx, memorystore.MemoryScope{TenantID: "acme", UserID: "bob"}, []memorystore.Memory{mem("b", t0)})
	_ = s.Write(ctx, memorystore.MemoryScope{TenantID: "globex", UserID: "carol"}, []memorystore.Memory{mem("c", t0)})

	if err := s.DeleteByTenant(ctx, "acme"); err != nil {
		t.Fatalf("DeleteByTenant: %v", err)
	}
	if got, _ := s.List(ctx, acmeAlice(), memorystore.MemoryFilter{}); len(got) != 0 {
		t.Error("acme memories survived DeleteByTenant")
	}
	if got, _ := s.List(ctx, memorystore.MemoryScope{TenantID: "globex", UserID: "carol"}, memorystore.MemoryFilter{}); len(got) != 1 {
		t.Error("another tenant's memory was erased by DeleteByTenant")
	}
	if err := s.DeleteByTenant(ctx, ""); !errors.Is(err, memorystore.ErrEmptyTenant) {
		t.Errorf("DeleteByTenant empty: got %v, want ErrEmptyTenant", err)
	}
}

func TestWriteEvictsOldestOverCapacity(t *testing.T) {
	s := memorystore.NewInMemory(3, nil) // cap 3 per user
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		_ = s.Write(ctx, acmeAlice(), []memorystore.Memory{
			mem(string(rune('a'+i)), t0.Add(time.Duration(i)*time.Minute)),
		})
	}
	got, _ := s.List(ctx, acmeAlice(), memorystore.MemoryFilter{})
	if len(got) != 3 {
		t.Fatalf("after 5 writes with cap 3: %d memories, want 3", len(got))
	}
	// The two oldest ('a', 'b') are evicted; 'c','d','e' remain.
	for _, m := range got {
		if m.Content == "a" || m.Content == "b" {
			t.Errorf("evicted memory %q survived; want oldest-first eviction", m.Content)
		}
	}
}

func TestEvictionIsPerUser(t *testing.T) {
	s := memorystore.NewInMemory(2, nil)
	ctx := context.Background()
	bob := memorystore.MemoryScope{TenantID: "acme", UserID: "bob"}
	for i := 0; i < 2; i++ {
		_ = s.Write(ctx, acmeAlice(), []memorystore.Memory{mem("a", t0.Add(time.Duration(i)*time.Minute))})
		_ = s.Write(ctx, bob, []memorystore.Memory{mem("b", t0.Add(time.Duration(i)*time.Minute))})
	}
	// Each user is at its own cap of 2 — no cross-user eviction.
	if got, _ := s.List(ctx, acmeAlice(), memorystore.MemoryFilter{}); len(got) != 2 {
		t.Errorf("alice has %d memories, want 2", len(got))
	}
	if got, _ := s.List(ctx, bob, memorystore.MemoryFilter{}); len(got) != 2 {
		t.Errorf("bob has %d memories, want 2", len(got))
	}
}

func TestCloneIsolatesMetadata(t *testing.T) {
	s := memorystore.NewInMemory(0, nil)
	ctx := context.Background()
	m := mem("x", t0)
	m.Metadata = map[string]any{"k": "v"}
	_ = s.Write(ctx, acmeAlice(), []memorystore.Memory{m})
	got, _ := s.List(ctx, acmeAlice(), memorystore.MemoryFilter{})
	got[0].Metadata["k"] = "tampered"
	reread, _ := s.List(ctx, acmeAlice(), memorystore.MemoryFilter{})
	if reread[0].Metadata["k"] != "v" {
		t.Error("mutating a returned Metadata map reached into stored state")
	}
}

func TestNewIDFormat(t *testing.T) {
	seen := map[string]bool{}
	for range 100 {
		id := memorystore.NewID()
		if !strings.HasPrefix(id, "mem_") || len(id) != len("mem_")+16 {
			t.Fatalf("NewID = %q", id)
		}
		if seen[id] {
			t.Fatalf("NewID returned a duplicate %q", id)
		}
		seen[id] = true
	}
}

func contents(ms []memorystore.Memory) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.Content
	}
	return out
}
