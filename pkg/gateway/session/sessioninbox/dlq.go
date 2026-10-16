// SPDX-License-Identifier: MIT

package sessioninbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// DefaultMaxDLQSize is the §7.2 default: the
// per-session dead-letter queue holds at most this many messages before
// evicting the oldest.
const DefaultMaxDLQSize = 500

// DefaultDLQTTL is the §7.2 dead-letter TTL default
// (`maxResumeWindowSeconds`, 900s if unset). A message enqueued to the
// DLQ for a recovering session is discarded after this window if the
// target never resumes.
const DefaultDLQTTL = 900 * time.Second

// TerminalDLQTTL is the §7.2 short TTL applied when inbox
// messages are drained to the DLQ on a terminal transition, allowing
// brief post-mortem retrieval by monitoring tools.
const TerminalDLQTTL = 60 * time.Second

// dlqKey is the §12.4 / §7.2 canonical DLQ Redis key
// `t:{tenant_id}:session:{session_id}:dlq`. The tenant prefix ensures a
// DLQ processor iterating across keys cannot read another tenant's
// messages.
func dlqKey(tenantID, sessionID string) string {
	return "t:" + tenantID + ":session:" + sessionID + ":dlq"
}

// DLQ is the §7.2 dead-letter queue: a Redis sorted set. It holds the
// messages the gateway has accepted for a session but has not yet written
// to its runtime, whatever state the session is in (pre-running,
// recovering, suspended, or running behind a held entry), until a delivery
// point drains them or the expiry sweep expires them. Each entry is scored
// by its acceptance time plus DefaultDLQTTL, so ascending score is
// acceptance order and delivery is FIFO. The sweep expires an entry at the
// later of its score and the session row's entry to its state plus the TTL.
//
// spec: §7.2 (Interactive Session Model); §12.4 DLQ key.
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

// enqueueDLQScript enforces the §7.2 cap
// atomically: when the set is already at the cap it drops the
// lowest-scored (oldest-to-expire, i.e. earliest-enqueued for a fixed
// TTL) member and returns it for the caller to surface as a
// `message_dropped` receipt (`reason: "dlq_overflow"`), then adds the
// new member.
//
// The score is a whole millisecond, so two messages accepted in the same
// millisecond would tie, and Redis orders tied members by their bytes, which
// for a JSON member means by message ID rather than by acceptance. The
// script breaks the tie in enqueue order instead: when the set already holds
// a member scored inside the new member's millisecond, the new member takes
// the next thousandth above the highest of them, up to .999, so the delivery
// of held messages takes same-millisecond entries in the order the gateway
// accepted them. A float64 score near the current Unix-millisecond epoch
// resolves about four decimal places, so a thousandth step is exact enough
// to order, and every fraction stays below the next whole millisecond, so
// expiry and CountAcceptedAfter still read the millisecond.
//
// KEYS[1] is the DLQ key; ARGV[1] is the cap; ARGV[2] is the expiry score in
// whole Unix-milliseconds; ARGV[3] is the serialized member; ARGV[4] is
// ARGV[2] plus one.
var enqueueDLQScript = redis.NewScript(`
local dropped = false
if redis.call('ZCARD', KEYS[1]) >= tonumber(ARGV[1]) then
  local low = redis.call('ZRANGE', KEYS[1], 0, 0)
  if low[1] then
    dropped = low[1]
    redis.call('ZREM', KEYS[1], low[1])
  end
end
local score = ARGV[2]
local top = redis.call('ZREVRANGEBYSCORE', KEYS[1], '(' .. ARGV[4], ARGV[2], 'WITHSCORES', 'LIMIT', 0, 1)
if top[2] then
  local step = math.floor((tonumber(top[2]) - tonumber(ARGV[2])) * 1000 + 0.5) + 1
  if step > 999 then
    step = 999
  end
  score = ARGV[2] .. '.' .. string.sub('00' .. tostring(step), -3)
end
redis.call('ZADD', KEYS[1], score, ARGV[3])
return dropped
`)

