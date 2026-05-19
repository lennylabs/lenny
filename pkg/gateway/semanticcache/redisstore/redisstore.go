// SPDX-License-Identifier: MIT

// Package redisstore is the Redis-backed §12.2.1 SemanticCache. It is
// the default backend for the §4.9 CachePolicy on a CredentialPool and
// a drop-in alternative to semanticcache.InMemory.
//
// Every key follows the §12.4 convention
// t:{tenant_id}:scache:{scope}:{hash}. A cache entry is a JSON value
// at that key carrying the query text, the query embedding, and the
// cached response, with the §4.9 ttl as the key expiry. Alongside the
// entries, a per-(tenant, scope, model, provider) Redis set indexes the
// hashes of the entries that share those fields, so a Get scans only
// the relevant candidate set rather than the whole keyspace. The index
// set lives under the same scope segment as its entries, so the §12.2
// erasure prefix scans cover it.
package redisstore

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/memorystore"
	"github.com/lennylabs/lenny/pkg/gateway/semanticcache"
)

// Store is the Redis-backed §12.2.1 SemanticCache. Construct with New.
type Store struct {
	client   redis.UniversalClient
	embedder memorystore.Embedder
	ttl      time.Duration
	thresh   float64
}

// New returns a Store backed by client. A nil embedder selects
// memorystore.HashingEmbedder; a non-positive ttl selects
// semanticcache.DefaultTTLSeconds; a non-positive threshold selects
// semanticcache.DefaultSimilarityThreshold.
func New(client redis.UniversalClient, embedder memorystore.Embedder, ttl time.Duration, threshold float64) *Store {
	if embedder == nil {
		embedder = memorystore.NewHashingEmbedder()
	}
	if ttl <= 0 {
		ttl = semanticcache.DefaultTTLSeconds * time.Second
	}
	if threshold <= 0 {
		threshold = semanticcache.DefaultSimilarityThreshold
	}
	return &Store{client: client, embedder: embedder, ttl: ttl, thresh: threshold}
}

var _ semanticcache.Store = (*Store)(nil)

// wireEntry is the JSON value stored at a §12.4 entry key.
type wireEntry struct {
	Query     string    `json:"query"`
	Response  string    `json:"response"`
	Model     string    `json:"model"`
	Provider  string    `json:"provider"`
	Embedding []float32 `json:"embedding,omitempty"`
}

// Get implements semanticcache.Store. It embeds query, reads the
// per-(tenant, scope, model, provider) index set, fetches the indexed
// entries, and returns the response of the nearest entry whose cosine
// similarity meets the threshold.
func (s *Store) Get(ctx context.Context, key semanticcache.Key, query string) (semanticcache.Entry, bool, error) {
	if err := key.Validate(); err != nil {
		return semanticcache.Entry{}, false, err
	}
	qv, err := s.embedder.Embed(query)
	if err != nil {
		return semanticcache.Entry{}, false, fmt.Errorf("semanticcache: embed query: %w", err)
	}
	idxKey, err := key.IndexKey()
	if err != nil {
		return semanticcache.Entry{}, false, err
	}
	members, err := s.client.SMembers(ctx, idxKey).Result()
	if err != nil {
		return semanticcache.Entry{}, false, fmt.Errorf("semanticcache: read index: %w", err)
	}
	if len(members) == 0 {
		return semanticcache.Entry{}, false, nil
	}

	entryKeys := make([]string, 0, len(members))
	for _, hash := range members {
		ek, err := key.EntryKey(hash)
		if err != nil {
			return semanticcache.Entry{}, false, err
		}
		entryKeys = append(entryKeys, ek)
	}
	raws, err := s.client.MGet(ctx, entryKeys...).Result()
	if err != nil {
		return semanticcache.Entry{}, false, fmt.Errorf("semanticcache: read entries: %w", err)
	}

	bestSim := -1.0
	var best wireEntry
	var staleHashes []string
	found := false
	for i, raw := range raws {
		if raw == nil {
			// The entry TTL-expired but the index set still lists it.
			// Record the stale hash so it is pruned after the scan.
			staleHashes = append(staleHashes, members[i])
			continue
		}
		str, ok := raw.(string)
		if !ok {
			continue
		}
		var e wireEntry
		if err := json.Unmarshal([]byte(str), &e); err != nil {
			return semanticcache.Entry{}, false, fmt.Errorf("semanticcache: decode entry: %w", err)
		}
		sim := semanticcache.Similarity(qv, e.Embedding, query, e.Query)
		if sim > bestSim {
			bestSim, best, found = sim, e, true
		}
	}
	if len(staleHashes) > 0 {
		// Best-effort prune; a failure here does not fail the lookup.
		_ = s.client.SRem(ctx, idxKey, staleSlice(staleHashes)...).Err()
	}
	if !found || bestSim < s.thresh {
		return semanticcache.Entry{}, false, nil
	}
	return semanticcache.Entry{
		Query:      best.Query,
		Response:   best.Response,
		Model:      best.Model,
		Provider:   best.Provider,
		Similarity: bestSim,
	}, true, nil
}

