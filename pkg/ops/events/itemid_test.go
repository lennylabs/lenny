// SPDX-License-Identifier: MIT

package events

import (
	"testing"

	gwevents "github.com/lennylabs/lenny/pkg/events"
)

// spec: 25.5 (the polling envelope is identical whichever source serves it) —
// items[].id is derived from the canonical eventKey alone, so it is stable for
// one event across every read source, distinct for two distinct events on one
// page, and never zero for a record that carries an eventKey. A record with no
// eventKey has no identity to derive and reports zero.
func TestReadItemID_DerivedFromEventKeyOnly_spec_25_5(t *testing.T) {
	if got := readItemID(""); got != 0 {
		t.Errorf("readItemID(\"\") = %d, want 0", got)
	}
	const key = "gw-a:1700000000000:1"
	first, second := readItemID(key), readItemID(key)
	if first != second {
		t.Errorf("readItemID(%q) is not deterministic: %d then %d", key, first, second)
	}
	if first == 0 {
		t.Errorf("readItemID(%q) = 0, which is reserved for a record with no eventKey", key)
	}
	if other := readItemID("gw-b:1700000000001:1"); other == first {
		t.Errorf("two distinct eventKeys derive the same items[].id %d", first)
	}
}

// spec: 25.5 (the polling envelope is identical whichever source serves it) —
// stampItemIDs replaces whatever per-source position an item arrived carrying
// (a local ring sequence, a remote replica's ring sequence, or the Redis
// stream's zero) with the eventKey-derived identity, and leaves the caller's
// slice untouched so the ring buffer's own records are not rewritten.
func TestStampItemIDs_OverwritesPerSourcePositionWithoutMutating_spec_25_5(t *testing.T) {
	in := []gwevents.BufferedEvent{
		{ID: 1, Event: evt("gw-a:1700000000000:1", "dev.lenny.alert_fired")},
		{ID: 1, Event: evt("gw-b:1700000000001:1", "dev.lenny.alert_fired")},
		{ID: 0, Event: evt("ops-1:1700000000002:1", "dev.lenny.alert_fired")},
	}
	out := stampItemIDs(in)
	if len(out) != len(in) {
		t.Fatalf("stampItemIDs returned %d items, want %d", len(out), len(in))
	}
	seen := make(map[uint64]string, len(out))
	for i, item := range out {
		if item.ID != readItemID(item.Event.ID) {
			t.Errorf("item %d id = %d, want the eventKey-derived %d", i, item.ID, readItemID(item.Event.ID))
		}
		if prev, dup := seen[item.ID]; dup {
			t.Errorf("items %s and %s share items[].id %d", prev, item.Event.ID, item.ID)
		}
		seen[item.ID] = item.Event.ID
	}
	if in[0].ID != 1 || in[2].ID != 0 {
		t.Errorf("stampItemIDs mutated its input: %+v", []uint64{in[0].ID, in[2].ID})
	}
}