// Enqueue adds msg to the (tenantID, sessionID) DLQ with the given TTL.
// The entry's score is the absolute expiry (msg.EnqueuedAt + ttl) in
// Unix-milliseconds, plus the enqueue-order fraction enqueueDLQScript adds
// to a member that ties an earlier one in the same millisecond, so a later
// SweepExpired removes it once the wall clock passes the score. A
// non-positive ttl selects DefaultDLQTTL. On
// overflow the oldest entry is evicted and returned as dropped.
//
// spec: §7.2.
func (d *DLQ) Enqueue(ctx context.Context, tenantID, sessionID string, msg Message, ttl time.Duration) (*Message, error) {
	if ttl <= 0 {
		ttl = DefaultDLQTTL
	}
	payload, err := DLQMember(msg)
	if err != nil {
		return nil, err
	}
	score := msg.EnqueuedAt.Add(ttl).UnixMilli()
	res, err := enqueueDLQScript.Run(ctx, d.client,
		[]string{dlqKey(tenantID, sessionID)}, d.max,
		strconv.FormatInt(score, 10), payload, strconv.FormatInt(score+1, 10)).Result()
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

// DLQMember returns the sorted-set member Enqueue stores for msg, so a caller
// that enqueued msg can Remove exactly that entry.
func DLQMember(msg Message) (string, error) {
	b, err := json.Marshal(msg)
	return string(b), err
}

// DrainAll reads every DLQ entry in FIFO (ascending-score) order and
// deletes the key in one transaction. It backs the §7.2 /
// §7.3 DLQ drain on terminal transition (the caller emits a
// `message_expired` event per entry) and the §7.2 resume
// delivery (the caller re-delivers each entry in FIFO order).
//
// spec: §7.2; §7.3.
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
// spec: §7.2; §15.4 message_expired reason
// `dlq_ttl_expired`.
func (d *DLQ) SweepExpired(ctx context.Context, tenantID, sessionID string, now time.Time) ([]Message, error) {
	res, err := sweepExpiredScript.Run(ctx, d.client,
		[]string{dlqKey(tenantID, sessionID)}, now.UnixMilli()).StringSlice()
	if err != nil {
		return nil, err
	}
	return decodeRaw(res)
}

// PeekOldest returns the DLQ's lowest-scored entry without removing it,
// decoded, together with its raw member for Remove. ok is false when the DLQ
// is empty or the call returns an error, including a head member whose
// envelope does not decode, so a failed peek is never mistaken for an entry.
// It ignores the entry's score, so an entry past its expiry is still
// returned. Every DLQ enqueue scores EnqueuedAt plus the default TTL,
// so the lowest score is the earliest accepted entry.
//
// spec: §7.2 (Message delivery routing — seven paths, path 6).
func (d *DLQ) PeekOldest(ctx context.Context, tenantID, sessionID string) (Message, string, bool, error) {
	raw, err := d.client.ZRange(ctx, dlqKey(tenantID, sessionID), 0, 0).Result()
	if err != nil {
		return Message{}, "", false, err
	}
	if len(raw) == 0 {
		return Message{}, "", false, nil
	}
	var msg Message
	if err := json.Unmarshal([]byte(raw[0]), &msg); err != nil {
		return Message{}, "", false, fmt.Errorf("decode DLQ head of session %s: %w", sessionID, err)
	}
	return msg, raw[0], true, nil
}

// Remove removes the one DLQ entry member names and reports whether it found
// it. A missing member is not an error.
//
// spec: §7.2 (Message delivery routing — seven paths, path 6).
func (d *DLQ) Remove(ctx context.Context, tenantID, sessionID, member string) (bool, error) {
	n, err := d.client.ZRem(ctx, dlqKey(tenantID, sessionID), member).Result()
	return n > 0, err
}

// CountAcceptedAfter counts the DLQ entries the gateway accepted strictly
// after since. Every held entry is scored by its acceptance time plus
// DefaultDLQTTL, so the count is the entries whose score exceeds since plus
// that TTL. It backs the backlog check of an interrupt suspension, which
// counts only messages accepted after the suspension.
//
// spec: §7.2 (Message delivery routing — seven paths, path 6).
func (d *DLQ) CountAcceptedAfter(ctx context.Context, tenantID, sessionID string, since time.Time) (int, error) {
	// An entry accepted in since's own millisecond can carry a tie-break
	// fraction above that millisecond, so the count starts at the next whole
	// millisecond rather than just above since's.
	floor := since.Add(DefaultDLQTTL).UnixMilli() + 1
	n, err := d.client.ZCount(ctx, dlqKey(tenantID, sessionID), strconv.FormatInt(floor, 10), "+inf").Result()
	return int(n), err
}

// Delete removes the session's DLQ and reports how many entries it held,
// reading the count and deleting the key in one transaction. It backs the
// §12.8 user erasure of the session's message queues; it emits nothing.
//
// spec: §12.8 (Compliance Interfaces).
func (d *DLQ) Delete(ctx context.Context, tenantID, sessionID string) (int, error) {
	key := dlqKey(tenantID, sessionID)
	var card *redis.IntCmd
	if _, err := d.client.TxPipelined(ctx, func(p redis.Pipeliner) error {
		card = p.ZCard(ctx, key)
		p.Del(ctx, key)
		return nil
	}); err != nil {
		return 0, err
	}
	return int(card.Val()), nil
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