// Put implements semanticcache.Store. It writes the entry at its
// §12.4 key with the configured TTL and records its hash in the
// per-(tenant, scope, model, provider) index set, refreshing the index
// set's TTL so a fully expired cache leaves no orphan index.
func (s *Store) Put(ctx context.Context, key semanticcache.Key, query, response string) error {
	if err := key.Validate(); err != nil {
		return err
	}
	qv, err := s.embedder.Embed(query)
	if err != nil {
		return fmt.Errorf("semanticcache: embed query: %w", err)
	}
	hash := key.QueryHash(query)
	entryKey, err := key.EntryKey(hash)
	if err != nil {
		return err
	}
	idxKey, err := key.IndexKey()
	if err != nil {
		return err
	}
	payload, err := json.Marshal(wireEntry{
		Query:     query,
		Response:  response,
		Model:     key.Model,
		Provider:  key.Provider,
		Embedding: qv,
	})
	if err != nil {
		return fmt.Errorf("semanticcache: encode entry: %w", err)
	}
	// The entry and its index membership are written in one pipeline.
	// The index set's TTL is set a generation longer than the entry so
	// a still-live entry is never dropped from a prematurely expired
	// index; once every entry expires the index follows.
	pipe := s.client.TxPipeline()
	pipe.Set(ctx, entryKey, payload, s.ttl)
	pipe.SAdd(ctx, idxKey, hash)
	pipe.Expire(ctx, idxKey, 2*s.ttl)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("semanticcache: write entry: %w", err)
	}
	return nil
}

// DeleteByUser implements semanticcache.Store. It deletes every key
// under the §12.4 t:{tenant_id}:scache:u:{user_id}: prefix, which
// covers both the user's per-user entries and their index sets.
func (s *Store) DeleteByUser(ctx context.Context, tenantID, userID string) error {
	if tenantID == "" {
		return semanticcache.ErrEmptyTenant
	}
	if userID == "" {
		return semanticcache.ErrEmptyUser
	}
	return s.deletePrefix(ctx, semanticcache.UserKeyPrefix(tenantID, userID))
}

// DeleteByTenant implements semanticcache.Store. It deletes every key
// under the §12.4 t:{tenant_id}:scache: prefix.
func (s *Store) DeleteByTenant(ctx context.Context, tenantID string) error {
	if tenantID == "" {
		return semanticcache.ErrEmptyTenant
	}
	return s.deletePrefix(ctx, semanticcache.TenantKeyPrefix(tenantID))
}

// deletePrefix SCANs prefix* and deletes every matching key in
// batches. SCAN is non-blocking, so a large cache does not stall
// Redis; DEL of each batch is bounded.
func (s *Store) deletePrefix(ctx context.Context, prefix string) error {
	iter := s.client.Scan(ctx, 0, prefix+"*", 256).Iterator()
	var batch []string
	for iter.Next(ctx) {
		batch = append(batch, iter.Val())
		if len(batch) >= 256 {
			if err := s.client.Del(ctx, batch...).Err(); err != nil {
				return fmt.Errorf("semanticcache: erase batch: %w", err)
			}
			batch = batch[:0]
		}
	}
	if err := iter.Err(); err != nil {
		return fmt.Errorf("semanticcache: scan %q: %w", prefix, err)
	}
	if len(batch) > 0 {
		if err := s.client.Del(ctx, batch...).Err(); err != nil {
			return fmt.Errorf("semanticcache: erase final batch: %w", err)
		}
	}
	return nil
}

// staleSlice converts a hash slice to the []any SRem expects.
func staleSlice(hashes []string) []any {
	out := make([]any, len(hashes))
	for i, h := range hashes {
		out[i] = h
	}
	return out
}
