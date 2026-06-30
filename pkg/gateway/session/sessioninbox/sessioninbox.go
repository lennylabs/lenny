// SPDX-License-Identifier: MIT

// Package sessioninbox implements the §7.2 session inbox and dead-letter
// queue (DLQ) used to buffer undelivered inter-session messages.
//
// The session inbox holds messages that arrive while the target runtime
// is not reading from stdin (blocked in `lenny/request_input`, busy in a
// tool call, or suspended). It operates in one of two modes selected by
// the deployment-level `messaging.durableInbox` flag:
//
//   - Default (`durableInbox:false`) — MemoryInbox, an in-memory queue on
//     the coordinating gateway replica. Lost on coordinator crash; the
//     inbox-to-DLQ drain on `resume_pending` narrows the loss window.
//   - Durable (`durableInbox:true`) — RedisInbox, a Redis list keyed
//     `t:{tenant_id}:session:{session_id}:inbox`. Survives coordinator
//     failover; the new coordinator recovers the list on lease acquisition.
//
// Both modes enforce the `maxInboxSize` cap by evicting the oldest
// buffered message and returning it to the caller so the sender receives
// a §15.4 `message_dropped` delivery receipt (`reason: "inbox_overflow"`).
//
// The DLQ (see dlq.go) holds messages addressed to a session in a
// recovering state (`resume_pending`, `awaiting_client_action`) and the
// messages drained from the inbox on those transitions. It is a Redis
// sorted set keyed `t:{tenant_id}:session:{session_id}:dlq`, scored by
// expiry timestamp.
//
// spec: §7.2 lines 274-311 (session inbox), 333-343 (dead-letter
// handling); §12.4 (Redis key prefixes); §15.4.1 (delivery receipt /
// message_expired schemas).
package sessioninbox

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// DefaultMaxInboxSize is the §7.2 line 281 `maxInboxSize` default: the
// per-session inbox buffers at most this many messages before evicting
// the oldest. The cap bounds memory growth during long `await_children`
// blocks.
const DefaultMaxInboxSize = 500

// Message is one buffered inter-session message. The payload is the
// opaque serialized §15.4 MessageEnvelope; the inbox/DLQ never inspect
// it. MessageID and SenderSessionID are carried out-of-band so the
// gateway can route the §15.4.1 `message_expired` event back to the
// sender's stream without decoding the payload.
//
// spec: §7.2 line 292 (`InboxMessage` fields); §15.4.1 message_expired
// schema (messageId, sender routing).
type Message struct {
	// MessageID is the §15.4 line 1784 message id (sender-supplied or
	// gateway-assigned `msg_` prefix). It correlates the buffered
	// message with the synchronous delivery receipt and any later
	// `message_expired` event.
	MessageID string `json:"message_id"`

	// SenderSessionID names the session that sent the message. The DLQ
	// drain-on-terminal and TTL-sweep paths publish `message_expired`
	// onto this session's event stream. Empty for external-client
	// (`POST /messages`) sources that carry no sender session.
	SenderSessionID string `json:"sender_session_id,omitempty"`

	// Payload is the serialized §15.4 MessageEnvelope delivered to the
	// runtime when the message is dequeued.
	Payload []byte `json:"payload"`

	// EnqueuedAt is the wall-clock time the coordinating replica
	// accepted the message. It establishes FIFO order in the in-memory
	// inbox and seeds the durable-inbox per-message TTL.
	EnqueuedAt time.Time `json:"enqueued_at"`
}

// Inbox is the per-session undelivered-message buffer. Implementations
// are safe for concurrent use by the coordinating replica's goroutines.
//
// spec: §7.2 lines 274-303.
type Inbox interface {
	// Enqueue appends msg to (tenantID, sessionID)'s inbox in FIFO
	// order. When the inbox already holds `maxInboxSize` messages the
	// oldest is evicted and returned as dropped so the caller emits the
	// §15.4 `message_dropped` receipt (`reason: "inbox_overflow"`); a
	// nil dropped means the message fit without eviction.
	// spec: §7.2 lines 282, 293.
	Enqueue(ctx context.Context, tenantID, sessionID string, msg Message) (dropped *Message, err error)

	// Drain returns every buffered message in FIFO order and clears the
	// inbox atomically. It backs the inbox-to-DLQ migration on
	// `resume_pending` (in-memory mode) and the inbox drain on terminal.
	// spec: §7.2 lines 305-311, 343.
	Drain(ctx context.Context, tenantID, sessionID string) ([]Message, error)

	// Len reports the buffered message count.
	Len(ctx context.Context, tenantID, sessionID string) (int, error)
}

// inboxKey is the §12.4 durable-inbox Redis key
// `t:{tenant_id}:session:{session_id}:inbox`. The in-memory inbox reuses
// the same string as its map key so the two modes are interchangeable.
func inboxKey(tenantID, sessionID string) string {
	return "t:" + tenantID + ":session:" + sessionID + ":inbox"
}

// MemoryInbox is the default (`durableInbox:false`) in-memory inbox. It
// holds messages on the coordinating gateway replica and is lost on
// coordinator crash; the §7.2 inbox-to-DLQ drain on `resume_pending`
// narrows the loss window.
//
// spec: §7.2 lines 276-284.
type MemoryInbox struct {
	mu    sync.Mutex
	max   int
	byKey map[string][]Message
}

