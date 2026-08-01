// SPDX-License-Identifier: MIT

package carrier

// Flow reads the flow-control rules.
//
// spec: §15.9.9 (a section no specification file declares any more, and
// no entry of the sense register records this occurrence)
func Flow() []byte { return nil }

// Tiers names the tier this behavior is exercised at, which a testing
// document numbers §14.13 in its own numbering while no specification
// file declares that section.
func Tiers() string { return "the tier the testing document numbers" }

// Audit writes the audit record §25 states, a file-level number a
// specification file carries in its level-one title alone, so no
// specification section is declared under it.
func Audit() []byte { return nil }
