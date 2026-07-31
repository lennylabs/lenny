// SPDX-License-Identifier: MIT

package line

import (
	"fmt"
	"regexp"

	"github.com/lennylabs/lenny/scripts/specshift/citation"
)

// anchorExpr matches an anchor citation, which is a section reference
// carrying no line number.
var anchorExpr = regexp.MustCompile(`§\d+(?:\.\d+)*`)

// Account reports whether a rewrite retired citations without emitting
// the anchors that replace them.
//
// The identity it checks is that each conversion leaves exactly one
// anchor standing where the citation stood, and that each strip leaves
// none. It is stated over the text rather than over the plan that
// produced it, so a conversion that emitted no anchor, or two, is caught
// by the same check that catches a deletion. Deleting a pointer is
// indistinguishable from retiring it to both the ratchet and the
// resolver, which read a file's count alone, so the accounting is what
// stops a file reaching zero by having its pointers dropped.
//
// Anchors written inside a citation, whether in its head or in a member
// gloss, are counted out of both sides, so a gloss naming a section does
// not read as an anchor the conversion was supposed to keep.
func Account(before, after string, stripped int) error {
	citedBefore := citation.Find(before)
	citedAfter := citation.Find(after)
	removed := len(citedBefore) - len(citedAfter)
	if removed < stripped {
		return fmt.Errorf("the rewrite reports %d stripped citation(s) and removed %d", stripped, removed)
	}
	converted := removed - stripped
	want := freeAnchors(before, citedBefore) + converted
	got := freeAnchors(after, citedAfter)
	if got == want {
		return nil
	}
	return fmt.Errorf("the rewrite retired %d citation(s), %d of them stripped, and left %d anchor(s) where %d are required",
		removed, stripped, got, want)
}

// freeAnchors counts the anchors standing outside every citation of the
// text.
func freeAnchors(text string, cited []citation.Citation) int {
	free := len(anchorExpr.FindAllString(text, -1))
	for _, c := range cited {
		free -= len(anchorExpr.FindAllString(c.Raw, -1))
	}
	return free
}
