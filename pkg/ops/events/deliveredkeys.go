// SPDX-License-Identifier: MIT

package events

import "github.com/lennylabs/lenny/pkg/gateway/eventbuffer"

// bufferReplayWindow is the floor on how many recently delivered eventKeys one
// SSE connection remembers: enough to cover a replay of the local ring merged
// with a gateway replica's ring, with room for the two windows to interleave.
// A deployment with no Redis wired can replay no more than that. spec: §25.5
// (eventKey dedup across sources).
const bufferReplayWindow = 4 * eventbuffer.DefaultBufferCapacity

// deliveredKeyWindow returns how many recently delivered eventKeys a session on
// this Service must remember for deduplication.
//
// Every re-delivery the §25.5 read side can produce comes from replaying a
// window that is itself bounded: a gateway replica's ring buffer re-served on
// each fan-out poll, this replica's ring merged into that window, and the
// MAXLEN-bounded Redis stream re-read from its head when a resume point cannot
// be resolved. The bound must cover the largest of those, which is the Redis
// stream's retained window whenever Redis is wired: a session that replays the
// whole retained window while remembering fewer keys than it holds writes every
// earlier event a second time, and nothing else on that path can catch the
// duplicate (the resume position is forward-only and deliverOnce does not
// consult it). Remembering that many keys, and no more, keeps the memory a
// long-lived connection holds flat rather than growing with everything it has
// ever delivered. spec: §25.5 (eventKey dedup across sources, exactly-once
// across the source switch).
func (s *Service) deliveredKeyWindow() int {
	window := bufferReplayWindow
	if s.redis != nil && s.redis.maxWindow > int64(window) {
		window = int(s.redis.maxWindow)
	}
	return window
}

// deliveredKeys is the set of eventKeys one SSE connection has already been
// written, so an event reaching the connection more than once is delivered
// once: from the fan-out window and a live local publish during a Redis outage,
// from the overlapping Redis and gateway-buffer windows across a source switch,
// or from the Redis stream after the recovery flush re-emitted an outage-window
// event out of stream order.
//
// It retains the most recent window keys and evicts in insertion order. A
// non-positive window falls back to bufferReplayWindow. The zero value is ready
// to use.
type deliveredKeys struct {
	window int
	seen   map[string]struct{}
	order  []string
}

// limit reports the retained-key bound this set evicts at.
func (d *deliveredKeys) limit() int {
	if d.window <= 0 {
		return bufferReplayWindow
	}
	return d.window
}

// add records key and reports whether it was newly added. An empty key is
// always reported as new: an event carrying no eventKey cannot be deduplicated
// against anything, and recording it would collapse every such event into one.
func (d *deliveredKeys) add(key string) bool {
	if key == "" {
		return true
	}
	if d.has(key) {
		return false
	}
	if d.seen == nil {
		d.seen = make(map[string]struct{})
	}
	d.seen[key] = struct{}{}
	d.order = append(d.order, key)
	if len(d.order) > d.limit() {
		delete(d.seen, d.order[0])
		d.order = d.order[1:]
	}
	return true
}

// has reports whether key is still in the retained window.
func (d *deliveredKeys) has(key string) bool {
	if key == "" || d.seen == nil {
		return false
	}
	_, ok := d.seen[key]
	return ok
}
