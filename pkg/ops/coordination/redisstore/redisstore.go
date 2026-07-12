// SPDX-License-Identifier: MIT

// Package redisstore is the Redis-backed §25.4 remediation-lock store
// (Tier 2). It holds each lock at the platform-scoped key ops:lock:{scope}
// as a hash {id, data} with a key-level PTTL, so exclusivity holds across
// every lenny-ops replica while Postgres is unreachable. The two
// CAS-critical paths — acquire and release — run as Lua scripts so the
// existence check and the write are atomic (no check-then-write window,
// §25.4 line 2166); acquiredAt and expiresAt are authored from the Redis
// TIME command so locks across replicas share one clock (§25.4 line 2202).
// A Redis outage surfaces as coordination.ErrStoreUnavailable so the
// tiered Service falls back to the in-memory (Tier 3) store.
//
// The keyspace is platform-scoped (remediation locks are platform
// operational records, not tenant data), so the keys carry no tenant
// prefix, matching the other ops: Redis namespaces.
//
// spec: §25.4 lines 2195-2202.
package redisstore

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/ops/coordination"
)

// Key namespaces. The scope key holds the lock; the id index maps a lock
// id back to its scope for Get/Release/Extend-by-id; the epoch key is the
// §25.4 line 2198 ops:lock-epoch:current counter.
const (
	scopePrefix = "ops:lock:"
	idxPrefix   = "ops:lockidx:"
	epochKey    = "ops:lock-epoch:current"
)

func scopeKey(scope string) string { return scopePrefix + scope }
func idxKey(id string) string      { return idxPrefix + id }

// Store is the Redis-backed §25.4 Tier 2 remediation-lock store.
type Store struct {
	client redis.UniversalClient
}

// New returns a Store backed by client.
func New(client redis.UniversalClient) *Store { return &Store{client: client} }

// Tier reports the redis lockStore label.
func (s *Store) Tier() string { return coordination.StoreRedis }

// unavailable maps a non-nil Redis error (other than the key-miss
// sentinel) to coordination.ErrStoreUnavailable so the Service treats a
// Redis outage as a tier fallback.
func unavailable(err error) error {
	if err == nil || errors.Is(err, redis.Nil) {
		return nil
	}
	return coordination.ErrStoreUnavailable
}

// acquireScript is the §25.4 line 2197 atomic acquire CAS: a held scope
// returns its current lock data (the conflict path); a free scope is
// written with the lock hash + id index under a shared PTTL.
//
//	KEYS[1] = scope key   KEYS[2] = id index key
//	ARGV[1] = lock id     ARGV[2] = lock json   ARGV[3] = pttl ms   ARGV[4] = scope
var acquireScript = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 1 then
  return redis.call('HGET', KEYS[1], 'data')
end
redis.call('HSET', KEYS[1], 'id', ARGV[1], 'data', ARGV[2])
redis.call('PEXPIRE', KEYS[1], ARGV[3])
redis.call('SET', KEYS[2], ARGV[4], 'PX', ARGV[3])
return false
`)

// releaseScript is the §25.4 line 2202 atomic release CAS: it deletes the
// scope and index keys only when the stored lock id matches the expected
// id, so a steal that re-keyed the scope is not clobbered by a stale
// release. Returns 1 on delete, 0 on id mismatch.
//
//	KEYS[1] = scope key   KEYS[2] = id index key   ARGV[1] = expected lock id
var releaseScript = redis.NewScript(`
if redis.call('HGET', KEYS[1], 'id') == ARGV[1] then
  redis.call('DEL', KEYS[1])
  redis.call('DEL', KEYS[2])
  return 1
