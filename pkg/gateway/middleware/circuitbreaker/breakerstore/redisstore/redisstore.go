// SPDX-License-Identifier: MIT

// Package redisstore is the Redis-backed §11.6 circuit-breaker
// registry. It is a drop-in alternative to breakerstore.Memory and,
// like it, satisfies cbmw.Registry so the gateway middleware reads
// breaker state from the same store.
//
// Breaker state is stored at the §12.4 platform-scoped key cb:{name}
// (circuit breakers are platform-wide operational controls, not
// tenant data, so the key carries no tenant prefix). Redis backing
// matters for correctness: an operator-opened breaker must survive a
// gateway restart and stay consistent across replicas, which the
// in-memory store cannot guarantee (§12.4 "operator-initiated safety
// blocks are never silently lifted").
package redisstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/circuitbreaker"
	cbmw "github.com/lennylabs/lenny/pkg/gateway/middleware/circuitbreaker"
	"github.com/lennylabs/lenny/pkg/gateway/middleware/circuitbreaker/breakerstore"
)

// keyPrefix is the §12.4 circuit-breaker key namespace.
const keyPrefix = "cb:"

func key(name string) string { return keyPrefix + name }

// Store is the Redis-backed breaker registry. Construct with New.
type Store struct {
	client redis.UniversalClient
}

// New returns a Store backed by client.
func New(client redis.UniversalClient) *Store { return &Store{client: client} }

var (
	_ breakerstore.Store = (*Store)(nil)
	_ cbmw.Registry      = (*Store)(nil)
)

// Open creates or re-opens a breaker. The (LimitTier, Scope) pair is
// pinned on first open; a later open against the same name with a
// different scope returns ErrScopeImmutable (§11.6). The read of the
// existing record, the scope check, and the write run inside a
// WATCH-guarded transaction so a concurrent open cannot slip a
// scope change past the immutability check.
func (s *Store) Open(ctx context.Context, b circuitbreaker.Breaker) (circuitbreaker.Breaker, error) {
	if err := b.Validate(); err != nil {
		return circuitbreaker.Breaker{}, err
	}
	b.State = circuitbreaker.StateOpen
	k := key(b.Name)

	txf := func(tx *redis.Tx) error {
		raw, err := tx.Get(ctx, k).Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return err
		}
		if err == nil {
			existing, derr := decode(raw)
			if derr != nil {
				return derr
			}
			if !circuitbreaker.ScopeMatches(existing, b) {
				return breakerstore.ErrScopeImmutable
			}
		}
		payload, merr := json.Marshal(b)
		if merr != nil {
			return fmt.Errorf("redisstore: encode breaker: %w", merr)
		}
		_, perr := tx.TxPipelined(ctx, func(p redis.Pipeliner) error {
			p.Set(ctx, k, payload, 0)
			return nil
		})
		return perr
	}
	if err := s.client.Watch(ctx, txf, k); err != nil {
		return circuitbreaker.Breaker{}, err
	}
	return b, nil
}

// Close transitions a named breaker to closed, preserving its scope
// and open-time metadata. Returns ErrNotFound when the breaker is
// absent. The transition is WATCH-guarded so it always closes the
// breaker's current record rather than a stale read.
func (s *Store) Close(ctx context.Context, name string) (circuitbreaker.Breaker, error) {
	k := key(name)
	var closed circuitbreaker.Breaker
	txf := func(tx *redis.Tx) error {
		raw, err := tx.Get(ctx, k).Result()
		if errors.Is(err, redis.Nil) {
			return breakerstore.ErrNotFound
		}
		if err != nil {
			return err
		}
		b, derr := decode(raw)
		if derr != nil {
			return derr
		}
		b.State = circuitbreaker.StateClosed
		payload, merr := json.Marshal(b)
		if merr != nil {
			return fmt.Errorf("redisstore: encode breaker: %w", merr)
		}
		_, perr := tx.TxPipelined(ctx, func(p redis.Pipeliner) error {
			p.Set(ctx, k, payload, 0)
			return nil
		})
		if perr != nil {
			return perr
		}
		closed = b
		return nil
	}
	if err := s.client.Watch(ctx, txf, k); err != nil {
		return circuitbreaker.Breaker{}, err
	}
	return closed, nil
}

// Get returns the named breaker, or ErrNotFound when it is absent.
func (s *Store) Get(ctx context.Context, name string) (circuitbreaker.Breaker, error) {
	raw, err := s.client.Get(ctx, key(name)).Result()
	if errors.Is(err, redis.Nil) {
		return circuitbreaker.Breaker{}, breakerstore.ErrNotFound
	}
	if err != nil {
		return circuitbreaker.Breaker{}, err
	}
	return decode(raw)
}

// List returns every breaker in name-ascending order.
func (s *Store) List(ctx context.Context) ([]circuitbreaker.Breaker, error) {
	return s.collect(ctx, false)
}

// Snapshot returns only the open breakers, satisfying cbmw.Registry.
func (s *Store) Snapshot(ctx context.Context) ([]circuitbreaker.Breaker, error) {
	return s.collect(ctx, true)
}

// collect SCANs the cb:* keyspace and decodes every breaker, optionally
// keeping only the open ones.
func (s *Store) collect(ctx context.Context, openOnly bool) ([]circuitbreaker.Breaker, error) {
	var keys []string
	iter := s.client.Scan(ctx, 0, keyPrefix+"*", 0).Iterator()
	for iter.Next(ctx) {
		keys = append(keys, iter.Val())
	}
	if err := iter.Err(); err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return nil, nil
	}
	vals, err := s.client.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}
	out := make([]circuitbreaker.Breaker, 0, len(vals))
	for _, v := range vals {
		raw, ok := v.(string)
		if !ok {
			// Key vanished between SCAN and MGET; skip it.
			continue
		}
		b, derr := decode(raw)
		if derr != nil {
			return nil, derr
		}
		if openOnly && b.State != circuitbreaker.StateOpen {
			continue
		}
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// decode unmarshals a stored breaker record.
func decode(raw string) (circuitbreaker.Breaker, error) {
	var b circuitbreaker.Breaker
	if err := json.Unmarshal([]byte(raw), &b); err != nil {
		return circuitbreaker.Breaker{}, fmt.Errorf("redisstore: decode breaker: %w", err)
	}
	return b, nil
}
