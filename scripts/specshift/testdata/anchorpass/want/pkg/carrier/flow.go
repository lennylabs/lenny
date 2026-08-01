// SPDX-License-Identifier: MIT

package carrier

// Flow reads the flow-control rules.
//
// spec: §15.9.9 (a section no specification file declares and no anchor
// the map retires spells, so the citation stands as it is written)
func Flow() []byte { return nil }

// Tiers names the tier this behavior is exercised at, which the testing
// document numbers §14.13 in its own numbering. No specification file
// declares that section and the migration retires no anchor of it.
func Tiers() string { return "the tier the testing document numbers" }

// Audit writes the audit record §25 states, which a specification file
// declares in its level-one title alone.
func Audit() []byte { return nil }

// Frame writes the adapter framing onto the connection.
//
// spec: §28.5.1 (the framing of the connection, whose anchor the map
// retires, and the one occurrence the sense register answers for)
func Frame() []byte { return nil }
