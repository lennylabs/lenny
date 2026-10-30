// SPDX-License-Identifier: MIT

package citation_test

import (
	"reflect"
	"testing"

	"github.com/lennylabs/lenny/scripts/specshift/citation"
)

// The bare spelling writes a dotted section number with no section sign
// directly ahead of the line-number keyword. Spec annotations in that
// spelling stood in Go test files while the form read only the
// section-sign and path spellings, so the resolver never read them and
// the ratchet never counted them (T-17.4.34). Each fixture sits in
// testdata for the reason head_spellings_test.go states.
//
// These cases carry no spec annotation: the citation grammar is
// migration tooling for the repository's own records rather than a
// platform behavior.

// bareRead is the part of a citation each case pins.
type bareRead struct {
	section string
	members []int
}

func readBare(t *testing.T, fixture string) []bareRead {
	t.Helper()
	var out []bareRead
	for _, c := range citation.Find(broadFixture(t, fixture)) {
		r := bareRead{section: c.Section}
		for _, m := range c.Members {
			r.members = append(r.members, m.Start)
		}
		out = append(out, r)
	}
	return out
}

func TestFindReadsTheBareSectionSpelling(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		fixture string
		want    []bareRead
	}{
		// One annotation with a gloss.
		{"head-bare.txt", []bareRead{{"25.8", []int{3508}}}},
		// A further section written behind the member is a section
		// rather than a second member, so the citation carries one
		// member and the section behind it is left to the carrier.
		{"head-bare-then-section.txt", []bareRead{{"11.3", []int{199}}}},
		// Chained bare citations, one wrapped across a comment line,
		// are read as separate citations, and no member list absorbs
		// the head of the citation behind it.
		{"head-bare-chained.txt", []bareRead{
			{"25.8", []int{3508}}, {"25.8", []int{3511}}, {"25.10", []int{3788}},
		}},
		// A section-sign reference written ahead of a bare citation does
		// not take the bare citation's line number as its own.
		{"head-bare-behind-sigil.txt", []bareRead{{"12.4", []int{197}}}},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			t.Parallel()
			got := readBare(t, tc.fixture)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Find read %+v out of %s, want %+v", got, tc.fixture, tc.want)
			}
			// The class predicate ranges over every occurrence the form
			// reads, so the residual scan subtracts each one rather than
			// reporting it.
			content := broadFixture(t, tc.fixture)
			for _, c := range citation.Find(content) {
				end := c.Offset + len(c.Raw)
				overlapped := false
				for _, b := range citation.FindBroad(content) {
					if b.Offset < end && c.Offset < b.End {
						overlapped = true
					}
				}
				if !overlapped {
					t.Errorf("the class predicate selected no occurrence overlapping %q", c.Text)
				}
			}
		})
	}
}

// TestFindLeavesBareProseUnread pins the bounds of the bare spelling: a
// single integer ahead of the keyword, a dotted number with no keyword
// behind it, a dotted number cut out of a word, and a dotted number
// whose keyword carries no line number are prose rather than citations.
// The form and the class predicate both leave them unread, so none
// enters a file's count or the residual.
func TestFindLeavesBareProseUnread(t *testing.T) {
	t.Parallel()
	content := broadFixture(t, "held-out-bare.txt")
	if read := citation.Find(content); len(read) > 0 {
		t.Errorf("the form read %q out of prose", read[0].Text)
	}
	if broad := citation.FindBroad(content); len(broad) > 0 {
		t.Errorf("the class predicate selected %q out of prose", broad[0].Text)
	}
}
