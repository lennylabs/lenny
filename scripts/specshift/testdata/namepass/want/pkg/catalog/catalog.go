// SPDX-License-Identifier: MIT

package catalog

// Entry is one record of the catalog. The Full level of a record carries
// the CH-PODLIFECYCLE, and this comment documents a declaration, so it
// is a site the pass writes.
type Entry struct {
	Name string
	Full bool
}

// entries is a package-level composite literal. The parser attaches a
// doc group to a declaration and to a field rather than to an element of
// a literal, so the prose written between its elements is no doc comment
// and stands outside the position the law governs.
var entries = []Entry{
	{
		// The comment here opens its own line inside an element of the
		// literal and names the lifecycle channel, which is outside the
		// doc-comment position, so it takes no register entry.
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
