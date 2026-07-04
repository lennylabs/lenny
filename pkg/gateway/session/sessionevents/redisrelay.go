// SPDX-License-Identifier: MIT

package sessionevents

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// DefaultStreamMaxLen is the §4.4 line 225 / §12.4 (lenny:events: relay
// row) Redis Streams retention cap. Each session's stream is trimmed to
// this many entries via `XADD ... MAXLEN ~ {cap}` so the cross-replica
// replay buffer is bounded. The cap matches the default per-session
// in-memory history (`NewBus(maxHistory)` default 256) so a reconnecting
// client whose cursor falls within the cap is served from either store
// equivalently.
const DefaultStreamMaxLen = 256

// streamKey returns the §4.4 / §12.4 (lenny:events: relay row) Redis
// Streams key for a session. The prefix scopes the stream to the gateway
// namespace so cross-product key collisions are impossible.
func streamKey(sessionID string) string {
	return "lenny:events:" + sessionID
}

// RedisRelay is the §4.4 line 225 / §12.4 (lenny:events: relay row)
// Redis-backed cross-replica event relay. It pairs with an in-memory
// Bus: every Publish on the Bus fans out to the relay's `XADD` so
// reading replicas can replay the stream via `XRANGE` (history) or
// `XREAD BLOCK ...` (live).
//
// The relay is a stateless wrapper over the redis.UniversalClient.
// Single-replica deployments leave the relay nil; the Bus keeps its
// existing in-memory semantics. Multi-replica deployments wire a
// non-nil relay and gain cross-replica history + live delivery.
//
// spec: §4.4 line 225 — "Event cursors / stream offsets" durable
// across replicas. spec: §12.4 — the lenny:events: cross-replica relay
// stream row in the canonical key-prefix table.
type RedisRelay struct {
	// Client is the Redis universal client. A nil client makes every
	// method a no-op so the gateway can wire the relay unconditionally.
	Client redis.UniversalClient
	// MaxLen is the per-session stream cap (`XADD MAXLEN ~ {cap}`). A
	// non-positive value selects DefaultStreamMaxLen.
	MaxLen int64
	// Now overrides time.Now for the published timestamp encoded into
	// each stream entry. Nil selects time.Now.
	Now func() time.Time
}

// NewRedisRelay returns a RedisRelay over client. A nil client yields
// a relay whose Publish / Read methods are no-ops, so callers do not
// branch on the Redis-flag at every call site.
func NewRedisRelay(client redis.UniversalClient) *RedisRelay {
	return &RedisRelay{
		Client: client,
		MaxLen: DefaultStreamMaxLen,
		Now:    time.Now,
	}
}

// streamEntry is the §4.4 stream-entry payload. The Bus fan-out
// marshals one of these per event so the read path can decode the
// original Event back.
type streamEntry struct {
	Seq       uint64 `json:"seq"`
	SessionID string `json:"sessionId"`
	Type      string `json:"type"`
	Data      string `json:"data"`
	// Timestamp is the UTC unix-nano. Stream entries' XADD id is
	// monotonic-on-server, but the gateway-side timestamp lets a
	// replay distinguish stamp from arrival.
	Timestamp int64 `json:"timestampUnixNano"`
}

// PublishEvent fan-outs one Event onto the session's Redis stream
// via `XADD`. The relay maintains the stream cap via `MAXLEN ~ {cap}`
// so the per-session stream is bounded. A failure is logged and
// swallowed: the in-memory delivery on the originating replica is
// still authoritative on this hop.
func (r *RedisRelay) PublishEvent(ctx context.Context, ev Event) {
	if r == nil || r.Client == nil {
		return
	}
	body, err := json.Marshal(streamEntry{
		Seq:       ev.Seq,
		SessionID: ev.SessionID,
		Type:      ev.Type,
		Data:      ev.Data,
		Timestamp: ev.Timestamp.UTC().UnixNano(),
	})
	if err != nil {
		log.Printf("sessionevents: marshal event seq=%d: %v", ev.Seq, err)
		return
	}
	maxLen := r.MaxLen
	if maxLen <= 0 {
		maxLen = DefaultStreamMaxLen
	}
	args := &redis.XAddArgs{
		Stream: streamKey(ev.SessionID),
		MaxLen: maxLen,
		Approx: true,
		ID:     "*",
		Values: map[string]any{"payload": body, "seq": ev.Seq},
	}
	if _, err := r.Client.XAdd(ctx, args).Result(); err != nil {
		log.Printf("sessionevents: XADD seq=%d session=%s: %v", ev.Seq, ev.SessionID, err)
	}
}