var _ Inbox = (*MemoryInbox)(nil)

// NewMemoryInbox returns an in-memory inbox capped at maxSize messages
// per session. A non-positive maxSize selects DefaultMaxInboxSize.
func NewMemoryInbox(maxSize int) *MemoryInbox {
	if maxSize <= 0 {
		maxSize = DefaultMaxInboxSize
	}
	return &MemoryInbox{max: maxSize, byKey: make(map[string][]Message)}
}

// Enqueue implements Inbox. On overflow it drops the oldest (head)
// message and returns a copy, matching §7.2 line 282 "the oldest
// buffered message is dropped".
func (m *MemoryInbox) Enqueue(_ context.Context, tenantID, sessionID string, msg Message) (*Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := inboxKey(tenantID, sessionID)
	q := append(m.byKey[key], msg)
	var dropped *Message
	if len(q) > m.max {
		old := q[0]
		dropped = &old
		q = q[1:]
	}
	m.byKey[key] = q
	return dropped, nil
}

// Drain implements Inbox.
func (m *MemoryInbox) Drain(_ context.Context, tenantID, sessionID string) ([]Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := inboxKey(tenantID, sessionID)
	q := m.byKey[key]
	delete(m.byKey, key)
	return q, nil
}

// Len implements Inbox.
func (m *MemoryInbox) Len(_ context.Context, tenantID, sessionID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.byKey[inboxKey(tenantID, sessionID)]), nil
}

// enqueueInboxScript enforces the §7.2 line 293 cap atomically: it
// checks LLEN before RPUSH and, when the list is already at the cap,
// LPOPs the oldest entry and returns it for the caller to surface as a
// `message_dropped` receipt. KEYS[1] is the inbox key; ARGV[1] is the
// cap; ARGV[2] is the serialized message.
var enqueueInboxScript = redis.NewScript(`
local dropped = false
if redis.call('LLEN', KEYS[1]) >= tonumber(ARGV[1]) then
  dropped = redis.call('LPOP', KEYS[1])
end
redis.call('RPUSH', KEYS[1], ARGV[2])
return dropped
`)

// RedisInbox is the durable (`durableInbox:true`) inbox backed by a
// Redis list. Enqueue is atomic via a Lua script; recovery reads the
// list in FIFO order with LRANGE. The list survives coordinator
// failover, so the new coordinator recovers undelivered messages on
// lease acquisition.
//
// spec: §7.2 lines 286-303; §12.4 durable-inbox key.
type RedisInbox struct {
	client redis.UniversalClient
	max    int
}

var _ Inbox = (*RedisInbox)(nil)

// NewRedisInbox returns a durable inbox backed by client, capped at
// maxSize messages per session. A non-positive maxSize selects
// DefaultMaxInboxSize.
func NewRedisInbox(client redis.UniversalClient, maxSize int) *RedisInbox {
	if maxSize <= 0 {
		maxSize = DefaultMaxInboxSize
	}
	return &RedisInbox{client: client, max: maxSize}
}

// Enqueue implements Inbox. A Redis error surfaces to the caller, which
// maps it to the §7.2 line 299 `error` receipt (`reason:
// "inbox_unavailable"`).
func (r *RedisInbox) Enqueue(ctx context.Context, tenantID, sessionID string, msg Message) (*Message, error) {
	payload, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	res, err := enqueueInboxScript.Run(ctx, r.client,
		[]string{inboxKey(tenantID, sessionID)}, r.max, payload).Result()
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

// Drain implements Inbox. It reads the whole list in FIFO order and
// deletes the key in one pipeline so a concurrent enqueue cannot lose a
// message between the read and the delete.
func (r *RedisInbox) Drain(ctx context.Context, tenantID, sessionID string) ([]Message, error) {
	key := inboxKey(tenantID, sessionID)
	var rangeCmd *redis.StringSliceCmd
	_, err := r.client.TxPipelined(ctx, func(p redis.Pipeliner) error {
		rangeCmd = p.LRange(ctx, key, 0, -1)
		p.Del(ctx, key)
		return nil
	})
	if err != nil {
		return nil, err
	}
	raw, err := rangeCmd.Result()
	if err != nil {
		return nil, err
	}
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

// Len implements Inbox.
func (r *RedisInbox) Len(ctx context.Context, tenantID, sessionID string) (int, error) {
	n, err := r.client.LLen(ctx, inboxKey(tenantID, sessionID)).Result()
	return int(n), err
}

// Expire sets the §7.2 line 305 recovery-window TTL on the durable
// inbox key. On `resume_pending` the durable inbox is left in Redis
// (rather than drained to the DLQ), so the gateway applies an EXPIRE to
// clean up stale messages if the session never resumes.
//
// spec: §7.2 line 305 (durableInbox:true resume_pending handling).
func (r *RedisInbox) Expire(ctx context.Context, tenantID, sessionID string, ttl time.Duration) error {
	return r.client.Expire(ctx, inboxKey(tenantID, sessionID), ttl).Err()
}

// ErrInboxUnavailable reports that a durable-inbox operation failed
// because Redis was unreachable. The §7.2 line 299 contract returns an
// `error` receipt with `reason: "inbox_unavailable"` to the sender.
var ErrInboxUnavailable = errors.New("sessioninbox: durable inbox unavailable")
