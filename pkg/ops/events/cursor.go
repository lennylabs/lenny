// SPDX-License-Identifier: MIT

package events

import (
	"encoding/base64"
	"fmt"
	"strings"
)

// Source kinds carried in an opaque §25.5 event cursor. The cursor
// encodes the source that produced it so a later request can detect a
// source transition. spec: §25.5 lines 2666-2675 ("source_kind is one
// of redis, buffer, or mixed"); §25.2 line 254 (common cursorKind
// values).
const (
	// SourceKindBuffer marks a cursor produced by the in-memory ring
	// buffer (the v1 lenny-ops event source).
	SourceKindBuffer = "buffer"
	// SourceKindRedis marks a cursor produced by the Redis stream. It
	// carries the canonical eventKey like every other source's cursor, so it
	// round-trips: the gateway-buffer and local-ring sources resolve that key
	// against their own windows (reported as mixed). It additionally carries
	// the Redis stream ID the position sits at, which the Redis source resumes
	// from directly instead of scanning the retained window for the key.
	SourceKindRedis = "redis"
	// SourceKindMixed marks a cursor served across a source transition —
	// the incoming cursor came from one source and the response was
	// served from another, located by matching eventKey.
	SourceKindMixed = "mixed"
)

// redisStreamIDSep separates the Redis stream ID from the canonical eventKey
// inside a redis-kind cursor payload. A stream ID ({millis}-{seq}) and an
// eventKey ({replicaID}:{emittedAt}:{nonce}) both contain characters the other
// field may contain, so the two are joined by a character neither can hold and
// the split is unambiguous. spec: §25.5 lines 2666-2675.
const redisStreamIDSep = "|"

// eventCursor is a decoded §25.5 opaque cursor: the source that minted it and
// the position it carries.
//
// EventKey is the canonical cross-source position every cursor carries, so any
// source can resolve the cursor against its own retained window. StreamID is
// set only on a redis-kind cursor and is the Redis stream ID that eventKey sits
// at, which lets the Redis source resume from the position directly rather than
// scanning its retained window for the key. It is meaningless to the other
// sources, which ignore it. spec: §25.5 lines 2666-2675.
type eventCursor struct {
	Kind     string
	StreamID string
	EventKey string
}

// encode renders c back to its opaque wire form, so a page with nothing new to
// serve can echo the caller's position without losing the Redis stream ID that
// makes the next resume a positioned read.
func (c eventCursor) encode() string {
	if c.Kind == SourceKindRedis && c.StreamID != "" {
		return encodeRedisCursor(c.StreamID, c.EventKey)
	}
	return encodeCursor(c.Kind, c.EventKey)
}

// encodeCursor builds the §25.5 opaque cursor: base64(source_kind ||
// ":" || source_position). The source position is the canonical
// eventKey (a ULID-like {replicaID}:{emittedAt}:{nonce}) so the cursor
// round-trips across sources — any source can resolve an eventKey to a
// local position. An empty key encodes the start-of-stream cursor.
// Agents MUST NOT parse the result. spec: §25.5 lines 2666-2675.
func encodeCursor(kind, eventKey string) string {
	if eventKey == "" {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString([]byte(kind + ":" + eventKey))
}

// encodeRedisCursor builds the §25.5 opaque cursor for a page served from the
// Redis ops:events:stream. It carries the Redis stream ID alongside the
// canonical eventKey, so a same-source resume reads from that stream position
// instead of scanning the retained window for the key, while a foreign source
// still resolves the eventKey the same cursor carries. An empty stream ID
// degrades to the key-only form. spec: §25.5 lines 2666-2675 (opaque cursor
// carrying the source kind and the source position).
func encodeRedisCursor(streamID, eventKey string) string {
	if eventKey == "" {
		return ""
	}
	if streamID == "" {
		return encodeCursor(SourceKindRedis, eventKey)
	}
	return base64.RawURLEncoding.EncodeToString(
		[]byte(SourceKindRedis + ":" + streamID + redisStreamIDSep + eventKey),
	)
}

// decodeCursor reverses encodeCursor and encodeRedisCursor. An empty string
// decodes to the start-of-stream cursor (the zero eventCursor). A malformed
// cursor returns an error so the polling handler can reject it with
// INVALID_EVENT_FILTER. spec: §25.5 lines 2666-2675, line 2795.
func decodeCursor(cursor string) (eventCursor, error) {
	if cursor == "" {
		return eventCursor{}, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return eventCursor{}, fmt.Errorf("cursor is not valid base64: %w", err)
	}
	kind, position, found := strings.Cut(string(raw), ":")
	if !found || kind == "" {
		return eventCursor{}, fmt.Errorf("cursor missing source-kind prefix")
	}
	c := eventCursor{Kind: kind, EventKey: position}
	if kind == SourceKindRedis {
		if streamID, eventKey, ok := strings.Cut(position, redisStreamIDSep); ok {
			c.StreamID, c.EventKey = streamID, eventKey
		}
	}
	return c, nil
}
