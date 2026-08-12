// SPDX-License-Identifier: MIT

package tier11_docs

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// The successor-pointer check reads the pointers a reduction leaves behind.
//
// N8 requires a section that gives up content to keep a permanent sentence
// naming the heading that now owns the content and the identifiers that moved.
// The sentence is not redundant with the anchor-redirect map: that map is
// consumed by the citation resolver and emptied once the rewrite completes,
// while the pointer serves a reader arriving by a route no tool rewrites, such
// as a section number quoted in a proposal, a commit message, or a review. A
// reduced section still exists and still discusses adjacent material, so a
// reader who lands on it can take the remaining prose for the whole answer.
//
// Two things are checked, and they are the two halves of the sentence N8
// requires. A section that gave up content carries at least one pointer into
// the heading that took it, and every such pointer names at least one channel
// identifier, so a reader learns which mechanism moved rather than only that
// something did. A pointer that names a heading and no identifier sends a
// reader to a section holding many cards with no way to tell which one applies.
//
// The reducing sections are named rather than derived. Which sections gave up
// content is a fact about a change that has already happened, and no predicate
// over the current tree recovers it: a section that never held the material and
// one that gave it up look identical afterwards. Naming them is what makes the
// check able to fail when a pointer is deleted.

// reducedSections are the sections that gave up content to §28, each with the
// heading that now owns it.
var reducedSections = []struct {
	File    string
	Section string
	Owner   string
}{
	{"spec/04_system-components.md", "4.7", "28.5"},
	{"spec/15_external-api-surface.md", "15.4", "28.5"},
}

// channelIdentifier matches the identifier form the naming law creates.
var channelIdentifier = regexp.MustCompile(`\bCH-[A-Z0-9-]+\b`)

// pointerIntoOwner matches a link into a §28.5 contract card. The tree writes
// the label both ways, as `[§28.5.3]` and as the spelled-out `[Section 28.5.3]`,
// and a matcher that reads only one of them reports a pointer that exists as
// missing.
var pointerIntoOwner = regexp.MustCompile(
	`\[(?:§|Section\s+)?(28\.5(?:\.\d+)?)\]\(([^)]*28_communication-channels\.md[^)]*)\)`,
)

// sectionBody returns the lines of the named section, which runs from its
// heading to the next heading at the same depth or shallower.
func sectionBody(t *testing.T, path, section string) []string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	lines := strings.Split(string(body), "\n")
	open := regexp.MustCompile(`^(#{2,6})\s+` + regexp.QuoteMeta(section) + `\s`)
	var depth int
	start := -1
	for i, l := range lines {
		if m := open.FindStringSubmatch(l); m != nil {
			depth, start = len(m[1]), i
			break
		}
	}
	if start < 0 {
		t.Fatalf("%s: no heading for §%s", path, section)
	}
	for i := start + 1; i < len(lines); i++ {
		if m := regexp.MustCompile(`^(#{1,6})\s`).FindStringSubmatch(lines[i]); m != nil && len(m[1]) <= depth {
			return lines[start:i]
		}
	}
	return lines[start:]
}

// spec: 28.2 (N8, successor pointers)
// diagnosis: a section that gave up content to §28 no longer tells a reader
// where the content went, or tells them the heading without naming the channel
// that moved. A reader landing on the reduced section will take its remaining
// prose for the whole answer.
func TestReducedSectionsPointAtTheHeadingThatOwnsTheirContent(t *testing.T) {
	t.Parallel()
	root := schematest.RepoRoot(t)
	for _, rs := range reducedSections {
		t.Run(rs.File+"§"+rs.Section, func(t *testing.T) {
			lines := sectionBody(t, filepath.Join(root, rs.File), rs.Section)
			var pointers, named int
			var unnamed []string
			for _, l := range lines {
				for _, m := range pointerIntoOwner.FindAllStringSubmatch(l, -1) {
					pointers++
					if channelIdentifier.MatchString(l) {
						named++
						continue
					}
					unnamed = append(unnamed, strings.TrimSpace(l))
					_ = m
				}
			}
			if pointers == 0 {
				t.Errorf("§%s gave up content to §%s and carries no pointer into it; N8 requires a "+
					"permanent sentence naming the heading that now owns the content",
					rs.Section, rs.Owner)
				return
			}
			if named == 0 {
				sort.Strings(unnamed)
				t.Errorf("§%s carries %d pointer(s) into §%s and none names a channel identifier; "+
					"a reader cannot tell which card applies. First: %.140s",
					rs.Section, pointers, rs.Owner, unnamed[0])
				return
			}
			t.Logf("§%s: %d pointer(s) into §%s, %d naming a channel identifier",
				rs.Section, pointers, rs.Owner, named)
		})
	}
}

// spec: 28.2 (N8, successor pointers)
// diagnosis: a pointer into §28.5 resolves to a card heading that does not
// exist, so the successor sentence sends a reader nowhere.
func TestSuccessorPointersNameACardThatExists(t *testing.T) {
	t.Parallel()
	root := schematest.RepoRoot(t)
	cards, err := os.ReadFile(filepath.Join(root, "spec/28_communication-channels.md"))
	if err != nil {
		t.Fatalf("read the channels specification: %v", err)
	}
	declared := map[string]bool{}
	for _, l := range strings.Split(string(cards), "\n") {
		if m := regexp.MustCompile(`^#{2,6}\s+(28\.5(?:\.\d+)?)\s`).FindStringSubmatch(l); m != nil {
			declared[m[1]] = true
		}
	}
	if len(declared) == 0 {
		t.Fatalf("the channels specification declares no §28.5 heading; the check would pass vacuously")
	}
	var checked int
	for _, rs := range reducedSections {
		for _, l := range sectionBody(t, filepath.Join(root, rs.File), rs.Section) {
			for _, m := range pointerIntoOwner.FindAllStringSubmatch(l, -1) {
				checked++
				if !declared[m[1]] {
					t.Errorf("%s §%s points at §%s, which the channels specification does not declare",
						rs.File, rs.Section, m[1])
				}
			}
		}
	}
	t.Logf("%d successor pointer(s) checked against %d declared card heading(s)", checked, len(declared))
}
