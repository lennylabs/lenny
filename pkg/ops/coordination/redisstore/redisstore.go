// SPDX-License-Identifier: MIT

// Package redisstore is the Redis-backed §25.4 remediation-lock store
// (Tier 2). Every key sits under the ops:{lock}: prefix, whose {lock}
// segment is a Redis Cluster hash tag, so every key the store writes hashes
// to one slot and each script runs on one Redis Cluster primary:
//
//   - ops:{lock}:scope:<scope> is a hash {id, data} holding the lock ID and
//     the serialized lock record, with a key-level PTTL;
//   - ops:{lock}:id:<lockID> holds the scope, so a lock ID resolves to its
//     scope key;
//   - ops:{lock}:epoch holds the Redis-side outage epoch.
//
// Acquire is one Lua script that conflicts on an existing scope key, reads
// the epoch key, and stamps that epoch into the record it writes. Extend,
// Steal, and Release resolve the lock ID through the ID key, decide the
// change in Go, and apply it with one compare-and-set script that writes
// only when the stored record still equals the record that was read; a
// failed comparison re-reads and decides again, at most three times, and
// then reports REMEDIATION_LOCK_CONFLICT. acquiredAt and expiresAt come from
// a Redis TIME reading taken before the script runs, and the script sets a
// relative PEXPIRE, so a key lives at least the requested TTL after that
// reading whichever primary answered TIME. Every write tolerates a client
// re-send. A Redis transport error surfaces as
// coordination.ErrStoreUnavailable so the tiered Service falls back to the
// in-memory (Tier 3) store; an ownership, absence, or retry-exhaustion
// outcome is a coordination.Error and never ErrStoreUnavailable.
//
// The keyspace is platform-scoped (remediation locks are platform
// operational records, not tenant data), so the keys carry no tenant
// prefix, matching the other ops: Redis namespaces.
//
// spec: §25.4 (Storage Tiers); §12.4 (Redis HA and Failure Modes).
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
	"github.com/lennylabs/lenny/pkg/redisconn"
)

// Key namespaces, all under the ops:{lock}: hash tag. The scope key holds
// the lock; the ID key maps a lock ID back to its scope for the by-ID
// operations; the epoch key is the §25.4 ops:{lock}:epoch outage-epoch
// counter. The scope: and id: segments keep a free-form scope from
// colliding with the epoch key or an ID key.
//
// spec: §25.4 (Storage Tiers, Tier Transitions).
const (
	scopePrefix = "ops:{lock}:scope:"
	idxPrefix   = "ops:{lock}:id:"
	epochKey    = "ops:{lock}:epoch"
)

// maxCASAttempts bounds the compare-and-set retries of Extend, Steal, and
// Release (§25.4 Storage Tiers: three failed comparisons end in
// REMEDIATION_LOCK_CONFLICT).
const maxCASAttempts = 3

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

// acquireScript is the §25.4 atomic acquire. A held scope replies {1, data}
// with the stored record (the conflict path), unless the scope already holds
// this lock ID at revision 0, which is a re-sent acquire and replies
// {0, epoch} with the epoch stamped on the first send. A free scope reads the
// epoch key (absent is 0), stamps it into the record built in Go, writes the
// scope hash and the ID key under one relative PTTL, and replies {0, epoch}.
//
//	KEYS[1] = scope key   KEYS[2] = ID key   KEYS[3] = epoch key
//	ARGV[1] = lock ID     ARGV[2] = lock JSON (epoch 0)
//	ARGV[3] = PTTL ms     ARGV[4] = scope
var acquireScript = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 1 then
  local d = redis.call('HGET', KEYS[1], 'data')
  if redis.call('HGET', KEYS[1], 'id') == ARGV[1] then
    local s = cjson.decode(d)
    if s.revision == 0 then return {0, tostring(s.epoch)} end
  end
  return {1, d}
