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
	// SourceKindRedis marks a cursor produced by the Redis stream. Its
	// encoded position is a Redis stream ID, so a redis cursor sent back to
	// the Redis source resumes directly by stream ID; a redis cursor served
	// from another source is translated by scanning for the matching
	// eventKey (reported as mixed).
	SourceKindRedis = "redis"
	// SourceKindMixed marks a cursor served across a source transition —
	// the incoming cursor came from one source and the response was
	// served from another, located by matching eventKey.
	SourceKindMixed = "mixed"
)

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

// decodeCursor reverses encodeCursor. An empty string decodes to the
// start-of-stream cursor (empty kind and key). A malformed cursor
// returns an error so the polling handler can reject it with
// INVALID_EVENT_FILTER. spec: §25.5 lines 2666-2675, line 2795.
func decodeCursor(cursor string) (kind, eventKey string, err error) {
	if cursor == "" {
		return "", "", nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return "", "", fmt.Errorf("cursor is not valid base64: %w", err)
	}
	parts := strings.SplitN(string(raw), ":", 2)
	if len(parts) != 2 || parts[0] == "" {
		return "", "", fmt.Errorf("cursor missing source-kind prefix")
	}
	return parts[0], parts[1], nil
}
