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
// Anchors written inside the run a rewrite replaces are counted out of
// both sides, so the citation's own reference does not read as an anchor
// the conversion was supposed to keep. That run is the citation's
// reference-and-members run rather than the whole citation: an anchor a
// delimited phrase behind the last member names stands outside the run,
// is left standing by the conversion and by the strip alike, and so is a
// free anchor on both sides of the identity. Counting it out of the
// before side alone would report every such site as a rewrite that
// emitted an anchor too many, and the run would abort on text neither
// rewrite touched.
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

// freeAnchors counts the anchors standing outside the reference-and-members
// run of every citation of the text, which is the run a rewrite replaces.
func freeAnchors(text string, cited []citation.Citation) int {
	free := len(anchorExpr.FindAllString(text, -1))
	for _, c := range cited {
		run := c.Raw[:pointerEnd(c)-c.Offset]
		free -= len(anchorExpr.FindAllString(run, -1))
	}
	return free
}
