// SPDX-License-Identifier: MIT

package citation_test

import (
	"testing"

	"github.com/lennylabs/lenny/scripts/specshift/citation"
)

// The form reads the sub-element name a citation writes between its
// reference and the line-number keyword, whatever the carrier wrote
// there. Each spelling below was written in the tree and left unread
// while the name was an enumeration of token patterns: the pass could
// not rewrite it, the resolver could not read it, and the ratchet did
// not count it, so a file carrying one reached a zero count with the
// stale pointer standing. A citation of the class the class's own
// driver cannot reach is the silent absorption the residual gate
// exists to prevent, so each spelling is pinned here.
//
// Every fixture sits in testdata, which is outside the read domain of
// the resolver, the ratchet, and the residual scan. A citation written
// in this file would be an occurrence of the class those gates range
// over, reported under this file's own path with no route out but the
// deletion of the case.
//
// These cases carry no spec annotation: the citation grammar is
// migration tooling for the repository's own records rather than a
// platform behavior.

func TestFindReadsEveryNameWrittenBetweenTheReferenceAndTheKeyword(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		fixture string
		start   int
		end     int
	}{
		{"head-separator-comma.txt", 90, 90},
		{"head-separator-slash.txt", 348, 348},
		{"head-colon-spaced.txt", 723, 723},
		{"head-paren-before-name.txt", 823, 823},
		{"head-long-name.txt", 2405, 2405},
		{"head-name-with-path.txt", 124, 124},
		{"head-dotted-field.txt", 916, 916},
		{"head-link-target.txt", 68, 68},
		{"head-emdash-name.txt", 137, 137},
		{"head-wrapped-quoted-name.txt", 40, 40},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			t.Parallel()
			content := broadFixture(t, tc.fixture)
			read := citation.Find(content)
			if len(read) == 0 {
				t.Fatalf("the form read no citation out of %s, so the pass cannot rewrite it, "+
					"the resolver cannot read it, and the ratchet does not count it", tc.fixture)
			}
			first := read[0].Members[0]
			if first.Start != tc.start || first.End != tc.end {
				t.Errorf("the citation %q carries the member %d-%d, want %d-%d",
					read[0].Text, first.Start, first.End, tc.start, tc.end)
			}
			// The class predicate ranges over every occurrence the form
			// reads, so a spelling the form now reads is a spelling the
			// residual scan subtracts rather than one it reports.
			broad := citation.FindBroad(content)
			if len(broad) == 0 {
				t.Fatalf("the class predicate selected no occurrence of %s", tc.fixture)
			}
			if broad[0].Offset >= read[0].Offset+len(read[0].Raw) || read[0].Offset >= broad[0].End {
				t.Errorf("the occurrence at [%d,%d) does not overlap the citation at [%d,%d)",
					broad[0].Offset, broad[0].End, read[0].Offset, read[0].Offset+len(read[0].Raw))
			}
		})
	}
}

// TestFindLeavesTheSpellingsTheFormHoldsOutUnread pins the other half of
// the relation: the run stops at a section sign, at a percent sign, and
// at a backslash, so a citation written behind another reference, an
// assertion message carrying a format verb, and a regular-expression
// literal written over the form itself are not read as citations. Each
// would otherwise enter a file's count with no route to zero but the
// rewriting of prose.
func TestFindLeavesTheSpellingsTheFormHoldsOutUnread(t *testing.T) {
	t.Parallel()
	for _, fixture := range []string{
		"head-held-out-format-verb.txt",
		"head-held-out-escape.txt",
		"head-held-out-word-tail.txt",
	} {
		t.Run(fixture, func(t *testing.T) {
			t.Parallel()
			content := broadFixture(t, fixture)
			if read := citation.Find(content); len(read) > 0 {
				t.Errorf("the form read %q out of %s", read[0].Text, fixture)
			}
			// The case pins nothing unless the class predicate selects
			// the occurrence, because a spelling neither the form nor
			// the predicate reads is outside the class rather than
			// held out of the form.
			if broad := citation.FindBroad(content); len(broad) == 0 {
				t.Fatalf("the class predicate selected no occurrence of %s", fixture)
			}
		})
	}
}
