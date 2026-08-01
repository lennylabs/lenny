// SPDX-License-Identifier: MIT

package carrier

// Flow reads the flow-control rules.
//
// spec: §15.9.9 (a section the anchor-move map retires no anchor for, so
// this citation is outside the pass's population)
func Flow() []byte { return nil }

// Tiers names the tier this behavior is exercised at, which a testing
// document numbers §14.13 in its own numbering while no specification
// file declares that section.
func Tiers() string { return "the tier the testing document numbers" }

// Audit writes the audit record §25 states, a file-level number a
// specification file carries in its level-one title, which this
// reduction leaves where it stands.
func Audit() []byte { return nil }
