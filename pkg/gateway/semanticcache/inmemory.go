// SPDX-License-Identifier: MIT

package semanticcache

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/memorystore"
)

// InMemory is the in-memory Store implementation — the test and
// minimal-gateway backend, mirroring the way memorystore ships both
// InMemory and a Postgres backend. Production swaps in the Redis-backed
// store behind the same interface. It enforces the same §12.4 key
// scheme so a test exercises the real key layout.
type InMemory struct {
	mu       sync.Mutex
	entries  map[string]storedEntry // keyed by the §12.4 entry key
	embedder memorystore.Embedder
	ttl      time.Duration
	thresh   float64
	clock    func() time.Time
}

// storedEntry is one cached pair plus the fields the semantic lookup
// and TTL eviction need.
type storedEntry struct {
	key       Key
	query     string
	response  string
	embedding []float32
	expiresAt time.Time
}

// NewInMemory returns an empty in-memory cache. A nil embedder selects
// memorystore.HashingEmbedder; a non-positive ttl selects
// DefaultTTLSeconds; a non-positive threshold selects
// DefaultSimilarityThreshold; a nil clock defaults to time.Now.
func NewInMemory(embedder memorystore.Embedder, ttl time.Duration, threshold float64, clock func() time.Time) *InMemory {
	if embedder == nil {
		embedder = memorystore.NewHashingEmbedder()
	}
	if ttl <= 0 {
		ttl = DefaultTTLSeconds * time.Second
	}
	if threshold <= 0 {
		threshold = DefaultSimilarityThreshold
	}
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &InMemory{
		entries:  map[string]storedEntry{},
		embedder: embedder,
		ttl:      ttl,
		thresh:   threshold,
		clock:    clock,
	}
}

var _ Store = (*InMemory)(nil)

// Get implements Store.
func (c *InMemory) Get(_ context.Context, key Key, query string) (Entry, bool, error) {
	if err := key.Validate(); err != nil {
		return Entry{}, false, err
	}
	qv, err := c.embedder.Embed(query)
	if err != nil {
		return Entry{}, false, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.evictExpiredLocked()

	// Scan only the entries that share the lookup's (tenant, scope,
	// model, provider) — the same candidate set the Redis index holds.
	idx, err := key.IndexKey()
	if err != nil {
		return Entry{}, false, err
	}
	bestSim := -1.0
	var best storedEntry
	found := false
	for _, e := range c.entries {
		ek, err := e.key.IndexKey()
		if err != nil || ek != idx {
			continue
		}
		sim := Similarity(qv, e.embedding, query, e.query)
		if sim > bestSim {
			bestSim, best, found = sim, e, true
		}
	}
	if !found || bestSim < c.thresh {
		return Entry{}, false, nil
	}
	return Entry{
		Query:      best.query,
		Response:   best.response,
		Model:      best.key.Model,
		Provider:   best.key.Provider,
		Similarity: bestSim,
	}, true, nil
}

// Put implements Store.
func (c *InMemory) Put(_ context.Context, key Key, query, response string) error {
	if err := key.Validate(); err != nil {
		return err
	}
	qv, err := c.embedder.Embed(query)
	if err != nil {
		return err
	}
	ek, err := key.EntryKey(key.QueryHash(query))
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.evictExpiredLocked()
	c.entries[ek] = storedEntry{
		key:       key,
		query:     query,
		response:  response,
		embedding: qv,
		expiresAt: c.clock().Add(c.ttl),
	}
	return nil
}

// DeleteByUser implements Store.
func (c *InMemory) DeleteByUser(_ context.Context, tenantID, userID string) error {
	if tenantID == "" {
		return ErrEmptyTenant
	}
	if userID == "" {
		return ErrEmptyUser
	}
	prefix := UserKeyPrefix(tenantID, userID)
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.entries {
		if strings.HasPrefix(k, prefix) {
			delete(c.entries, k)
		}
	}
	return nil
}

// DeleteByTenant implements Store.
func (c *InMemory) DeleteByTenant(_ context.Context, tenantID string) error {
	if tenantID == "" {
		return ErrEmptyTenant
	}
	prefix := TenantKeyPrefix(tenantID)
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.entries {
		if strings.HasPrefix(k, prefix) {
			delete(c.entries, k)
		}
	}
	return nil
}

// evictExpiredLocked drops entries past their TTL. The caller holds the
// lock. The Redis backend gets this for free from per-key EXPIRE; the
// in-memory store does it on each access.
func (c *InMemory) evictExpiredLocked() {
	now := c.clock()
	for k, e := range c.entries {
		if !e.expiresAt.After(now) {
			delete(c.entries, k)
		}
	}
}
