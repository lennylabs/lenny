// SPDX-License-Identifier: MIT

package events

import (
	"hash/fnv"

	gwevents "github.com/lennylabs/lenny/pkg/events"
)

// stampItemIDs rewrites the top-level wrapper id of every item on a §25.5 poll
// page to the source-independent identity readItemID derives from the item's
// canonical eventKey, and returns the rewritten page.
//
// It is the single authority for items[].id on the polling surface, applied
// once after the page has been assembled and filtered, so the field means the
// same thing whichever source served the request. Each source arrives carrying
// a different position in that field: the local ring stamps its own monotonic
// per-replica sequence, the gateway fan-out carries each responding replica's
// sequence verbatim out of its buffer page, and the Redis stream holds no ring
// position at all. Serving those through untouched would put three value
// domains behind one field, and would emit colliding ids inside a single
// fan-out page, since every replica's ring numbers itself from zero.
//
// The input is not mutated: a page served from the local ring aliases the ring
// buffer's own records, and the fan-out merge is shared with the SSE path.
// spec: §25.5 (the polling envelope is identical whichever source serves it).
func stampItemIDs(items []gwevents.BufferedEvent) []gwevents.BufferedEvent {
	if len(items) == 0 {
		return items
	}
	out := make([]gwevents.BufferedEvent, len(items))
	copy(out, items)
	for i := range out {
		out[i].ID = readItemID(out[i].Event.ID)
	}
	return out
}

// readItemID derives the §25.5 poll envelope's items[].id from the canonical
// eventKey.
//
// The eventKey is the one identity an event carries unchanged across the Redis
// ops:events:stream, a gateway replica's ring, and this replica's local ring,
// so a value derived from it alone is stable for one event whichever source
// served it and distinct for two distinct events on one page. It is a synthetic
// identity rather than a position: ordering comes from the order of items on
// the page, and a caller resumes on the CloudEvents id (Event.ID) and the
// opaque pagination cursor. An event with no eventKey has no identity to derive
// and gets zero. spec: §25.5 (a caller resumes on the CloudEvents id and the
// opaque cursor).
func readItemID(eventKey string) uint64 {
	if eventKey == "" {
		return 0
	}
	h := fnv.New64a()
	// Hash.Write never returns an error.
	_, _ = h.Write([]byte(eventKey))
	sum := h.Sum64()
	if sum == 0 {
		// Zero is reserved for an event carrying no eventKey, so a hash that
		// lands there is nudged off it rather than reported as absent.
		return 1
	}
	return sum
}
