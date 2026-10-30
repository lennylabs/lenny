// SPDX-License-Identifier: MIT

package name

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/scripts/specshift/citation"
)

// These cases hold the broad reserved-phrase predicate to the two
// properties the residual scan over the class rests on: it selects every
// occurrence the enumerated matcher reads, so the difference between the
// two populations is the residual rather than a second enumeration, and
// it selects an occurrence the enumerated matcher refuses, so the tail
// the enumeration misses is triaged rather than passing unread.
//
// Every specimen a case reads sits under testdata, which is outside the
// read domain of the naming lint and of the residual scan. A specimen
// written in this source would be a site of the class the pass removes,
// standing under this file's own path.

// broadCarrier is the target each case reads its fixture as. The
// in-file position rule the pass holds a Go carrier to is selected by
// the carrier's extension, so the fixture is read as a Go file.
const broadCarrier = "pkg/carrier/carrier.go"

// broadFixture reads one fixture carrier.
func broadFixture(t *testing.T, fixture string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", fixture))
	if err != nil {
		t.Fatalf("read the fixture %s: %v", fixture, err)
	}
	return string(body)
}

// broadExpected reads the occurrence a case expects, which is held in a
// fixture of its own for the reason the file comment states.
func broadExpected(t *testing.T, fixture string) string {
	t.Helper()
	return strings.TrimSuffix(broadFixture(t, fixture), "\n")
}

// broadOccurrenceKey renders one occurrence's position and text, which
// is what a case matches the two populations by.
func broadOccurrenceKey(line int, text string) string { return fmt.Sprintf("%d:%s", line, text) }

// broadSites reads the sites the pass writes in one fixture.
func broadSites(t *testing.T, content string) []Site {
	t.Helper()
	sites, err := Sites(broadCarrier, content)
	if err != nil {
		t.Fatalf("read the sites of the fixture carrier: %v", err)
	}
	return sites
}

// TestBroadPhrasesContainEverySiteTheMatcherReads pins the containment
// the residual subtraction rests on. The scan reports the occurrences
// the predicate selects less the sites the pass writes, so an occurrence
// the pass writes and the predicate does not select would be subtracted
// from a population it never joined, and the scan would report one
// member fewer than the tree carries for as long as the two disagreed.
//
// The fixture also carries an occurrence in a comment inside a function
// body, which the position rule places outside the pass. The predicate
// applies no position rule, so it selects strictly more than the pass
// writes, and the difference is what the register triages.
//
// spec: §28.1 (N3, the naming law: the prohibition covers the doc
// comment of a tracked Go file, in the space-separated spelling and in
// the hyphenated compound spelling alike)
func TestBroadPhrasesContainEverySiteTheMatcherReads(t *testing.T) {
	t.Parallel()
	content := broadFixture(t, "broad-carrier.go.txt")
	sites := broadSites(t, content)
	if len(sites) < 2 {
		t.Fatalf("the fixture carries %d site(s), and the case pins containment over both spellings", len(sites))
	}
	broad := FindBroadPhrases(content)
	selected := map[string]bool{}
	for _, b := range broad {
		selected[broadOccurrenceKey(b.Line, b.Text)] = true
	}
	for _, s := range sites {
		if !selected[broadOccurrenceKey(s.Line, s.Text)] {
			t.Errorf("line %d carries a site the pass writes that the predicate does not select, "+
				"so the residual scan would subtract it from a population it never joined", s.Line)
		}
	}
	if len(broad) <= len(sites) {
		t.Errorf("the predicate selected %d occurrence(s) against %d site(s); the fixture carries one in a "+
			"function-body comment, which the pass's position rule leaves outside it and the predicate reads",
			len(broad), len(sites))
	}
}

