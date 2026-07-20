// SPDX-License-Identifier: MIT

package events

import "github.com/lennylabs/lenny/pkg/gateway/eventbuffer"

// deliveredKeyWindow bounds how many recently delivered eventKeys one SSE
// connection remembers for deduplication.
//
// Every re-delivery the §25.5 read side can produce comes from replaying a
// window that is itself bounded: a gateway replica's ring buffer re-served on
// each fan-out poll, this replica's ring merged into that window, and the
// MAXLEN-bounded Redis stream re-read after the recovery flush appends the
// outage window to its tail. Remembering the most recent keys across the
// largest of those windows catches every duplicate, while keeping the memory a
// long-lived connection holds flat rather than growing with everything it has
// ever delivered. The bound is a multiple of the ring capacity so a window that
// interleaves two sources still fits. spec: §25.5 (eventKey dedup across
// sources).
const deliveredKeyWindow = 4 * eventbuffer.DefaultBufferCapacity

// deliveredKeys is the set of eventKeys one SSE connection has already been
// written, so an event reaching the connection more than once is delivered
// once: from the fan-out window and a live local publish during a Redis outage,
// from the overlapping Redis and gateway-buffer windows across a source switch,
// or from the Redis stream after the recovery flush re-emitted an outage-window
// event out of stream order.
//
// It retains the most recent deliveredKeyWindow keys and evicts in insertion
// order. The zero value is ready to use.
type deliveredKeys struct {
	seen  map[string]struct{}
	order []string
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
		d.seen = make(map[string]struct{}, deliveredKeyWindow)
	}
	d.seen[key] = struct{}{}
	d.order = append(d.order, key)
	if len(d.order) > deliveredKeyWindow {
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
