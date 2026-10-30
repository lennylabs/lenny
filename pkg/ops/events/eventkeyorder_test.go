// SPDX-License-Identifier: MIT

package events

import "testing"

// spec: 25.5 (eventKey format {replicaID}:{emittedAt}:{nonce}; cross-source
// cursor translation orders by eventKey) — the ordering the cross-source
// continuation point rests on: by emission instant first, then the per-replica
// nonce, across replica ids, with a deterministic fallback for a key that does
// not carry the canonical numeric fields.
func TestEventKeyLess_OrdersByEmissionThenNonce_spec_25_5(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
		why  string
	}{
		{"ops:1000:1", "ops:1000:2", true, "same instant, lower nonce first"},
		{"ops:1000:2", "ops:1000:1", false, "same instant, higher nonce later"},
		{"ops:1000:9", "ops:1001:1", true, "an earlier instant orders first regardless of nonce"},
		{"gw-a:1000:1", "ops:1000:1", true, "same instant and nonce on two replicas ties on the full key"},
		{"ops:1000:1", "ops:1000:1", false, "a key does not order before itself"},
		{"ops-2:5:1", "ops:5:2", true, "a replica id containing no colon still parses from the right"},
		{"aaa-legacy-key", "ops:1000:1", true, "an unparseable key falls back to a byte comparison"},
		{"zzz-legacy-key", "ops:1000:1", false, "the byte-comparison fallback stays a total order"},
	}
	for _, c := range cases {
		if got := eventKeyLess(c.a, c.b); got != c.want {
			t.Errorf("eventKeyLess(%q, %q) = %v, want %v (%s)", c.a, c.b, got, c.want, c.why)
		}
	}
}

// spec: 25.5 (eventKey format) — the parse takes the two numeric fields from
// the right so a replica id carrying its own colons still yields an order, and
// reports failure rather than a zero order for a key that is not canonical.
func TestParseEventKey_TakesNumericFieldsFromTheRight_spec_25_5(t *testing.T) {
	at, nonce, ok := parseEventKey("ops:pod:3:1700000000:42")
	if !ok || at != 1700000000 || nonce != 42 {
		t.Fatalf("parseEventKey = (%d, %d, %v), want (1700000000, 42, true)", at, nonce, ok)
	}
	for _, bad := range []string{"", "ops", "ops:1000", "ops:abc:1", "ops:1000:xyz"} {
		if _, _, ok := parseEventKey(bad); ok {
			t.Errorf("parseEventKey(%q) reported success; a non-canonical key must fall back to the byte order", bad)
		}
	}
}
