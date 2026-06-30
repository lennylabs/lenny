// SPDX-License-Identifier: MIT

// Package redisstore is the Redis-backed storagequota.Counter. The
// per-tenant storage byte total lives in a Redis key so the §11.2
// quota holds across gateway replicas. The reservation is atomic via
// a Lua script that read-checks-reserves in a single EVAL, closing the
// race where two concurrent uploads both pass a non-atomic pre-check
// and collectively exceed the quota.
package redisstore

import (
	"context"
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/quota/storagequota"
)

// keyPrefix is the §11.2 storage-quota key namespace.
const keyPrefix = "sq:"

func key(tenantID string) string { return keyPrefix + tenantID }

// reserveScript performs the §11.2 atomic read-check-reserve. KEYS[1]
// is the tenant's byte key; ARGV[1] is the incoming byte count and
// ARGV[2] the quota limit. It returns {1, priorUsed} when the
// reservation is admitted and {0, priorUsed} when it would exceed the
// limit, leaving the counter untouched.
var reserveScript = redis.NewScript(`
local used = tonumber(redis.call('GET', KEYS[1]) or '0')
local incoming = tonumber(ARGV[1])
local limit = tonumber(ARGV[2])
if used + incoming > limit then
  return {0, used}
end
redis.call('INCRBY', KEYS[1], incoming)
return {1, used}
`)

// adjustScript shifts the counter by ARGV[1], clamping at zero so a
// release can never drive the byte total negative.
var adjustScript = redis.NewScript(`
local v = tonumber(redis.call('GET', KEYS[1]) or '0') + tonumber(ARGV[1])
if v < 0 then v = 0 end
redis.call('SET', KEYS[1], v)
return v
`)

// Store is the Redis-backed storage counter. Construct with New.
type Store struct {
	client redis.UniversalClient
}

// New returns a Store backed by client.
func New(client redis.UniversalClient) *Store { return &Store{client: client} }

var _ storagequota.Counter = (*Store)(nil)

// Reserve implements storagequota.Counter.
func (s *Store) Reserve(ctx context.Context, tenantID string, incoming, limit int64) (int64, error) {
	res, err := reserveScript.Run(ctx, s.client, []string{key(tenantID)}, incoming, limit).Int64Slice()
	if err != nil {
		return 0, fmt.Errorf("storagequota: reserve: %w", err)
	}
	if len(res) != 2 {
		return 0, fmt.Errorf("storagequota: reserve: unexpected script result %v", res)
	}
	priorUsed := res[1]
	if res[0] == 0 {
		return priorUsed, storagequota.ErrQuotaExceeded
	}
	return priorUsed, nil
}

// Adjust implements storagequota.Counter.
func (s *Store) Adjust(ctx context.Context, tenantID string, delta int64) error {
	if err := adjustScript.Run(ctx, s.client, []string{key(tenantID)}, delta).Err(); err != nil {
		return fmt.Errorf("storagequota: adjust: %w", err)
	}
	return nil
}

// Used implements storagequota.Counter.
func (s *Store) Used(ctx context.Context, tenantID string) (int64, error) {
	v, err := s.client.Get(ctx, key(tenantID)).Int64()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("storagequota: used: %w", err)
	}
	return v, nil
}

// Set implements storagequota.Counter. It overwrites the tenant's
// byte key with value (clamped at zero) — the §11 line 37 rehydration
// write that reconstructs the counter from the durable artifact_store
// sum after a Redis restart.
//
// spec: §11 line 37.
func (s *Store) Set(ctx context.Context, tenantID string, value int64) error {
	if value < 0 {
		value = 0
	}
	if err := s.client.Set(ctx, key(tenantID), value, 0).Err(); err != nil {
		return fmt.Errorf("storagequota: set: %w", err)
	}
	return nil
}
