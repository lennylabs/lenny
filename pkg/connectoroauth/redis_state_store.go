// SPDX-License-Identifier: MIT

package connectoroauth

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisStateStore is the production §9.3 StateStore. The spec binds the
// authorization-request `state` to "Redis, TTL = 10 min" (state.go
// DefaultStateTTL) so the pending PKCE context survives a gateway
// restart and is shared across replicas: a callback that lands on a
// different replica than the authorize call still resolves its flow
// context. The in-process MemoryStateStore loses both properties.
//
// Single-use consumption is enforced atomically: Consume swaps the live
// entry for a tombstone in one Lua round-trip, so two concurrent
// callbacks for the same `state` cannot both succeed and a replay
// returns ErrStateConsumed rather than ErrStateUnknown. The tombstone
// surfaces a replay attempt in the §9.3 audit trail.
//
// Expiry is delegated to Redis native key TTL rather than a Sweep
// (state.go:289: "a Redis-backed store relies on native key expiry
// instead"). An entry past its TTL is evicted by Redis, so Consume
// returns ErrStateUnknown for it; the Memory store's ErrStateExpired
// distinction is a forensic nicety that the Redis backing does not
// reproduce. The handler maps both to a 4xx caller error.
//
// spec: §9.3 line 157.
type RedisStateStore struct {
	client redis.UniversalClient
}

// NewRedisStateStore returns a StateStore backed by client.
func NewRedisStateStore(client redis.UniversalClient) *RedisStateStore {
	return &RedisStateStore{client: client}
}

var _ StateStore = (*RedisStateStore)(nil)

// redisStateKeyPrefix namespaces the per-flow state entries. The `state`
// value is a cryptographically unique HMAC nonce (StateSigner.Mint), so
// the key needs no tenant scoping — Consume is given only the `state`
// string and must derive the key from it alone.
const redisStateKeyPrefix = "conn:oauth:state:"

// stateConsumedTombstone is the value written in place of a consumed
// flow context. It begins with a NUL byte so it can never collide with
// a JSON-encoded FlowContext (which always begins with `{`).
const stateConsumedTombstone = "\x00consumed"

func redisStateKey(state string) string { return redisStateKeyPrefix + state }

// consumeStateScript atomically resolves a single-use state entry:
//
//	0 → no entry (unknown or TTL-expired)
//	1 → entry is the consumed tombstone (replay)
//	2 → live entry; it is replaced with the tombstone and the encoded
//	    FlowContext is returned for the caller to decode.
//
// ARGV[1] is the tombstone marker, ARGV[2] the tombstone TTL in ms.
var consumeStateScript = redis.NewScript(`
local cur = redis.call('GET', KEYS[1])
if cur == false then return {0, ''} end
if cur == ARGV[1] then return {1, ''} end
redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[2])
return {2, cur}
`)

// Put implements StateStore. It stores fc under state with the given
// TTL; a non-positive TTL selects DefaultStateTTL. A second Put for the
// same state replaces the entry, matching MemoryStateStore semantics.
func (s *RedisStateStore) Put(state string, fc FlowContext, ttl time.Duration) error {
	if state == "" {
		return errors.New("connectoroauth: empty state key")
	}
	if ttl <= 0 {
		ttl = DefaultStateTTL
	}
	payload, err := json.Marshal(fc)
	if err != nil {
		return err
	}
	return s.client.Set(context.Background(), redisStateKey(state), payload, ttl).Err()
}

// Consume implements StateStore. It atomically looks up, validates, and
// tombstones the flow context for state. It returns ErrStateUnknown
// when no entry exists (never stored, or TTL-expired), ErrStateConsumed
// when a prior Consume already tombstoned it, and the decoded
// FlowContext otherwise. The now argument is accepted for interface
// parity; Redis native key expiry is authoritative for this backing.
func (s *RedisStateStore) Consume(state string, _ time.Time) (FlowContext, error) {
	res, err := consumeStateScript.Run(
		context.Background(),
		s.client,
		[]string{redisStateKey(state)},
		stateConsumedTombstone,
		DefaultStateTTL.Milliseconds(),
	).Slice()
	if err != nil {
		return FlowContext{}, err
	}
	if len(res) != 2 {
		return FlowContext{}, errors.New("connectoroauth: malformed consume result")
	}
	code, ok := res[0].(int64)
	if !ok {
		return FlowContext{}, errors.New("connectoroauth: malformed consume result code")
	}
	switch code {
	case 0:
		return FlowContext{}, ErrStateUnknown
	case 1:
		return FlowContext{}, ErrStateConsumed
	}
	payload, ok := res[1].(string)
	if !ok {
		return FlowContext{}, errors.New("connectoroauth: malformed consume payload")
	}
	var fc FlowContext
	if err := json.Unmarshal([]byte(payload), &fc); err != nil {
		return FlowContext{}, err
	}
	return fc, nil
}
