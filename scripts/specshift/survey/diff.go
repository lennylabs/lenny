// SPDX-License-Identifier: MIT

package survey

import (
	"sort"

	"github.com/lennylabs/lenny/scripts/specshift/ref"
)

// Change kinds of an item between two snapshots.
const (
	Added    = "added"
	Modified = "modified"
	Retired  = "retired"
)

// ItemChange is one item that differs between two snapshots.
type ItemChange struct {
	// Change is Added, Modified, or Retired.
	Change string
	// Old is the item before, nil when it was added; New the item after,
	// nil when it was retired.
	Old, New *Item
	// Record is the retirement record the later snapshot holds for a
	// retired item, and HasRecord whether it holds one.
	Record    ref.Retired
	HasRecord bool
}

// Item returns the item the change concerns: the new one when it exists.
func (c ItemChange) Item() *Item {
	if c.New != nil {
		return c.New
	}
	return c.Old
}

// Moved reports whether a modified item changed page.
func (c ItemChange) Moved() bool {
	return c.Old != nil && c.New != nil && c.Old.File != c.New.File
}

// compare returns the items of one kind that the later snapshot adds,
// modifies, or retires relative to the earlier one, in identifier order.
// An item is modified when its text or its page differs. An item is
// retired when the later snapshot no longer holds it, whether or not the
// later snapshot records its retirement.
func compare(before, after *Snapshot, kind string) []ItemChange {
	var out []ItemChange
	for _, it := range after.sortedItems(kind) {
		old, ok := before.Items[it.key()]
		switch {
		case !ok:
			out = append(out, ItemChange{Change: Added, New: it})
		case old.Text != it.Text || old.File != it.File:
			out = append(out, ItemChange{Change: Modified, Old: old, New: it})
		}
	}
	for _, it := range before.sortedItems(kind) {
		if _, ok := after.Items[it.key()]; ok {
			continue
		}
		c := ItemChange{Change: Retired, Old: it}
		c.Record, c.HasRecord = after.retirement(it.ID)
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Change != out[j].Change {
			return changeOrder[out[i].Change] < changeOrder[out[j].Change]
		}
		return naturalLess(out[i].Item().ID, out[j].Item().ID)
	})
	return out
}

// changeOrder orders the change kinds in a report.
var changeOrder = map[string]int{Added: 0, Modified: 1, Retired: 2}
