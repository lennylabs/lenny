// SPDX-License-Identifier: MIT

package tier11_docs

import (
	"fmt"
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

// reducedSection is one section that gave up content, named with the heading
// that now owns it.
type reducedSection struct {
	File    string
	Section string
	Owner   string
}

// reducedSections are the sections that gave up content to §28, each with the
// heading that now owns it. The list is the check's domain: a section that gave
// up no content is absent from it and is never inspected.
var reducedSections = []reducedSection{
	{"spec/04_system-components.md", "4.7", "28.5"},
	{"spec/15_external-api-surface.md", "15.4", "28.5"},
}

// inDomain reports whether the check inspects the named section.
func inDomain(domain []reducedSection, file, section string) bool {
	for _, rs := range domain {
		if rs.File == file && rs.Section == section {
			return true
		}
	}
	return false
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

// successorFaults returns what is wrong with the successor pointers the reduced
// section's body carries, together with the number of pointers it inspected.
// Nothing is wrong when the body carries at least one pointer into the owning
// heading and at least one of those pointers names a channel identifier.
func successorFaults(lines []string, rs reducedSection) (faults []string, pointers int) {
	var named int
	var unnamed []string
	for _, l := range lines {
		for range pointerIntoOwner.FindAllStringSubmatch(l, -1) {
			pointers++
			if channelIdentifier.MatchString(l) {
				named++
				continue
			}
			unnamed = append(unnamed, strings.TrimSpace(l))
		}
	}
	if pointers == 0 {
		return []string{fmt.Sprintf("§%s gave up content to §%s and carries no pointer into it; N8 requires a "+
			"permanent sentence naming the heading that now owns the content", rs.Section, rs.Owner)}, 0
	}
	if named == 0 {
		sort.Strings(unnamed)
		return []string{fmt.Sprintf("§%s carries %d pointer(s) into §%s and none names a channel identifier; "+
			"a reader cannot tell which card applies. First: %.140s",
			rs.Section, pointers, rs.Owner, unnamed[0])}, pointers
	}
	return nil, pointers
}

// undeclaredPointers returns the pointers in the body that name a heading no
// specification file declares. Each fault names the reduced section and the
// heading that is missing, because a reader needs both to find the successor
// sentence that has gone stale.
func undeclaredPointers(lines []string, rs reducedSection, declared map[string]bool) []string {
	var faults []string
	for _, l := range lines {
		for _, m := range pointerIntoOwner.FindAllStringSubmatch(l, -1) {
			if !declared[m[1]] {
				faults = append(faults, fmt.Sprintf("%s §%s points at §%s, which no specification file declares",
					rs.File, rs.Section, m[1]))
			}
		}
	}
	return faults
}

// vacuityFault returns the fault a run carries when it inspected no reduced
// section or no pointer, and the empty string otherwise. A run that selects
// nothing reports the same green as a run over a tree whose pointers are all
// present, so the empty population is itself a failure.
func vacuityFault(sections, pointers int) string {
	if sections == 0 {
		return "no reduced section is named; the check would pass vacuously over the §4.7 and §15.4 reductions it is landed for"
	}
	if pointers == 0 {
		return fmt.Sprintf("the run inspected %d reduced section(s) and no successor pointer; the check would pass vacuously", sections)
	}
	return ""
}

// declaredCardHeadings returns the §28.5 contract-card headings the channels
// specification declares. A pointer's link target names that file, so a heading
// absent from it is a heading no specification file carries.
func declaredCardHeadings(t *testing.T, root string) map[string]bool {
	t.Helper()
	cards, err := os.ReadFile(filepath.Join(root, "spec/28_communication-channels.md"))
	if err != nil {
		t.Fatalf("read the channels specification: %v", err)
	}
	heading := regexp.MustCompile(`^#{2,6}\s+(28\.5(?:\.\d+)?)\s`)
	declared := map[string]bool{}
	for _, l := range strings.Split(string(cards), "\n") {
		if m := heading.FindStringSubmatch(l); m != nil {
			declared[m[1]] = true
		}
	}
	return declared
}

// spec: §28.1 N8 (successor pointers)
// diagnosis: a section that gave up content to §28 no longer tells a reader
// where the content went, or tells them the heading without naming the channel
// that moved. A reader landing on the reduced section will take its remaining
// prose for the whole answer.
func TestReducedSectionsPointAtTheHeadingThatOwnsTheirContent(t *testing.T) {
	t.Parallel()
	root := schematest.RepoRoot(t)
	var inspected int
	for _, rs := range reducedSections {
		t.Run(rs.File+"§"+rs.Section, func(t *testing.T) {
			lines := sectionBody(t, filepath.Join(root, rs.File), rs.Section)
			faults, pointers := successorFaults(lines, rs)
			inspected += pointers
			for _, fault := range faults {
				t.Errorf("%s: %s", rs.File, fault)
			}
			t.Logf("§%s: %d pointer(s) into §%s", rs.Section, pointers, rs.Owner)
		})
	}
	if fault := vacuityFault(len(reducedSections), inspected); fault != "" {
		t.Fatalf("%s", fault)
	}
}

// spec: §28.1 N8 (successor pointers)
// diagnosis: a pointer into §28.5 resolves to a card heading that does not
// exist, so the successor sentence sends a reader nowhere.
func TestSuccessorPointersNameACardThatExists(t *testing.T) {
	t.Parallel()
	root := schematest.RepoRoot(t)
	declared := declaredCardHeadings(t, root)
	if len(declared) == 0 {
		t.Fatalf("the channels specification declares no §28.5 heading; the check would pass vacuously")
	}
	var checked int
	for _, rs := range reducedSections {
		lines := sectionBody(t, filepath.Join(root, rs.File), rs.Section)
		for _, fault := range undeclaredPointers(lines, rs, declared) {
			t.Errorf("%s", fault)
		}
		for _, l := range lines {
			checked += len(pointerIntoOwner.FindAllStringSubmatch(l, -1))
		}
	}
	if fault := vacuityFault(len(reducedSections), checked); fault != "" {
		t.Fatalf("%s", fault)
	}
	t.Logf("%d successor pointer(s) checked against %d declared card heading(s)", checked, len(declared))
}

// headingTheFixturePointsAt returns the heading a fixture's pointer names that
// the declared set does not carry. The expectation is read out of the fixture
// rather than written into this file, so the deliberately absent heading is
// spelled once, in the fixture, where it is a link target instead of a citation
// a reader would try to resolve.
func headingTheFixturePointsAt(t *testing.T, lines []string, declared map[string]bool) string {
	t.Helper()
	for _, l := range lines {
		for _, m := range pointerIntoOwner.FindAllStringSubmatch(l, -1) {
			if !declared[m[1]] {
				return m[1]
			}
		}
	}
	t.Fatalf("the fixture names no undeclared heading; it cannot exercise the undeclared-pointer case")
	return ""
}

// spec: §28.1 N8 (successor pointers)
// diagnosis: the successor-pointer predicate reports a correct pointer as
// missing, passes a reduced section that carries none, passes a pointer into a
// heading no specification file declares, reaches a section that gave up no
// content, or reports green on a run that inspected nothing. In the last case
// the check ships inert against the §4.7 and §15.4 reductions it exists for.
func TestSuccessorPointerPredicateReadsThePointerAndItsTarget(t *testing.T) {
	t.Parallel()
	reduced := reducedSection{File: "spec/04_system-components.md", Section: "4.7", Owner: "28.5"}
	declared := map[string]bool{"28.5": true, "28.5.3": true}

	for _, tc := range []struct {
		name    string
		fixture string
		want    int
	}{
		{"a pointer into the heading that owns the content is accepted", "successor-pointer-owning-card.md", 0},
		{"a reduced section carrying no pointer is reported", "successor-pointer-absent.md", 1},
		{"a pointer that names no channel identifier is reported", "successor-pointer-unnamed-card.md", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := successorFaults(ownershipFixture(t, tc.fixture), reduced)
			if len(got) != tc.want {
				t.Errorf("fixture %s: %d fault(s) reported, want %d: %v", tc.fixture, len(got), tc.want, got)
			}
			if tc.want > 0 && !strings.Contains(got[0], "§"+reduced.Section) {
				t.Errorf("fixture %s: the fault does not name the section: %s", tc.fixture, got[0])
			}
		})
	}

	t.Run("a pointer at a heading no specification file declares is reported", func(t *testing.T) {
		body := ownershipFixture(t, "successor-pointer-missing-card.md")
		missing := headingTheFixturePointsAt(t, body, declared)
		got := undeclaredPointers(body, reduced, declared)
		if len(got) != 1 {
			t.Fatalf("%d fault(s) reported, want 1: %v", len(got), got)
		}
		for _, want := range []string{"§" + reduced.Section, "§" + missing} {
			if !strings.Contains(got[0], want) {
				t.Errorf("the fault does not name %s: %s", want, got[0])
			}
		}
		if len(undeclaredPointers(ownershipFixture(t, "successor-pointer-owning-card.md"), reduced, declared)) != 0 {
			t.Errorf("a pointer at a declared heading was reported as undeclared")
		}
	})

	t.Run("a section that gave up no content is outside the domain", func(t *testing.T) {
		unreduced := reducedSection{File: "spec/04_system-components.md", Section: "4.6", Owner: "28.5"}
		if inDomain(reducedSections, unreduced.File, unreduced.Section) {
			t.Fatalf("§%s gave up no content and must not be in the check's domain", unreduced.Section)
		}
		faults, _ := successorFaults(ownershipFixture(t, "successor-pointer-unreduced-section.md"), unreduced)
		if len(faults) == 0 {
			t.Errorf("the body carries no pointer and reported no fault; the domain is not what spares it")
		}
	})

	t.Run("a run that inspects nothing fails", func(t *testing.T) {
		if vacuityFault(0, 0) == "" {
			t.Errorf("a run over an empty domain reported green")
		}
		if vacuityFault(len(reducedSections), 0) == "" {
			t.Errorf("a run that inspected no pointer reported green")
		}
		if fault := vacuityFault(len(reducedSections), 1); fault != "" {
			t.Errorf("a run that inspected a pointer reported a vacuity fault: %s", fault)
		}
	})
}
