// SPDX-License-Identifier: MIT

// Unit coverage for the §8.10 line 129 per-replica LRU read cache that
// fronts the durable session_tree_archive store. Exercises the
// write-through, hit/miss, eviction-bound, node-key-sharing, and
// replay-warming behaviour against an instrumented in-memory inner
// store so a cache hit can be distinguished from an inner-store call.
//
// spec: §8.10 line 129; §8.2 line 129.
package treearchive

import (
	"context"
	"strconv"
	"testing"
	"time"
)

// countingStore wraps Memory and counts the inner reads so a test can
// assert a cache hit served a request without touching the inner store.
type countingStore struct {
	inner      *Memory
	gets       int
	getByNodes int
	replays    int
	archives   int
}

func newCountingStore() *countingStore { return &countingStore{inner: NewMemory()} }

func (c *countingStore) Archive(ctx context.Context, n ArchivedNode) error {
	c.archives++
	return c.inner.Archive(ctx, n)
}

func (c *countingStore) Replay(ctx context.Context, tenantID, rootSessionID string) ([]ArchivedNode, error) {
	c.replays++
	return c.inner.Replay(ctx, tenantID, rootSessionID)
}

func (c *countingStore) Get(ctx context.Context, tenantID, rootSessionID, nodeSessionID string) (ArchivedNode, error) {
	c.gets++
	return c.inner.Get(ctx, tenantID, rootSessionID, nodeSessionID)
}

func (c *countingStore) GetByNode(ctx context.Context, tenantID, nodeSessionID string) (ArchivedNode, error) {
	c.getByNodes++
	return c.inner.GetByNode(ctx, tenantID, nodeSessionID)
}

func cnode(root, id string) ArchivedNode {
	return ArchivedNode{
		TenantID:        "acme",
		RootSessionID:   root,
		NodeSessionID:   id,
		ParentSessionID: root,
		State:           "completed",
		Result:          `{"taskId":"` + id + `"}`,
		SettledAt:       time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

// spec: §8.10 line 129 — a write-through refreshes the cache so the
// follow-up read serves from memory without an inner Get.
func TestCachedArchiveWriteThroughServesGetFromCache(t *testing.T) {
	inner := newCountingStore()
	c := NewCached(inner, 8)
	if err := c.Archive(context.Background(), cnode("root", "c1")); err != nil {
		t.Fatalf("archive: %v", err)
	}
	got, err := c.Get(context.Background(), "acme", "root", "c1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.NodeSessionID != "c1" {
		t.Errorf("got node %q, want c1", got.NodeSessionID)
	}
	if inner.gets != 0 {
		t.Errorf("inner Get called %d times; write-through should serve from cache", inner.gets)
	}
}

// spec: §8.10 line 129 — a miss falls through to the inner store and
// caches the result; the second read is a hit.
func TestCachedGetMissThenHit(t *testing.T) {
	inner := newCountingStore()
	_ = inner.Archive(context.Background(), cnode("root", "c1"))
	c := NewCached(inner, 8)

	if _, err := c.Get(context.Background(), "acme", "root", "c1"); err != nil {
		t.Fatalf("first get: %v", err)
	}
	if _, err := c.Get(context.Background(), "acme", "root", "c1"); err != nil {
		t.Fatalf("second get: %v", err)
	}
	if inner.gets != 1 {
		t.Errorf("inner Get called %d times, want 1 (miss then hit)", inner.gets)
	}
}

// spec: §8.10 line 129 — Get and GetByNode for the same node share one
// cache entry keyed by (tenant, node) so a GetByNode after a Get is a
// hit.
func TestCachedGetAndGetByNodeShareEntry(t *testing.T) {
	inner := newCountingStore()
	_ = inner.Archive(context.Background(), cnode("root", "c1"))
	c := NewCached(inner, 8)

	if _, err := c.Get(context.Background(), "acme", "root", "c1"); err != nil {
		t.Fatalf("get: %v", err)
	}
	if _, err := c.GetByNode(context.Background(), "acme", "c1"); err != nil {
		t.Fatalf("getbynode: %v", err)
	}
	if inner.getByNodes != 0 {
		t.Errorf("inner GetByNode called %d times; Get should have warmed the shared entry", inner.getByNodes)
	}
}

// spec: §8.10 line 129 — the cache is bounded; a tree with more
// completed branches than the cap evicts the LRU tail rather than
// growing without bound.
func TestCachedEvictsLRUAtCapacity(t *testing.T) {
	inner := newCountingStore()
	c := NewCached(inner, 3)
	for i := 0; i < 5; i++ {
		id := "c" + strconv.Itoa(i)
		if err := c.Archive(context.Background(), cnode("root", id)); err != nil {
			t.Fatalf("archive %s: %v", id, err)
		}
	}
	if c.Len() != 3 {
		t.Errorf("cache holds %d entries, want 3 (eviction bound)", c.Len())
	}
	// c0 and c1 are the LRU tail and were evicted; reading c0 hits the
	// inner store.
	if _, err := c.Get(context.Background(), "acme", "root", "c0"); err != nil {
		t.Fatalf("get c0: %v", err)
	}
	if inner.gets != 1 {
		t.Errorf("inner Get called %d times for evicted c0, want 1", inner.gets)
	}
}

// spec: §8.10 line 129 — a Replay warms the cache with every node it
// returns so the awaiting_client_action resume path (replay then
// per-child read) serves the follow-up reads from memory.
func TestCachedReplayWarmsCache(t *testing.T) {
	inner := newCountingStore()
	_ = inner.Archive(context.Background(), cnode("root", "c1"))
	_ = inner.Archive(context.Background(), cnode("root", "c2"))
	c := NewCached(inner, 8)

	nodes, err := c.Replay(context.Background(), "acme", "root")
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("replay returned %d nodes, want 2", len(nodes))
	}
	if _, err := c.Get(context.Background(), "acme", "root", "c1"); err != nil {
		t.Fatalf("get c1: %v", err)
	}
	if _, err := c.GetByNode(context.Background(), "acme", "c2"); err != nil {
		t.Fatalf("getbynode c2: %v", err)
	}
	if inner.gets != 0 || inner.getByNodes != 0 {
		t.Errorf("post-replay reads hit inner store (gets=%d getByNodes=%d); replay should warm cache", inner.gets, inner.getByNodes)
	}
}

// spec: §8.10 line 129 — a non-positive cap selects the documented
// default of 128 entries.
func TestNewCachedDefaultsCapacity(t *testing.T) {
	c := NewCached(newCountingStore(), 0)
	if c.cap != DefaultCacheEntries {
		t.Errorf("default cap = %d, want %d", c.cap, DefaultCacheEntries)
	}
}

// spec: §8.10 line 129 — a Get whose cached entry belongs to a
// different tree root does not satisfy the request; it falls through to
// the inner store, which scopes by root.
func TestCachedGetRootMismatchFallsThrough(t *testing.T) {
	inner := newCountingStore()
	_ = inner.Archive(context.Background(), cnode("rootA", "c1"))
	c := NewCached(inner, 8)
	// Warm the entry under rootA via GetByNode (no root in the key).
	if _, err := c.GetByNode(context.Background(), "acme", "c1"); err != nil {
		t.Fatalf("getbynode: %v", err)
	}
	// A Get under the wrong root must not return the cached rootA entry.
	if _, err := c.Get(context.Background(), "acme", "rootB", "c1"); err == nil {
		t.Errorf("Get under rootB returned a cached rootA entry; root scoping violated")
	}
}
