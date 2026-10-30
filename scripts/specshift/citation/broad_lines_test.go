// SPDX-License-Identifier: MIT

package citation_test

import (
	"os"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/scripts/specshift/citation"
)

// The broad predicate carries the wrap of every occurrence it read, so a
// caller that records an occurrence in a tracked file writes it the way
// it was written rather than folding it onto one line. A one-line record
// of a wrapped citation is a citation the form reads, under the path of
// the file the record was written into.
//
// These cases carry no spec annotation: the citation grammar is
// migration tooling for the repository's own records rather than a
// platform behavior.
//
// Every citation a case reads sits in testdata, which is outside the
// read domain of the resolver, the ratchet, and the residual scan. A
// citation written in this file would be an occurrence of the class
// those gates range over, reported under this file's own path with no
// route out but the deletion of the case.

// broadFixture reads one fixture carrier.
func broadFixture(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read the fixture %s: %v", name, err)
	}
	return string(body)
}

func TestBroadCarriesTheWrapOfAnOccurrenceItJoined(t *testing.T) {
	t.Parallel()
	// The wrap falls inside the quoted qualifier of the fixture, which is
	// the position that makes the folded text a citation the form reads
	// while neither line on its own is one.
	found := citation.FindBroad(broadFixture(t, "broad-wrapped.txt"))
	if len(found) != 1 {
		t.Fatalf("the predicate read %d occurrence(s) of a wrapped citation, want one", len(found))
	}
	b := found[0]
	if len(b.Lines) != 2 {
		t.Fatalf("the occurrence %q carries %d line(s), want the two the wrap left", b.Text, len(b.Lines))
	}
	if joined := strings.Join(b.Lines, " "); joined != b.Text {
		t.Errorf("the lines join to %q, and the text is %q", joined, b.Text)
	}
	// Each line on its own is unreadable by the citation form, which is
	// what makes a record that preserves the wrap invisible to the
	// ratchet and the resolver.
	for _, line := range b.Lines {
		if got := citation.Find(line); len(got) > 0 {
			t.Errorf("the form read %q out of the wrapped line %q", got[0].Raw, line)
		}
	}
	if got := citation.Find(b.Text); len(got) == 0 {
		t.Errorf("the folded text %q is read by no citation form, so the case pins nothing", b.Text)
	}
}

func TestBroadCarriesOneLineForAnUnwrappedOccurrence(t *testing.T) {
	t.Parallel()
	found := citation.FindBroad(broadFixture(t, "broad-plain.txt"))
	if len(found) != 1 {
		t.Fatalf("the predicate read %d occurrence(s), want one", len(found))
	}
	if len(found[0].Lines) != 1 || found[0].Lines[0] != found[0].Text {
		t.Errorf("an unwrapped occurrence carries the lines %q, want the one line %q",
			found[0].Lines, found[0].Text)
	}
}
