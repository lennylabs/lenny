// SPDX-License-Identifier: MIT

package treearchive

import (
	"container/list"
	"context"
	"sync"
)

// DefaultCacheEntries is the §8.10 line 129 per-replica LRU cache size:
// "the gateway fetches it from Postgres on demand (with a per-replica
// LRU cache, default 128 entries)."
const DefaultCacheEntries = 128

// Cached fronts a durable Store with a per-replica LRU read cache. It
// implements the §8.10 line 129 / §8.2 line 129 caching model: the
// inner Store (Postgres in production) is the authoritative record, and
// this wrapper keeps the hot set of recently-read settled nodes in
// memory so a parent re-reading a child's result does not hit Postgres
// every time. The cache is bounded so a long-running tree with many
// completed branches does not accumulate unbounded memory on a replica.
//
// The cache is keyed by (tenant_id, node_session_id): a node id is
// globally unique, so Get and GetByNode share one cache entry per node.
// Archive writes through to the inner store and refreshes the cached
// entry. Replay passes through to the inner store and warms the cache
// with every node it returns, so a replay-then-read sequence (the §7.1
// awaiting_client_action resume path) serves the follow-up reads from
// memory.
type Cached struct {
	inner Store
	cap   int

	mu    sync.Mutex
	ll    *list.List               // MRU at front, LRU at back
	items map[string]*list.Element // cache key -> *list.Element holding entry
}

// entry is one cached node held in the LRU list.
type entry struct {
	key  string
	node ArchivedNode
}

// NewCached wraps inner with an LRU read cache holding at most
// maxEntries nodes. A maxEntries <= 0 selects DefaultCacheEntries.
func NewCached(inner Store, maxEntries int) *Cached {
	if maxEntries <= 0 {
		maxEntries = DefaultCacheEntries
	}
	return &Cached{
		inner: inner,
		cap:   maxEntries,
		ll:    list.New(),
		items: make(map[string]*list.Element),
	}
}

var _ Store = (*Cached)(nil)

// cacheKey joins the tenant and node ids. The NUL separator cannot
// appear in an id, so distinct pairs never collide.
func cacheKey(tenantID, nodeSessionID string) string {
	return tenantID + "\x00" + nodeSessionID
}

// put inserts or refreshes the entry for n and evicts the LRU tail when
// the cache is over capacity. The caller must not hold the lock.
func (c *Cached) put(n ArchivedNode) {
	k := cacheKey(n.TenantID, n.NodeSessionID)
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[k]; ok {
		el.Value.(*entry).node = n
		c.ll.MoveToFront(el)
		return
	}
	c.items[k] = c.ll.PushFront(&entry{key: k, node: n})
	for c.ll.Len() > c.cap {
		oldest := c.ll.Back()
		if oldest == nil {
			break
		}
		c.ll.Remove(oldest)
		delete(c.items, oldest.Value.(*entry).key)
	}
}

// lookup returns the cached node and true on a hit, promoting it to MRU.
func (c *Cached) lookup(tenantID, nodeSessionID string) (ArchivedNode, bool) {
	k := cacheKey(tenantID, nodeSessionID)
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[k]
	if !ok {
		return ArchivedNode{}, false
	}
	c.ll.MoveToFront(el)
	return el.Value.(*entry).node, true
}

// Archive writes through to the inner store and refreshes the cache so
// a subsequent read observes the latest record without a store round
// trip. A write-through failure leaves the cache untouched.
func (c *Cached) Archive(ctx context.Context, n ArchivedNode) error {
	if err := c.inner.Archive(ctx, n); err != nil {
		return err
	}
	c.put(n)
	return nil
}

// Replay passes through to the inner store (a tree-wide scan is never
// served from the per-node cache) and warms the cache with every node
// it returns.
func (c *Cached) Replay(ctx context.Context, tenantID, rootSessionID string) ([]ArchivedNode, error) {
	nodes, err := c.inner.Replay(ctx, tenantID, rootSessionID)
	if err != nil {
		return nil, err
	}
	for _, n := range nodes {
		c.put(n)
	}
	return nodes, nil
}

// Get serves a cache hit from memory and falls through to the inner
// store on a miss, caching the result. The cache key ignores the root
// id (a node id is globally unique), so a Get and a GetByNode for the
// same node share one entry.
func (c *Cached) Get(ctx context.Context, tenantID, rootSessionID, nodeSessionID string) (ArchivedNode, error) {
	if n, ok := c.lookup(tenantID, nodeSessionID); ok && n.RootSessionID == rootSessionID {
		return n, nil
	}
	n, err := c.inner.Get(ctx, tenantID, rootSessionID, nodeSessionID)
	if err != nil {
		return ArchivedNode{}, err
	}
	c.put(n)
	return n, nil
}

// GetByNode serves a cache hit from memory and falls through to the
// inner store on a miss, caching the result.
func (c *Cached) GetByNode(ctx context.Context, tenantID, nodeSessionID string) (ArchivedNode, error) {
	if n, ok := c.lookup(tenantID, nodeSessionID); ok {
		return n, nil
	}
	n, err := c.inner.GetByNode(ctx, tenantID, nodeSessionID)
	if err != nil {
		return ArchivedNode{}, err
	}
	c.put(n)
	return n, nil
}

// DeleteBySession writes through to the inner store and evicts every
// cached node belonging to the erased tree so a post-erasure read cannot
// serve a stale settled-child result from memory. A write-through
// failure leaves the cache untouched.
func (c *Cached) DeleteBySession(ctx context.Context, tenantID, rootSessionID string) (int, error) {
	n, err := c.inner.DeleteBySession(ctx, tenantID, rootSessionID)
	if err != nil {
		return 0, err
	}
	c.evictRoot(tenantID, rootSessionID)
	return n, nil
}

// evictRoot drops every cached entry whose node belongs to the tree
// rooted at rootSessionID within tenantID. The cache is keyed by
// (tenant, node), so the eviction walks the LRU list and matches on the
// node's RootSessionID rather than the cache key.
func (c *Cached) evictRoot(tenantID, rootSessionID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for el := c.ll.Front(); el != nil; {
		next := el.Next()
		e := el.Value.(*entry)
		if e.node.TenantID == tenantID && e.node.RootSessionID == rootSessionID {
			c.ll.Remove(el)
			delete(c.items, e.key)
		}
		el = next
	}
}

// Len returns the number of entries currently cached. It exists for
// tests that assert the LRU eviction bound.
func (c *Cached) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ll.Len()
}