end
return 0
`)

// setEpochScript writes ARGV[1] as the epoch only when it exceeds the
// stored value (the §25.4 MAX reconciliation never moves the epoch
// backward). Returns the resulting value.
//
//	KEYS[1] = epoch key   ARGV[1] = candidate epoch
var setEpochScript = redis.NewScript(`
local cur = tonumber(redis.call('GET', KEYS[1]) or '0')
local cand = tonumber(ARGV[1])
if cand > cur then redis.call('SET', KEYS[1], ARGV[1]) end
return redis.call('GET', KEYS[1])
`)

// Acquire creates a §25.4 lock on req.Scope, stamping the current
// ops:lock-epoch:current value (the §25.4 line 2198 read; the passed epoch
// is advisory and ignored so Redis remains the authoritative Tier 2 epoch
// source). acquiredAt and expiresAt come from the Redis TIME command.
func (s *Store) Acquire(ctx context.Context, req coordination.LockRequest, _ uint64) (*coordination.Lock, error) {
	now, err := s.serverTime(ctx)
	if err != nil {
		return nil, unavailable(err)
	}
	epoch, err := s.Epoch(ctx)
	if err != nil {
		return nil, err
	}
	ttl := coordination.NormalizeTTL(req.TTLSeconds)
	lock := coordination.Lock{
		ID:          coordination.NewLockID(),
		Scope:       req.Scope,
		Operation:   req.Operation,
		AcquiredBy:  req.AcquiredBy,
		OperationID: req.OperationID,
		AcquiredAt:  now,
		ExpiresAt:   now.Add(time.Duration(ttl) * time.Second),
		LockStore:   coordination.StoreRedis,
		Epoch:       epoch,
	}
	blob, err := json.Marshal(lock)
	if err != nil {
		return nil, err
	}
	pttl := strconv.FormatInt(int64(ttl)*1000, 10)
	res, err := acquireScript.Run(ctx, s.client,
		[]string{scopeKey(req.Scope), idxKey(lock.ID)}, lock.ID, blob, pttl, req.Scope).Result()
	// A free scope returns the Lua `false` (a nil reply, surfaced as
	// redis.Nil) — the acquired path. A held scope returns the existing
	// lock data string — the conflict path. Any other error is an outage.
	if errors.Is(err, redis.Nil) {
		return &lock, nil
	}
	if err != nil {
		return nil, unavailable(err)
	}
	if held, ok := res.(string); ok {
		return nil, conflictFromBlob(req.Scope, held)
	}
	return &lock, nil
}

// conflictFromBlob builds the §25.4 REMEDIATION_LOCK_CONFLICT error from
// the existing lock's data blob.
func conflictFromBlob(scope, blob string) error {
	var held coordination.Lock
	if err := json.Unmarshal([]byte(blob), &held); err == nil && held.AcquiredBy != "" {
		return &coordination.Error{
			Code: coordination.ErrCodeConflict,
			Message: "scope " + scope + " is held by " + held.AcquiredBy +
				" until " + held.ExpiresAt.Format(time.RFC3339),
		}
	}
	return &coordination.Error{Code: coordination.ErrCodeConflict, Message: "scope " + scope + " is held"}
}

// List returns every active §25.4 lock ordered by scope. Keys that expire
// between the scan and the read are skipped.
func (s *Store) List(ctx context.Context) ([]coordination.Lock, error) {
	locks, err := s.scanAll(ctx)
	if err != nil {
		return nil, unavailable(err)
	}
	sort.Slice(locks, func(i, j int) bool { return locks[i].Scope < locks[j].Scope })
	return locks, nil
}

// ActiveLocks returns the same set as List; it is the reconciliation
// snapshot source.
func (s *Store) ActiveLocks(ctx context.Context) ([]coordination.Lock, error) {
	return s.List(ctx)
}

// Get returns a single §25.4 lock by id, or REMEDIATION_LOCK_NOT_FOUND.
func (s *Store) Get(ctx context.Context, lockID string) (*coordination.Lock, error) {
	lock, err := s.readByID(ctx, lockID)
	if err != nil {
		return nil, unavailable(err)
	}
	if lock == nil {
		return nil, &coordination.Error{Code: coordination.ErrCodeNotFound, Message: "no lock " + lockID}
	}
	return lock, nil
}

// Extend renews a §25.4 lock's TTL. caller MUST be the current acquiredBy;
// extension increments revision and re-stamps the key PTTL from Redis TIME.
func (s *Store) Extend(ctx context.Context, lockID string, ttlSeconds int, caller string) (*coordination.Lock, error) {
	lock, err := s.readByID(ctx, lockID)
	if err != nil {
		return nil, unavailable(err)
	}
	if lock == nil {
		return nil, &coordination.Error{Code: coordination.ErrCodeNotFound, Message: "no lock " + lockID}
	}
	if lock.AcquiredBy != caller {
		return nil, &coordination.Error{Code: coordination.ErrCodeNotOwned, Message: "lock " + lockID + " is not owned by the caller"}
	}
	now, err := s.serverTime(ctx)
	if err != nil {
		return nil, unavailable(err)
	}
	ttl := coordination.NormalizeTTL(ttlSeconds)
	lock.ExpiresAt = now.Add(time.Duration(ttl) * time.Second)
	lock.Revision++
	if err := s.persist(ctx, *lock, ttl); err != nil {
		return nil, unavailable(err)
	}
	return lock, nil
}

// Release frees a §25.4 lock. caller MUST be the current acquiredBy. The
// delete is an atomic CAS on the lock id so a steal is not clobbered.
func (s *Store) Release(ctx context.Context, lockID, caller string) error {
	lock, err := s.readByID(ctx, lockID)
	if err != nil {
		return unavailable(err)
	}
	if lock == nil {
		return &coordination.Error{Code: coordination.ErrCodeNotFound, Message: "no lock " + lockID}
	}
	if lock.AcquiredBy != caller {
		return &coordination.Error{Code: coordination.ErrCodeNotOwned, Message: "lock " + lockID + " is not owned by the caller"}
	}
	if _, err := releaseScript.Run(ctx, s.client,
		[]string{scopeKey(lock.Scope), idxKey(lockID)}, lockID).Result(); err != nil {
		return unavailable(err)
	}
	return nil
}

// Steal takes over a §25.4 lock: caller becomes acquiredBy, the prior
// holder is recorded in stolenFrom, revision increments, and the TTL is
// reset from Redis TIME.
func (s *Store) Steal(ctx context.Context, lockID string, req coordination.StealRequest) (*coordination.Lock, error) {
	lock, err := s.readByID(ctx, lockID)
	if err != nil {
		return nil, unavailable(err)
	}
	if lock == nil {
		return nil, &coordination.Error{Code: coordination.ErrCodeNotFound, Message: "no lock " + lockID}
	}
	now, err := s.serverTime(ctx)
	if err != nil {
		return nil, unavailable(err)
	}
	ttl := coordination.NormalizeTTL(req.TTLSeconds)
	lock.StolenFrom = lock.AcquiredBy
	lock.AcquiredBy = req.AcquiredBy
	lock.OperationID = ""
	lock.AcquiredAt = now
	lock.ExpiresAt = now.Add(time.Duration(ttl) * time.Second)
	lock.Revision++
	if err := s.persist(ctx, *lock, ttl); err != nil {
		return nil, unavailable(err)
	}
	return lock, nil
}

// Reap is a no-op for the Redis tier: key-level PTTL expires locks
// natively, so there is no orphaned-row sweep. It satisfies the Store
// contract and always reports zero reaped.
func (s *Store) Reap(_ context.Context) (int, error) { return 0, nil }

// RemoveByID drops the lock with lockID from Redis regardless of holder.
// The §25.4 reconciliation calls it to remove a losing split-brain Redis
// lock once the pre-outage Postgres holder has won ("The Redis lock is
// removed", line 2267), so a later Postgres outage cannot resurface the
// stale lock. The delete is the same atomic id-matched CAS as Release, so a
// steal that re-keyed the scope is not clobbered; a lock that has already
// expired or been re-keyed is a no-op.
//
// spec: §25.4 line 2267.
func (s *Store) RemoveByID(ctx context.Context, lockID string) error {
	lock, err := s.readByID(ctx, lockID)
	if err != nil {
		return unavailable(err)
	}
	if lock == nil {
		return nil
	}
	if _, err := releaseScript.Run(ctx, s.client,
		[]string{scopeKey(lock.Scope), idxKey(lockID)}, lockID).Result(); err != nil {
		return unavailable(err)
	}
	return nil
}

// Epoch returns the §25.4 ops:lock-epoch:current value, defaulting to 0.
func (s *Store) Epoch(ctx context.Context) (uint64, error) {
	v, err := s.client.Get(ctx, epochKey).Uint64()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	if err != nil {
		return 0, coordination.ErrStoreUnavailable
	}
	return v, nil
}

// IncrementEpoch advances ops:lock-epoch:current by one (the §25.4 line
// 2220 Postgres→Tier-2 transition) and returns the new value.
func (s *Store) IncrementEpoch(ctx context.Context) (uint64, error) {
	v, err := s.client.Incr(ctx, epochKey).Result()
	if err != nil {
		return 0, coordination.ErrStoreUnavailable
	}
	return uint64(v), nil
}

// SetEpoch writes v as ops:lock-epoch:current when it exceeds the stored
// value (the §25.4 MAX reconciliation, applied to the Redis side after a
// Postgres recovery).
func (s *Store) SetEpoch(ctx context.Context, v uint64) error {
	if _, err := setEpochScript.Run(ctx, s.client, []string{epochKey}, strconv.FormatUint(v, 10)).Result(); err != nil {
		return coordination.ErrStoreUnavailable
	}
	return nil
}

// serverTime reads the Redis TIME command so acquiredAt/expiresAt use the
// §25.4 line 2202 consistent clock source across replicas.
func (s *Store) serverTime(ctx context.Context) (time.Time, error) {
	t, err := s.client.Time(ctx).Result()
	if err != nil {
		return time.Time{}, err
	}
	return t.UTC(), nil
}

// ServerTime exposes the §25.4 line 2202 Redis TIME read so the
// Postgres-Redis clock-skew sampler can compare the Redis dependency
// clock against Postgres now() without duplicating the TIME read. It
// reuses the same source acquiredAt/expiresAt are authored from, so the
// monitored skew is the skew that affects lease expiry computation.
//
// spec: §25.4 line 2280 (Postgres-Redis skew monitoring).
func (s *Store) ServerTime(ctx context.Context) (time.Time, error) {
	return s.serverTime(ctx)
}

// persist re-writes lock's hash + index with a fresh PTTL. Used by Extend
// and Steal, which operate on an already-identified lock.
func (s *Store) persist(ctx context.Context, lock coordination.Lock, ttl int) error {
	blob, err := json.Marshal(lock)
	if err != nil {
		return err
	}
	pttl := time.Duration(ttl) * time.Second
	pipe := s.client.TxPipeline()
	pipe.HSet(ctx, scopeKey(lock.Scope), "id", lock.ID, "data", blob)
	pipe.PExpire(ctx, scopeKey(lock.Scope), pttl)
	pipe.Set(ctx, idxKey(lock.ID), lock.Scope, pttl)
	_, err = pipe.Exec(ctx)
	return err
}

// readByID resolves a lock id to its scope via the index, then reads the
// scope hash. It returns (nil, nil) when either key is absent or expired.
func (s *Store) readByID(ctx context.Context, id string) (*coordination.Lock, error) {
	scope, err := s.client.Get(ctx, idxKey(id)).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s.readByScope(ctx, scope)
}

// readByScope reads and decodes the lock at the scope hash, or (nil, nil)
// on a miss.
func (s *Store) readByScope(ctx context.Context, scope string) (*coordination.Lock, error) {
	blob, err := s.client.HGet(ctx, scopeKey(scope), "data").Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var lock coordination.Lock
	if err := json.Unmarshal(blob, &lock); err != nil {
		return nil, nil
	}
	return &lock, nil
}

// scanAll iterates the scope keyspace and decodes every live lock.
func (s *Store) scanAll(ctx context.Context) ([]coordination.Lock, error) {
	iter := s.client.Scan(ctx, 0, scopePrefix+"*", 256).Iterator()
	var out []coordination.Lock
	for iter.Next(ctx) {
		blob, err := s.client.HGet(ctx, iter.Val(), "data").Bytes()
		if errors.Is(err, redis.Nil) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var lock coordination.Lock
		if err := json.Unmarshal(blob, &lock); err != nil {
			continue
		}
		out = append(out, lock)
	}
	if err := iter.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// Compile-time guard that *Store satisfies the coordination tier and
// epoch contracts.
var (
	_ coordination.Store        = (*Store)(nil)
	_ coordination.EpochManager = (*Store)(nil)
)
