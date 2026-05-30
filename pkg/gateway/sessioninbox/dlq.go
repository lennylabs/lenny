// SPDX-License-Identifier: MIT

package sessioninbox

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

// DefaultMaxDLQSize is the §7.2 line 341 `maxDLQSize` default: the
// per-session dead-letter queue holds at most this many messages before
// evicting the oldest.
const DefaultMaxDLQSize = 500

// DefaultDLQTTL is the §7.2 line 341 dead-letter TTL default
// (`maxResumeWindowSeconds`, 900s if unset). A message enqueued to the
// DLQ for a recovering session is discarded after this window if the
// target never resumes.
const DefaultDLQTTL = 900 * time.Second

// TerminalDLQTTL is the §7.2 line 343 short TTL applied when inbox
// messages are drained to the DLQ on a terminal transition, allowing
// brief post-mortem retrieval by monitoring tools.
const TerminalDLQTTL = 60 * time.Second

// dlqKey is the §12.4 / §7.2 line 341 canonical DLQ Redis key
// `t:{tenant_id}:session:{session_id}:dlq`. The tenant prefix ensures a
// DLQ processor iterating across keys cannot read another tenant's
// messages.
func dlqKey(tenantID, sessionID string) string {
	return "t:" + tenantID + ":session:" + sessionID + ":dlq"
}

// DLQ is the §7.2 dead-letter queue: a Redis sorted set scored by expiry
// timestamp. It buffers messages addressed to a session in a recovering
// state (`resume_pending`, `awaiting_client_action`) and the messages
// drained from a session's inbox on `resume_pending` / terminal
// transitions. Entries past their score are expired; entries are
// delivered in FIFO (ascending-score) order on resume.
//
// spec: §7.2 lines 305-311, 341, 343; §12.4 DLQ key.
type DLQ struct {
	client redis.UniversalClient
	max    int
}

// NewDLQ returns a DLQ backed by client, capped at maxSize messages per
// session. A non-positive maxSize selects DefaultMaxDLQSize.
func NewDLQ(client redis.UniversalClient, maxSize int) *DLQ {
	if maxSize <= 0 {
		maxSize = DefaultMaxDLQSize
	}
	return &DLQ{client: client, max: maxSize}
}

// enqueueDLQScript enforces the §7.2 line 341 `maxDLQSize` cap
// atomically: when the set is already at the cap it drops the
// lowest-scored (oldest-to-expire, i.e. earliest-enqueued for a fixed
// TTL) member and returns it for the caller to surface as a
// `message_dropped` receipt (`reason: "dlq_overflow"`), then adds the
// new member. KEYS[1] is the DLQ key; ARGV[1] is the cap; ARGV[2] is the
// expiry score; ARGV[3] is the serialized member.
var enqueueDLQScript = redis.NewScript(`
local dropped = false
if redis.call('ZCARD', KEYS[1]) >= tonumber(ARGV[1]) then
  local low = redis.call('ZRANGE', KEYS[1], 0, 0)
  if low[1] then
    dropped = low[1]
    redis.call('ZREM', KEYS[1], low[1])
  end
end
redis.call('ZADD', KEYS[1], ARGV[2], ARGV[3])
return dropped
`)

// Enqueue adds msg to the (tenantID, sessionID) DLQ with the given TTL.
// The entry's score is the absolute expiry (msg.EnqueuedAt + ttl) in
// Unix-milliseconds, so a later SweepExpired removes it once the wall
// clock passes the score. A non-positive ttl selects DefaultDLQTTL. On
// overflow the oldest entry is evicted and returned as dropped.
//
// spec: §7.2 line 341.
func (d *DLQ) Enqueue(ctx context.Context, tenantID, sessionID string, msg Message, ttl time.Duration) (*Message, error) {
	if ttl <= 0 {
		ttl = DefaultDLQTTL
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	score := float64(msg.EnqueuedAt.Add(ttl).UnixMilli())
	res, err := enqueueDLQScript.Run(ctx, d.client,
		[]string{dlqKey(tenantID, sessionID)}, d.max, score, payload).Result()
	if errors.Is(err, redis.Nil) {
		// The Lua script returned `false` (no eviction); nothing dropped.
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	s, ok := res.(string)
	if !ok {
		return nil, nil
	}
	var dropped Message
	if err := json.Unmarshal([]byte(s), &dropped); err != nil {
		return nil, err
	}
	return &dropped, nil
}

// DrainAll reads every DLQ entry in FIFO (ascending-score) order and
// deletes the key in one transaction. It backs the §7.2 line 343 /
// §7.3 line 425 DLQ drain on terminal transition (the caller emits a
// `message_expired` event per entry) and the §7.2 line 341 resume
// delivery (the caller re-delivers each entry in FIFO order).
//
// spec: §7.2 lines 341, 343; §7.3 line 425.
func (d *DLQ) DrainAll(ctx context.Context, tenantID, sessionID string) ([]Message, error) {
	key := dlqKey(tenantID, sessionID)
	var rangeCmd *redis.StringSliceCmd
	_, err := d.client.TxPipelined(ctx, func(p redis.Pipeliner) error {
		rangeCmd = p.ZRange(ctx, key, 0, -1)
		p.Del(ctx, key)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return decodeMessages(rangeCmd)
}

// sweepExpiredScript atomically reads and removes every DLQ entry whose
// score (absolute expiry) is at or below the supplied now timestamp,
// returning the removed members so the caller can emit one
// `message_expired` event per entry. KEYS[1] is the DLQ key; ARGV[1] is
// the now cutoff in Unix-milliseconds.
var sweepExpiredScript = redis.NewScript(`
local expired = redis.call('ZRANGEBYSCORE', KEYS[1], '-inf', ARGV[1])
if #expired > 0 then
  redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', ARGV[1])
end
return expired
`)

// SweepExpired removes every DLQ entry whose expiry has passed as of now
// and returns them so the caller emits a `message_expired` event with
// `reason: "dlq_ttl_expired"` on each sender's stream.
//
// spec: §7.2 line 341 (TTL expiry); §15.4.1 message_expired reason
// `dlq_ttl_expired`.
func (d *DLQ) SweepExpired(ctx context.Context, tenantID, sessionID string, now time.Time) ([]Message, error) {
	res, err := sweepExpiredScript.Run(ctx, d.client,
		[]string{dlqKey(tenantID, sessionID)}, now.UnixMilli()).StringSlice()
	if err != nil {
		return nil, err
	}
	return decodeRaw(res)
}

// Len reports the DLQ entry count.
func (d *DLQ) Len(ctx context.Context, tenantID, sessionID string) (int, error) {
	n, err := d.client.ZCard(ctx, dlqKey(tenantID, sessionID)).Result()
	return int(n), err
}

// decodeMessages decodes the result of a ZRange command into Messages.
func decodeMessages(cmd *redis.StringSliceCmd) ([]Message, error) {
	raw, err := cmd.Result()
	if err != nil {
		return nil, err
	}
	return decodeRaw(raw)
}

// decodeRaw decodes serialized DLQ/inbox members into Messages.
func decodeRaw(raw []string) ([]Message, error) {
	out := make([]Message, 0, len(raw))
	for _, s := range raw {
		var msg Message
		if err := json.Unmarshal([]byte(s), &msg); err != nil {
			return nil, err
		}
		out = append(out, msg)
	}
	return out, nil
}