// TestBroadPhrasesSelectAWrapTheEnumeratedSeparatorRefuses pins the
// over-breadth the class exists for. The enumerated matcher admits one
// consumed continuation between the two words, and a phrase wrapped
// across a blank comment line stands with two, so the matcher reads
// neither the phrase nor either half of it. The predicate admits a
// continuation run of any length, so the occurrence is selected and
// triaged rather than standing in a doc comment no lint reports.
//
// spec: §28.1 (N3, the naming law: a matcher joins two consecutive
// comment lines before it applies either spelling, so a phrase wrapped
// across a comment boundary is one site)
func TestBroadPhrasesSelectAWrapTheEnumeratedSeparatorRefuses(t *testing.T) {
	t.Parallel()
	content := broadFixture(t, "broad-blank-wrap.go.txt")
	broad := FindBroadPhrases(content)
	if len(broad) != 1 {
		t.Fatalf("the predicate selected %d occurrence(s) of the wrapped phrase, want one", len(broad))
	}
	if want := broadExpected(t, "broad-blank-wrap-member.txt"); broad[0].Text != want {
		t.Errorf("the occurrence reads %q, and the register records it as %q", broad[0].Text, want)
	}
	if lines := FindReservedPhrases(content); len(lines) != 0 {
		t.Errorf("the enumerated matcher read the occurrence on line %d, so the case pins no over-breadth", lines[0])
	}
	if sites := broadSites(t, content); len(sites) != 0 {
		t.Errorf("the pass writes %d site(s) here, so the occurrence is not the unreachable one the case reads",
			len(sites))
	}
}

// TestBroadPhrasesSelectNoSingleTokenSpelling pins the boundary between
// the two classes. A word byte is outside both branches of the
// predicate's separator, so the two words written as one token are not a
// bare noun phrase here. That spelling is a retired channel identifier,
// the naming table carries it, and the identifier class owns it: a
// predicate that selected it would report every occurrence of it as an
// unclassified member of a class whose pass does not rewrite it.
//
// spec: §28.3 (naming table: the single-token spelling is a retired
// identifier spelling)
func TestBroadPhrasesSelectNoSingleTokenSpelling(t *testing.T) {
	t.Parallel()
	broad := FindBroadPhrases(broadFixture(t, "broad-token.go.txt"))
	if len(broad) != 0 {
		t.Errorf("the predicate selected %q out of a single token, which the identifier class owns", broad[0].Text)
	}
}

// TestBroadPhraseTextRendersAContinuationJoinAsASpace pins the spelling
// an occurrence is recorded under. The scan reads the joined text, so an
// occurrence wrapped across two comment lines carries the join byte
// inside it. A register entry holding that byte would be keyed on a
// spelling nothing else in the tree writes, and the entry would never
// match the occurrence again.
//
// The rendered text and the line are the ones the pass reports for the
// same occurrence, which is what lets the scan subtract one population
// from the other by position and text.
//
// spec: §28.1 (N3, the naming law: a phrase wrapped across a comment
// boundary is one site)
func TestBroadPhraseTextRendersAContinuationJoinAsASpace(t *testing.T) {
	t.Parallel()
	content := broadFixture(t, "broad-wrapped.go.txt")
	broad := FindBroadPhrases(content)
	if len(broad) != 1 {
		t.Fatalf("the predicate selected %d occurrence(s) of the wrapped phrase, want one", len(broad))
	}
	b := broad[0]
	if strings.ContainsRune(b.Text, citation.JoinByte) {
		t.Errorf("the occurrence carries the join byte, so it is recorded under a spelling nothing else writes")
	}
	if want := broadExpected(t, "broad-wrapped-member.txt"); b.Text != want {
		t.Errorf("the occurrence reads %q, want %q", b.Text, want)
	}
	if b.Offset >= b.End || b.End > len(content) {
		t.Fatalf("the occurrence spans [%d, %d) of a %d-byte source, so it maps back to no span of it",
			b.Offset, b.End, len(content))
	}
	if at := citation.LineOf(content, b.Offset); at != b.Line {
		t.Errorf("the occurrence opens on source line %d and reports line %d", at, b.Line)
	}
	sites := broadSites(t, content)
	if len(sites) != 1 {
		t.Fatalf("the pass writes %d site(s) for the same occurrence, want one", len(sites))
	}
	if sites[0].Text != b.Text || sites[0].Line != b.Line {
		t.Errorf("the pass reports the occurrence as %q on line %d and the predicate as %q on line %d, "+
			"so the scan subtracts neither from the other",
			sites[0].Text, sites[0].Line, b.Text, b.Line)
	}
}