end
local e = tonumber(redis.call('GET', KEYS[3]) or '0')
local l = cjson.decode(ARGV[2])
l.epoch = e
local d = cjson.encode(l)
redis.call('HSET', KEYS[1], 'id', ARGV[1], 'data', d)
redis.call('PEXPIRE', KEYS[1], ARGV[3])
redis.call('SET', KEYS[2], ARGV[4], 'PX', ARGV[3])
return {0, tostring(e)}
`)

// casScript is the §25.4 compare-and-set behind Extend, Steal, and Release.
// It writes the new record, or deletes both keys when the new record is
// empty, only when the stored data string equals the expected one. A stored
// record already equal to the new record is a re-sent or identical
// concurrent write and counts as success. Returns 1 on success, 0 when the
// comparison fails.
//
//	KEYS[1] = scope key   KEYS[2] = ID key
//	ARGV[1] = expected data   ARGV[2] = new data, or '' for delete
//	ARGV[3] = PTTL ms         ARGV[4] = scope   ARGV[5] = lock ID
var casScript = redis.NewScript(`
local cur = redis.call('HGET', KEYS[1], 'data')
if ARGV[2] ~= '' and cur == ARGV[2] then return 1 end
if cur ~= ARGV[1] then return 0 end
if ARGV[2] == '' then
  redis.call('DEL', KEYS[1], KEYS[2])
  return 1
