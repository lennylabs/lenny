// SPDX-License-Identifier: MIT

// Package skipreason holds the reason categories a skip call may open
// with, which TESTING.md §17.9 governs. The values live in their own
// importable package because `cmd/lenny-test` is a main package: the
// tier-0 classifier that holds every skip call site in the tree to
// these categories cannot import the scaffold-marker reader that also
// reads them, so one of the two readers would otherwise restate the
// list and drift from the other.
//
// Both readers call OpenedWithCategory, so widening or renaming a
// category here changes what the scaffold-marker reader accepts and
// what the classifier reports in the same edit.
//
// Nothing in this package carries a spec annotation. The harness
// reduces such an annotation to a bare section number and would credit
// the resulting failure to spec/17_*, which defines no skip convention;
// the convention belongs to TESTING.md §17.9.
package skipreason

import "strings"

// Categories are the prefixes a skip reason may open with. A reason
// that opens with none of them states nothing a reader can act on: the
// test reports neither pass nor fail and names no route back.
var Categories = []string{
	"not implemented:",
	"blocked:",
	"phase-gated:",
	"not-yet-applicable:",
	"not yet applicable:",
	"flaky-time:",
	"flaky-network:",
	"flaky-ordering:",
	"quarantined:",
}

// OpenedWithCategory reports whether a skip reason opens with one of
// the categories.
func OpenedWithCategory(reason string) bool {
	for _, category := range Categories {
		if strings.HasPrefix(reason, category) {
			return true
		}
	}
	return false
}
