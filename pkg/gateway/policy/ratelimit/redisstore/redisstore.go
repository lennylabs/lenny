// SPDX-License-Identifier: MIT

// Package redisstore is the Redis-backed ratelimit.Counter. The
// per-minute request count lives in a Redis key so the §11.1 rate
// limit holds across gateway replicas. The window key embeds the
// minute epoch, so a fresh key — starting from zero — is created when
// the minute advances; the key carries a short TTL so spent windows
// self-evict.
package redisstore

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/policy/ratelimit"
)

// windowTTLSeconds is how long a minute-window key lives. Two minutes
// outlives the one-minute window with margin for clock skew, then the
// spent key self-evicts.
const windowTTLSeconds = 120

// incrScript increments the window key and sets its TTL on the first
// request in the window, returning the running count.
var incrScript = redis.NewScript(`
local n = redis.call('INCR', KEYS[1])
if n == 1 then
  redis.call('EXPIRE', KEYS[1], ARGV[1])
end
return n
`)

// Store is the Redis-backed request counter. Construct with New.
type Store struct {
	client redis.UniversalClient
}

// New returns a Store backed by client.
func New(client redis.UniversalClient) *Store { return &Store{client: client} }

var _ ratelimit.Counter = (*Store)(nil)

// Incr implements ratelimit.Counter.
func (s *Store) Incr(ctx context.Context, key string, now time.Time) (int, error) {
	windowKey := fmt.Sprintf("rl:%s:%d", key, now.Unix()/60)
	n, err := incrScript.Run(ctx, s.client, []string{windowKey}, windowTTLSeconds).Int()
	if err != nil {
		return 0, fmt.Errorf("ratelimit: incr: %w", err)
	}
	return n, nil
}
