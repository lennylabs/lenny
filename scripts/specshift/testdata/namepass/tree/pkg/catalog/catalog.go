// SPDX-License-Identifier: MIT

package catalog

// Entry is one record of the catalog.
type Entry struct {
	Name string
	Full bool
}

// entries is a package-level composite literal. The parser attaches a
// doc group to a declaration and to a field rather than to an element of
// a literal, so the prose written between its elements is attached to
// nothing and a rule resting on attachment would leave it with no
// writer.
var entries = []Entry{
	{
		// The Full level carries the lifecycle channel, and this comment
		// opens its own line inside an element of the literal, so it is a
		// site the pass writes.
		Name: "full",
		Full: true,
	},
	{
		Name: "minimal", // The trailing comment here names the lifecycle channel, which is outside the position the law governs.
		Full: false,
	},
}

// Names reports how many records the catalog holds.
func Names() int {
	return len(entries)
}