// History returns every retained stream entry for sessionID whose Seq
// is strictly greater than afterSeq, in chronological order. This is
// the cross-replica reconnect-with-cursor path: a client that
// reconnects to a different replica reads the original replica's
// publishes from the Redis stream.
//
// A nil relay or nil client returns an empty slice so the in-memory
// Bus path falls back to its local history.
func (r *RedisRelay) History(ctx context.Context, sessionID string, afterSeq uint64) ([]Event, error) {
	if r == nil || r.Client == nil {
		return nil, nil
	}
	res, err := r.Client.XRange(ctx, streamKey(sessionID), "-", "+").Result()
	if err != nil {
		return nil, fmt.Errorf("sessionevents: XRANGE %s: %w", sessionID, err)
	}
	out := make([]Event, 0, len(res))
	for _, msg := range res {
		ev, ok := decodeEntry(msg)
		if !ok {
			continue
		}
		if ev.Seq > afterSeq {
			out = append(out, ev)
		}
	}
	return out, nil
}

// LiveFromCursor returns a channel that streams live events with Seq
// > afterSeq from the session's Redis stream until ctx is cancelled.
// The implementation uses `XREAD BLOCK 0` so the reader sleeps inside
// the Redis call and wakes on every newly XADDed entry.
//
// The channel is closed when ctx is cancelled or when Redis returns
// a terminal error. A nil relay or nil client returns a closed
// channel so the caller does not block waiting for events that will
// never arrive.
func (r *RedisRelay) LiveFromCursor(ctx context.Context, sessionID string, afterSeq uint64) <-chan Event {
	out := make(chan Event, 16)
	if r == nil || r.Client == nil {
		close(out)
		return out
	}
	go func() {
		defer close(out)
		lastID := lastIDFromSeq(afterSeq)
		for {
			if ctx.Err() != nil {
				return
			}
			res, err := r.Client.XRead(ctx, &redis.XReadArgs{
				Streams: []string{streamKey(sessionID), lastID},
				Block:   0,
				Count:   32,
			}).Result()
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				// Transient Redis error — sleep briefly and retry.
				time.Sleep(50 * time.Millisecond)
				continue
			}
			for _, stream := range res {
				for _, msg := range stream.Messages {
					ev, ok := decodeEntry(msg)
					if !ok {
						lastID = msg.ID
						continue
					}
					if ev.Seq > afterSeq {
						select {
						case out <- ev:
						case <-ctx.Done():
							return
						}
					}
					lastID = msg.ID
				}
			}
		}
	}()
	return out
}

// decodeEntry rebuilds an Event from one stream XMessage. The "payload"
// field carries the JSON-encoded streamEntry; the relay decodes it
// back into the original Event shape.
func decodeEntry(msg redis.XMessage) (Event, bool) {
	raw, ok := msg.Values["payload"]
	if !ok {
		return Event{}, false
	}
	var body []byte
	switch v := raw.(type) {
	case string:
		body = []byte(v)
	case []byte:
		body = v
	default:
		return Event{}, false
	}
	var entry streamEntry
	if err := json.Unmarshal(body, &entry); err != nil {
		return Event{}, false
	}
	return Event{
		Seq:       entry.Seq,
		SessionID: entry.SessionID,
		Type:      entry.Type,
		Data:      entry.Data,
		Timestamp: time.Unix(0, entry.Timestamp).UTC(),
	}, true
}

// lastIDFromSeq returns the Redis Streams ID a reader passes to
// XREAD when resuming from a logical Seq cursor. v1 uses "0" so the
// reader gets the full retained stream then filters by Seq; a future
// optimisation will record Seq → stream-id at XADD time so the reader
// resumes at the precise entry. The filter at the relay's read site
// keeps the wire-level behaviour identical either way.
func lastIDFromSeq(seq uint64) string {
	if seq == 0 {
		return "0"
	}
	_ = strconv.FormatUint(seq, 10)
	return "0" // see above; v1 reads all and filters
}
