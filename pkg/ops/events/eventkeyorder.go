// SPDX-License-Identifier: MIT

package events

import (
	"strconv"
	"strings"
)

// eventKeyLess reports whether eventKey a orders strictly before eventKey b.
//
// The canonical eventKey is {replicaID}:{emittedAt}:{nonce}, so the emission
// timestamp and the per-replica nonce give a total order that holds across
// replicas and across sources: the same event carries the same key whether it
// is served from the Redis stream, a gateway replica's ring, or the local ring.
// Cross-source cursor translation needs that order rather than equality alone,
// because the event a caller last consumed is routinely absent from the source
// it switches to (a gateway event emitted while Redis was down was never
// XADDed; a lenny-ops event never lands in any gateway ring). The continuation
// point is then the first event ordering at or after the carried key, and only
// a key ordering before the whole retained window is a gap.
//
// A key that does not parse falls back to a byte comparison so an unrecognised
// identifier still yields a deterministic order. spec: §25.5 (cross-source
// cursor translation, continuation at the first greater-or-equal eventKey).
func eventKeyLess(a, b string) bool {
	if a == b {
		return false
	}
	at, an, aok := parseEventKey(a)
	bt, bn, bok := parseEventKey(b)
	if !aok || !bok {
		return a < b
	}
	if at != bt {
		return at < bt
	}
	if an != bn {
		return an < bn
	}
	// Same instant and same nonce on two replicas: order by the full key so
	// the comparison stays a total order.
	return a < b
}

// parseEventKey splits the canonical {replicaID}:{emittedAt}:{nonce} eventKey
// into its ordering components. The replica id may itself contain colons, so
// the two numeric fields are taken from the right. ok is false when either
// numeric field is absent or unparseable. spec: §25.5 (eventKey format).
func parseEventKey(key string) (emittedAt, nonce uint64, ok bool) {
	head, nonceField, found := lastCut(key)
	if !found {
		return 0, 0, false
	}
	_, timeField, found := lastCut(head)
	if !found {
		return 0, 0, false
	}
	emittedAt, err := strconv.ParseUint(timeField, 10, 64)
	if err != nil {
		return 0, 0, false
	}
	nonce, err = strconv.ParseUint(nonceField, 10, 64)
	if err != nil {
		return 0, 0, false
	}
	return emittedAt, nonce, true
}

// lastCut splits s around the last colon.
func lastCut(s string) (before, after string, found bool) {
	i := strings.LastIndex(s, ":")
	if i < 0 {
		return s, "", false
	}
	return s[:i], s[i+1:], true
}
