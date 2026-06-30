// SPDX-License-Identifier: MIT

package opsservice

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/redis/go-redis/v9"
)

// DefaultRedisEventSourceCount bounds how many stream entries one Poll
// drains. The §25.5 webhook worker calls Poll once per loop tick; the
// cap keeps a single tick from materialising an unbounded slice when a
// large backlog has accumulated between ticks. Remaining entries are
// drained on the following tick because the cursor only advances past
// the entries actually returned.
const DefaultRedisEventSourceCount = 1000

// StreamReader is the subset of redis.UniversalClient the
// RedisEventSource consumes the §25.5 ops:events:stream with.
// redis.UniversalClient satisfies it; tests substitute a fake.
//
// spec: §25.5 lines 2592-2611 — the gateway, the controllers, and
// lenny-ops all XADD operational events to the platform-scoped Redis
// stream ops:events:stream; the webhook fan-out worker reads the same
// stream via XRANGE from a per-process cursor.
type StreamReader interface {
	XRangeN(ctx context.Context, stream, start, stop string, count int64) *redis.XMessageSliceCmd
	XRevRangeN(ctx context.Context, stream, start, stop string, count int64) *redis.XMessageSliceCmd
}

// RedisEventSource is the production §25.5 EventSource. It consumes the
// platform-scoped Redis stream that every replica (gateway, controllers,
// peer lenny-ops) writes operational events to, and yields each new
// event to the webhook delivery worker. It pairs with the producer side
// (pkg/gateway/eventbuffer.StreamEmitter): the StreamEmitter XADDs each event
// as a single "event" field carrying the marshalled CloudEvents record,
// and this source reads that field back out.
//
// The read cursor is per-process and independent (§25.5 "per-connection
// independent read cursor, no consumer group"). On the first Poll the
// source snaps its cursor to the current stream tail so a cold start
// does not replay the whole MAXLEN-bounded backlog to every webhook
// subscription; every subsequent Poll returns the entries appended
// since the previous Poll via an exclusive XRANGE.
//
// spec: §25.3 lines 665-667; §25.5 lines 2552, 2590-2611.
type RedisEventSource struct {
	client StreamReader
	stream string
	count  int64

	mu     sync.Mutex
	lastID string
	primed bool
}

// NewRedisEventSource returns a RedisEventSource reading stream from
// client. An empty stream falls back to the §25.5 default key. A
// non-positive count uses DefaultRedisEventSourceCount.
func NewRedisEventSource(client StreamReader, stream string) *RedisEventSource {
	if stream == "" {
		stream = "ops:events:stream"
	}
	return &RedisEventSource{
		client: client,
		stream: stream,
		count:  DefaultRedisEventSourceCount,
	}
}

// Poll implements EventSource. It returns the operational events
// appended to the stream since the previous Poll. The first Poll primes
// the cursor to the stream tail and returns no events; subsequent Polls
// drain the entries that arrived after the cursor.
func (s *RedisEventSource) Poll(ctx context.Context) ([]WebhookEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.primed {
		// Snap the cursor to the current tail (most-recent entry) so a
		// cold start delivers only events that arrive afterwards. An
		// empty stream leaves the cursor at the zero id so the first
		// real entry is delivered.
		tail, err := s.client.XRevRangeN(ctx, s.stream, "+", "-", 1).Result()
		if err != nil {
			return nil, err
		}
		s.lastID = "0-0"
		if len(tail) > 0 {
			s.lastID = tail[0].ID
		}
		s.primed = true
		return nil, nil
	}

	// The "(" prefix makes the range start exclusive of lastID, so an
	// entry is never delivered twice.
	msgs, err := s.client.XRangeN(ctx, s.stream, "("+s.lastID, "+", s.count).Result()
	if err != nil {
		return nil, err
	}
	if len(msgs) == 0 {
		return nil, nil
	}
	out := make([]WebhookEvent, 0, len(msgs))
	for _, m := range msgs {
		s.lastID = m.ID
		ev, ok := decodeStreamEvent(m)
		if !ok {
			continue
		}
		out = append(out, ev)
	}
	return out, nil
}

// decodeStreamEvent rebuilds a WebhookEvent from one stream entry. The
// StreamEmitter stores the marshalled CloudEvents record under the
// "event" field; the CloudEvents id and type drive the webhook
// X-Lenny-* headers and subscription matching, and the raw record is
// the body posted to the callback.
func decodeStreamEvent(m redis.XMessage) (WebhookEvent, bool) {
	raw, ok := m.Values["event"]
	if !ok {
		return WebhookEvent{}, false
	}
	var body []byte
	switch v := raw.(type) {
	case string:
		body = []byte(v)
	case []byte:
		body = v
	default:
		return WebhookEvent{}, false
	}
	var ce struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	}
	if err := json.Unmarshal(body, &ce); err != nil {
		return WebhookEvent{}, false
	}
	return WebhookEvent{ID: ce.ID, Type: ce.Type, Body: body}, true
}

// Compile-time guard that *RedisEventSource satisfies EventSource.
var _ EventSource = (*RedisEventSource)(nil)