end
redis.call('HSET', KEYS[1], 'id', ARGV[5], 'data', ARGV[2])
redis.call('PEXPIRE', KEYS[1], ARGV[3])
redis.call('SET', KEYS[2], ARGV[4], 'PX', ARGV[3])
return 1
`)

// releaseScript is the §25.4 reconciliation removal: it deletes the scope
// key and the ID key only when the stored lock ID matches the expected ID,
// regardless of acquiredBy. Returns 1 on delete, 0 on ID mismatch, so a
// re-sent removal deletes nothing.
//
//	KEYS[1] = scope key   KEYS[2] = ID key   ARGV[1] = expected lock ID
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

// Acquire creates a §25.4 lock on req.Scope. acquiredAt and expiresAt come
// from a Redis TIME reading taken before the script; acquireScript then
// reads ops:{lock}:epoch and stamps it into the lock inside the same script,
// so the stamped epoch is the value current when the lock was written. The
// epoch argument is unused because Redis holds the Tier 2 epoch.
//
// spec: §25.4 (Storage Tiers, Clock Source).
func (s *Store) Acquire(ctx context.Context, req coordination.LockRequest, _ uint64) (*coordination.Lock, error) {
	now, err := s.serverTime(ctx)
	if err != nil {
		return nil, unavailable(err)
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
	}
	blob, err := json.Marshal(lock)
	if err != nil {
		return nil, err
	}
	res, err := acquireScript.Run(ctx, s.client,
		[]string{scopeKey(req.Scope), idxKey(lock.ID), epochKey},
		lock.ID, blob, pttlMillis(ttl), req.Scope).Result()
	if err != nil {
		return nil, coordination.ErrStoreUnavailable
	}
	conflict, payload, ok := parseAcquireReply(res)
	if !ok {
		return nil, coordination.ErrStoreUnavailable
	}
	if conflict {
		return nil, conflictFromBlob(req.Scope, payload)
	}
	epoch, err := strconv.ParseUint(payload, 10, 64)
	if err != nil {
		return nil, coordination.ErrStoreUnavailable
	}
	lock.Epoch = epoch
	return &lock, nil
}

// parseAcquireReply decodes the acquireScript reply: {0, epoch} is an
// acquired lock and {1, data} a conflict carrying the stored record. ok is
// false for any other reply.
func parseAcquireReply(res interface{}) (conflict bool, payload string, ok bool) {
	parts, isSlice := res.([]interface{})
	if !isSlice || len(parts) != 2 {
		return false, "", false
	}
	code, isInt := parts[0].(int64)
	payload, isStr := parts[1].(string)
	if !isInt || !isStr || (code != 0 && code != 1) {
		return false, "", false
	}
	return code == 1, payload, true
}

// pttlMillis renders a TTL in seconds as the PTTL argument of a script.
func pttlMillis(ttlSeconds int) string {
	return strconv.FormatInt(int64(ttlSeconds)*1000, 10)
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
	lock, _, err := s.readByID(ctx, lockID)
	if err != nil {
		return nil, unavailable(err)
	}
	if lock == nil {
		return nil, notFound(lockID)
	}
	return lock, nil
}

// Extend renews a §25.4 lock's TTL. caller MUST be the current acquiredBy;
// extension increments revision and recomputes expiresAt from Redis TIME.
//
// spec: §25.4 (Storage Tiers).
func (s *Store) Extend(ctx context.Context, lockID string, ttlSeconds int, caller string) (*coordination.Lock, error) {
	ttl := coordination.NormalizeTTL(ttlSeconds)
	return s.mutate(ctx, lockID, ttl, func(cur coordination.Lock, now time.Time) (*coordination.Lock, error) {
		if cur.AcquiredBy != caller {
			return nil, notOwned(lockID)
		}
		cur.ExpiresAt = now.Add(time.Duration(ttl) * time.Second)
		cur.Revision++
		return &cur, nil
	})
}

// Release frees a §25.4 lock. caller MUST be the current acquiredBy. The
// delete is a compare-and-set on the record the ownership check read, so a
// lock a steal replaced is never deleted by its former holder.
//
// spec: §25.4 (Storage Tiers).
func (s *Store) Release(ctx context.Context, lockID, caller string) error {
	_, err := s.mutate(ctx, lockID, 0, func(cur coordination.Lock, _ time.Time) (*coordination.Lock, error) {
		if cur.AcquiredBy != caller {
			return nil, notOwned(lockID)
		}
		return nil, nil
	})
	return err
}

// Steal takes over a §25.4 lock: caller becomes acquiredBy, the prior
// holder is recorded in stolenFrom, revision increments, and the TTL is
// reset from Redis TIME. Steal has no holder check (§25.4 Stealing).
//
// spec: §25.4 (Storage Tiers).
func (s *Store) Steal(ctx context.Context, lockID string, req coordination.StealRequest) (*coordination.Lock, error) {
	ttl := coordination.NormalizeTTL(req.TTLSeconds)
	return s.mutate(ctx, lockID, ttl, func(cur coordination.Lock, now time.Time) (*coordination.Lock, error) {
		cur.StolenFrom = cur.AcquiredBy
		cur.AcquiredBy = req.AcquiredBy
		cur.OperationID = ""
		cur.AcquiredAt = now
		cur.ExpiresAt = now.Add(time.Duration(ttl) * time.Second)
		cur.Revision++
		return &cur, nil
	})
}

// decideFunc computes a by-ID mutation from the stored lock and the Redis
// TIME reading. A nil lock with a nil error deletes the lock; an error is
// returned to the caller unchanged.
type decideFunc func(cur coordination.Lock, now time.Time) (*coordination.Lock, error)

// mutate applies a §25.4 by-ID change with casScript. Each attempt reads
// the lock through the ID key, reads Redis TIME, decides, and runs the
// compare-and-set against the raw stored data string (never a re-marshal,
// because the acquire script re-encodes the record). A failed comparison
// starts the next attempt; after maxCASAttempts it returns
// REMEDIATION_LOCK_CONFLICT, never ErrStoreUnavailable, because that error
// would send the request to the in-memory tier and could create a second
// holder. A Redis transport error maps to ErrStoreUnavailable.
//
// spec: §25.4 (Storage Tiers, Error Codes).
func (s *Store) mutate(ctx context.Context, id string, ttl int, decide decideFunc) (*coordination.Lock, error) {
	for attempt := 0; attempt < maxCASAttempts; attempt++ {
		cur, raw, err := s.readByID(ctx, id)
		if err != nil {
			return nil, unavailable(err)
		}
		if cur == nil {
			return nil, notFound(id)
		}
		now, err := s.serverTime(ctx)
		if err != nil {
			return nil, unavailable(err)
		}
		next, err := decide(*cur, now)
		if err != nil {
			return nil, err
		}
		applied, err := s.compareAndSet(ctx, *cur, raw, next, ttl)
		if err != nil {
			return nil, err
		}
		if applied {
			return next, nil
		}
	}
	return nil, &coordination.Error{
		Code:    coordination.ErrCodeConflict,
		Message: "lock " + id + " changed concurrently on every attempt",
	}
}

// compareAndSet runs casScript for cur, expecting the stored data string
// raw and writing next (or deleting when next is nil). It reports whether
// the script applied the change.
func (s *Store) compareAndSet(ctx context.Context, cur coordination.Lock, raw string, next *coordination.Lock, ttl int) (bool, error) {
	newData := ""
	if next != nil {
		blob, err := json.Marshal(next)
		if err != nil {
			return false, err
		}
		newData = string(blob)
	}
	res, err := casScript.Run(ctx, s.client,
		[]string{scopeKey(cur.Scope), idxKey(cur.ID)},
		raw, newData, pttlMillis(ttl), cur.Scope, cur.ID).Int64()
	if err != nil {
		return false, coordination.ErrStoreUnavailable
	}
	return res == 1, nil
}

func notFound(lockID string) error {
	return &coordination.Error{Code: coordination.ErrCodeNotFound, Message: "no lock " + lockID}
}

func notOwned(lockID string) error {
	return &coordination.Error{Code: coordination.ErrCodeNotOwned, Message: "lock " + lockID + " is not owned by the caller"}
}

// Reap is a no-op for the Redis tier: key-level PTTL expires locks
// natively, so there is no orphaned-row sweep. It satisfies the Store
// contract and always reports zero reaped.
func (s *Store) Reap(_ context.Context) (int, error) { return 0, nil }

// RemoveByID drops the lock with lockID from Redis regardless of holder.
// The §25.4 reconciliation calls it to remove a losing split-brain Redis
// lock once the pre-outage Postgres holder has won, so a later Postgres
// outage cannot resurface the stale lock. The delete is the ID-matched
// releaseScript over the scope key and the ID key; a lock that has already
// expired or been removed, or a re-sent removal, is a no-op.
//
// spec: §25.4 (Storage Tiers, Tier Transitions).
func (s *Store) RemoveByID(ctx context.Context, lockID string) error {
	lock, _, err := s.readByID(ctx, lockID)
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

// Epoch returns the §25.4 ops:{lock}:epoch value, defaulting to 0.
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

// IncrementEpoch advances ops:{lock}:epoch by one (the §25.4
// Postgres→Tier-2 transition) and returns the new value.
func (s *Store) IncrementEpoch(ctx context.Context) (uint64, error) {
	v, err := s.client.Incr(ctx, epochKey).Result()
	if err != nil {
		return 0, coordination.ErrStoreUnavailable
	}
	return uint64(v), nil
}

// SetEpoch writes v as ops:{lock}:epoch when it exceeds the stored value
// (the §25.4 MAX reconciliation, applied to the Redis side after a Postgres
// recovery).
func (s *Store) SetEpoch(ctx context.Context, v uint64) error {
	if _, err := setEpochScript.Run(ctx, s.client, []string{epochKey}, strconv.FormatUint(v, 10)).Result(); err != nil {
		return coordination.ErrStoreUnavailable
	}
	return nil
}

// serverTime reads the Redis TIME command so acquiredAt/expiresAt use the
// §25.4 consistent clock source across replicas.
func (s *Store) serverTime(ctx context.Context) (time.Time, error) {
	t, err := s.client.Time(ctx).Result()
	if err != nil {
		return time.Time{}, err
	}
	return t.UTC(), nil
}

// ServerTime exposes the §25.4 Redis TIME read so the
// Postgres-Redis clock-skew sampler can compare the Redis dependency
// clock against Postgres now() without duplicating the TIME read. It
// reuses the same source acquiredAt/expiresAt are authored from, so the
// monitored skew is the skew that affects lease expiry computation.
//
// spec: §25.4 (Clock Source).
func (s *Store) ServerTime(ctx context.Context) (time.Time, error) {
	return s.serverTime(ctx)
}

// readByID resolves a lock ID to its scope through the ID key, then reads
// the scope hash. It returns the decoded lock and the raw stored data
// string, or a nil lock with a nil error when either key is absent, the
// record does not decode, or the scope key holds a different lock ID (a
// stale ID key whose scope another lock has since acquired).
//
// spec: §25.4 (Storage Tiers).
func (s *Store) readByID(ctx context.Context, id string) (*coordination.Lock, string, error) {
	scope, err := s.client.Get(ctx, idxKey(id)).Result()
	if errors.Is(err, redis.Nil) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	raw, err := s.client.HGet(ctx, scopeKey(scope), "data").Result()
	if errors.Is(err, redis.Nil) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	var lock coordination.Lock
	if err := json.Unmarshal([]byte(raw), &lock); err != nil || lock.ID != id {
		return nil, "", nil
	}
	return &lock, raw, nil
}

// scanAll iterates the scope keyspace and decodes every live lock. Every
// ops:{lock}:scope:<scope> key shares the {lock} hash tag, so on a Redis
// Cluster they sit in one slot; the SCAN still runs on every primary
// (redisconn.ScanKeys with no routing key), so the listing stays complete
// while that slot migrates during resharding.
//
// spec: §25.4 (Storage Tiers); §12.4 (Redis HA and Failure Modes).
func (s *Store) scanAll(ctx context.Context) ([]coordination.Lock, error) {
	var out []coordination.Lock
	err := redisconn.ScanKeys(ctx, s.client, "", scopePrefix+"*", 256, func(keys []string) error {
		for _, k := range keys {
			blob, err := s.client.HGet(ctx, k, "data").Bytes()
			if errors.Is(err, redis.Nil) {
				continue
			}
			if err != nil {
				return err
			}
			var lock coordination.Lock
			if err := json.Unmarshal(blob, &lock); err != nil {
				continue
			}
			out = append(out, lock)
		}
		return nil
	})
	if err != nil {
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
